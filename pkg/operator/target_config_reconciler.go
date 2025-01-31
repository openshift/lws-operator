package operator

import (
	"context"
	"fmt"
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/controller"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/lws-operator/bindata"
	leaderworkersetapiv1 "github.com/openshift/lws-operator/pkg/apis/leaderworkersetoperator/v1"
	leaderworkersetoperatorv1clientset "github.com/openshift/lws-operator/pkg/generated/clientset/versioned/typed/leaderworkersetoperator/v1"
	operatorclientinformers "github.com/openshift/lws-operator/pkg/generated/informers/externalversions/leaderworkersetoperator/v1"
	"github.com/openshift/lws-operator/pkg/operator/operatorclient"
)

type TargetConfigReconciler struct {
	ctx                           context.Context
	targetImage                   string
	operatorClient                leaderworkersetoperatorv1clientset.LeaderWorkerSetOperatorInterface
	dynamicClient                 dynamic.Interface
	leaderWorkerSetOperatorClient *operatorclient.LeaderWorkerSetClient
	kubeClient                    kubernetes.Interface
	apiextensionClient            *apiextclientv1.Clientset
	eventRecorder                 events.Recorder
	queue                         workqueue.TypedRateLimitingInterface[string]
	namespace                     string
}

func NewTargetConfigReconciler(
	ctx context.Context,
	targetImage string,
	namespace string,
	operatorConfigClient leaderworkersetoperatorv1clientset.LeaderWorkerSetOperatorInterface,
	operatorClientInformer operatorclientinformers.LeaderWorkerSetOperatorInformer,
	leaderWorkerSetOperatorClient *operatorclient.LeaderWorkerSetClient,
	dynamicClient dynamic.Interface,
	kubeClient kubernetes.Interface,
	apiExtensionClient *apiextclientv1.Clientset,
	eventRecorder events.Recorder,
) *TargetConfigReconciler {
	c := &TargetConfigReconciler{
		ctx:                           ctx,
		operatorClient:                operatorConfigClient,
		dynamicClient:                 dynamicClient,
		leaderWorkerSetOperatorClient: leaderWorkerSetOperatorClient,
		kubeClient:                    kubeClient,
		apiextensionClient:            apiExtensionClient,
		eventRecorder:                 eventRecorder,
		queue:                         workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[string](), workqueue.TypedRateLimitingQueueConfig[string]{Name: "TargetConfigReconciler"}),
		targetImage:                   targetImage,
		namespace:                     namespace,
	}

	operatorClientInformer.Informer().AddEventHandler(c.eventHandler())

	return c
}

func (c *TargetConfigReconciler) sync() error {
	leaderWorkerSetOperator, err := c.operatorClient.Get(c.ctx, operatorclient.OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		klog.ErrorS(err, "unable to get operator configuration", "namespace", c.namespace, "lws", operatorclient.OperatorConfigName)
		return err
	}

	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1",
		Kind:       "LeaderWorkerSetOperator",
		Name:       leaderWorkerSetOperator.Name,
		UID:        leaderWorkerSetOperator.UID,
	}

	_, _, err = c.manageClusterRoleManager(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage manager cluster role err: %v", err)
		return err
	}

	_, _, err = c.manageClusterRoleMetrics(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage metrics cluster role err: %v", err)
		return err
	}

	_, _, err = c.manageClusterRoleProxy(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage proxy cluster role err: %v", err)
		return err
	}

	_, _, err = c.manageClusterRoleBindingManager(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage manager cluster role binding err: %v", err)
		return err
	}

	_, _, err = c.manageClusterRoleBindingMetrics(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage metrics cluster role binding err: %v", err)
		return err
	}

	_, _, err = c.manageClusterRoleBindingProxy(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage proxy cluster role binding err: %v", err)
		return err
	}

	_, _, err = c.manageRole(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage cluster role err: %v", err)
		return err
	}

	_, _, err = c.manageRoleMonitoring(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage cluster role err: %v", err)
		return err
	}

	_, _, err = c.manageRoleBinding(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage cluster role binding err: %v", err)
		return err
	}

	_, _, err = c.manageRoleBindingMonitoring(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage cluster role binding err: %v", err)
		return err
	}

	_, _, err = c.manageConfigmap(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage configmap err: %v", err)
		return err
	}

	_, _, err = c.manageCustomResourceDefinition(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage leaderworkerset CRD err: %v", err)
		return err
	}

	_, _, err = c.manageServiceAccount(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage service account err: %v", err)
		return err
	}

	_, _, err = c.manageServiceController(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage service err: %v", err)
		return err
	}

	_, _, err = c.manageServiceWebhook(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage service err: %v", err)
		return err
	}

	_, _, err = c.manageMutatingWebhook(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage service err: %v", err)
		return err
	}

	_, _, err = c.manageValidatingWebhook(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage service err: %v", err)
		return err
	}

	_, err = c.manageServiceMonitor(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage service account err: %v", err)
		return err
	}

	deployment, _, err := c.manageDeployments(leaderWorkerSetOperator, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage deployment err: %v", err)
		return err
	}

	_, _, err = v1helpers.UpdateStatus(c.ctx, c.leaderWorkerSetOperatorClient, func(status *operatorv1.OperatorStatus) error {
		resourcemerge.SetDeploymentGeneration(&status.Generations, deployment)
		return nil
	})

	return err
}

