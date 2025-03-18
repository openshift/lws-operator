package operatorclient

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorapplyconfigurationv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	"github.com/openshift/library-go/pkg/apiserver/jsonpatch"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	leaderworkersetoperatorapiv1 "github.com/openshift/lws-operator/pkg/apis/leaderworkersetoperator/v1"
	leaderworkersetoperatorv1 "github.com/openshift/lws-operator/pkg/generated/applyconfiguration/leaderworkersetoperator/v1"
	leaderworkersetoperatorinterface "github.com/openshift/lws-operator/pkg/generated/clientset/versioned/typed/leaderworkersetoperator/v1"
	leaderworkersetoperatorlisterv1 "github.com/openshift/lws-operator/pkg/generated/listers/leaderworkersetoperator/v1"
)

const OperatorConfigName = "cluster"

var _ v1helpers.OperatorClient = &LeaderWorkerSetClient{}

type LeaderWorkerSetClient struct {
	Ctx            context.Context
	SharedInformer cache.SharedIndexInformer
	OperatorClient leaderworkersetoperatorinterface.OpenShiftOperatorV1Interface
	Lister         leaderworkersetoperatorlisterv1.LeaderWorkerSetOperatorLister
}

func (l *LeaderWorkerSetClient) Informer() cache.SharedIndexInformer {
	return l.SharedInformer
}

