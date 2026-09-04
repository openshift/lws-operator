package operator

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/clock"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/lws-operator/pkg/operator/operatorclient"
)

const (
	allowNetworkPolicyOperandName       = "lws-allow-operand"
	defaultDenyNetworkPolicyOperandName = "lws-default-deny-operand"
	testNamespace                       = "openshift-lws-operator"
)

// verifyNetworkPolicy checks that the network policy has the expected name and namespace
func verifyNetworkPolicy(t *testing.T, obj *networkingv1.NetworkPolicy, expectedName string) {
	t.Helper()

	if obj.GetName() != expectedName {
		t.Errorf("Expected policy name %q, got %q", expectedName, obj.GetName())
	}

	if obj.GetNamespace() != testNamespace {
		t.Errorf("Expected policy namespace %q, got %q", testNamespace, obj.GetNamespace())
	}
}

func TestManageNetworkPolicyOperandDefaultDeny(t *testing.T) {
	ctx := context.Background()
	kubeClient := fake.NewSimpleClientset()
	recorder := events.NewInMemoryRecorder("test", clock.RealClock{})

	reconciler := &TargetConfigReconciler{
		kubeClient:    kubeClient,
		eventRecorder: recorder,
		namespace:     testNamespace,
		resourceCache: resourceapply.NewResourceCache(),
	}

	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1",
		Kind:       "LeaderWorkerSetOperator",
		Name:       operatorclient.OperatorConfigName,
		UID:        "test-uid",
	}

	// Test creating the default-deny network policy
	t.Run("creates default-deny network policy", func(t *testing.T) {
		policy, modified, err := reconciler.manageNetworkPolicyOperandDefaultDeny(ctx, ownerReference)
		if err != nil {
			t.Fatalf("manageNetworkPolicyOperandDefaultDeny failed: %v", err)
		}

		if !modified {
			t.Error("Expected modified=true when creating policy")
		}

		verifyNetworkPolicy(t, policy, defaultDenyNetworkPolicyOperandName)

		// Verify it has the correct pod selector
		if len(policy.Spec.PodSelector.MatchLabels) != 2 {
			t.Errorf("Expected 2 pod selector labels, got %d", len(policy.Spec.PodSelector.MatchLabels))
		}

		if policy.Spec.PodSelector.MatchLabels["app.kubernetes.io/name"] != "lws" {
			t.Errorf("Expected pod selector label app.kubernetes.io/name=lws, got %q", policy.Spec.PodSelector.MatchLabels["app.kubernetes.io/name"])
		}

		if policy.Spec.PodSelector.MatchLabels["control-plane"] != "controller-manager" {
			t.Errorf("Expected pod selector label control-plane=controller-manager, got %q", policy.Spec.PodSelector.MatchLabels["control-plane"])
		}

		// Verify policy types
		if len(policy.Spec.PolicyTypes) != 2 {
			t.Errorf("Expected 2 policy types (Ingress, Egress), got %d", len(policy.Spec.PolicyTypes))
		}
		hasIngress := false
		hasEgress := false
		for _, pt := range policy.Spec.PolicyTypes {
			if pt == networkingv1.PolicyTypeIngress {
				hasIngress = true
			}
			if pt == networkingv1.PolicyTypeEgress {
				hasEgress = true
			}
		}
		if !hasIngress {
			t.Error("Expected policy to have Ingress policy type")
		}
		if !hasEgress {
			t.Error("Expected policy to have Egress policy type")
		}

		// Verify default-deny has no ingress or egress rules (denies all)
		if len(policy.Spec.Ingress) != 0 {
			t.Errorf("Expected default-deny to have 0 ingress rules, got %d", len(policy.Spec.Ingress))
		}
		if len(policy.Spec.Egress) != 0 {
			t.Errorf("Expected default-deny to have 0 egress rules, got %d", len(policy.Spec.Egress))
		}
	})

	// Test idempotency
	t.Run("is idempotent on subsequent calls", func(t *testing.T) {
		policy, modified, err := reconciler.manageNetworkPolicyOperandDefaultDeny(ctx, ownerReference)
		if err != nil {
			t.Fatalf("manageNetworkPolicyOperandDefaultDeny failed: %v", err)
		}

		if modified {
			t.Error("Expected modified=false when policy already exists and is correct")
		}

		verifyNetworkPolicy(t, policy, defaultDenyNetworkPolicyOperandName)
	})
}

