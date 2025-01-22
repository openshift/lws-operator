package operator

import (
	"context"
	"os"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
)

const (
	podNamespaceEnv   = "POD_NAMESPACE"
	operatorNamespace = "openshift-lws-operator"
)

func RunOperator(ctx context.Context, cc *controllercmd.ControllerContext) error {
	// TODO: implement
	return nil
}

// getNamespace returns in-cluster namespace
func getNamespace() string {
	if nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return string(nsBytes)
	}
	if podNamespace := os.Getenv(podNamespaceEnv); len(podNamespace) > 0 {
		return podNamespace
	}
	return operatorNamespace
}
