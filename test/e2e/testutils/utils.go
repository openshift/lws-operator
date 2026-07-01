package testutils

import (
	"context"
	"fmt"
	"time"

	"github.com/onsi/gomega"
	v1 "github.com/openshift/api/operator/v1"
	operatorv1 "github.com/openshift/lws-operator/pkg/apis/leaderworkersetoperator/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

const (
	operatorNamespace = "openshift-lws-operator"
	OperandName       = "lws-controller-manager"
)

func GetOperatorState(ctx context.Context, clients *TestClients) (*operatorv1.LeaderWorkerSetOperator, v1.ManagementState, error) {
	if clients == nil || clients.LWSOperatorClient == nil {
		return nil, "", fmt.Errorf("nil clients or LWSOperatorClient")
	}
	lwsOperator, err := clients.LWSOperatorClient.Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("failed to get operator: %w", err)
	}

	return lwsOperator, lwsOperator.Spec.ManagementState, nil
}

func SetManagementState(ctx context.Context, clients *TestClients, operator *operatorv1.LeaderWorkerSetOperator, state v1.ManagementState) {
	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		current, getErr := clients.LWSOperatorClient.Get(ctx, operator.Name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		current.Spec.ManagementState = state
		_, updateErr := clients.LWSOperatorClient.Update(ctx, current, metav1.UpdateOptions{})
		return updateErr
	})
	gomega.Expect(retryErr).NotTo(gomega.HaveOccurred(), "failed to update operator state after retries")
}

func WaitForManagementState(ctx context.Context, clients *TestClients, state v1.ManagementState) {
	gomega.Eventually(func() v1.ManagementState {
		_, currentState, err := GetOperatorState(ctx, clients)
		if err != nil {
			klog.Errorf("GetOperatorState error: %v", err)
			return ""
		}
		return currentState
	}, 2*time.Minute, 2*time.Second).Should(
		gomega.Equal(state),
		"managementState should become %q", state)
}

func ScaleDeployment(ctx context.Context, clients *TestClients, OperandName string, replicas int32) {
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err := clients.KubeClient.AppsV1().Deployments(operatorNamespace).Patch(
		ctx,
		OperandName,
		types.StrategicMergePatchType,
		[]byte(patch),
		metav1.PatchOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to scale deployment %q to %d replicas", OperandName, replicas)
}

func VerifyDeploymentReplicas(ctx context.Context, clients *TestClients, deploymentName string, expected int32) {
	gomega.Eventually(func() int32 {
		deployment, err := clients.KubeClient.AppsV1().Deployments(operatorNamespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("deployment get error: %v", err)
			return -1
		}
		if deployment.Spec.Replicas == nil {
			return -1
		}
		return *deployment.Spec.Replicas
	}, 2*time.Minute, 2*time.Second).Should(
		gomega.Equal(expected),
		"deployment %q replicas should reach %d", deploymentName, expected)
}

func VerifyPodCount(ctx context.Context, clients *TestClients, namespace, labelSelector string, expected int) {
	gomega.Eventually(func() int {
		return GetPodCount(ctx, clients, namespace, labelSelector)
	}, 5*time.Minute, 10*time.Second).Should(
		gomega.Equal(expected),
		"Pod count should reach %d (deployment replicas=%d)", expected, getDeploymentReplicas(ctx, clients, OperandName))
}

func getDeploymentReplicas(ctx context.Context, clients *TestClients, deploymentName string) int32 {
	deployment, err := clients.KubeClient.AppsV1().Deployments(operatorNamespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil || deployment.Spec.Replicas == nil {
		return -1
	}
	return *deployment.Spec.Replicas
}

func SetNodePlacement(ctx context.Context, clients *TestClients, operator *operatorv1.LeaderWorkerSetOperator, nodePlacement *operatorv1.NodePlacement) {
	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		current, getErr := clients.LWSOperatorClient.Get(ctx, operator.Name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		current.Spec.NodePlacement = nodePlacement
		_, updateErr := clients.LWSOperatorClient.Update(ctx, current, metav1.UpdateOptions{})
		return updateErr
	})
	gomega.Expect(retryErr).NotTo(gomega.HaveOccurred(), "failed to update operator nodePlacement after retries")
}

func VerifyDeploymentNodePlacement(ctx context.Context, clients *TestClients, deploymentName string, expectedSelector map[string]string, expectedTolerations []corev1.Toleration) {
	gomega.Eventually(func() error {
		deployment, err := clients.KubeClient.AppsV1().Deployments(operatorNamespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		return compareDeploymentNodePlacement(deployment, expectedSelector, expectedTolerations)
	}, 5*time.Minute, 10*time.Second).Should(gomega.Succeed(), "deployment nodePlacement should match operator spec")
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

func GetPodCount(ctx context.Context, clients *TestClients, namespace, labelSelector string) int {
	pods, err := clients.KubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		klog.Errorf("Pod list error: %v\n", err)
		return -1
	}
	return len(pods.Items)
}
