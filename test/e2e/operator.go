package e2e

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/openshift/lws-operator/deploy"
	lwsoperatorv1 "github.com/openshift/lws-operator/pkg/apis/leaderworkersetoperator/v1"
	lwsoperatorv1clientset "github.com/openshift/lws-operator/pkg/generated/clientset/versioned/typed/leaderworkersetoperator/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

const (
	oteOperatorNamespace  = "openshift-lws-operator"
	oteOperandLabel       = "control-plane=controller-manager"
	oteOperandName        = "lws-controller-manager"
	oteOperatorDeployment = "openshift-lws-operator"
	oteNetworkPolicyName  = "lws-allow-operand"

	certManagerURL = "https://github.com/cert-manager/cert-manager/releases/download/v1.17.0/cert-manager.yaml"
)

//go:embed testdata/curl-test-pod.yaml
var curlPodTemplate string

//go:embed testdata/lws-webhook-test.yaml
var lwsWebhookTestYAML string

var (
	deployTmpDir         string
	certManagerInstalled bool
)

// Ginkgo test specs - calls the shared test functions
var _ = g.Describe("[Operator][Serial] LWS Operator", g.Ordered, func() {
	var (
		ctx        context.Context
		cancelFnc  context.CancelFunc
		kubeClient *k8sclient.Clientset
	)

	g.BeforeAll(func() {
		var err error
		ctx, cancelFnc, kubeClient, err = setupOperator(g.GinkgoTB())
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	g.AfterAll(func() {
		teardownOperator()
		cancelFnc()
	})

	g.It("should verify conditions are correct [Suite:openshift/lws-operator/operator/serial]", func() {
		testConditions(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should apply nodePlacement to operand deployment [Suite:openshift/lws-operator/operator/serial]", func() {
		testNodePlacement(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should recover operand pod after deletion [Suite:openshift/lws-operator/operator/serial]", func() {
		testPodDeleteRecovery(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should allow manual scaling when managementState is Unmanaged [Suite:openshift/lws-operator/operator/serial]", func() {
		testUnmanagedScaling(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should handle managementState Removed [Suite:openshift/lws-operator/operator/serial]", func() {
		testRemovedScaling(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should create NetworkPolicy with correct spec [Suite:openshift/lws-operator/operator/serial]", func() {
		expectNetworkPolicyValid(ctx, kubeClient, "NetworkPolicy should exist with correct spec")
	})

	g.It("should reconcile NetworkPolicy after mutation [Suite:openshift/lws-operator/operator/serial]", func() {
		netpolClient := kubeClient.NetworkingV1().NetworkPolicies(oteOperatorNamespace)

		klog.Infof("Patching webhook port from 9443 to 1234")
		patch := []byte(`[{"op": "replace", "path": "/spec/ingress/0/ports/0/port", "value": 1234}]`)
		_, err := netpolClient.Patch(ctx, oteNetworkPolicyName, types.JSONPatchType, patch, metav1.PatchOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to patch webhook port")

		expectNetworkPolicyValid(ctx, kubeClient, "operator should revert webhook port mutation")

		klog.Infof("Tampering monitoring namespace selector")
		patch = []byte(`[{"op": "replace", "path": "/spec/ingress/2/from/0/namespaceSelector/matchLabels", "value": {"kubernetes.io/metadata.name": "fake-namespace"}}]`)
		_, err = netpolClient.Patch(ctx, oteNetworkPolicyName, types.JSONPatchType, patch, metav1.PatchOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to patch monitoring selector")

		expectNetworkPolicyValid(ctx, kubeClient, "operator should revert monitoring selector mutation")

		klog.Infof("Removing all egress rules")
		patch = []byte(`[{"op": "replace", "path": "/spec/egress", "value": []}]`)
		_, err = netpolClient.Patch(ctx, oteNetworkPolicyName, types.JSONPatchType, patch, metav1.PatchOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to patch egress")

		expectNetworkPolicyValid(ctx, kubeClient, "operator should restore egress rules")
	})

	g.It("should recreate NetworkPolicy after deletion [Suite:openshift/lws-operator/operator/serial]", func() {
		netpolClient := kubeClient.NetworkingV1().NetworkPolicies(oteOperatorNamespace)

		klog.Infof("Deleting NetworkPolicy %s", oteNetworkPolicyName)
		err := netpolClient.Delete(ctx, oteNetworkPolicyName, metav1.DeleteOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to delete NetworkPolicy")

		expectNetworkPolicyValid(ctx, kubeClient, "operator should recreate NetworkPolicy after deletion")
	})

	g.It("should recover NetworkPolicy after config drift on operator restart [Suite:openshift/lws-operator/operator/serial]", func() {
		netpolClient := kubeClient.NetworkingV1().NetworkPolicies(oteOperatorNamespace)

		klog.Infof("Scaling down operator to 0 replicas")
		scaleDeployment(g.GinkgoTB(), ctx, kubeClient, oteOperatorDeployment, 0)
		verifyPodCount(g.GinkgoTB(), ctx, kubeClient, oteOperatorNamespace, "name="+oteOperatorDeployment, 0)

		klog.Infof("Wiping all ingress rules from NetworkPolicy")
		jsonPatch := []byte(`[{"op": "replace", "path": "/spec/ingress", "value": []}]`)
		_, err := netpolClient.Patch(ctx, oteNetworkPolicyName, types.JSONPatchType, jsonPatch, metav1.PatchOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to wipe ingress rules")

		netpol, err := netpolClient.Get(ctx, oteNetworkPolicyName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get NetworkPolicy after wipe")
		o.Expect(netpol.Spec.Ingress).To(o.BeEmpty(), "expected 0 ingress rules after wipe")

		klog.Infof("Scaling operator back up to 1 replica")
		scaleDeployment(g.GinkgoTB(), ctx, kubeClient, oteOperatorDeployment, 1)

		o.Eventually(func() error {
			deploy, err := kubeClient.AppsV1().Deployments(oteOperatorNamespace).Get(ctx, oteOperatorDeployment, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if deploy.Status.ReadyReplicas < 1 {
				return fmt.Errorf("operator not ready yet: %d ready replicas", deploy.Status.ReadyReplicas)
			}
			return nil
		}, 2*time.Minute, 2*time.Second).Should(o.Succeed(), "operator should become ready")

		expectNetworkPolicyValid(ctx, kubeClient, "operator should recover NetworkPolicy after restart")
	})

	g.It("should allow webhook traffic on port 9443 [Suite:openshift/lws-operator/operator/serial]", func() {
		operandPod, err := getOperandPod(ctx, kubeClient)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get operand pod")
		webhookURL := fmt.Sprintf("https://%s:9443", operandPod.Status.PodIP)

		code, err := runCurlPod(ctx, oteOperatorNamespace, webhookURL)
		o.Expect(err).NotTo(o.HaveOccurred(), "curl from same namespace failed")
		o.Expect(code).NotTo(o.Equal("000"), "webhook port 9443 blocked from same namespace")

		code, err = runCurlPod(ctx, "default", webhookURL)
		o.Expect(err).NotTo(o.HaveOccurred(), "curl from default namespace failed")
		o.Expect(code).NotTo(o.Equal("000"), "webhook port 9443 blocked from default namespace")
	})

	g.It("should allow webhook via kube-apiserver [Suite:openshift/lws-operator/operator/serial]", func() {
		klog.Infof("Creating LeaderWorkerSet to test webhook via kube-apiserver")
		lwsName, err := ocCreate(ctx, lwsWebhookTestYAML)
		defer func() {
			_ = runCommand("oc", "delete", "lws", lwsName, "-n", "default", "--ignore-not-found")
		}()
		o.Expect(err).NotTo(o.HaveOccurred(), "webhook rejected LeaderWorkerSet creation")
		klog.Infof("LeaderWorkerSet %s created successfully", lwsName)
	})

	g.It("should allow metrics from monitoring and block from random namespace [Suite:openshift/lws-operator/operator/serial]", func() {
		operandPod, err := getOperandPod(ctx, kubeClient)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get operand pod")
		metricsURL := fmt.Sprintf("https://%s:8443", operandPod.Status.PodIP)

		code, err := runCurlPod(ctx, "openshift-monitoring", metricsURL)
		o.Expect(err).NotTo(o.HaveOccurred(), "curl from openshift-monitoring failed")
		o.Expect(code).NotTo(o.Equal("000"), "metrics port 8443 blocked from openshift-monitoring")

		blockNS, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-netpol-e2e-"},
		}, metav1.CreateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create blocking namespace")
		defer func() {
			_ = kubeClient.CoreV1().Namespaces().Delete(ctx, blockNS.Name, metav1.DeleteOptions{})
		}()

		code, err = runCurlPod(ctx, blockNS.Name, metricsURL)
		o.Expect(err).NotTo(o.HaveOccurred(), "curl from %s failed", blockNS.Name)
		o.Expect(code).To(o.Equal("000"), "metrics port 8443 should be blocked from %s", blockNS.Name)
	})

	g.It("should block traffic on unlisted port [Suite:openshift/lws-operator/operator/serial]", func() {
		operandPod, err := getOperandPod(ctx, kubeClient)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get operand pod")

		code, err := runCurlPod(ctx, oteOperatorNamespace, fmt.Sprintf("https://%s:8080", operandPod.Status.PodIP))
		o.Expect(err).NotTo(o.HaveOccurred(), "curl for unlisted port failed")
		o.Expect(code).To(o.Equal("000"), "unlisted port 8080 should be blocked")
	})

	g.It("should allow egress from operand to API server [Suite:openshift/lws-operator/operator/serial]", func() {
		cmd := exec.CommandContext(ctx, "oc", "exec", "-n", oteOperatorNamespace,
			"deployment/"+oteOperandName, "--",
			"curl", "-sk", "--connect-timeout", "5", "-o", "/dev/null", "-w", "%{http_code}",
			"https://kubernetes.default.svc.cluster.local/healthz")
		out, err := cmd.CombinedOutput()
		o.Expect(err).NotTo(o.HaveOccurred(), "egress to API server failed: %s", string(out))
		o.Expect(strings.TrimSpace(string(out))).To(o.Equal("200"), "expected HTTP 200 from API server")
	})
})

// setupOperator installs cert-manager, deploys the operator, and waits for readiness.
func setupOperator(t testing.TB) (context.Context, context.CancelFunc, *k8sclient.Clientset, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	klog.Infof("Verifying required environment variables")
	if os.Getenv("KUBECONFIG") == "" {
		return nil, cancel, nil, fmt.Errorf("KUBECONFIG must be set")
	}

	if os.Getenv("OPERATOR_IMAGE") == "" && os.Getenv("RELATED_IMAGE_OPERAND_IMAGE") == "" {
		if os.Getenv("RELEASE_IMAGE_LATEST") == "" {
			return nil, cancel, nil, fmt.Errorf("RELEASE_IMAGE_LATEST must be set when OPERATOR_IMAGE and RELATED_IMAGE_OPERAND_IMAGE are not set")
		}
		if os.Getenv("NAMESPACE") == "" {
			return nil, cancel, nil, fmt.Errorf("NAMESPACE must be set when OPERATOR_IMAGE and RELATED_IMAGE_OPERAND_IMAGE are not set")
		}
	}

	var operatorImage string
	if os.Getenv("OPERATOR_IMAGE") != "" {
		operatorImage = os.Getenv("OPERATOR_IMAGE")
	} else {
		registry := strings.Split(os.Getenv("RELEASE_IMAGE_LATEST"), "/")[0]
		operatorImage = registry + "/" + os.Getenv("NAMESPACE") + "/pipeline:lws-operator"
	}
	klog.Infof("Using operator image: %s", operatorImage)

	var operandImage string
	if os.Getenv("RELATED_IMAGE_OPERAND_IMAGE") != "" {
		operandImage = os.Getenv("RELATED_IMAGE_OPERAND_IMAGE")
	} else {
		registry := strings.Split(os.Getenv("RELEASE_IMAGE_LATEST"), "/")[0]
		operandImage = registry + "/" + os.Getenv("NAMESPACE") + "/pipeline:kubernetes-sigs-lws"
	}
	klog.Infof("Using operand image: %s", operandImage)

	klog.Infof("Installing cert-manager")
	if err := runCommand("oc", "apply", "-f", certManagerURL); err != nil {
		return nil, cancel, nil, fmt.Errorf("failed to install cert-manager: %w", err)
	}
	certManagerInstalled = true
	if err := runCommand("oc", "-n", "cert-manager", "wait", "--for=condition=ready", "pod",
		"-l", "app.kubernetes.io/instance=cert-manager", "--timeout=2m"); err != nil {
		return nil, cancel, nil, fmt.Errorf("failed to wait for cert-manager: %w", err)
	}
	klog.Infof("Waiting for cert-manager-webhook deployment to be Available")
	if err := runCommand("oc", "wait", "deployment", "cert-manager-webhook",
		"-n", "cert-manager", "--for=condition=Available", "--timeout=5m"); err != nil {
		return nil, cancel, nil, fmt.Errorf("failed to wait for cert-manager-webhook availability: %w", err)
	}

	klog.Infof("Writing deploy manifests to temp directory")
	var err error
	deployTmpDir, err = os.MkdirTemp("", "lws-deploy-")
	if err != nil {
		return nil, cancel, nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	entries, err := deploy.Assets.ReadDir(".")
	if err != nil {
		return nil, cancel, nil, fmt.Errorf("failed to read deploy assets: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := deploy.Assets.ReadFile(entry.Name())
		if err != nil {
			return nil, cancel, nil, fmt.Errorf("failed to read asset %s: %w", entry.Name(), err)
		}

		content := string(data)
		content = strings.ReplaceAll(content, "${OPERATOR_IMAGE}", operatorImage)
		content = strings.ReplaceAll(content, "${OPERAND_IMAGE}", operandImage)

		if err := os.WriteFile(filepath.Join(deployTmpDir, entry.Name()), []byte(content), 0644); err != nil {
			return nil, cancel, nil, fmt.Errorf("failed to write asset %s: %w", entry.Name(), err)
		}
	}

	klog.Infof("Applying deploy manifests")
	err = wait.PollUntilContextTimeout(ctx, 1*time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		if applyErr := runCommand("oc", "apply", "-f", deployTmpDir, "--server-side"); applyErr != nil {
			klog.Infof("oc apply failed (will retry): %v", applyErr)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, cancel, nil, fmt.Errorf("failed to apply deploy manifests: %w", err)
	}

	klog.Infof("Waiting for operator deployment")
	if err := runCommand("oc", "wait", "deployment", oteOperatorDeployment,
		"-n", oteOperatorNamespace, "--for=create", "--timeout=2m"); err != nil {
		return nil, cancel, nil, fmt.Errorf("failed waiting for operator deployment creation: %w", err)
	}
	if err := runCommand("oc", "wait", "deployment", oteOperatorDeployment,
		"-n", oteOperatorNamespace, "--for=condition=Available", "--timeout=5m"); err != nil {
		return nil, cancel, nil, fmt.Errorf("failed waiting for operator deployment availability: %w", err)
	}

	klog.Infof("Waiting for operand deployment")
	if err := runCommand("oc", "wait", "deployment", oteOperandName,
		"-n", oteOperatorNamespace, "--for=create", "--timeout=2m"); err != nil {
		return nil, cancel, nil, fmt.Errorf("failed waiting for operand deployment creation: %w", err)
	}
	if err := runCommand("oc", "wait", "deployment", oteOperandName,
		"-n", oteOperatorNamespace, "--for=condition=Available", "--timeout=5m"); err != nil {
		return nil, cancel, nil, fmt.Errorf("failed waiting for operand deployment availability: %w", err)
	}

	klog.Infof("Operator and operand are ready")
	kubeClient := GetKubeClient()
	return ctx, cancel, kubeClient, nil
}

func teardownOperator() {
	if deployTmpDir != "" {
		klog.Infof("Deleting deployed operator resources")
		if err := runCommand("oc", "delete", "-f", deployTmpDir, "--ignore-not-found"); err != nil {
			klog.Warningf("Failed to delete deploy manifests: %v", err)
		}

		klog.Infof("Waiting for namespace %s to be deleted", oteOperatorNamespace)
		_ = runCommand("oc", "wait", "namespace", oteOperatorNamespace, "--for=delete", "--timeout=2m")

		_ = os.RemoveAll(deployTmpDir)
		deployTmpDir = ""
	}

	if certManagerInstalled {
		klog.Infof("Deleting cert-manager")
		if err := runCommand("oc", "delete", "-f", certManagerURL, "--ignore-not-found"); err != nil {
			klog.Warningf("Failed to delete cert-manager: %v", err)
		}
		certManagerInstalled = false
	}
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %v\n%s", name, args, err, string(out))
	}
	return nil
}

// ocCreate writes yaml to a temp file, runs "oc create -f", and returns the
// created resource name parsed from oc output (e.g. "pod/foo created" → "foo").
func ocCreate(ctx context.Context, yaml string) (string, error) {
	tmpFile, err := os.CreateTemp("", "e2e-*.yaml")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.WriteString(yaml); err != nil {
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "oc", "create", "-f", tmpFile.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("oc create failed: %v\n%s", err, string(out))
	}
	name := strings.TrimSpace(string(out))
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.TrimSuffix(name, " created"), nil
}

// runCurlPod creates a curl test pod and returns the HTTP status code from its logs.
func runCurlPod(ctx context.Context, namespace, targetURL string) (string, error) {
	yaml := strings.ReplaceAll(curlPodTemplate, "{{NAMESPACE}}", namespace)
	yaml = strings.ReplaceAll(yaml, "{{TARGET_URL}}", targetURL)

	podName, err := ocCreate(ctx, yaml)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = runCommand("oc", "delete", "pod", podName, "-n", namespace, "--ignore-not-found")
	}()

	// Wait for pod completion (Succeeded or Failed — both are valid terminal states)
	_ = runCommand("oc", "wait", "pod", podName, "-n", namespace,
		"--for=jsonpath={.status.phase}=Succeeded", "--timeout=60s")
	_ = runCommand("oc", "wait", "pod", podName, "-n", namespace,
		"--for=jsonpath={.status.phase}=Failed", "--timeout=10s")

	cmd := exec.CommandContext(ctx, "oc", "logs", podName, "-n", namespace)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get curl pod logs: %v\n%s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// testConditions verifies that the operator conditions are correct.
func testConditions(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	lwsOperatorClient := GetLWSOperatorClient()
	o.Eventually(func() error {
		lwsOperators, err := lwsOperatorClient.List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list LWSOperators: %v", err)
		}
		if len(lwsOperators.Items) != 1 {
			return fmt.Errorf("unexpected number of LWSOperators %d", len(lwsOperators.Items))
		}

		for _, condition := range lwsOperators.Items[0].Status.Conditions {
			if strings.HasSuffix(condition.Type, operatorv1.OperatorStatusTypeDegraded) && condition.Status == operatorv1.ConditionTrue {
				return fmt.Errorf("degraded condition exists: %+v", lwsOperators.Items[0].Status.Conditions)
			}
		}

		cond := v1helpers.FindOperatorCondition(lwsOperators.Items[0].Status.Conditions, operatorv1.OperatorStatusTypeAvailable)
		if cond == nil || cond.Status != operatorv1.ConditionTrue {
			return fmt.Errorf("LWS operator is not available")
		}
		return nil
	}, 5*time.Minute, 5*time.Second).Should(o.Succeed(), "operator should be available with no degraded conditions")
}

// testNodePlacement verifies that nodePlacement is applied to the operand deployment.
func testNodePlacement(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	lwsOperatorClient := GetLWSOperatorClient()

	lwsOperator, _, err := getOperatorState(ctx, lwsOperatorClient)
	if err != nil {
		t.Fatalf("Failed to get operator state: %v", err)
	}

	nodeSelector := map[string]string{
		"e2e.lws.openshift.io/node-placement": "test",
	}
	tolerations := []corev1.Toleration{
		{
			Key:      "e2e.lws.openshift.io/node-placement",
			Operator: corev1.TolerationOpEqual,
			Value:    "test",
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}
	nodePlacement := &lwsoperatorv1.NodePlacement{
		NodeSelector: nodeSelector,
		Tolerations:  tolerations,
	}

	defer func() {
		setNodePlacement(t, ctx, lwsOperatorClient, lwsOperator, nil)
		verifyDeploymentNodePlacement(t, ctx, kubeClient, oteOperandName, nil, nil)
	}()

	setNodePlacement(t, ctx, lwsOperatorClient, lwsOperator, nodePlacement)
	verifyDeploymentNodePlacement(t, ctx, kubeClient, oteOperandName, nodeSelector, tolerations)
}

// testPodDeleteRecovery verifies that the operand pod recovers after deletion.
func testPodDeleteRecovery(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	pods, err := kubeClient.CoreV1().Pods(oteOperatorNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: oteOperandLabel,
	})
	if err != nil {
		t.Fatalf("Failed to list operand pods: %v", err)
	}
	if len(pods.Items) == 0 {
		t.Fatalf("No operand pods found")
	}

	err = kubeClient.CoreV1().Pods(oteOperatorNamespace).DeleteCollection(
		ctx,
		metav1.DeleteOptions{
			GracePeriodSeconds: ptr.To[int64](30),
		},
		metav1.ListOptions{
			LabelSelector: oteOperandLabel,
		},
	)
	if err != nil {
		t.Fatalf("Failed to delete operand pods: %v", err)
	}

	err = wait.PollUntilContextTimeout(ctx, 2*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		newPods, err := kubeClient.CoreV1().Pods(oteOperatorNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: oteOperandLabel,
		})
		if err != nil {
			return false, err
		}

		activePods := make([]corev1.Pod, 0)
		for _, pod := range newPods.Items {
			if pod.DeletionTimestamp == nil {
				activePods = append(activePods, pod)
			}
		}
		if len(activePods) == 0 {
			return false, nil
		}
		for _, pod := range activePods {
			if pod.Status.Phase != corev1.PodRunning {
				klog.Infof("Pod %s status: %s", pod.Name, pod.Status.Phase)
				return false, nil
			}
			klog.Infof("Pod %s is Running", pod.Name)
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("Failed waiting for operand pod recovery: %v", err)
	}
}

// testUnmanagedScaling verifies manual scaling works when managementState is Unmanaged.
func testUnmanagedScaling(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	lwsOperatorClient := GetLWSOperatorClient()

	lwsOperator, originalState, err := getOperatorState(ctx, lwsOperatorClient)
	if err != nil {
		t.Fatalf("Failed to get operator state: %v", err)
	}
	originalPodCount := getPodCount(ctx, kubeClient, oteOperatorNamespace, oteOperandLabel)
	if originalPodCount < 0 {
		t.Fatalf("Failed to read initial operand pod count")
	}

	defer func() {
		setManagementState(t, ctx, lwsOperatorClient, lwsOperator, originalState)
		waitForManagementState(t, ctx, lwsOperatorClient, originalState)
		if originalState == "" || originalState == operatorv1.Managed {
			verifyDeploymentReplicas(t, ctx, kubeClient, oteOperandName, int32(originalPodCount))
		}
		verifyPodCount(t, ctx, kubeClient, oteOperatorNamespace, oteOperandLabel, originalPodCount)
	}()

	klog.Infof("Setting managementState to Unmanaged")
	setManagementState(t, ctx, lwsOperatorClient, lwsOperator, operatorv1.Unmanaged)
	waitForManagementState(t, ctx, lwsOperatorClient, operatorv1.Unmanaged)

	klog.Infof("Scaling up to 3 replicas")
	scaleDeployment(t, ctx, kubeClient, oteOperandName, 3)
	verifyDeploymentReplicas(t, ctx, kubeClient, oteOperandName, 3)
	verifyPodCount(t, ctx, kubeClient, oteOperatorNamespace, oteOperandLabel, 3)
}

// testRemovedScaling verifies behavior when managementState is Removed.
func testRemovedScaling(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	lwsOperatorClient := GetLWSOperatorClient()

	lwsOperator, originalState, err := getOperatorState(ctx, lwsOperatorClient)
	if err != nil {
		t.Fatalf("Failed to get operator state: %v", err)
	}
	originalPodCount := getPodCount(ctx, kubeClient, oteOperatorNamespace, oteOperandLabel)
	if originalPodCount < 0 {
		t.Fatalf("Failed to read initial operand pod count")
	}

	defer func() {
		newctx := context.TODO()
		setManagementState(t, newctx, lwsOperatorClient, lwsOperator, originalState)
		waitForManagementState(t, newctx, lwsOperatorClient, originalState)
		if originalState == "" || originalState == operatorv1.Managed {
			verifyDeploymentReplicas(t, newctx, kubeClient, oteOperandName, int32(originalPodCount))
		}
		verifyPodCount(t, newctx, kubeClient, oteOperatorNamespace, oteOperandLabel, originalPodCount)
	}()

	klog.Infof("Setting managementState to Removed")
	setManagementState(t, ctx, lwsOperatorClient, lwsOperator, operatorv1.Removed)
	waitForManagementState(t, ctx, lwsOperatorClient, operatorv1.Removed)

	klog.Infof("Scaling up to 3 replicas")
	scaleDeployment(t, ctx, kubeClient, oteOperandName, 3)
	verifyDeploymentReplicas(t, ctx, kubeClient, oteOperandName, 3)
	verifyPodCount(t, ctx, kubeClient, oteOperatorNamespace, oteOperandLabel, 3)
}

// Helper functions

func getOperatorState(ctx context.Context, lwsOperatorClient lwsoperatorv1clientset.LeaderWorkerSetOperatorInterface) (*lwsoperatorv1.LeaderWorkerSetOperator, operatorv1.ManagementState, error) {
	lwsOperator, err := lwsOperatorClient.Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("failed to get operator: %w", err)
	}
	return lwsOperator, lwsOperator.Spec.ManagementState, nil
}

func setManagementState(t testing.TB, ctx context.Context, lwsOperatorClient lwsoperatorv1clientset.LeaderWorkerSetOperatorInterface, operator *lwsoperatorv1.LeaderWorkerSetOperator, state operatorv1.ManagementState) {
	t.Helper()
	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		current, getErr := lwsOperatorClient.Get(ctx, operator.Name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		current.Spec.ManagementState = state
		_, updateErr := lwsOperatorClient.Update(ctx, current, metav1.UpdateOptions{})
		return updateErr
	})
	if retryErr != nil {
		t.Fatalf("Failed to set management state to %s: %v", state, retryErr)
	}
}

func waitForManagementState(t testing.TB, ctx context.Context, lwsOperatorClient lwsoperatorv1clientset.LeaderWorkerSetOperatorInterface, state operatorv1.ManagementState) {
	t.Helper()
	o.Eventually(func() operatorv1.ManagementState {
		lwsOperator, err := lwsOperatorClient.Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			klog.Errorf("GetOperatorState error: %v", err)
			return ""
		}
		return lwsOperator.Spec.ManagementState
	}, 2*time.Minute, 2*time.Second).Should(
		o.Equal(state),
		"managementState should become %q", state)
}

func scaleDeployment(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset, operandName string, replicas int32) {
	t.Helper()
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err := kubeClient.AppsV1().Deployments(oteOperatorNamespace).Patch(
		ctx,
		operandName,
		types.StrategicMergePatchType,
		[]byte(patch),
		metav1.PatchOptions{})
	if err != nil {
		t.Fatalf("Failed to scale deployment %s to %d replicas: %v", operandName, replicas, err)
	}
}

func verifyDeploymentReplicas(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset, deploymentName string, expected int32) {
	t.Helper()
	o.Eventually(func() int32 {
		deployment, err := kubeClient.AppsV1().Deployments(oteOperatorNamespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("deployment get error: %v", err)
			return -1
		}
		if deployment.Spec.Replicas == nil {
			return -1
		}
		return *deployment.Spec.Replicas
	}, 2*time.Minute, 2*time.Second).Should(
		o.Equal(expected),
		"deployment %q replicas should reach %d", deploymentName, expected)
}

func verifyPodCount(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset, namespace, labelSelector string, expected int) {
	t.Helper()
	o.Eventually(func() int {
		return getPodCount(ctx, kubeClient, namespace, labelSelector)
	}, 5*time.Minute, 10*time.Second).Should(
		o.Equal(expected),
		"Pod count should reach %d", expected)
}

func getPodCount(ctx context.Context, kubeClient *k8sclient.Clientset, namespace, labelSelector string) int {
	pods, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		klog.Errorf("Pod list error: %v\n", err)
		return -1
	}
	return len(pods.Items)
}

func getOperandPod(ctx context.Context, kubeClient *k8sclient.Clientset) (*corev1.Pod, error) {
	pods, err := kubeClient.CoreV1().Pods(oteOperatorNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=lws," + oteOperandLabel,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list operand pods: %v", err)
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp == nil && p.Status.Phase == corev1.PodRunning && p.Status.PodIP != "" {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no operand pod found in Running state with an assigned IP")
}

func setNodePlacement(t testing.TB, ctx context.Context, lwsOperatorClient lwsoperatorv1clientset.LeaderWorkerSetOperatorInterface, operator *lwsoperatorv1.LeaderWorkerSetOperator, nodePlacement *lwsoperatorv1.NodePlacement) {
	t.Helper()
	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		current, getErr := lwsOperatorClient.Get(ctx, operator.Name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		current.Spec.NodePlacement = nodePlacement
		_, updateErr := lwsOperatorClient.Update(ctx, current, metav1.UpdateOptions{})
		return updateErr
	})
	if retryErr != nil {
		t.Fatalf("Failed to update operator nodePlacement: %v", retryErr)
	}
}

func verifyDeploymentNodePlacement(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset, deploymentName string, expectedSelector map[string]string, expectedTolerations []corev1.Toleration) {
	t.Helper()
	o.Eventually(func() error {
		deployment, err := kubeClient.AppsV1().Deployments(oteOperatorNamespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		return compareDeploymentNodePlacement(deployment, expectedSelector, expectedTolerations)
	}, 5*time.Minute, 10*time.Second).Should(o.Succeed(), "deployment nodePlacement should match operator spec")
}

func expectNetworkPolicyValid(ctx context.Context, kubeClient *k8sclient.Clientset, msg string) {
	o.Eventually(func() error {
		netpol, err := kubeClient.NetworkingV1().NetworkPolicies(oteOperatorNamespace).Get(ctx, oteNetworkPolicyName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		return validateNetworkPolicySpec(netpol)
	}, 1*time.Minute, 2*time.Second).Should(o.Succeed(), msg)
}

func validateNetworkPolicySpec(netpol *networkingv1.NetworkPolicy) error {
	sel := netpol.Spec.PodSelector
	if sel.MatchLabels["app.kubernetes.io/name"] != "lws" || sel.MatchLabels["control-plane"] != "controller-manager" {
		return fmt.Errorf("unexpected podSelector labels: %v", sel.MatchLabels)
	}
	if len(netpol.Spec.Ingress) < 2 {
		return fmt.Errorf("expected at least 2 ingress rules, got %d", len(netpol.Spec.Ingress))
	}
	if len(netpol.Spec.Ingress[0].Ports) != 1 || netpol.Spec.Ingress[0].Ports[0].Port.IntValue() != 9443 {
		return fmt.Errorf("webhook ingress rule: expected single port 9443, got %v", netpol.Spec.Ingress[0].Ports)
	}
	monitoringRule := netpol.Spec.Ingress[len(netpol.Spec.Ingress)-1]
	if len(monitoringRule.Ports) != 1 || monitoringRule.Ports[0].Port.IntValue() != 8443 {
		return fmt.Errorf("monitoring ingress rule: expected single port 8443, got %v", monitoringRule.Ports)
	}
	hasMonitoringSelector := false
	for _, from := range monitoringRule.From {
		if from.NamespaceSelector != nil && from.NamespaceSelector.MatchLabels["openshift.io/cluster-monitoring"] == "true" {
			hasMonitoringSelector = true
		}
	}
	if !hasMonitoringSelector {
		return fmt.Errorf("monitoring rule: missing openshift.io/cluster-monitoring namespace selector")
	}
	if len(netpol.Spec.Egress) != 1 || len(netpol.Spec.Egress[0].Ports) != 0 || len(netpol.Spec.Egress[0].To) != 0 {
		return fmt.Errorf("expected single unrestricted egress rule, got %v", netpol.Spec.Egress)
	}
	policyTypes := fmt.Sprintf("%v", netpol.Spec.PolicyTypes)
	if !strings.Contains(policyTypes, "Ingress") || !strings.Contains(policyTypes, "Egress") {
		return fmt.Errorf("policyTypes must include Ingress and Egress, got %v", netpol.Spec.PolicyTypes)
	}
	return nil
}

func compareDeploymentNodePlacement(deployment *appsv1.Deployment, expectedSelector map[string]string, expectedTolerations []corev1.Toleration) error {
	podSpec := deployment.Spec.Template.Spec

	if expectedSelector == nil {
		if len(podSpec.NodeSelector) != 0 {
			return fmt.Errorf("nodeSelector: got %v want empty", podSpec.NodeSelector)
		}
	} else {
		for key, value := range expectedSelector {
			if podSpec.NodeSelector[key] != value {
				return fmt.Errorf("nodeSelector %q: got %q want %q", key, podSpec.NodeSelector[key], value)
			}
		}
		for key := range podSpec.NodeSelector {
			if _, ok := expectedSelector[key]; !ok {
				return fmt.Errorf("unexpected nodeSelector key %q", key)
			}
		}
	}

	if expectedTolerations == nil {
		if len(podSpec.Tolerations) != 0 {
			return fmt.Errorf("tolerations: got %v want empty", podSpec.Tolerations)
		}
	} else if len(podSpec.Tolerations) != len(expectedTolerations) {
		return fmt.Errorf("tolerations: got %d want %d", len(podSpec.Tolerations), len(expectedTolerations))
	} else {
		for i := range expectedTolerations {
			got := podSpec.Tolerations[i]
			want := expectedTolerations[i]
			if got.Key != want.Key || got.Operator != want.Operator || got.Value != want.Value || got.Effect != want.Effect {
				return fmt.Errorf("toleration[%d]: got %+v want %+v", i, got, want)
			}
			if (got.TolerationSeconds == nil) != (want.TolerationSeconds == nil) {
				return fmt.Errorf("toleration[%d] TolerationSeconds mismatch: got %v want %v", i, got.TolerationSeconds, want.TolerationSeconds)
			}
			if got.TolerationSeconds != nil && want.TolerationSeconds != nil && *got.TolerationSeconds != *want.TolerationSeconds {
				return fmt.Errorf("toleration[%d] TolerationSeconds: got %d want %d", i, *got.TolerationSeconds, *want.TolerationSeconds)
			}
		}
	}
	return nil
}