func (l *LeaderWorkerSetClient) GetObjectMeta() (meta *metav1.ObjectMeta, err error) {
	var instance *leaderworkersetoperatorapiv1.LeaderWorkerSetOperator
	if l.SharedInformer.HasSynced() {
		instance, err = l.Lister.Get(OperatorConfigName)
		if err != nil {
			return nil, err
		}
	} else {
		instance, err = l.OperatorClient.LeaderWorkerSetOperators().Get(l.Ctx, OperatorConfigName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
	}
	return &instance.ObjectMeta, nil
}

func (l *LeaderWorkerSetClient) GetOperatorState() (spec *operatorv1.OperatorSpec, status *operatorv1.OperatorStatus, resourceVersion string, err error) {
	if !l.SharedInformer.HasSynced() {
		return l.GetOperatorStateWithQuorum(l.Ctx)
	}
	instance, err := l.Lister.Get(OperatorConfigName)
	if err != nil {
		return nil, nil, "", err
	}
	return &instance.Spec.OperatorSpec, &instance.Status.OperatorStatus, instance.ResourceVersion, nil
}

func (l *LeaderWorkerSetClient) GetOperatorStateWithQuorum(ctx context.Context) (spec *operatorv1.OperatorSpec, status *operatorv1.OperatorStatus, resourceVersion string, err error) {
	instance, err := l.OperatorClient.LeaderWorkerSetOperators().Get(ctx, OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, "", err
	}
	return &instance.Spec.OperatorSpec, &instance.Status.OperatorStatus, instance.ResourceVersion, nil
}

func (l *LeaderWorkerSetClient) UpdateOperatorSpec(ctx context.Context, resourceVersion string, in *operatorv1.OperatorSpec) (out *operatorv1.OperatorSpec, newResourceVersion string, err error) {
	original, err := l.OperatorClient.LeaderWorkerSetOperators().Get(ctx, OperatorConfigName, metav1.GetOptions{ResourceVersion: resourceVersion})
	if err != nil {
		return nil, "", err
	}
	original.Spec.OperatorSpec = *in

	ret, err := l.OperatorClient.LeaderWorkerSetOperators().Update(ctx, original, metav1.UpdateOptions{})
	if err != nil {
		return nil, "", err
	}

	return &ret.Spec.OperatorSpec, ret.ResourceVersion, nil
}

func (l *LeaderWorkerSetClient) UpdateOperatorStatus(ctx context.Context, resourceVersion string, in *operatorv1.OperatorStatus) (out *operatorv1.OperatorStatus, err error) {
	original, err := l.OperatorClient.LeaderWorkerSetOperators().Get(ctx, OperatorConfigName, metav1.GetOptions{ResourceVersion: resourceVersion})
	if err != nil {
		return nil, err
	}
	original.Status.OperatorStatus = *in

	ret, err := l.OperatorClient.LeaderWorkerSetOperators().UpdateStatus(ctx, original, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}
	return &ret.Status.OperatorStatus, nil
}

func (l *LeaderWorkerSetClient) ApplyOperatorSpec(ctx context.Context, fieldManager string, applyConfiguration *operatorapplyconfigurationv1.OperatorSpecApplyConfiguration) (err error) {
	if applyConfiguration == nil {
		return fmt.Errorf("applyConfiguration must have a value")
	}

	desiredSpec := &leaderworkersetoperatorv1.LeaderWorkerSetOperatorSpecApplyConfiguration{
		OperatorSpecApplyConfiguration: *applyConfiguration,
	}
	desired := leaderworkersetoperatorv1.LeaderWorkerSetOperator(OperatorConfigName)
	desired.WithSpec(desiredSpec)

	instance, err := l.OperatorClient.LeaderWorkerSetOperators().Get(ctx, OperatorConfigName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
	// do nothing and proceed with the apply
	case err != nil:
		return fmt.Errorf("unable to get operator configuration: %w", err)
	default:
		original, err := leaderworkersetoperatorv1.ExtractLeaderWorkerSetOperator(instance, fieldManager)
		if err != nil {
			return fmt.Errorf("unable to extract operator configuration from spec: %w", err)
		}
		if equality.Semantic.DeepEqual(original, desired) {
			return nil
		}
	}

	_, err = l.OperatorClient.LeaderWorkerSetOperators().Apply(ctx, desired, metav1.ApplyOptions{
		Force:        true,
		FieldManager: fieldManager,
	})
	if err != nil {
		return fmt.Errorf("unable to Apply for operator using fieldManager %q: %w", fieldManager, err)
	}

	return nil
}

func (l *LeaderWorkerSetClient) ApplyOperatorStatus(ctx context.Context, fieldManager string, applyConfiguration *operatorapplyconfigurationv1.OperatorStatusApplyConfiguration) (err error) {
	if applyConfiguration == nil {
		return fmt.Errorf("applyConfiguration must have a value")
	}

	desiredStatus := &leaderworkersetoperatorv1.LeaderWorkerSetOperatorStatusApplyConfiguration{
		OperatorStatusApplyConfiguration: *applyConfiguration,
	}
	desired := leaderworkersetoperatorv1.LeaderWorkerSetOperator(OperatorConfigName)
	desired.WithStatus(desiredStatus)

	instance, err := l.OperatorClient.LeaderWorkerSetOperators().Get(ctx, OperatorConfigName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		// do nothing and proceed with the apply
		v1helpers.SetApplyConditionsLastTransitionTime(clock.RealClock{}, &desired.Status.Conditions, nil)
	case err != nil:
		return fmt.Errorf("unable to get operator configuration: %w", err)
	default:
		original, err := leaderworkersetoperatorv1.ExtractLeaderWorkerSetOperatorStatus(instance, fieldManager)
		if err != nil {
			return fmt.Errorf("unable to extract operator configuration from status: %w", err)
		}
		if equality.Semantic.DeepEqual(original, desired) {
			return nil
		}
		if original.Status != nil {
			v1helpers.SetApplyConditionsLastTransitionTime(clock.RealClock{}, &desired.Status.Conditions, original.Status.Conditions)
		} else {
			v1helpers.SetApplyConditionsLastTransitionTime(clock.RealClock{}, &desired.Status.Conditions, nil)
		}
	}

	_, err = l.OperatorClient.LeaderWorkerSetOperators().ApplyStatus(ctx, desired, metav1.ApplyOptions{
		Force:        true,
		FieldManager: fieldManager,
	})
	if err != nil {
		return fmt.Errorf("unable to ApplyStatus for operator using fieldManager %q: %w", fieldManager, err)
	}

	return nil
}

func (l *LeaderWorkerSetClient) PatchOperatorStatus(ctx context.Context, jsonPatch *jsonpatch.PatchSet) (err error) {
	jsonPatchBytes, err := jsonPatch.Marshal()
	if err != nil {
		return err
	}
	_, err = l.OperatorClient.LeaderWorkerSetOperators().Patch(ctx, OperatorConfigName, types.JSONPatchType, jsonPatchBytes, metav1.PatchOptions{}, "/status")
	return err
}
