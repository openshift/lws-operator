package operator

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
)

var (
	monitoringScheme = runtime.NewScheme()
	monitoringCodecs = serializer.NewCodecFactory(monitoringScheme)
)

func init() {
	if err := monitoringv1.AddToScheme(monitoringScheme); err != nil {
		panic(err)
	}
}

func ReadServiceMonitorV1OrDie(objBytes []byte) *monitoringv1.ServiceMonitor {
	requiredObj, err := runtime.Decode(monitoringCodecs.UniversalDecoder(monitoringv1.SchemeGroupVersion), objBytes)
	if err != nil {
		panic(err)
	}
	return requiredObj.(*monitoringv1.ServiceMonitor)
}

// ApplyServiceMonitor applies the ServiceMonitor.
func ApplyServiceMonitor(ctx context.Context, client dynamic.Interface, recorder events.Recorder, required *monitoringv1.ServiceMonitor, cache resourceapply.ResourceCache) (*unstructured.Unstructured, bool, error) {
	requiredAsStr, err := yaml.Marshal(required)
	if err != nil {
		return nil, false, err
	}
	requiredAsObj, err := resourceread.ReadGenericWithUnstructured(requiredAsStr)
	if err != nil {
		return nil, false, err
	}
	requiredAsUnstructured, ok := requiredAsObj.(*unstructured.Unstructured)
	if !ok {
		return nil, false, fmt.Errorf("serviceMonitor is not an Unstructured")
	}

	return resourceapply.ApplyUnstructuredResourceImproved(ctx, client, recorder, requiredAsUnstructured, cache, monitoringv1.SchemeGroupVersion.WithResource(monitoringv1.ServiceMonitorName), nil, nil)

}
