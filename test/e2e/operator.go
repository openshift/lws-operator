package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

var (
	netpolTestPodName    = fmt.Sprintf("netpol-e2e-test-%s", randomString(5))
	netpolBlockNamespace = fmt.Sprintf("test-netpol-e2e-%s", randomString(5))
	netpolLWSName        = fmt.Sprintf("netpol-e2e-webhook-%s", randomString(5))
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
		testNetworkPolicyExists(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should reconcile NetworkPolicy after mutation [Suite:openshift/lws-operator/operator/serial]", func() {
		testNetworkPolicyReconciliation(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should recreate NetworkPolicy after deletion [Suite:openshift/lws-operator/operator/serial]", func() {
		testNetworkPolicyDeletion(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should recover NetworkPolicy after config drift on operator restart [Suite:openshift/lws-operator/operator/serial]", func() {
		testNetworkPolicyDriftRecovery(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should allow webhook traffic on port 9443 [Suite:openshift/lws-operator/operator/serial]", func() {
		testNetworkPolicyWebhookAccess(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should allow webhook via kube-apiserver [Suite:openshift/lws-operator/operator/serial]", func() {
		testNetworkPolicyWebhookViaAPIServer(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should allow metrics from monitoring and block from random namespace [Suite:openshift/lws-operator/operator/serial]", func() {
		testNetworkPolicyMetricsAccess(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should block traffic on unlisted port [Suite:openshift/lws-operator/operator/serial]", func() {
		testNetworkPolicyUnlistedPortBlocked(g.GinkgoTB(), ctx, kubeClient)
	})

	g.It("should allow egress from operand to API server [Suite:openshift/lws-operator/operator/serial]", func() {
		testNetworkPolicyOperandEgress(g.GinkgoTB(), ctx, kubeClient)
	})
})

// setupOperator installs cert-manager, deploys the operator, and waits for readiness.
// This function works with both standard Go testing and Ginkgo.
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

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func runCommandOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("%s %v: %v\nstderr: %s", name, args, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
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

func runCurlTestPod(ctx context.Context, kubeClient *k8sclient.Clientset, namespace, targetURL string) (string, error) {
	podClient := kubeClient.CoreV1().Pods(namespace)

	err := podClient.Delete(ctx, netpolTestPodName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("failed to delete stale test pod: %v", err)
	}
	if err = wait.PollUntilContextTimeout(ctx, 1*time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		_, getErr := podClient.Get(ctx, netpolTestPodName, metav1.GetOptions{})
		if getErr == nil {
			return false, nil
		}
		if apierrors.IsNotFound(getErr) {
			return true, nil
		}
		return false, getErr
	}); err != nil {
		return "", fmt.Errorf("failed waiting for stale test pod deletion: %v", err)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      netpolTestPodName,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: ptr.To(true),
				RunAsUser:    ptr.To(int64(1000)),
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "curl",
					Image:   "curlimages/curl",
					Command: []string{"curl", "-sk", "--connect-timeout", "5", "-o", "/dev/null", "-w", "%{http_code}", targetURL},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: ptr.To(false),
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
				},
			},
		},
	}

	_, err = podClient.Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create curl test pod: %v", err)
	}
	defer func() {
		if delErr := podClient.Delete(ctx, netpolTestPodName, metav1.DeleteOptions{}); delErr != nil && !apierrors.IsNotFound(delErr) {
			klog.Errorf("Failed to clean up test pod %s: %v", netpolTestPodName, delErr)
		}
	}()

	err = wait.PollUntilContextTimeout(ctx, 2*time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		p, err := podClient.Get(ctx, netpolTestPodName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed, nil
	})
	if err != nil {
		return "", fmt.Errorf("curl test pod did not complete: %v", err)
	}

	req := podClient.GetLogs(netpolTestPodName, &corev1.PodLogOptions{})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get pod logs: %v", err)
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			klog.Errorf("Failed to close log stream: %v", closeErr)
		}
	}()

	body, err := io.ReadAll(stream)
	if err != nil {
		return "", fmt.Errorf("failed to read pod logs: %v", err)
	}

	return strings.TrimSpace(string(body)), nil
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

