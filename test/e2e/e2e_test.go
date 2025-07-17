/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	v1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/openshift/lws-operator/pkg/generated/clientset/versioned/scheme"
	"github.com/openshift/lws-operator/test/e2e/testutils"
)

const (
	operatorNamespace               = "openshift-lws-operator"
	operandLabel                    = "control-plane=controller-manager"
	operatorReadyTime time.Duration = 3 * time.Minute
	operatorPoll                    = 10 * time.Second
)

var _ = Describe("LWS Operator", Ordered, func() {
	It("Verifying that conditions are correct", func() {
		ctx := context.TODO()
		lwsOperators, err := clients.LWSOperatorClient.List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())

		Expect(lwsOperators.Items).To(HaveLen(1))

		By("checking no degraded condition exists")
		degraded := false
		for _, condition := range lwsOperators.Items[0].Status.Conditions {
			if strings.HasSuffix(condition.Type, v1.OperatorStatusTypeDegraded) {
				if condition.Status == v1.ConditionTrue {
					degraded = true
				}
			}
		}
		Expect(degraded).To(BeFalse(), "degraded condition exists: %+v", lwsOperators.Items[0].Status.Conditions)

		By("checking the availability condition exists")
		Eventually(func() error {
			ctx := context.TODO()
			lwsOperators, err := clients.LWSOperatorClient.List(ctx, metav1.ListOptions{})
			if err != nil {
				klog.Errorf("Failed to list CRDs: %v", err)
				return err
			}

			if len(lwsOperators.Items) != 1 {
				return fmt.Errorf("unexpected number of LWSOperators %d", len(lwsOperators.Items))
			}

			By("checking available condition")
			cond := v1helpers.FindOperatorCondition(lwsOperators.Items[0].Status.Conditions, v1.OperatorStatusTypeAvailable)
			if cond == nil || cond.Status != v1.ConditionTrue {
				return fmt.Errorf("LWS operator is not available")
			}
			return nil
		}, 5*time.Minute, 5*time.Second).Should(Succeed(), "available condition is not found")
	})

	It("Verifying operand pod deleted and recovery", func() {
		ctx := context.TODO()
		pods, err := clients.KubeClient.CoreV1().Pods(operatorNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: operandLabel,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(pods.Items).ToNot(BeEmpty())
		err = clients.KubeClient.CoreV1().Pods(operatorNamespace).DeleteCollection(
			ctx,
			metav1.DeleteOptions{
				GracePeriodSeconds: ptr.To[int64](30),
			},
			metav1.ListOptions{
				LabelSelector: operandLabel,
			},
		)
		Expect(err).NotTo(HaveOccurred())

		// Wait recovery(Deployment will recreate Pod)
		err = wait.PollUntilContextTimeout(ctx, 2*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
			newPods, err := clients.KubeClient.CoreV1().Pods(operatorNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: operandLabel,
			})
			if err != nil {
				return false, err
			}
			activePods := make([]corev1.Pod, 0)
			for _, pod := range newPods.Items {
				if pod.DeletionTimestamp == nil {
					activePods = append(activePods, pod)
				}
			}
			if len(activePods) == 0 {
				return false, nil
			}
			for _, pod := range activePods {
				if pod.Status.Phase != corev1.PodRunning {
					klog.Infof("Pod %s status: %s", pod.Name, pod.Status.Phase)
					return false, nil
				}
				klog.Infof("Pod %s is Running", pod.Name)
			}
			return true, nil
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("should expose metrics endpoint with TLS", func() {
		var (
			err                error
			podName            = "curl-metrics-test"
			containerName      = "curl-metrics"
			certMountPath      = "/etc/lws/metrics/certs"
			metricsServiceName = "lws-controller-manager-metrics-service"
		)

		By("Creating curl test pod")
		ctx := context.TODO()
		curlPod := testutils.MakeCurlMetricsPod(operatorNamespace)
		defer func() {
			err = clients.KubeClient.CoreV1().Pods(operatorNamespace).Delete(context.TODO(), "curl-metrics-test", metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to delete curl metrics test pod")
		}()
		_, err = clients.KubeClient.CoreV1().Pods(operatorNamespace).Create(ctx, &curlPod.Pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to create curl metrics test pod")

		Eventually(func() error {
			pod, err := clients.KubeClient.CoreV1().Pods(operatorNamespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get pod: %w", err)
			}
			if pod.Status.Phase != corev1.PodRunning {
				return fmt.Errorf("pod %q not ready, phase: %s", podName, pod.Status.Phase)
			}
			return nil
		}, operatorReadyTime, operatorPoll).Should(Succeed(), "curl-metrics-test pod did not become ready")

		By("Visit metrics endpoint with TLS")
		Eventually(func() error {
			metricsOutput, _, err := Lexecute(ctx, clients.RestConfig, clients.KubeClient, operatorNamespace, podName, containerName,
				[]string{
					//"/bin/sh", "-c",
					fmt.Sprintf(
						"curl -v -s --cacert %s/ca.crt -H \"Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)\" https://%s.%s.svc:8443/metrics",
						certMountPath,
						metricsServiceName,
						operatorNamespace,
					),
				})
			if err != nil {
				return fmt.Errorf("exec into pod failed: %w", err)
			}

			if !strings.Contains(string(metricsOutput), "controller_runtime_reconcile_total") {
				return fmt.Errorf("expected metric not found in output")
			}
			return nil
		}, operatorReadyTime, operatorPoll).Should(Succeed(), "expected HTTP 200 OK from metrics endpoint")
	})
})

func Lexecute(ctx context.Context, restConfig *rest.Config, kubeClient kubernetes.Interface, namespace, podName, containerName string, command []string) ([]byte, []byte, error) {
	var out, outErr bytes.Buffer
	req := kubeClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: command,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return nil, nil, err
	}
	if err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &out, Stderr: &outErr, Tty: false}); err != nil {
		return nil, nil, err
	}
	return out.Bytes(), outErr.Bytes(), nil
}
