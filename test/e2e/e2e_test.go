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
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	v1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const (
	operatorNamespace = "openshift-lws-operator"
	operandLabel      = "control-plane=controller-manager"
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
		err = wait.PollUntilContextTimeout(ctx, 2*time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
			newPods, err := clients.KubeClient.CoreV1().Pods(operatorNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: operandLabel,
			})
			if err != nil {
				if errors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			if len(newPods.Items) == 0 {
				return false, nil
			}
			for _, pod := range newPods.Items {
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
})
