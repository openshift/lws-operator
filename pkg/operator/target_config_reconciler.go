package operator

import (
	"context"
	"fmt"
	"strings"
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/utils/ptr"

	operatorv1 "github.com/openshift/api/operator/v1"
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

const (
	MetricsCertificateSecretName  = "metrics-server-cert"
	WebhookCertificateSecretName  = "webhook-server-cert"
	WebhookCertificateName        = "lws-serving-cert"
	CertManagerInjectCaAnnotation = "cert-manager.io/inject-ca-from"
	// PrometheusClientCertsPath is a mounted secret in the openshift-monitoring prometheus
	PrometheusClientCertsPath = "/etc/prometheus/secrets/metrics-client-certs/"
)

type TargetConfigReconciler struct {
	targetImage                   string
	operatorClient                leaderworkersetoperatorv1clientset.LeaderWorkerSetOperatorInterface
	dynamicClient                 dynamic.Interface
	discoveryClient               discovery.DiscoveryInterface
	leaderWorkerSetOperatorClient *operatorclient.LeaderWorkerSetClient
	kubeClient                    kubernetes.Interface
	apiextensionClient            *apiextclientv1.Clientset
	eventRecorder                 events.Recorder
	kubeInformersForNamespaces    v1helpers.KubeInformersForNamespaces
	secretLister                  v1.SecretLister
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
	discoveryClient discovery.DiscoveryInterface,
	kubeClient kubernetes.Interface,
	apiExtensionClient *apiextclientv1.Clientset,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &TargetConfigReconciler{
		operatorClient:                operatorConfigClient,
		dynamicClient:                 dynamicClient,
		leaderWorkerSetOperatorClient: leaderWorkerSetOperatorClient,
		kubeClient:                    kubeClient,
		discoveryClient:               discoveryClient,
		apiextensionClient:            apiExtensionClient,
		eventRecorder:                 eventRecorder,
		kubeInformersForNamespaces:    kubeInformersForNamespaces,
		secretLister:                  kubeInformersForNamespaces.SecretLister(),
		targetImage:                   targetImage,
		namespace:                     namespace,
		resourceCache:                 resourceapply.NewResourceCache(),
	}

	return factory.New().WithInformers(
		// for the operator changes
		operatorClientInformer.Informer(),
		// for the deployment and its configmap and secret
		kubeInformersForNamespaces.InformersFor(namespace).Apps().V1().Deployments().Informer(),
		kubeInformersForNamespaces.InformersFor(namespace).Core().V1().ConfigMaps().Informer(),
		kubeInformersForNamespaces.InformersFor(namespace).Core().V1().Secrets().Informer(),
	).ResyncEvery(time.Minute*5).
		WithSync(c.sync).
		WithSyncDegradedOnError(leaderWorkerSetOperatorClient).
		ToController("TargetConfigController", eventRecorder)
}

func (c *TargetConfigReconciler) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	found, err := isResourceRegistered(c.discoveryClient, schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Issuer",
	})
	if err != nil {
		return fmt.Errorf("unable to check cert-manager is installed: %w", err)
	}
	if !found {
		return fmt.Errorf("please make sure that cert-manager is installed on your cluster")
	}

	spec, _, _, err := c.leaderWorkerSetOperatorClient.GetOperatorState()
	if err != nil {
		return err
	}

	if spec.ManagementState != operatorv1.Managed {
		return nil
	}

	leaderWorkerSetOperator, err := c.operatorClient.Get(ctx, operatorclient.OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to get operator configuration %s/%s: %w", c.namespace, operatorclient.OperatorConfigName, err)
	}

	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1",
		Kind:       "LeaderWorkerSetOperator",
		Name:       leaderWorkerSetOperator.Name,
		UID:        leaderWorkerSetOperator.UID,
	}

	specAnnotations := make(map[string]string)

	_, _, err = c.manageClusterRoleManager(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageClusterRoleMetrics(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageClusterRoleProxy(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageClusterRoleBindingManager(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageClusterRoleBindingMetrics(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageClusterRoleBindingProxy(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageRole(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageRoleMonitoring(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageRoleBinding(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageRoleBindingMonitoring(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageServiceWebhook(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageIssuerCR(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageCertificateWebhookCR(ctx, ownerReference)
	if err != nil {
		return err
	}

	webhookSecret, _, err := c.checkSecretReady(WebhookCertificateSecretName)
	if err != nil {
		return err
	}
	specAnnotations["secrets/"+webhookSecret.Name] = webhookSecret.ResourceVersion

	_, _, err = c.manageCertificateMetricsCR(ctx, ownerReference)
	if err != nil {
		return err
	}

	metricsSecret, _, err := c.checkSecretReady(MetricsCertificateSecretName)
	if err != nil {
		return err
	}
	specAnnotations["secrets/"+metricsSecret.Name] = metricsSecret.ResourceVersion

	configMap, _, err := c.manageConfigmap(ctx, ownerReference)
	if err != nil {
		return err
	}
	specAnnotations["configmaps/"+configMap.Name] = configMap.ResourceVersion

	_, _, err = c.manageCustomResourceDefinition(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageServiceAccount(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageServiceController(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageMutatingWebhook(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageValidatingWebhook(ctx, ownerReference)
	if err != nil {
		return err
	}

	_, _, err = c.manageServiceMonitor(ctx, ownerReference)
	if err != nil {
		return err
	}

	deployment, _, err := c.manageDeployments(ctx, leaderWorkerSetOperator, ownerReference, specAnnotations)
	if err != nil {
		return err
	}

	_, _, err = v1helpers.UpdateStatus(ctx, c.leaderWorkerSetOperatorClient, func(status *operatorv1.OperatorStatus) error {
		resourcemerge.SetDeploymentGeneration(&status.Generations, deployment)
		return nil
	}, v1helpers.UpdateConditionFn(operatorv1.OperatorCondition{
		Type:   operatorv1.OperatorStatusTypeAvailable,
		Status: operatorv1.ConditionTrue,
		Reason: "AsExpected",
	}))

	return err
}

func (c *TargetConfigReconciler) manageConfigmap(ctx context.Context, ownerReference metav1.OwnerReference) (*corev1.ConfigMap, bool, error) {
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

func (c *TargetConfigReconciler) checkSecretReady(secretName string) (*corev1.Secret, bool, error) {
	secret, err := c.secretLister.Secrets(c.namespace).Get(secretName)
	// secret should be generated by the cert manager
	if err != nil {
		return nil, false, err
	}
	if len(secret.Data["tls.crt"]) == 0 || len(secret.Data["tls.key"]) == 0 {
		return nil, false, fmt.Errorf("%s secret is not initialized", secret.Name)
	}
	return secret, false, nil
}

func (c *TargetConfigReconciler) manageRole(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.Role, bool, error) {
	required := resourceread.ReadRoleV1OrDie(bindata.MustAsset("assets/lws-controller-generated/rbac.authorization.k8s.io_v1_role_lws-leader-election-role.yaml"))
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

func (c *TargetConfigReconciler) manageIssuerCR(ctx context.Context, ownerReference metav1.OwnerReference) (*unstructured.Unstructured, bool, error) {
	gvr := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "issuers",
	}

	issuer, err := resourceread.ReadGenericWithUnstructured(bindata.MustAsset("assets/lws-controller-generated/cert-manager.io_v1_issuer_lws-selfsigned-issuer.yaml"))
	if err != nil {
		return nil, false, err
	}
	issuerAsUnstructured, ok := issuer.(*unstructured.Unstructured)
	if !ok {
		return nil, false, fmt.Errorf("issuer is not an Unstructured")
	}
	issuerAsUnstructured.SetNamespace(c.namespace)
	ownerReferences := issuerAsUnstructured.GetOwnerReferences()
	ownerReferences = append(ownerReferences, ownerReference)
	issuerAsUnstructured.SetOwnerReferences(ownerReferences)

	return resourceapply.ApplyUnstructuredResourceImproved(ctx, c.dynamicClient, c.eventRecorder, issuerAsUnstructured, c.resourceCache, gvr, nil, nil)
}

func (c *TargetConfigReconciler) manageCertificateWebhookCR(ctx context.Context, ownerReference metav1.OwnerReference) (*unstructured.Unstructured, bool, error) {
	gvr := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "certificates",
	}

	service := resourceread.ReadServiceV1OrDie(bindata.MustAsset("assets/lws-controller-generated/v1_service_lws-webhook-service.yaml"))
	issuer, err := resourceread.ReadGenericWithUnstructured(bindata.MustAsset("assets/lws-controller-generated/cert-manager.io_v1_certificate_lws-serving-cert.yaml"))
	if err != nil {
		return nil, false, err
	}
	issuerAsUnstructured, ok := issuer.(*unstructured.Unstructured)
	if !ok {
		return nil, false, fmt.Errorf("issuer is not an Unstructured")
	}
	issuerAsUnstructured.SetNamespace(c.namespace)
	ownerReferences := issuerAsUnstructured.GetOwnerReferences()
	ownerReferences = append(ownerReferences, ownerReference)
	issuerAsUnstructured.SetOwnerReferences(ownerReferences)
	dnsNames, found, err := unstructured.NestedStringSlice(issuerAsUnstructured.Object, "spec", "dnsNames")
	if !found || err != nil {
		return nil, false, fmt.Errorf("%v: .spec.dnsNames not found: %v", issuerAsUnstructured.GetName(), err)
	}
	for i := range dnsNames {
		dnsNames[i] = strings.Replace(dnsNames[i], "SERVICE_NAME", service.Name, 1)
		dnsNames[i] = strings.Replace(dnsNames[i], "SERVICE_NAMESPACE", c.namespace, 1)
	}
	err = unstructured.SetNestedStringSlice(issuerAsUnstructured.Object, dnsNames, "spec", "dnsNames")
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyUnstructuredResourceImproved(ctx, c.dynamicClient, c.eventRecorder, issuerAsUnstructured, c.resourceCache, gvr, nil, nil)
}

func (c *TargetConfigReconciler) manageCertificateMetricsCR(ctx context.Context, ownerReference metav1.OwnerReference) (*unstructured.Unstructured, bool, error) {
	gvr := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "certificates",
	}

	service := resourceread.ReadServiceV1OrDie(bindata.MustAsset("assets/lws-controller-generated/v1_service_lws-controller-manager-metrics-service.yaml"))
	issuer, err := resourceread.ReadGenericWithUnstructured(bindata.MustAsset("assets/lws-controller-generated/cert-manager.io_v1_certificate_lws-metrics-cert.yaml"))
	if err != nil {
		return nil, false, err
	}
	issuerAsUnstructured, ok := issuer.(*unstructured.Unstructured)
	if !ok {
		return nil, false, fmt.Errorf("issuer is not an Unstructured")
	}
	issuerAsUnstructured.SetNamespace(c.namespace)
	ownerReferences := issuerAsUnstructured.GetOwnerReferences()
	ownerReferences = append(ownerReferences, ownerReference)
	issuerAsUnstructured.SetOwnerReferences(ownerReferences)
	dnsNames, found, err := unstructured.NestedStringSlice(issuerAsUnstructured.Object, "spec", "dnsNames")
	if !found || err != nil {
		return nil, false, fmt.Errorf("%v: .spec.dnsNames not found: %v", issuerAsUnstructured.GetName(), err)
	}
	for i := range dnsNames {
		dnsNames[i] = strings.Replace(dnsNames[i], "SERVICE_NAME", service.Name, 1)
		dnsNames[i] = strings.Replace(dnsNames[i], "SERVICE_NAMESPACE", c.namespace, 1)
	}
	err = unstructured.SetNestedStringSlice(issuerAsUnstructured.Object, dnsNames, "spec", "dnsNames")
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyUnstructuredResourceImproved(ctx, c.dynamicClient, c.eventRecorder, issuerAsUnstructured, c.resourceCache, gvr, nil, nil)
}

func (c *TargetConfigReconciler) manageServiceController(ctx context.Context, ownerReference metav1.OwnerReference) (*corev1.Service, bool, error) {
	required := resourceread.ReadServiceV1OrDie(bindata.MustAsset("assets/lws-controller-generated/v1_service_lws-controller-manager-metrics-service.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyService(ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageServiceWebhook(ctx context.Context, ownerReference metav1.OwnerReference) (*corev1.Service, bool, error) {
	required := resourceread.ReadServiceV1OrDie(bindata.MustAsset("assets/lws-controller-generated/v1_service_lws-webhook-service.yaml"))
	required.Namespace = c.namespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	return resourceapply.ApplyService(ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageServiceAccount(ctx context.Context, ownerReference metav1.OwnerReference) (*corev1.ServiceAccount, bool, error) {
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

	err := injectCertManagerCA(required, c.namespace)
	if err != nil {
		return nil, false, err
	}

	currentCRD, err := c.apiextensionClient.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, required.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		// no action needed
	case err != nil && !apierrors.IsNotFound(err):
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

	err := injectCertManagerCA(required, c.namespace)
	if err != nil {
		return nil, false, err
	}

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

	err := injectCertManagerCA(required, c.namespace)
	if err != nil {
		return nil, false, err
	}

	return resourceapply.ApplyValidatingWebhookConfigurationImproved(ctx, c.kubeClient.AdmissionregistrationV1(), c.eventRecorder, required, c.resourceCache)
}

func (c *TargetConfigReconciler) manageServiceMonitor(ctx context.Context, ownerReference metav1.OwnerReference) (*unstructured.Unstructured, bool, error) {
	service := resourceread.ReadServiceV1OrDie(bindata.MustAsset("assets/lws-controller-generated/v1_service_lws-controller-manager-metrics-service.yaml"))
	serviceMonitor := ReadServiceMonitorV1OrDie(bindata.MustAsset("assets/lws-controller-generated/monitoring.coreos.com_v1_servicemonitor_lws-controller-manager-metrics-monitor.yaml"))
	serviceMonitor.SetNamespace(c.namespace)
	serviceMonitor.SetOwnerReferences([]metav1.OwnerReference{
		ownerReference,
	})

	for i, endpoint := range serviceMonitor.Spec.Endpoints {
		endpoint.TLSConfig.ServerName = ptr.To(strings.Replace(ptr.Deref(endpoint.TLSConfig.ServerName, ""), "SERVICE_NAME", service.Name, 1))
		endpoint.TLSConfig.ServerName = ptr.To(strings.Replace(ptr.Deref(endpoint.TLSConfig.ServerName, ""), "SERVICE_NAMESPACE", service.Namespace, 1))
		// clear out the references
		endpoint.TLSConfig.Cert.Secret = nil
		endpoint.TLSConfig.Cert.ConfigMap = nil
		endpoint.TLSConfig.KeySecret = nil
		// set mounted secret in the openshift-monitoring prometheus
		endpoint.TLSConfig.CertFile = fmt.Sprintf("%s/%s", PrometheusClientCertsPath, "tls.crt")
		endpoint.TLSConfig.KeyFile = fmt.Sprintf("%s/%s", PrometheusClientCertsPath, "tls.key")
		serviceMonitor.Spec.Endpoints[i] = endpoint
	}

	return ApplyServiceMonitor(ctx, c.dynamicClient, c.eventRecorder, serviceMonitor, c.resourceCache)
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
			"${CONTROLLER_IMAGE}:latest": c.targetImage,
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

func isResourceRegistered(discoveryClient discovery.DiscoveryInterface, gvk schema.GroupVersionKind) (bool, error) {
	apiResourceLists, err := discoveryClient.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, apiResource := range apiResourceLists.APIResources {
		if apiResource.Kind == gvk.Kind {
			return true, nil
		}
	}
	return false, nil
}

func injectCertManagerCA(obj metav1.Object, namespace string) error {
	annotations := obj.GetAnnotations()
	if _, ok := annotations[CertManagerInjectCaAnnotation]; !ok {
		return fmt.Errorf("%s is missing %s annotation", obj.GetName(), CertManagerInjectCaAnnotation)
	}
	injectAnnotation := annotations[CertManagerInjectCaAnnotation]
	injectAnnotation = strings.Replace(injectAnnotation, "CERTIFICATE_NAMESPACE", namespace, 1)
	injectAnnotation = strings.Replace(injectAnnotation, "CERTIFICATE_NAME", WebhookCertificateName, 1)
	annotations[CertManagerInjectCaAnnotation] = injectAnnotation
	obj.SetAnnotations(annotations)
	return nil
}