func (c *TargetConfigReconciler) manageConfigmap(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*v1.ConfigMap, bool, error) {
	configData := bindata.MustAsset("assets/lws-controller-config/config.yaml")
	required := resourceread.ReadConfigMapV1OrDie(bindata.MustAsset("assets/lws-controller/configmap.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	required.Data = map[string]string{
		"controller_manager_config.yaml": string(configData),
	}

	return resourceapply.ApplyConfigMap(c.ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageRole(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*rbacv1.Role, bool, error) {
	required := resourceread.ReadRoleV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrole_lws-manager-role.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyRole(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageRoleMonitoring(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*rbacv1.Role, bool, error) {
	required := resourceread.ReadRoleV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_role_lws-prometheus-k8s.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	controller.EnsureOwnerRef(required, ownerReference)

	return resourceapply.ApplyRole(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageRoleBinding(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*rbacv1.RoleBinding, bool, error) {
	required := resourceread.ReadRoleBindingV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_rolebinding_lws-leader-election-rolebinding.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	for i := range required.Subjects {
		required.Subjects[i].Namespace = c.namespace
	}

	return resourceapply.ApplyRoleBinding(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageRoleBindingMonitoring(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*rbacv1.RoleBinding, bool, error) {
	required := resourceread.ReadRoleBindingV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_rolebinding_lws-prometheus-k8s.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyRoleBinding(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleManager(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRole, bool, error) {
	required := resourceread.ReadClusterRoleV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrole_lws-manager-role.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyClusterRole(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleMetrics(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRole, bool, error) {
	required := resourceread.ReadClusterRoleV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrole_lws-metrics-reader.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyClusterRole(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleProxy(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRole, bool, error) {
	required := resourceread.ReadClusterRoleV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrole_lws-proxy-role.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyClusterRole(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleBindingManager(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRoleBinding, bool, error) {
	required := resourceread.ReadClusterRoleBindingV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrolebinding_lws-manager-rolebinding.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	for i := range required.Subjects {
		required.Subjects[i].Namespace = c.namespace
	}

	return resourceapply.ApplyClusterRoleBinding(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleBindingMetrics(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRoleBinding, bool, error) {
	required := resourceread.ReadClusterRoleBindingV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrolebinding_lws-metrics-reader-rolebinding.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	for i := range required.Subjects {
		required.Subjects[i].Namespace = c.namespace
	}

	return resourceapply.ApplyClusterRoleBinding(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleBindingProxy(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRoleBinding, bool, error) {
	required := resourceread.ReadClusterRoleBindingV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrolebinding_lws-proxy-rolebinding.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	for i := range required.Subjects {
		required.Subjects[i].Namespace = c.namespace
	}

	return resourceapply.ApplyClusterRoleBinding(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageServiceController(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*v1.Service, bool, error) {
	required := resourceread.ReadServiceV1OrDie(bindata.MustAsset("assets/lws-controller-generated/v1_service_lws-controller-manager-metrics-service.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyService(c.ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageServiceWebhook(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*v1.Service, bool, error) {
	required := resourceread.ReadServiceV1OrDie(bindata.MustAsset("assets/lws-controller-generated/v1_service_lws-webhook-service.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyService(c.ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageServiceAccount(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*v1.ServiceAccount, bool, error) {
	required := resourceread.ReadServiceAccountV1OrDie(bindata.MustAsset("assets/lws-controller-generated/v1_serviceaccount_lws-controller-manager.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyServiceAccount(c.ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageCustomResourceDefinition(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*apiextensionv1.CustomResourceDefinition, bool, error) {
	required := resourceread.ReadCustomResourceDefinitionV1OrDie(bindata.MustAsset("assets/lws-controller-generated/apiextensions.k8s.io_v1_customresourcedefinition_leaderworkersets.leaderworkerset.x-k8s.io.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	if required.Spec.Conversion != nil &&
		required.Spec.Conversion.Webhook != nil &&
		required.Spec.Conversion.Webhook.ClientConfig != nil &&
		required.Spec.Conversion.Webhook.ClientConfig.Service != nil {
		required.Spec.Conversion.Webhook.ClientConfig.Service.Namespace = c.namespace
	}

	return resourceapply.ApplyCustomResourceDefinitionV1(c.ctx, c.apiextensionClient.ApiextensionsV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageMutatingWebhook(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*admissionv1.MutatingWebhookConfiguration, bool, error) {
	required := resourceread.ReadMutatingWebhookConfigurationV1OrDie(bindata.MustAsset("assets/lws-controller-generated/admissionregistration.k8s.io_v1_mutatingwebhookconfiguration_lws-mutating-webhook-configuration.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	for i := range required.Webhooks {
		if required.Webhooks[i].ClientConfig.Service != nil {
			required.Webhooks[i].ClientConfig.Service.Namespace = c.namespace
		}
	}

	return resourceapply.ApplyMutatingWebhookConfigurationImproved(c.ctx, c.kubeClient.AdmissionregistrationV1(), c.eventRecorder, required, resourceapply.NewResourceCache())
}

func (c *TargetConfigReconciler) manageValidatingWebhook(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*admissionv1.ValidatingWebhookConfiguration, bool, error) {
	required := resourceread.ReadValidatingWebhookConfigurationV1OrDie(bindata.MustAsset("assets/lws-controller-generated/admissionregistration.k8s.io_v1_validatingwebhookconfiguration_lws-validating-webhook-configuration.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	for i := range required.Webhooks {
		if required.Webhooks[i].ClientConfig.Service != nil {
			required.Webhooks[i].ClientConfig.Service.Namespace = c.namespace
		}
	}

	return resourceapply.ApplyValidatingWebhookConfigurationImproved(c.ctx, c.kubeClient.AdmissionregistrationV1(), c.eventRecorder, required, resourceapply.NewResourceCache())
}

func (c *TargetConfigReconciler) manageServiceMonitor(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (bool, error) {
	required := resourceread.ReadUnstructuredOrDie(bindata.MustAsset("assets/lws-controller-generated/monitoring.coreos.com_v1_servicemonitor_lws-controller-manager-metrics-monitor.yaml"))
	required.SetNamespace(c.namespace)
	required.SetOwnerReferences([]metav1.OwnerReference{
		ownerReference,
	})

	_, changed, err := resourceapply.ApplyKnownUnstructured(c.ctx, c.dynamicClient, c.eventRecorder, required)
	return changed, err
}

func (c *TargetConfigReconciler) manageDeployments(leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator, ownerReference metav1.OwnerReference) (*appsv1.Deployment, bool, error) {
	required := resourceread.ReadDeploymentV1OrDie(bindata.MustAsset("assets/lws-controller-generated/apps_v1_deployment_lws-controller-manager.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	// Update image
	// Update annotations
	// Use -      nodeSelector:
	//-        "node-role.kubernetes.io/worker": ""
	// Add volumemounts

	if c.targetImage != "" {
		images := map[string]string{
			"${CONTROLLER_IMAGE}": c.targetImage,
		}

		for i := range required.Spec.Template.Spec.Containers {
			for env, img := range images {
				if required.Spec.Template.Spec.Containers[i].Image == env {
					required.Spec.Template.Spec.Containers[i].Image = img
					break
				}
			}
		}
	}

	switch leaderWorkerSetOperator.Spec.LogLevel {
	case operatorv1.Normal:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("--zap-log-level=%d", 2))
	case operatorv1.Debug:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("--zap-log-level=%d", 4))
	case operatorv1.Trace:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("--zap-log-level=%d", 6))
	case operatorv1.TraceAll:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("--zap-log-level=%d", 8))
	default:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("--zap-log-level=%d", 2))
	}

	return resourceapply.ApplyDeployment(
		c.ctx,
		c.kubeClient.AppsV1(),
		c.eventRecorder,
		required,
		resourcemerge.ExpectedDeploymentGeneration(required, leaderWorkerSetOperator.Status.Generations))
}

// Run starts the kube-scheduler and blocks until stopCh is closed.
func (c *TargetConfigReconciler) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting TargetConfigReconciler")
	defer klog.Infof("Shutting down TargetConfigReconciler")

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *TargetConfigReconciler) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *TargetConfigReconciler) processNextWorkItem() bool {
	dsKey, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(dsKey)

	err := c.sync()
	if err == nil {
		c.queue.Forget(dsKey)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	c.queue.AddRateLimited(dsKey)

	return true
}

// eventHandler queues the operator to check spec and status
func (c *TargetConfigReconciler) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(workQueueKey) },
	}
}