// testNetworkPolicyExists verifies that the NetworkPolicy exists with the correct spec.
func testNetworkPolicyExists(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	o.Eventually(func() error {
		netpol, err := kubeClient.NetworkingV1().NetworkPolicies(oteOperatorNamespace).Get(ctx, oteNetworkPolicyName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get NetworkPolicy: %v", err)
		}
		return validateNetworkPolicySpec(netpol)
	}, 1*time.Minute, 5*time.Second).Should(o.Succeed(), "NetworkPolicy should exist with correct spec")
}

// testNetworkPolicyReconciliation verifies that the operator reverts mutations to the NetworkPolicy.
func testNetworkPolicyReconciliation(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	netpolClient := kubeClient.NetworkingV1().NetworkPolicies(oteOperatorNamespace)

	klog.Infof("Patching webhook port from 9443 to 1234")
	patch := []byte(`[{"op": "replace", "path": "/spec/ingress/0/ports/0/port", "value": 1234}]`)
	_, err := netpolClient.Patch(ctx, oteNetworkPolicyName, types.JSONPatchType, patch, metav1.PatchOptions{})
	if err != nil {
		t.Fatalf("Failed to patch NetworkPolicy webhook port: %v", err)
	}

	o.Eventually(func() error {
		netpol, err := netpolClient.Get(ctx, oteNetworkPolicyName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get NetworkPolicy: %v", err)
		}
		return validateNetworkPolicySpec(netpol)
	}, 1*time.Minute, 2*time.Second).Should(o.Succeed(), "operator should revert webhook port mutation")

	klog.Infof("Tampering monitoring namespace selector")
	patch = []byte(`[{"op": "replace", "path": "/spec/ingress/2/from/0/namespaceSelector/matchLabels", "value": {"kubernetes.io/metadata.name": "fake-namespace"}}]`)
	_, err = netpolClient.Patch(ctx, oteNetworkPolicyName, types.JSONPatchType, patch, metav1.PatchOptions{})
	if err != nil {
		t.Fatalf("Failed to patch NetworkPolicy monitoring selector: %v", err)
	}

	o.Eventually(func() error {
		netpol, err := netpolClient.Get(ctx, oteNetworkPolicyName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get NetworkPolicy: %v", err)
		}
		return validateNetworkPolicySpec(netpol)
	}, 1*time.Minute, 2*time.Second).Should(o.Succeed(), "operator should revert monitoring selector mutation")

	klog.Infof("Removing all egress rules")
	patch = []byte(`[{"op": "replace", "path": "/spec/egress", "value": []}]`)
	_, err = netpolClient.Patch(ctx, oteNetworkPolicyName, types.JSONPatchType, patch, metav1.PatchOptions{})
	if err != nil {
		t.Fatalf("Failed to patch NetworkPolicy egress: %v", err)
	}

	o.Eventually(func() error {
		netpol, err := netpolClient.Get(ctx, oteNetworkPolicyName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get NetworkPolicy: %v", err)
		}
		return validateNetworkPolicySpec(netpol)
	}, 1*time.Minute, 2*time.Second).Should(o.Succeed(), "operator should restore egress rules")
}

// testNetworkPolicyDeletion verifies that the operator recreates the NetworkPolicy after deletion.
func testNetworkPolicyDeletion(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	netpolClient := kubeClient.NetworkingV1().NetworkPolicies(oteOperatorNamespace)

	klog.Infof("Deleting NetworkPolicy %s", oteNetworkPolicyName)
	err := netpolClient.Delete(ctx, oteNetworkPolicyName, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete NetworkPolicy: %v", err)
	}

	o.Eventually(func() error {
		netpol, err := netpolClient.Get(ctx, oteNetworkPolicyName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("NetworkPolicy not yet recreated: %v", err)
		}
		return validateNetworkPolicySpec(netpol)
	}, 1*time.Minute, 2*time.Second).Should(o.Succeed(), "operator should recreate NetworkPolicy after deletion")
}