func TestManageNetworkPolicyOperandAllow(t *testing.T) {
	ctx := context.Background()
	kubeClient := fake.NewSimpleClientset()
	recorder := events.NewInMemoryRecorder("test", clock.RealClock{})

	reconciler := &TargetConfigReconciler{
		kubeClient:    kubeClient,
		eventRecorder: recorder,
		namespace:     testNamespace,
		resourceCache: resourceapply.NewResourceCache(),
	}

	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1",
		Kind:       "LeaderWorkerSetOperator",
		Name:       operatorclient.OperatorConfigName,
		UID:        "test-uid",
	}

	// Test creating the allow network policy
	t.Run("creates allow network policy", func(t *testing.T) {
		policy, modified, err := reconciler.manageNetworkPolicyOperandAllow(ctx, ownerReference)
		if err != nil {
			t.Fatalf("manageNetworkPolicyOperandAllow failed: %v", err)
		}

		if !modified {
			t.Error("Expected modified=true when creating policy")
		}

		verifyNetworkPolicy(t, policy, allowNetworkPolicyOperandName)

		// Verify it has the correct pod selector
		if len(policy.Spec.PodSelector.MatchLabels) != 2 {
			t.Errorf("Expected 2 pod selector labels, got %d", len(policy.Spec.PodSelector.MatchLabels))
		}

		// Verify ingress rules exist
		if len(policy.Spec.Ingress) != 3 {
			t.Errorf("Expected 3 ingress rules (webhook, metrics from namespace, metrics from monitoring), got %d", len(policy.Spec.Ingress))
		}

		// Verify webhook ingress (port 9443)
		foundWebhook := false
		for _, rule := range policy.Spec.Ingress {
			for _, port := range rule.Ports {
				if port.Port != nil && port.Port.IntVal == 9443 && *port.Protocol == corev1.ProtocolTCP {
					foundWebhook = true
				}
			}
		}
		if !foundWebhook {
			t.Error("Expected to find webhook ingress rule on TCP port 9443")
		}

		// Verify metrics ingress (port 8443)
		foundMetrics := false
		for _, rule := range policy.Spec.Ingress {
			for _, port := range rule.Ports {
				if port.Port != nil && port.Port.IntVal == 8443 && *port.Protocol == corev1.ProtocolTCP {
					foundMetrics = true
				}
			}
		}
		if !foundMetrics {
			t.Error("Expected to find metrics ingress rule on TCP port 8443")
		}

		// Verify egress rule allows all
		if len(policy.Spec.Egress) != 1 {
			t.Errorf("Expected 1 egress rule (allow all), got %d", len(policy.Spec.Egress))
		}
		// Unrestricted egress has no From, Ports, or To fields
		if len(policy.Spec.Egress[0].To) != 0 || len(policy.Spec.Egress[0].Ports) != 0 {
			t.Error("Expected unrestricted egress rule (empty To and Ports)")
		}
	})

	// Test idempotency
	t.Run("is idempotent on subsequent calls", func(t *testing.T) {
		policy, modified, err := reconciler.manageNetworkPolicyOperandAllow(ctx, ownerReference)
		if err != nil {
			t.Fatalf("manageNetworkPolicyOperandAllow failed: %v", err)
		}

		if modified {
			t.Error("Expected modified=false when policy already exists and is correct")
		}

		verifyNetworkPolicy(t, policy, allowNetworkPolicyOperandName)
	})
}

