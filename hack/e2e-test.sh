#!/usr/bin/env bash

# Copyright 2025 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

function cert_manager_deploy {
      oc apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.0/cert-manager.yaml
      oc -n cert-manager wait --for condition=ready pod -l app.kubernetes.io/instance=cert-manager --timeout=2m
}

function deploy_lws_operator {
      if [ -z "$KUBECONFIG" ]; then
        echo "KUBECONFIG is empty"
        exit 1
      fi
      if [ -z "$NAMESPACE" ]; then
        echo "NAMESPACE is empty"
        exit 1
      fi
      if [ -z "$RELATED_IMAGE_OPERAND_IMAGE" ]; then
        echo "RELATED_IMAGE_OPERAND_IMAGE is empty"
        exit 1
      fi
      if [ -z "$OPERATOR_IMAGE" ]; then
        echo "OPERATOR_IMAGE is empty"
        exit 1
      fi

      echo "Define operator and operand images built in CI"
      sed -i "s|\${OPERAND_IMAGE}|$RELATED_IMAGE_OPERAND_IMAGE|g" deploy/05_deployment.yaml
      sed -i "s|\${OPERATOR_IMAGE}|$OPERATOR_IMAGE|g" deploy/05_deployment.yaml

      echo "Apply the resources under deploy directory"
      oc apply -f deploy/ --server-side
      echo "Wait for the deployments to be available"
      oc wait deployment openshift-lws-operator -n openshift-lws-operator --for=condition=Available --timeout=5m
      oc wait deployment lws-controller-manager -n openshift-lws-operator --for=condition=Available --timeout=5m
}

cert_manager_deploy
deploy_lws_operator