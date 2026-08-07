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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	v1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	lwsoperatorv1 "github.com/openshift/lws-operator/pkg/apis/leaderworkersetoperator/v1"
	"github.com/openshift/lws-operator/test/e2e/testutils"
)

const (
	operatorNamespace = "openshift-lws-operator"
	operandLabel      = "control-plane=controller-manager"
	OperandName       = "lws-controller-manager"
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

	It("applies nodePlacement to lws-controller-manager deployment", func() {
		ctx := context.TODO()
		lwsOperator, _, err := testutils.GetOperatorState(ctx, clients)
		Expect(err).NotTo(HaveOccurred())

		nodeSelector := map[string]string{
			"e2e.lws.openshift.io/node-placement": "test",
		}
		tolerations := []corev1.Toleration{
			{
				Key:      "e2e.lws.openshift.io/node-placement",
				Operator: corev1.TolerationOpEqual,
				Value:    "test",
				Effect:   corev1.TaintEffectNoSchedule,
			},
		}
		nodePlacement := &lwsoperatorv1.NodePlacement{
			NodeSelector: nodeSelector,
			Tolerations:  tolerations,
		}

		defer func() {
			testutils.SetNodePlacement(ctx, clients, lwsOperator, nil)
			testutils.VerifyDeploymentNodePlacement(ctx, clients, OperandName, nil, nil)
		}()

		testutils.SetNodePlacement(ctx, clients, lwsOperator, nodePlacement)
		testutils.VerifyDeploymentNodePlacement(ctx, clients, OperandName, nodeSelector, tolerations)
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

	It("should allow manual scaling when managementState is Unmanaged", func() {
		ctx := context.TODO()
		By("Fetching initial operator state")
		lwsOperator, originalState, err := testutils.GetOperatorState(ctx, clients)
		Expect(err).ShouldNot(HaveOccurred())
		originalPodCount := testutils.GetPodCount(ctx, clients, operatorNamespace, operandLabel)

		defer func() {
			testutils.SetManagementState(ctx, clients, lwsOperator, originalState)
			testutils.WaitForManagementState(ctx, clients, originalState)
			if originalState == "" || originalState == v1.Managed {
				testutils.VerifyDeploymentReplicas(ctx, clients, OperandName, int32(originalPodCount))
			}
			testutils.VerifyPodCount(ctx, clients, operatorNamespace, operandLabel, originalPodCount)
		}()
		By("Setting managementState to Unmanaged")
		testutils.SetManagementState(ctx, clients, lwsOperator, v1.Unmanaged)
		testutils.WaitForManagementState(ctx, clients, v1.Unmanaged)

		By("Scaling up to 3 replicas")
		testutils.ScaleDeployment(ctx, clients, OperandName, 3)
		testutils.VerifyDeploymentReplicas(ctx, clients, OperandName, 3)
		testutils.VerifyPodCount(ctx, clients, operatorNamespace, operandLabel, 3)
	})

	It("when managementState is Removed test", func() {
		//note: Now we keep lws-controller-manager according to actual senarios"
		ctx := context.TODO()
		By("Fetching initial operator state")
		lwsOperator, originalState, err := testutils.GetOperatorState(ctx, clients)
		Expect(err).ShouldNot(HaveOccurred())
		originalPodCount := testutils.GetPodCount(ctx, clients, operatorNamespace, operandLabel)

		defer func() {
			newctx := context.TODO()
			testutils.SetManagementState(newctx, clients, lwsOperator, originalState)
			testutils.WaitForManagementState(newctx, clients, originalState)
			if originalState == "" || originalState == v1.Managed {
				testutils.VerifyDeploymentReplicas(newctx, clients, OperandName, int32(originalPodCount))
			}
			testutils.VerifyPodCount(newctx, clients, operatorNamespace, operandLabel, originalPodCount)
		}()
		By("Setting managementState to Removed")
		testutils.SetManagementState(ctx, clients, lwsOperator, v1.Removed)
		testutils.WaitForManagementState(ctx, clients, v1.Removed)

		By("Scaling up to 3 replicas")
		testutils.ScaleDeployment(ctx, clients, OperandName, 3)
		testutils.VerifyDeploymentReplicas(ctx, clients, OperandName, 3)
		testutils.VerifyPodCount(ctx, clients, operatorNamespace, operandLabel, 3)
	})
})

var _ = Describe("LWS Operator CRD and Webhook Management", Ordered, func() {
	It("should install all required CRDs", func() {
		ctx := context.TODO()

		requiredCRDs := []string{
			"leaderworkersets.leaderworkerset.x-k8s.io",
			"disaggregatedsets.disaggregatedset.x-k8s.io",
			"disaggregatedsetrolescalers.disaggregatedset.x-k8s.io",
			"leaderworkersetoperators.operator.openshift.io",
		}

		for _, crdName := range requiredCRDs {
			By(fmt.Sprintf("Checking CRD: %s", crdName))
			crd, err := clients.APIExtClient.CustomResourceDefinitions().Get(ctx, crdName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "CRD %s should exist", crdName)
			Expect(crd).NotTo(BeNil())
			klog.Infof("CRD %s is installed", crdName)
		}
	})

	It("should not have cert-manager annotation errors in operator logs", func() {
		ctx := context.TODO()
		pods, err := clients.KubeClient.CoreV1().Pods(operatorNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "name=openshift-lws-operator",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(pods.Items).NotTo(BeEmpty())

		for _, pod := range pods.Items {
			By(fmt.Sprintf("Checking logs for pod: %s", pod.Name))
			logs, err := clients.KubeClient.CoreV1().Pods(operatorNamespace).
				GetLogs(pod.Name, &corev1.PodLogOptions{}).
				Do(ctx).
				Raw()
			Expect(err).NotTo(HaveOccurred())

			logContent := string(logs)
			Expect(logContent).NotTo(ContainSubstring("is missing cert-manager.io/inject-ca-from annotation"),
				"Operator logs should not contain cert-manager annotation errors")
			klog.Infof("Pod %s logs verified - no cert-manager errors", pod.Name)
		}
	})

	It("should configure all required webhooks", func() {
		ctx := context.TODO()

		By("Checking ValidatingWebhookConfiguration")
		vwhc, err := clients.KubeClient.AdmissionregistrationV1().
			ValidatingWebhookConfigurations().
			Get(ctx, "lws-validating-webhook-configuration", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		webhookNames := []string{
			"vleaderworkerset.kb.io",
			"vpod.kb.io",
			"vdisaggregatedset.kb.io",
		}

		foundWebhooks := make(map[string]bool)
		for _, wh := range vwhc.Webhooks {
			foundWebhooks[wh.Name] = true
			klog.Infof("Found webhook: %s", wh.Name)
		}

		for _, name := range webhookNames {
			Expect(foundWebhooks).To(HaveKey(name), "Webhook %s should be configured", name)
		}

		By("Checking MutatingWebhookConfiguration")
		mwhc, err := clients.KubeClient.AdmissionregistrationV1().
			MutatingWebhookConfigurations().
			Get(ctx, "lws-mutating-webhook-configuration", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		mutatingWebhooks := []string{
			"mleaderworkerset.kb.io",
			"mpod.kb.io",
		}

		foundMutating := make(map[string]bool)
		for _, wh := range mwhc.Webhooks {
			foundMutating[wh.Name] = true
			klog.Infof("Found mutating webhook: %s", wh.Name)
		}

		for _, name := range mutatingWebhooks {
			Expect(foundMutating).To(HaveKey(name), "Mutating webhook %s should be configured", name)
		}
	})

	It("should start DisaggregatedSet controller in operand", func() {
		ctx := context.TODO()

		By("Getting operand pod logs")
		pods, err := clients.KubeClient.CoreV1().Pods(operatorNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: operandLabel,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(pods.Items).NotTo(BeEmpty())

		controllerFound := false
		for _, pod := range pods.Items {
			By(fmt.Sprintf("Checking logs for pod: %s", pod.Name))
			logs, err := clients.KubeClient.CoreV1().Pods(operatorNamespace).
				GetLogs(pod.Name, &corev1.PodLogOptions{}).
				Do(ctx).
				Raw()
			if err != nil {
				klog.Warningf("Could not get logs for pod %s: %v", pod.Name, err)
				continue
			}

			logContent := string(logs)

			By("Verifying DisaggregatedSet controller started")
			if strings.Contains(logContent, "Starting Controller") &&
				strings.Contains(logContent, "disaggregatedset") {
				controllerFound = true
				klog.Infof("DisaggregatedSet controller found in pod %s", pod.Name)

				// Also verify workers started
				Expect(logContent).To(ContainSubstring("Starting workers"),
					"DisaggregatedSet controller should start workers")
				break
			}
		}

		Expect(controllerFound).To(BeTrue(), "DisaggregatedSet controller should be running in at least one operand pod")
	})

	It("should have RBAC permissions for DisaggregatedSet resources", func() {
		ctx := context.TODO()

		By("Checking operand ClusterRole")
		clusterRole, err := clients.KubeClient.RbacV1().
			ClusterRoles().
			Get(ctx, "openshift-lws-operator-operand", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		hasDisaggregatedSetPermissions := false
		hasDisaggregatedSetRoleScalerPermissions := false

		for _, rule := range clusterRole.Rules {
			for _, apiGroup := range rule.APIGroups {
				if apiGroup == "disaggregatedset.x-k8s.io" {
					klog.Infof("Found disaggregatedset.x-k8s.io API group in ClusterRole")

					if containsString(rule.Resources, "disaggregatedsets") {
						hasDisaggregatedSetPermissions = true
						Expect(rule.Verbs).To(ContainElements("get", "list", "watch"),
							"Should have basic read permissions for disaggregatedsets")
						klog.Infof("DisaggregatedSet permissions verified")
					}

					if containsString(rule.Resources, "disaggregatedsetrolescalers") {
						hasDisaggregatedSetRoleScalerPermissions = true
						Expect(rule.Verbs).To(ContainElements("get", "list", "watch"),
							"Should have basic read permissions for disaggregatedsetrolescalers")
						klog.Infof("DisaggregatedSetRoleScaler permissions verified")
					}
				}
			}
		}

		Expect(hasDisaggregatedSetPermissions).To(BeTrue(),
			"ClusterRole should have permissions for disaggregatedsets")
		Expect(hasDisaggregatedSetRoleScalerPermissions).To(BeTrue(),
			"ClusterRole should have permissions for disaggregatedsetrolescalers")
	})

	It("should not require cert-manager annotation for DisaggregatedSet CRDs", func() {
		ctx := context.TODO()

		disaggregatedCRDs := []string{
			"disaggregatedsets.disaggregatedset.x-k8s.io",
			"disaggregatedsetrolescalers.disaggregatedset.x-k8s.io",
		}

		for _, crdName := range disaggregatedCRDs {
			By(fmt.Sprintf("Checking CRD: %s", crdName))
			crd, err := clients.APIExtClient.CustomResourceDefinitions().
				Get(ctx, crdName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			// DisaggregatedSet CRDs should not have conversion webhooks
			if crd.Spec.Conversion != nil && crd.Spec.Conversion.Strategy != "" {
				Expect(string(crd.Spec.Conversion.Strategy)).To(Equal("None"),
					"%s should not use webhook conversion", crdName)
				klog.Infof("CRD %s uses None conversion strategy", crdName)
			}

			// Should not have cert-manager annotation
			annotations := crd.GetAnnotations()
			_, hasCertManagerAnnotation := annotations["cert-manager.io/inject-ca-from"]
			Expect(hasCertManagerAnnotation).To(BeFalse(),
				"%s should not have cert-manager annotation", crdName)
			klog.Infof("CRD %s correctly has no cert-manager annotation", crdName)
		}

		By("Verifying LeaderWorkerSet CRD has cert-manager annotation")
		lwsCRD, err := clients.APIExtClient.CustomResourceDefinitions().
			Get(ctx, "leaderworkersets.leaderworkerset.x-k8s.io", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// LeaderWorkerSet should have the annotation
		annotations := lwsCRD.GetAnnotations()
		_, hasCertManagerAnnotation := annotations["cert-manager.io/inject-ca-from"]
		Expect(hasCertManagerAnnotation).To(BeTrue(),
			"LeaderWorkerSet CRD should have cert-manager annotation for webhook conversion")
		klog.Infof("LeaderWorkerSet CRD correctly has cert-manager annotation")
	})
})

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