// testNetworkPolicyDriftRecovery verifies that the operator recovers the NetworkPolicy
// after being scaled down, the policy is tampered, and the operator is scaled back up.
func testNetworkPolicyDriftRecovery(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	netpolClient := kubeClient.NetworkingV1().NetworkPolicies(oteOperatorNamespace)

	klog.Infof("Scaling down operator to 0 replicas")
	scaleDeployment(t, ctx, kubeClient, oteOperatorDeployment, 0)
	verifyPodCount(t, ctx, kubeClient, oteOperatorNamespace, "name="+oteOperatorDeployment, 0)

	klog.Infof("Wiping all ingress rules from NetworkPolicy")
	jsonPatch := []byte(`[{"op": "replace", "path": "/spec/ingress", "value": []}]`)
	_, err := netpolClient.Patch(ctx, oteNetworkPolicyName, types.JSONPatchType, jsonPatch, metav1.PatchOptions{})
	if err != nil {
		t.Fatalf("Failed to wipe NetworkPolicy ingress: %v", err)
	}

	netpol, err := netpolClient.Get(ctx, oteNetworkPolicyName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get NetworkPolicy after wipe: %v", err)
	}
	if len(netpol.Spec.Ingress) != 0 {
		t.Fatalf("Expected 0 ingress rules after wipe, got %d", len(netpol.Spec.Ingress))
	}

	klog.Infof("Scaling operator back up to 1 replica")
	scaleDeployment(t, ctx, kubeClient, oteOperatorDeployment, 1)

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

	o.Eventually(func() error {
		netpol, err := netpolClient.Get(ctx, oteNetworkPolicyName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get NetworkPolicy: %v", err)
		}
		return validateNetworkPolicySpec(netpol)
	}, 1*time.Minute, 2*time.Second).Should(o.Succeed(), "operator should recover NetworkPolicy after restart")
}

// validateNetworkPolicySpec checks that the NetworkPolicy has the expected structure:
// - Webhook ingress on port 9443 (open to all)
// - Metrics ingress on port 8443 from monitoring namespaces
// - All egress allowed
func validateNetworkPolicySpec(netpol *networkingv1.NetworkPolicy) error {
	expectedLabels := map[string]string{
		"app.kubernetes.io/name": "lws",
		"control-plane":          "controller-manager",
	}
	selector := netpol.Spec.PodSelector
	if len(selector.MatchLabels) != len(expectedLabels) {
		return fmt.Errorf("podSelector: expected %d labels, got %d: %v", len(expectedLabels), len(selector.MatchLabels), selector.MatchLabels)
	}
	for k, v := range expectedLabels {
		if actual, ok := selector.MatchLabels[k]; !ok || actual != v {
			return fmt.Errorf("podSelector: expected %s=%s, got %s=%s", k, v, k, actual)
		}
	}
	if len(selector.MatchExpressions) != 0 {
		return fmt.Errorf("podSelector: unexpected matchExpressions: %v", selector.MatchExpressions)
	}

	if len(netpol.Spec.Ingress) < 2 {
		return fmt.Errorf("expected at least 2 ingress rules, got %d", len(netpol.Spec.Ingress))
	}

	webhookRule := netpol.Spec.Ingress[0]
	if len(webhookRule.Ports) != 1 {
		return fmt.Errorf("webhook rule: expected 1 port, got %d", len(webhookRule.Ports))
	}
	if webhookRule.Ports[0].Port.IntValue() != 9443 {
		return fmt.Errorf("webhook port: expected 9443, got %d", webhookRule.Ports[0].Port.IntValue())
	}

	monitoringIdx := len(netpol.Spec.Ingress) - 1
	monitoringRule := netpol.Spec.Ingress[monitoringIdx]
	if len(monitoringRule.Ports) != 1 {
		return fmt.Errorf("monitoring rule: expected 1 port, got %d", len(monitoringRule.Ports))
	}
	if monitoringRule.Ports[0].Port.IntValue() != 8443 {
		return fmt.Errorf("monitoring port: expected 8443, got %d", monitoringRule.Ports[0].Port.IntValue())
	}
	foundClusterMonitoring := false
	for _, from := range monitoringRule.From {
		if from.NamespaceSelector != nil {
			if val, ok := from.NamespaceSelector.MatchLabels["openshift.io/cluster-monitoring"]; ok && val == "true" {
				foundClusterMonitoring = true
			}
		}
	}
	if !foundClusterMonitoring {
		return fmt.Errorf("monitoring rule: missing openshift.io/cluster-monitoring selector")
	}

	if len(netpol.Spec.Egress) != 1 {
		return fmt.Errorf("expected exactly 1 egress rule, got %d", len(netpol.Spec.Egress))
	}
	egressRule := netpol.Spec.Egress[0]
	if len(egressRule.Ports) != 0 || len(egressRule.To) != 0 {
		return fmt.Errorf("egress rule must be unrestricted (empty), got ports=%d to=%d", len(egressRule.Ports), len(egressRule.To))
	}

	hasIngress, hasEgress := false, false
	for _, pt := range netpol.Spec.PolicyTypes {
		if pt == networkingv1.PolicyTypeIngress {
			hasIngress = true
		}
		if pt == networkingv1.PolicyTypeEgress {
			hasEgress = true
		}
	}
	if !hasIngress {
		return fmt.Errorf("policyTypes missing Ingress")
	}
	if !hasEgress {
		return fmt.Errorf("policyTypes missing Egress")
	}

	return nil
}

