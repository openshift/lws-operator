package operator

import (
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"

	leaderworkersetoperatorv1 "github.com/openshift/lws-operator/pkg/apis/leaderworkersetoperator/v1"
)

// applyNodePlacement updates pod scheduling fields on the deployment pod template from the operator CR.
// Each field within nodePlacement is applied independently: omitted fields leave the operand manifest unchanged.
func applyNodePlacement(podSpec *corev1.PodSpec, nodePlacement *leaderworkersetoperatorv1.NodePlacement) {
	if nodePlacement == nil {
		return
	}

	if len(nodePlacement.NodeSelector) > 0 {
		podSpec.NodeSelector = maps.Clone(nodePlacement.NodeSelector)
	}

	if nodePlacement.Tolerations != nil {
		podSpec.Tolerations = slices.Clone(nodePlacement.Tolerations)
	}
}