func TestNetworkPolicyReconciliation(t *testing.T) {
	ctx := context.Background()
	kubeClient := fake.NewSimpleClientset()
	recorder := events.NewInMemoryRecorder("test", clock.RealClock{})

	reconciler := &TargetConfigReconciler{
		kubeClient:    kubeClient,
		eventRecorder: recorder,
		namespace:     testNamespace,
		resourceCache: resourceapply.NewResourceCache(),
	}

	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1",
		Kind:       "LeaderWorkerSetOperator",
		Name:       operatorclient.OperatorConfigName,
		UID:        "test-uid",
	}

	t.Run("reconciles after deletion", func(t *testing.T) {
		// Create the policy
		_, _, err := reconciler.manageNetworkPolicyOperandAllow(ctx, ownerReference)
		if err != nil {
			t.Fatalf("Failed to create policy: %v", err)
		}

		// Delete the policy
		err = kubeClient.NetworkingV1().NetworkPolicies(testNamespace).Delete(ctx, allowNetworkPolicyOperandName, metav1.DeleteOptions{})
		if err != nil {
			t.Fatalf("Failed to delete policy: %v", err)
		}

		// Reconcile should recreate it
		policy, modified, err := reconciler.manageNetworkPolicyOperandAllow(ctx, ownerReference)
		if err != nil {
			t.Fatalf("Failed to reconcile after deletion: %v", err)
		}

		if !modified {
			t.Error("Expected modified=true when recreating deleted policy")
		}

		verifyNetworkPolicy(t, policy, allowNetworkPolicyOperandName)
	})

	t.Run("reconciles after bad mutation", func(t *testing.T) {
		// Create the policy
		_, _, err := reconciler.manageNetworkPolicyOperandDefaultDeny(ctx, ownerReference)
		if err != nil {
			t.Fatalf("Failed to create policy: %v", err)
		}

		// Mutate the policy (change pod selector)
		policy, err := kubeClient.NetworkingV1().NetworkPolicies(testNamespace).Get(ctx, defaultDenyNetworkPolicyOperandName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Failed to get policy: %v", err)
		}

		policy.Spec.PodSelector.MatchLabels = map[string]string{"wrong": "label"}
		_, err = kubeClient.NetworkingV1().NetworkPolicies(testNamespace).Update(ctx, policy, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("Failed to mutate policy: %v", err)
		}

		// Reconcile should restore it
		corrected, modified, err := reconciler.manageNetworkPolicyOperandDefaultDeny(ctx, ownerReference)
		if err != nil {
			t.Fatalf("Failed to reconcile after mutation: %v", err)
		}

		if !modified {
			t.Error("Expected modified=true when fixing mutated policy")
		}

		// Verify pod selector is correct again
		if corrected.Spec.PodSelector.MatchLabels["app.kubernetes.io/name"] != "lws" {
			t.Errorf("Expected pod selector to be restored, got %v", corrected.Spec.PodSelector.MatchLabels)
		}
	})
}

func TestNetworkPolicyOwnerReferences(t *testing.T) {
	ctx := context.Background()
	kubeClient := fake.NewSimpleClientset()
	recorder := events.NewInMemoryRecorder("test", clock.RealClock{})

	reconciler := &TargetConfigReconciler{
		kubeClient:    kubeClient,
		eventRecorder: recorder,
		namespace:     testNamespace,
		resourceCache: resourceapply.NewResourceCache(),
	}

	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1",
		Kind:       "LeaderWorkerSetOperator",
		Name:       operatorclient.OperatorConfigName,
		UID:        "test-uid-123",
	}

	t.Run("sets owner references correctly", func(t *testing.T) {
		policy, _, err := reconciler.manageNetworkPolicyOperandAllow(ctx, ownerReference)
		if err != nil {
			t.Fatalf("Failed to create policy: %v", err)
		}

		if len(policy.OwnerReferences) != 1 {
			t.Fatalf("Expected 1 owner reference, got %d", len(policy.OwnerReferences))
		}

		ref := policy.OwnerReferences[0]
		if ref.APIVersion != ownerReference.APIVersion {
			t.Errorf("Expected APIVersion %q, got %q", ownerReference.APIVersion, ref.APIVersion)
		}
		if ref.Kind != ownerReference.Kind {
			t.Errorf("Expected Kind %q, got %q", ownerReference.Kind, ref.Kind)
		}
		if ref.Name != ownerReference.Name {
			t.Errorf("Expected Name %q, got %q", ownerReference.Name, ref.Name)
		}
		if ref.UID != ownerReference.UID {
			t.Errorf("Expected UID %q, got %q", ownerReference.UID, ref.UID)
		}
	})
}

