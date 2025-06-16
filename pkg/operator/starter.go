package operator

import (
	"context"
	"os"
	"time"

	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	operatorconfigclient "github.com/openshift/lws-operator/pkg/generated/clientset/versioned"
	operatorclientinformers "github.com/openshift/lws-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/lws-operator/pkg/operator/operatorclient"
)

const (
	operatorNamespace = "openshift-lws-operator"
)

func RunOperator(ctx context.Context, cc *controllercmd.ControllerContext) error {
	kubeClient, err := kubernetes.NewForConfig(cc.ProtoKubeConfig)
	if err != nil {
		return err
	}

	dynamicClient, err := dynamic.NewForConfig(cc.ProtoKubeConfig)
	if err != nil {
		return err
	}

	apiextensionClient, err := apiextclientv1.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	operatorConfigClient, err := operatorconfigclient.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}
	operatorConfigInformers := operatorclientinformers.NewSharedInformerFactory(operatorConfigClient, 10*time.Minute)

	namespace := cc.OperatorNamespace
	if namespace == "openshift-config-managed" {
		// we need to fall back to our default namespace rather than library-go's when running outside the cluster
		namespace = operatorNamespace
	}

	kubeInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(kubeClient,
		"",
		namespace,
	)

	leaderWorkerSetOperatorClient := &operatorclient.LeaderWorkerSetClient{
		Ctx:            ctx,
		SharedInformer: operatorConfigInformers.OpenShiftOperator().V1().LeaderWorkerSetOperators().Informer(),
		Lister:         operatorConfigInformers.OpenShiftOperator().V1().LeaderWorkerSetOperators().Lister(),
		OperatorClient: operatorConfigClient.OpenShiftOperatorV1(),
	}

	targetConfigReconciler := NewTargetConfigReconciler(
		os.Getenv("RELATED_IMAGE_OPERAND_IMAGE"),
		namespace,
		operatorConfigClient.OpenShiftOperatorV1().LeaderWorkerSetOperators(),
		operatorConfigInformers.OpenShiftOperator().V1().LeaderWorkerSetOperators(),
		kubeInformersForNamespaces,
		leaderWorkerSetOperatorClient,
		dynamicClient,
		discoveryClient,
		kubeClient,
		apiextensionClient,
		cc.EventRecorder,
	)

	logLevelController := loglevel.NewClusterOperatorLoggingController(leaderWorkerSetOperatorClient, cc.EventRecorder)

	klog.Infof("Starting informers")
	operatorConfigInformers.Start(ctx.Done())
	kubeInformersForNamespaces.Start(ctx.Done())

	klog.Infof("Starting log level controller")
	go logLevelController.Run(ctx, 1)
	klog.Infof("Starting target config reconciler. Test")
	go targetConfigReconciler.Run(ctx, 1)

	<-ctx.Done()
	return nil
}
