package operator

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/clock"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/lws-operator/pkg/operator/operatorclient"
)

func TestManageNetworkPolicyOperandAllow(t *testing.T) {
	ctx := context.Background()
	kubeClient := fake.NewSimpleClientset()
	recorder := events.NewInMemoryRecorder("test", clock.RealClock{})

	reconciler := &TargetConfigReconciler{
		kubeClient:    kubeClient,
		eventRecorder: recorder,
		namespace:     "openshift-lws-operator",
		resourceCache: resourceapply.NewResourceCache(),
	}

	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1",
		Kind:       "LeaderWorkerSetOperator",
		Name:       operatorclient.OperatorConfigName,
		UID:        "test-uid",
	}

	_, _, err := reconciler.manageNetworkPolicyOperandAllow(ctx, ownerReference)
	if err != nil {
		t.Fatalf("failed to apply network policy: %v", err)
	}
}
