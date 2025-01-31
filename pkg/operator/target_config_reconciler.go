package operator

import (
	"context"
	"fmt"
	"strconv"
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/controller"
	"github.com/openshift/library-go/pkg/controller/factory"
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
	targetImage                   string
	operatorClient                leaderworkersetoperatorv1clientset.LeaderWorkerSetOperatorInterface
	dynamicClient                 dynamic.Interface
	leaderWorkerSetOperatorClient *operatorclient.LeaderWorkerSetClient
	kubeClient                    kubernetes.Interface
	apiextensionClient            *apiextclientv1.Clientset
	eventRecorder                 events.Recorder
	kubeInformersForNamespaces    v1helpers.KubeInformersForNamespaces
	namespace                     string
	resourceCache                 resourceapply.ResourceCache
}

func NewTargetConfigReconciler(
	targetImage string,
	namespace string,
	operatorConfigClient leaderworkersetoperatorv1clientset.LeaderWorkerSetOperatorInterface,
	operatorClientInformer operatorclientinformers.LeaderWorkerSetOperatorInformer,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	leaderWorkerSetOperatorClient *operatorclient.LeaderWorkerSetClient,
	dynamicClient dynamic.Interface,
	kubeClient kubernetes.Interface,
	apiExtensionClient *apiextclientv1.Clientset,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &TargetConfigReconciler{
		operatorClient:                operatorConfigClient,
		dynamicClient:                 dynamicClient,
		leaderWorkerSetOperatorClient: leaderWorkerSetOperatorClient,
		kubeClient:                    kubeClient,
		apiextensionClient:            apiExtensionClient,
		eventRecorder:                 eventRecorder,
		kubeInformersForNamespaces:    kubeInformersForNamespaces,
		targetImage:                   targetImage,
		namespace:                     namespace,
		resourceCache:                 resourceapply.NewResourceCache(),
	}

	return factory.New().WithInformers(
		// for the operator changes
		operatorClientInformer.Informer(),
		// for the deployment and its configmap and secret
		kubeInformersForNamespaces.InformersFor(operatorNamespace).Apps().V1().Deployments().Informer(),
	).ResyncEvery(time.Minute*5).WithSync(c.sync).ToController("TargetConfigController", eventRecorder)
}

func (c *TargetConfigReconciler) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	leaderWorkerSetOperator, err := c.operatorClient.Get(ctx, operatorclient.OperatorConfigName, metav1.GetOptions{})
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

	specAnnotations := map[string]string{
		"leaderworkersetoperator.operator.openshift.io/cluster": strconv.FormatInt(leaderWorkerSetOperator.Generation, 10),
	}

	_, _, err = c.manageClusterRoleManager(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage manager cluster role err: %v", err)
		return err
	}

	_, _, err = c.manageClusterRoleMetrics(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage metrics cluster role err: %v", err)
		return err
	}

	_, _, err = c.manageClusterRoleProxy(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage proxy cluster role err: %v", err)
		return err
	}

	_, _, err = c.manageClusterRoleBindingManager(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage manager cluster role binding err: %v", err)
		return err
	}

	_, _, err = c.manageClusterRoleBindingMetrics(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage metrics cluster role binding err: %v", err)
		return err
	}

	_, _, err = c.manageClusterRoleBindingProxy(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage proxy cluster role binding err: %v", err)
		return err
	}

	_, _, err = c.manageRole(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage cluster role err: %v", err)
		return err
	}

	_, _, err = c.manageRoleMonitoring(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage cluster role err: %v", err)
		return err
	}

	_, _, err = c.manageRoleBinding(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage cluster role binding err: %v", err)
		return err
	}

	_, _, err = c.manageRoleBindingMonitoring(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage cluster role binding err: %v", err)
		return err
	}

	_, _, err = c.manageConfigmap(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage configmap err: %v", err)
		return err
	}
	// TODO more spec annotation is needed?

	_, _, err = c.manageCustomResourceDefinition(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage leaderworkerset CRD err: %v", err)
		return err
	}

	_, _, err = c.manageServiceAccount(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage service account err: %v", err)
		return err
	}

	_, _, err = c.manageServiceController(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage service err: %v", err)
		return err
	}

	_, _, err = c.manageServiceWebhook(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage service err: %v", err)
		return err
	}

	_, _, err = c.manageMutatingWebhook(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage service err: %v", err)
		return err
	}

	_, _, err = c.manageValidatingWebhook(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage service err: %v", err)
		return err
	}

	_, err = c.manageServiceMonitor(ctx, ownerReference)
	if err != nil {
		klog.Errorf("unable to manage service account err: %v", err)
		return err
	}

	deployment, _, err := c.manageDeployments(ctx, leaderWorkerSetOperator, ownerReference, specAnnotations)
	if err != nil {
		klog.Errorf("unable to manage deployment err: %v", err)
		return err
	}

	_, _, err = v1helpers.UpdateStatus(ctx, c.leaderWorkerSetOperatorClient, func(status *operatorv1.OperatorStatus) error {
		resourcemerge.SetDeploymentGeneration(&status.Generations, deployment)
		return nil
	})

	return err
}