// testNetworkPolicyWebhookAccess verifies webhook port 9443 is reachable from
// the same namespace and from a different namespace.
func testNetworkPolicyWebhookAccess(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	operandPod, err := getOperandPod(ctx, kubeClient)
	if err != nil {
		t.Fatalf("Failed to get operand pod: %v", err)
	}
	podIP := operandPod.Status.PodIP
	webhookURL := fmt.Sprintf("https://%s:9443", podIP)

	klog.Infof("Testing webhook access from same namespace (%s)", oteOperatorNamespace)
	code, err := runCurlTestPod(ctx, kubeClient, oteOperatorNamespace, webhookURL)
	if err != nil {
		t.Fatalf("Failed to run curl test from same namespace: %v", err)
	}
	if code == "000" {
		t.Fatalf("Webhook port 9443 blocked from same namespace: got %s, expected HTTP response", code)
	}
	klog.Infof("Webhook from same namespace: HTTP %s", code)

	klog.Infof("Testing webhook access from default namespace")
	code, err = runCurlTestPod(ctx, kubeClient, "default", webhookURL)
	if err != nil {
		t.Fatalf("Failed to run curl test from default namespace: %v", err)
	}
	if code == "000" {
		t.Fatalf("Webhook port 9443 blocked from default namespace: got %s, expected HTTP response", code)
	}
	klog.Infof("Webhook from default namespace: HTTP %s", code)
}