func TestCheckNetworkPolicyExists(t *testing.T) {
	ctx := context.Background()
	kubeClient := fake.NewSimpleClientset()
	recorder := events.NewInMemoryRecorder("test", clock.RealClock{})

	reconciler := &TargetConfigReconciler{
		kubeClient:    kubeClient,
		eventRecorder: recorder,
		namespace:     testNamespace,
		resourceCache: resourceapply.NewResourceCache(),
	}

	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1",
		Kind:       "LeaderWorkerSetOperator",
		Name:       operatorclient.OperatorConfigName,
		UID:        "test-uid",
	}

	t.Run("returns false for non-existent policy", func(t *testing.T) {
		exists, err := reconciler.checkNetworkPolicyExists(ctx, "non-existent-ns", "non-existent-policy")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if exists {
			t.Error("Expected checkNetworkPolicyExists to return false for non-existent policy")
		}
	})

	t.Run("returns true after creating policy", func(t *testing.T) {
		// Create a policy
		_, _, err := reconciler.manageNetworkPolicyOperandAllow(ctx, ownerReference)
		if err != nil {
			t.Fatalf("Failed to create network policy: %v", err)
		}

		// Check it exists
		exists, err := reconciler.checkNetworkPolicyExists(ctx, testNamespace, allowNetworkPolicyOperandName)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !exists {
			t.Error("Expected checkNetworkPolicyExists to return true for created policy")
		}
	})
}

func TestDefaultDenyConditionalLogic(t *testing.T) {
	ctx := context.Background()
	kubeClient := fake.NewSimpleClientset()
	recorder := events.NewInMemoryRecorder("test", clock.RealClock{})

	reconciler := &TargetConfigReconciler{
		kubeClient:    kubeClient,
		eventRecorder: recorder,
		namespace:     testNamespace,
		resourceCache: resourceapply.NewResourceCache(),
	}

	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1",
		Kind:       "LeaderWorkerSetOperator",
		Name:       operatorclient.OperatorConfigName,
		UID:        "test-uid",
	}

	t.Run("default-deny created when allow policy exists", func(t *testing.T) {
		// Create allow policy
		_, _, err := reconciler.manageNetworkPolicyOperandAllow(ctx, ownerReference)
		if err != nil {
			t.Fatalf("Failed to create allow policy: %v", err)
		}

		// Verify allow exists
		allowExists, err := reconciler.checkNetworkPolicyExists(ctx, testNamespace, allowNetworkPolicyOperandName)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !allowExists {
			t.Fatal("Expected allow policy to exist")
		}

		// Create default-deny (should succeed because allow exists)
		_, _, err = reconciler.manageNetworkPolicyOperandDefaultDeny(ctx, ownerReference)
		if err != nil {
			t.Fatalf("Failed to create default-deny policy: %v", err)
		}

		// Verify default-deny exists
		defaultDenyExists, err := reconciler.checkNetworkPolicyExists(ctx, testNamespace, defaultDenyNetworkPolicyOperandName)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !defaultDenyExists {
			t.Error("Expected default-deny policy to exist when allow policy exists")
		}
	})

	t.Run("default-deny deleted when allow policy is missing", func(t *testing.T) {
		// Create both policies
		_, _, err := reconciler.manageNetworkPolicyOperandAllow(ctx, ownerReference)
		if err != nil {
			t.Fatalf("Failed to create allow policy: %v", err)
		}
		_, _, err = reconciler.manageNetworkPolicyOperandDefaultDeny(ctx, ownerReference)
		if err != nil {
			t.Fatalf("Failed to create default-deny policy: %v", err)
		}

		// Delete allow policy
		err = kubeClient.NetworkingV1().NetworkPolicies(testNamespace).Delete(ctx, allowNetworkPolicyOperandName, metav1.DeleteOptions{})
		if err != nil {
			t.Fatalf("Failed to delete allow policy: %v", err)
		}

		// Simulate reconciliation: check if allow exists
		allowExists, err := reconciler.checkNetworkPolicyExists(ctx, testNamespace, allowNetworkPolicyOperandName)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if allowExists {
			t.Fatal("Expected allow policy to not exist after deletion")
		}

		// Delete default-deny (safety mechanism)
		err = reconciler.deleteNetworkPolicyOperandDefaultDeny(ctx, testNamespace)
		if err != nil {
			t.Fatalf("Failed to delete default-deny policy: %v", err)
		}

		// Verify default-deny is gone
		defaultDenyExists, err := reconciler.checkNetworkPolicyExists(ctx, testNamespace, defaultDenyNetworkPolicyOperandName)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if defaultDenyExists {
			t.Error("Expected default-deny policy to be deleted when allow policy is missing")
		}
	})

	t.Run("deleteNetworkPolicyOperandDefaultDeny is idempotent", func(t *testing.T) {
		// Try to delete non-existent policy (should not error)
		err := reconciler.deleteNetworkPolicyOperandDefaultDeny(ctx, testNamespace)
		if err != nil {
			t.Errorf("Expected no error when deleting non-existent policy, got: %v", err)
		}
	})
}
