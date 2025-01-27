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

	leaderworkersetoperatorv1 "github.com/openshift/lws-operator/pkg/generated/applyconfiguration/leaderworkersetoperator/v1"
	leaderworkersetoperatorinterface "github.com/openshift/lws-operator/pkg/generated/clientset/versioned/typed/leaderworkersetoperator/v1"
)

const OperatorConfigName = "cluster"

var _ v1helpers.OperatorClient = &LeaderWorkerSetClient{}

type LeaderWorkerSetClient struct {
	Ctx               context.Context
	SharedInformer    cache.SharedIndexInformer
	OperatorClient    leaderworkersetoperatorinterface.LeaderWorkerSetOperatorInterface
	OperatorNamespace string
}

func (l *LeaderWorkerSetClient) Informer() cache.SharedIndexInformer {
	return l.SharedInformer
}

func (l *LeaderWorkerSetClient) GetObjectMeta() (meta *metav1.ObjectMeta, err error) {
	instance, err := l.OperatorClient.Get(l.Ctx, OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &instance.ObjectMeta, nil
}

func (l *LeaderWorkerSetClient) GetOperatorState() (spec *operatorv1.OperatorSpec, status *operatorv1.OperatorStatus, resourceVersion string, err error) {
	instance, err := l.OperatorClient.Get(l.Ctx, OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, "", err
	}
	return &instance.Spec.OperatorSpec, &instance.Status.OperatorStatus, instance.ResourceVersion, nil
}

func (l *LeaderWorkerSetClient) GetOperatorStateWithQuorum(ctx context.Context) (spec *operatorv1.OperatorSpec, status *operatorv1.OperatorStatus, resourceVersion string, err error) {
	return l.GetOperatorState()
}

func (l *LeaderWorkerSetClient) UpdateOperatorSpec(ctx context.Context, oldResourceVersion string, in *operatorv1.OperatorSpec) (out *operatorv1.OperatorSpec, newResourceVersion string, err error) {
	original, err := l.OperatorClient.Get(ctx, OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, "", err
	}
	copy := original.DeepCopy()
	copy.ResourceVersion = oldResourceVersion
	copy.Spec.OperatorSpec = *in

	ret, err := l.OperatorClient.Update(ctx, copy, metav1.UpdateOptions{})
	if err != nil {
		return nil, "", err
	}

	return &ret.Spec.OperatorSpec, ret.ResourceVersion, nil
}

func (l *LeaderWorkerSetClient) UpdateOperatorStatus(ctx context.Context, oldResourceVersion string, in *operatorv1.OperatorStatus) (out *operatorv1.OperatorStatus, err error) {
	original, err := l.OperatorClient.Get(ctx, OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	copy := original.DeepCopy()
	copy.ResourceVersion = oldResourceVersion
	copy.Status.OperatorStatus = *in

	ret, err := l.OperatorClient.UpdateStatus(ctx, copy, metav1.UpdateOptions{})
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
	desired := leaderworkersetoperatorv1.LeaderWorkerSetOperator(OperatorConfigName, l.OperatorNamespace)
	desired.WithSpec(desiredSpec)

	instance, err := l.OperatorClient.Get(ctx, OperatorConfigName, metav1.GetOptions{})
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

	_, err = l.OperatorClient.Apply(ctx, desired, metav1.ApplyOptions{
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
	desired := leaderworkersetoperatorv1.LeaderWorkerSetOperator(OperatorConfigName, l.OperatorNamespace)
	desired.WithStatus(desiredStatus)

	instance, err := l.OperatorClient.Get(ctx, OperatorConfigName, metav1.GetOptions{})
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

	_, err = l.OperatorClient.ApplyStatus(ctx, desired, metav1.ApplyOptions{
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
	_, err = l.OperatorClient.Patch(ctx, OperatorConfigName, types.JSONPatchType, jsonPatchBytes, metav1.PatchOptions{}, "/status")
	return err
}
