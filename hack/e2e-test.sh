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

SCRIPT_ROOT="$(realpath "$(dirname "$(readlink -f "$0")")"/..)"

# Upstream tests use kubectl instead of oc.
# We need to symlink oc to kubectl
KUBECTL_PATH="$(mktemp -d)"
export PATH=${KUBECTL_PATH}:${PATH}
if ! which kubectl >/dev/null; then
  ln -s "$(which oc)" "${KUBECTL_PATH}/kubectl"
fi

if [ -z "$KUBECONFIG" ]; then
  echo "KUBECONFIG is empty"
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

CLONE_PATH=""

function cleanup() {
  if [ -n "$CLONE_PATH" ]; then
    rm -rf "${CLONE_PATH}"
  fi
  rm -rf "${KUBECTL_PATH}"
}
trap cleanup EXIT SIGINT TERM

function cert_manager_deploy {
      oc apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.0/cert-manager.yaml
      oc -n cert-manager wait --for condition=ready pod -l app.kubernetes.io/instance=cert-manager --timeout=2m
}

function deploy_lws_operator {
      echo "Define operator and operand images built in CI"
      sed -i -e "s|\${OPERAND_IMAGE}|$RELATED_IMAGE_OPERAND_IMAGE|g" -e "s|\${OPERATOR_IMAGE}|$OPERATOR_IMAGE|g" deploy/05_deployment.yaml

      echo "Apply the resources under deploy directory"
      # Error is totally expected in here. Because we are applying
      # ordered resources in an unordered way (i.e. CRD should be ready when CR is applied).
      # A few simply retry should work.
      RETRY_COUNT=0
      while true; do
          if oc apply -f deploy/ --server-side; then
              break
          else
              RETRY_COUNT=$((RETRY_COUNT + 1))
              if [[ $RETRY_COUNT -ge 3 ]]; then
                  exit 1
              fi
              sleep 1
          fi
      done
      echo "Wait for the deployments to be available"
      oc wait deployment openshift-lws-operator -n openshift-lws-operator --for=create --timeout=2m
      oc wait deployment openshift-lws-operator -n openshift-lws-operator --for=condition=Available --timeout=5m
      oc wait deployment lws-controller-manager -n openshift-lws-operator --for=create --timeout=2m
      oc wait deployment lws-controller-manager -n openshift-lws-operator --for=condition=Available --timeout=5m
}

function run_e2e_operator_tests() {
  echo "Running e2e tests for operator"
  $GINKGO -v ./test/e2e/...
}

function run_e2e_operand_tests() {
  echo "Running e2e tests for operand"
  CLONE_PATH="$(mktemp -d)"
  BRANCH="$(cat "${SCRIPT_ROOT}/operand-git-ref")"
  git clone -b "${BRANCH}" "https://github.com/openshift/kubernetes-sigs-lws" "${CLONE_PATH}"
  pushd "${CLONE_PATH}"
    GO_VERSION=$(grep "^go " go.mod | awk '{print $2}')
    # extract the version in "1.24" format
    MAJOR_GO_VERSION=$(echo "${GO_VERSION}" | awk -F'.' '{print $1"."$2}')
    echo "GO_VERSION: $GO_VERSION MAJOR_GO_VERSION: $MAJOR_GO_VERSION"
    if ! go version | grep -q "go${MAJOR_GO_VERSION}"; then
      wget https://go.dev/dl/go"${GO_VERSION}".linux-amd64.tar.gz
      tar -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
      export PATH=${CLONE_PATH}/go/bin:$PATH
    fi
  popd
  LWS_NAMESPACE=openshift-lws-operator $GINKGO -v /"${CLONE_PATH}"/test/e2e/...
}

cert_manager_deploy
deploy_lws_operator
if [ "${RUN_OPERATOR_TEST:-}" == 'true' ]; then
  run_e2e_operator_tests
fi
if [ "${RUN_OPERAND_TEST:-}" == 'true' ]; then
  run_e2e_operand_tests
fi
