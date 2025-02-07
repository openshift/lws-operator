package operator

import (
	"context"

	"github.com/spf13/cobra"
	"k8s.io/utils/clock"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/lws-operator/pkg/operator"
	"github.com/openshift/lws-operator/pkg/version"
)

func NewOperator(ctx context.Context) *cobra.Command {
	cmd := controllercmd.
		NewControllerCommandConfig("openshift-lws-operator", version.Get(), operator.RunOperator, clock.RealClock{}).
		NewCommandWithContext(ctx)
	cmd.Use = "operator"
	cmd.Short = "Start the Cluster LeaderWorkerSet Operator"

	return cmd
}
