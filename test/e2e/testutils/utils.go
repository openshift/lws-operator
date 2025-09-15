package testutils

import (
	"context"
	"fmt"
	"time"

	"github.com/onsi/gomega"
	v1 "github.com/openshift/api/operator/v1"
	operatorv1 "github.com/openshift/lws-operator/pkg/apis/leaderworkersetoperator/v1"

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

func ScaleDeployment(ctx context.Context, clients *TestClients, OperandName string, replicas int32) {
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err := clients.KubeClient.AppsV1().Deployments(operatorNamespace).Patch(
		ctx,
		OperandName,
		types.StrategicMergePatchType,
		[]byte(patch),
		metav1.PatchOptions{})
	if err != nil {
		klog.Errorf("WARNING: Failed to restore replicas: %v\n", err)
	}
}

func VerifyPodCount(ctx context.Context, clients *TestClients, namespace, labelSelector string, expected int) {
	gomega.Eventually(func() int {
		return GetPodCount(ctx, clients, namespace, labelSelector)
	}, 5*time.Minute, 10*time.Second).Should(
		gomega.Equal(expected),
		"Pod count should reach %d", expected)
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