// testNetworkPolicyWebhookViaAPIServer verifies the webhook works end-to-end by
// creating a LeaderWorkerSet resource through kube-apiserver.
func testNetworkPolicyWebhookViaAPIServer(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()

	lwsYAML := fmt.Sprintf(`apiVersion: leaderworkerset.x-k8s.io/v1
kind: LeaderWorkerSet
metadata:
  name: %s
  namespace: default
spec:
  replicas: 1
  leaderWorkerTemplate:
    size: 1
    leaderTemplate:
      metadata:
        labels:
          app: netpol-e2e-test
      spec:
        containers:
        - name: leader
          image: registry.access.redhat.com/ubi9/ubi-minimal:latest
          command: ["sleep", "10"]
    workerTemplate:
      spec:
        containers:
        - name: worker
          image: registry.access.redhat.com/ubi9/ubi-minimal:latest
          command: ["sleep", "10"]
`, netpolLWSName)
	tmpFile, err := os.CreateTemp("", "lws-netpol-test-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		if removeErr := os.Remove(tmpFile.Name()); removeErr != nil {
			klog.Errorf("Failed to remove temp file %s: %v", tmpFile.Name(), removeErr)
		}
	}()

	if _, err := tmpFile.WriteString(lwsYAML); err != nil {
		t.Fatalf("Failed to write LWS YAML: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	klog.Infof("Creating LeaderWorkerSet to test webhook via kube-apiserver")
	err = runCommand("oc", "apply", "-f", tmpFile.Name())
	defer func() {
		if delErr := runCommand("oc", "delete", "lws", netpolLWSName, "-n", "default", "--ignore-not-found"); delErr != nil {
			klog.Errorf("Failed to clean up LeaderWorkerSet: %v", delErr)
		}
	}()
	if err != nil {
		t.Fatalf("Webhook rejected LeaderWorkerSet creation: %v", err)
	}
	klog.Infof("LeaderWorkerSet created successfully — webhook is accessible via kube-apiserver")
}

// testNetworkPolicyMetricsAccess verifies metrics port 8443 is accessible from
// openshift-monitoring but blocked from a random namespace.
func testNetworkPolicyMetricsAccess(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	operandPod, err := getOperandPod(ctx, kubeClient)
	if err != nil {
		t.Fatalf("Failed to get operand pod: %v", err)
	}
	metricsURL := fmt.Sprintf("https://%s:8443", operandPod.Status.PodIP)

	klog.Infof("Testing metrics access from openshift-monitoring")
	code, err := runCurlTestPod(ctx, kubeClient, "openshift-monitoring", metricsURL)
	if err != nil {
		t.Fatalf("Failed to run curl test from openshift-monitoring: %v", err)
	}
	if code == "000" {
		t.Fatalf("Metrics port 8443 blocked from openshift-monitoring: got %s, expected HTTP response", code)
	}
	klog.Infof("Metrics from openshift-monitoring: HTTP %s", code)

	klog.Infof("Testing metrics access from random namespace (should be blocked)")
	_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: netpolBlockNamespace},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("Failed to create namespace %s: %v", netpolBlockNamespace, err)
	}
	defer func() {
		if delErr := kubeClient.CoreV1().Namespaces().Delete(ctx, netpolBlockNamespace, metav1.DeleteOptions{}); delErr != nil && !apierrors.IsNotFound(delErr) {
			klog.Errorf("Failed to clean up namespace %s: %v", netpolBlockNamespace, delErr)
		}
	}()

	code, err = runCurlTestPod(ctx, kubeClient, netpolBlockNamespace, metricsURL)
	if err != nil {
		t.Fatalf("Failed to run curl test from %s: %v", netpolBlockNamespace, err)
	}
	if code != "000" {
		t.Fatalf("Metrics port 8443 should be blocked from %s: got %s, expected 000", netpolBlockNamespace, code)
	}
	klog.Infof("Metrics from random namespace correctly blocked: HTTP %s", code)
}

// testNetworkPolicyUnlistedPortBlocked verifies that an unlisted port (8080) is
// blocked even from the same namespace.
func testNetworkPolicyUnlistedPortBlocked(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	operandPod, err := getOperandPod(ctx, kubeClient)
	if err != nil {
		t.Fatalf("Failed to get operand pod: %v", err)
	}
	blockedURL := fmt.Sprintf("https://%s:8080", operandPod.Status.PodIP)

	klog.Infof("Testing unlisted port 8080 from same namespace (should be blocked)")
	code, err := runCurlTestPod(ctx, kubeClient, oteOperatorNamespace, blockedURL)
	if err != nil {
		t.Fatalf("Failed to run curl test for unlisted port: %v", err)
	}
	if code != "000" {
		t.Fatalf("Unlisted port 8080 should be blocked: got %s, expected 000", code)
	}
	klog.Infof("Unlisted port 8080 correctly blocked: HTTP %s", code)
}

// testNetworkPolicyOperandEgress verifies that the operand can reach the
// Kubernetes API server (egress is allowed).
func testNetworkPolicyOperandEgress(t testing.TB, ctx context.Context, kubeClient *k8sclient.Clientset) {
	t.Helper()
	klog.Infof("Testing egress from operand to kube-apiserver")
	output, err := runCommandOutput(ctx, "oc", "exec", "-n", oteOperatorNamespace,
		"deployment/"+oteOperandName, "--",
		"curl", "-sk", "--connect-timeout", "5", "-o", "/dev/null", "-w", "%{http_code}",
		"https://kubernetes.default.svc.cluster.local/healthz")
	if err != nil {
		t.Fatalf("Egress from operand to API server failed: %v", err)
	}
	if output != "200" {
		t.Fatalf("Egress to API server: expected HTTP 200, got %s", output)
	}
	klog.Infof("Egress from operand to API server: HTTP %s", output)
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
