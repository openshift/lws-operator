package operatorclient

import (
	"context"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorapplyconfigurationv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	"github.com/openshift/library-go/pkg/apiserver/jsonpatch"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	lwsoperatorinterface "github.com/openshift/lws-operator/pkg/generated/clientset/versioned/typed/lwsoperator/v1alpha1"
)

const OperatorNamespace = "openshift-lws-operator"
const OperatorConfigName = "cluster"
const OperandName = "lws-controller-manager"

var _ v1helpers.OperatorClient = &LWSOperatorClient{}

type LWSOperatorClient struct {
	Ctx            context.Context
	SharedInformer cache.SharedIndexInformer
	OperatorClient lwsoperatorinterface.LwsOperatorsV1alpha1Interface
}

func (L LWSOperatorClient) Informer() cache.SharedIndexInformer {
	//TODO implement me
	panic("implement me")
}

func (L LWSOperatorClient) GetObjectMeta() (meta *v1.ObjectMeta, err error) {
	//TODO implement me
	panic("implement me")
}

func (L LWSOperatorClient) GetOperatorState() (spec *operatorv1.OperatorSpec, status *operatorv1.OperatorStatus, resourceVersion string, err error) {
	//TODO implement me
	panic("implement me")
}

func (L LWSOperatorClient) GetOperatorStateWithQuorum(ctx context.Context) (spec *operatorv1.OperatorSpec, status *operatorv1.OperatorStatus, resourceVersion string, err error) {
	//TODO implement me
	panic("implement me")
}

func (L LWSOperatorClient) UpdateOperatorSpec(ctx context.Context, oldResourceVersion string, in *operatorv1.OperatorSpec) (out *operatorv1.OperatorSpec, newResourceVersion string, err error) {
	//TODO implement me
	panic("implement me")
}

func (L LWSOperatorClient) UpdateOperatorStatus(ctx context.Context, oldResourceVersion string, in *operatorv1.OperatorStatus) (out *operatorv1.OperatorStatus, err error) {
	//TODO implement me
	panic("implement me")
}

func (L LWSOperatorClient) ApplyOperatorSpec(ctx context.Context, fieldManager string, applyConfiguration *operatorapplyconfigurationv1.OperatorSpecApplyConfiguration) (err error) {
	//TODO implement me
	panic("implement me")
}

func (L LWSOperatorClient) ApplyOperatorStatus(ctx context.Context, fieldManager string, applyConfiguration *operatorapplyconfigurationv1.OperatorStatusApplyConfiguration) (err error) {
	//TODO implement me
	panic("implement me")
}

func (L LWSOperatorClient) PatchOperatorStatus(ctx context.Context, jsonPatch *jsonpatch.PatchSet) (err error) {
	//TODO implement me
	panic("implement me")
}
