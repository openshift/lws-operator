package e2e

import (
	"testing"

	o "github.com/onsi/gomega"
)

// NOTE: This test is also available in the OTE framework (test/e2e/operator.go).
// This dual implementation allows tests to run both as standard Go tests (via go test)
// and through the Ginkgo/OTE framework (for OpenShift CI integration).
//
// The actual test logic is in operator.go's standalone functions, which are called
// by both this standard Go test and the Ginkgo specs.

// TestExtended runs the operator tests using standard Go testing.
func TestExtended(t *testing.T) {
	o.RegisterTestingT(t)
	ctx, cancelFnc, kubeClient, err := setupOperator(t)
	defer func() {
		teardownOperator()
		if cancelFnc != nil {
			cancelFnc()
		}
	}()
	if err != nil {
		t.Fatalf("Failed to setup operator: %v", err)
	}

	t.Run("should have correct operator conditions", func(t *testing.T) {
		o.RegisterTestingT(t)
		testConditions(t, ctx, kubeClient)
	})

	t.Run("should apply nodePlacement to operand deployment", func(t *testing.T) {
		o.RegisterTestingT(t)
		testNodePlacement(t, ctx, kubeClient)
	})

	t.Run("should recover operand pods after deletion", func(t *testing.T) {
		o.RegisterTestingT(t)
		testPodDeleteRecovery(t, ctx, kubeClient)
	})

	t.Run("should allow manual scaling when managementState is Unmanaged", func(t *testing.T) {
		o.RegisterTestingT(t)
		testUnmanagedScaling(t, ctx, kubeClient)
	})

	t.Run("should handle managementState Removed", func(t *testing.T) {
		o.RegisterTestingT(t)
		testRemovedScaling(t, ctx, kubeClient)
	})
}