func (c *TargetConfigReconciler) manageConfigmap(ctx context.Context, ownerReference metav1.OwnerReference) (*v1.ConfigMap, bool, error) {
	configData := bindata.MustAsset("assets/lws-controller-config/config.yaml")
	required := resourceread.ReadConfigMapV1OrDie(bindata.MustAsset("assets/lws-controller/configmap.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	required.Data = map[string]string{
		"controller_manager_config.yaml": string(configData),
	}

	return resourceapply.ApplyConfigMap(ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageRole(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.Role, bool, error) {
	required := resourceread.ReadRoleV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrole_lws-manager-role.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyRole(ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageRoleMonitoring(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.Role, bool, error) {
	required := resourceread.ReadRoleV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_role_lws-prometheus-k8s.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	controller.EnsureOwnerRef(required, ownerReference)

	return resourceapply.ApplyRole(ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageRoleBinding(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.RoleBinding, bool, error) {
	required := resourceread.ReadRoleBindingV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_rolebinding_lws-leader-election-rolebinding.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	for i := range required.Subjects {
		required.Subjects[i].Namespace = c.namespace
	}

	return resourceapply.ApplyRoleBinding(ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageRoleBindingMonitoring(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.RoleBinding, bool, error) {
	required := resourceread.ReadRoleBindingV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_rolebinding_lws-prometheus-k8s.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyRoleBinding(ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleManager(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRole, bool, error) {
	required := resourceread.ReadClusterRoleV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrole_lws-manager-role.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyClusterRole(ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleMetrics(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRole, bool, error) {
	required := resourceread.ReadClusterRoleV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrole_lws-metrics-reader.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyClusterRole(ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleProxy(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRole, bool, error) {
	required := resourceread.ReadClusterRoleV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrole_lws-proxy-role.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyClusterRole(ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleBindingManager(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRoleBinding, bool, error) {
	required := resourceread.ReadClusterRoleBindingV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrolebinding_lws-manager-rolebinding.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	for i := range required.Subjects {
		required.Subjects[i].Namespace = c.namespace
	}

	return resourceapply.ApplyClusterRoleBinding(ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleBindingMetrics(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRoleBinding, bool, error) {
	required := resourceread.ReadClusterRoleBindingV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrolebinding_lws-metrics-reader-rolebinding.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	for i := range required.Subjects {
		required.Subjects[i].Namespace = c.namespace
	}

	return resourceapply.ApplyClusterRoleBinding(ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleBindingProxy(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRoleBinding, bool, error) {
	required := resourceread.ReadClusterRoleBindingV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_clusterrolebinding_lws-proxy-rolebinding.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	for i := range required.Subjects {
		required.Subjects[i].Namespace = c.namespace
	}

	return resourceapply.ApplyClusterRoleBinding(ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageServiceController(ctx context.Context, ownerReference metav1.OwnerReference) (*v1.Service, bool, error) {
	required := resourceread.ReadServiceV1OrDie(bindata.MustAsset("assets/lws-controller-generated/v1_service_lws-controller-manager-metrics-service.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyService(ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageServiceWebhook(ctx context.Context, ownerReference metav1.OwnerReference) (*v1.Service, bool, error) {
	required := resourceread.ReadServiceV1OrDie(bindata.MustAsset("assets/lws-controller-generated/v1_service_lws-webhook-service.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyService(ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageServiceAccount(ctx context.Context, ownerReference metav1.OwnerReference) (*v1.ServiceAccount, bool, error) {
	required := resourceread.ReadServiceAccountV1OrDie(bindata.MustAsset("assets/lws-controller-generated/v1_serviceaccount_lws-controller-manager.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyServiceAccount(ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageCustomResourceDefinition(ctx context.Context, ownerReference metav1.OwnerReference) (*apiextensionv1.CustomResourceDefinition, bool, error) {
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

	annotations := required.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["cert-manager.io/inject-ca-from"] = fmt.Sprintf("%s/webhook-cert", c.namespace)
	required.SetAnnotations(annotations)

	currentCRD, err := c.apiextensionClient.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, required.Name, metav1.GetOptions{})
	switch {
	case errors.IsNotFound(err):
		// no action needed
	case err != nil && !errors.IsNotFound(err):
		return nil, false, err
	case err == nil:
		if required.Spec.Conversion != nil && required.Spec.Conversion.Webhook != nil && required.Spec.Conversion.Webhook.ClientConfig != nil {
			required.Spec.Conversion.Webhook.ClientConfig.CABundle = currentCRD.Spec.Conversion.Webhook.ClientConfig.CABundle
		}
	}

	return resourceapply.ApplyCustomResourceDefinitionV1(ctx, c.apiextensionClient.ApiextensionsV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageMutatingWebhook(ctx context.Context, ownerReference metav1.OwnerReference) (*admissionv1.MutatingWebhookConfiguration, bool, error) {
	required := resourceread.ReadMutatingWebhookConfigurationV1OrDie(bindata.MustAsset("assets/lws-controller-generated/admissionregistration.k8s.io_v1_mutatingwebhookconfiguration_lws-mutating-webhook-configuration.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	for i := range required.Webhooks {
		if required.Webhooks[i].ClientConfig.Service != nil {
			required.Webhooks[i].ClientConfig.Service.Namespace = c.namespace
		}
	}

	annotations := required.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["cert-manager.io/inject-ca-from"] = fmt.Sprintf("%s/webhook-cert", c.namespace)
	required.SetAnnotations(annotations)

	return resourceapply.ApplyMutatingWebhookConfigurationImproved(ctx, c.kubeClient.AdmissionregistrationV1(), c.eventRecorder, required, c.resourceCache)
}

func (c *TargetConfigReconciler) manageValidatingWebhook(ctx context.Context, ownerReference metav1.OwnerReference) (*admissionv1.ValidatingWebhookConfiguration, bool, error) {
	required := resourceread.ReadValidatingWebhookConfigurationV1OrDie(bindata.MustAsset("assets/lws-controller-generated/admissionregistration.k8s.io_v1_validatingwebhookconfiguration_lws-validating-webhook-configuration.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	for i := range required.Webhooks {
		if required.Webhooks[i].ClientConfig.Service != nil {
			required.Webhooks[i].ClientConfig.Service.Namespace = c.namespace
		}
	}

	annotations := required.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["cert-manager.io/inject-ca-from"] = fmt.Sprintf("%s/webhook-cert", c.namespace)
	required.SetAnnotations(annotations)

	return resourceapply.ApplyValidatingWebhookConfigurationImproved(ctx, c.kubeClient.AdmissionregistrationV1(), c.eventRecorder, required, c.resourceCache)
}

func (c *TargetConfigReconciler) manageServiceMonitor(ctx context.Context, ownerReference metav1.OwnerReference) (bool, error) {
	required := resourceread.ReadUnstructuredOrDie(bindata.MustAsset("assets/lws-controller-generated/monitoring.coreos.com_v1_servicemonitor_lws-controller-manager-metrics-monitor.yaml"))
	required.SetNamespace(c.namespace)
	required.SetOwnerReferences([]metav1.OwnerReference{
		ownerReference,
	})

	_, changed, err := resourceapply.ApplyKnownUnstructured(ctx, c.dynamicClient, c.eventRecorder, required)
	return changed, err
}

func (c *TargetConfigReconciler) manageDeployments(ctx context.Context,
	leaderWorkerSetOperator *leaderworkersetapiv1.LeaderWorkerSetOperator,
	ownerReference metav1.OwnerReference,
	specAnnotations map[string]string) (*appsv1.Deployment, bool, error) {
	required := resourceread.ReadDeploymentV1OrDie(bindata.MustAsset("assets/lws-controller-generated/apps_v1_deployment_lws-controller-manager.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

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

	required.Spec.Template.Spec.NodeSelector = map[string]string{
		"node-role.kubernetes.io/worker": "",
	}

	resourcemerge.MergeMap(ptr.To(false), &required.Spec.Template.Annotations, specAnnotations)

	newArgs := []string{
		"--config=/controller_manager_config.yaml",
	}

	switch leaderWorkerSetOperator.Spec.LogLevel {
	case operatorv1.Normal:
		newArgs = append(newArgs, fmt.Sprintf("--zap-log-level=%d", 2))
	case operatorv1.Debug:
		newArgs = append(newArgs, fmt.Sprintf("--zap-log-level=%d", 4))
	case operatorv1.Trace:
		newArgs = append(newArgs, fmt.Sprintf("--zap-log-level=%d", 6))
	case operatorv1.TraceAll:
		newArgs = append(newArgs, fmt.Sprintf("--zap-log-level=%d", 9))
	default:
		newArgs = append(newArgs, fmt.Sprintf("--zap-log-level=%d", 2))
	}

	// replace the default arg values from upstream
	required.Spec.Template.Spec.Containers[0].Args = newArgs

	return resourceapply.ApplyDeployment(
		ctx,
		c.kubeClient.AppsV1(),
		c.eventRecorder,
		required,
		resourcemerge.ExpectedDeploymentGeneration(required, leaderWorkerSetOperator.Status.Generations))
}
