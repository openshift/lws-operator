package operator

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	leaderworkersetoperatorv1 "github.com/openshift/lws-operator/pkg/apis/leaderworkersetoperator/v1"
)

func TestApplyNodePlacement(t *testing.T) {
	infraToleration := corev1.Toleration{
		Key:      "node-role.kubernetes.io/infra",
		Operator: corev1.TolerationOpExists,
		Effect:   corev1.TaintEffectNoSchedule,
	}

	t.Run("nil nodePlacement leaves pod spec unchanged", func(t *testing.T) {
		podSpec := corev1.PodSpec{
			NodeSelector: map[string]string{"existing": "label"},
		}
		applyNodePlacement(&podSpec, nil)
		if podSpec.NodeSelector["existing"] != "label" {
			t.Fatalf("expected existing nodeSelector to remain, got %v", podSpec.NodeSelector)
		}
	})

	t.Run("sets nodeSelector and tolerations", func(t *testing.T) {
		podSpec := corev1.PodSpec{}
		applyNodePlacement(&podSpec, &leaderworkersetoperatorv1.NodePlacement{
			NodeSelector: map[string]string{
				"node-role.kubernetes.io/infra": "",
			},
			Tolerations: []corev1.Toleration{infraToleration},
		})

		value, ok := podSpec.NodeSelector["node-role.kubernetes.io/infra"]
		if !ok {
			t.Fatalf("expected nodeSelector key node-role.kubernetes.io/infra to be present, got %v", podSpec.NodeSelector)
		}
		if value != "" {
			t.Fatalf("unexpected nodeSelector value: %q", value)
		}
		if len(podSpec.Tolerations) != 1 || podSpec.Tolerations[0].Key != infraToleration.Key {
			t.Fatalf("unexpected tolerations: %v", podSpec.Tolerations)
		}
	})

	t.Run("empty nodeSelector leaves upstream nodeSelector unchanged", func(t *testing.T) {
		podSpec := corev1.PodSpec{
			NodeSelector: map[string]string{"stale": "label"},
		}
		applyNodePlacement(&podSpec, &leaderworkersetoperatorv1.NodePlacement{
			NodeSelector: map[string]string{},
		})
		if podSpec.NodeSelector["stale"] != "label" {
			t.Fatalf("expected upstream nodeSelector to remain, got %v", podSpec.NodeSelector)
		}
	})

	t.Run("omitted nodeSelector leaves upstream nodeSelector unchanged", func(t *testing.T) {
		podSpec := corev1.PodSpec{
			NodeSelector: map[string]string{"stale": "label"},
		}
		applyNodePlacement(&podSpec, &leaderworkersetoperatorv1.NodePlacement{
			Tolerations: []corev1.Toleration{infraToleration},
		})
		if podSpec.NodeSelector["stale"] != "label" {
			t.Fatalf("expected upstream nodeSelector to remain, got %v", podSpec.NodeSelector)
		}
		if len(podSpec.Tolerations) != 1 {
			t.Fatalf("expected tolerations to be set, got %v", podSpec.Tolerations)
		}
	})

	t.Run("omitted tolerations leaves upstream tolerations unchanged", func(t *testing.T) {
		podSpec := corev1.PodSpec{
			Tolerations: []corev1.Toleration{infraToleration},
		}
		applyNodePlacement(&podSpec, &leaderworkersetoperatorv1.NodePlacement{
			NodeSelector: map[string]string{
				"node-role.kubernetes.io/infra": "",
			},
		})
		if len(podSpec.Tolerations) != 1 || podSpec.Tolerations[0].Key != infraToleration.Key {
			t.Fatalf("expected upstream tolerations to remain, got %v", podSpec.Tolerations)
		}
	})

	t.Run("empty tolerations clears tolerations on pod spec", func(t *testing.T) {
		podSpec := corev1.PodSpec{
			Tolerations: []corev1.Toleration{infraToleration},
		}
		applyNodePlacement(&podSpec, &leaderworkersetoperatorv1.NodePlacement{
			Tolerations: []corev1.Toleration{},
		})
		if len(podSpec.Tolerations) != 0 {
			t.Fatalf("expected empty tolerations, got %v", podSpec.Tolerations)
		}
	})

	t.Run("copies nodeSelector and tolerations without aliasing CR data", func(t *testing.T) {
		matchLabels := map[string]string{"node-role.kubernetes.io/infra": ""}
		tolerations := []corev1.Toleration{infraToleration}
		nodePlacement := &leaderworkersetoperatorv1.NodePlacement{
			NodeSelector: matchLabels,
			Tolerations:  tolerations,
		}

		podSpec := corev1.PodSpec{}
		applyNodePlacement(&podSpec, nodePlacement)

		matchLabels["node-role.kubernetes.io/infra"] = "changed"
		tolerations[0].Key = "changed"

		value, ok := podSpec.NodeSelector["node-role.kubernetes.io/infra"]
		if !ok || value != "" {
			t.Fatalf("nodeSelector was aliased to CR data: %v", podSpec.NodeSelector)
		}
		if podSpec.Tolerations[0].Key != infraToleration.Key {
			t.Fatalf("tolerations were aliased to CR data: %v", podSpec.Tolerations)
		}
	})
}
