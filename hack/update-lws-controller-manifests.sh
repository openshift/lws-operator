#!/usr/bin/env bash
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
LWS_ASSETS_DIR="${SCRIPT_ROOT}/bindata/assets/lws-controller-generated"
LWS_DEPLOY_DIR="${SCRIPT_ROOT}/deploy"
LWS_CONTROLLER_DIR="${LWS_CONTROLLER_DIR:-${HOME}/go/src/sigs.k8s.io/lws}"

LWS_BRANCH_OR_TAG="${LWS_BRANCH_OR_TAG:-"$(cat "${SCRIPT_ROOT}/operand-git-ref")"}"
LWS_NAMESPACE="${LWS_NAMESPACE:-openshift-lws-operator}"

# Ensure yq is installed
if ! command -v yq &> /dev/null; then
    echo "yq is not installed. Installing yq..."
    go install -mod=readonly github.com/mikefarah/yq/v4@v4.45.1
fi

if [ ! -d "${LWS_CONTROLLER_DIR}" ]; then
  echo "${LWS_CONTROLLER_DIR} is not a valid directory" >&2
  exit 1
fi
if [ -d "${LWS_ASSETS_DIR}" ];then
  rm -r "${LWS_ASSETS_DIR}"
fi
mkdir -p "${LWS_ASSETS_DIR}" "${SCRIPT_ROOT}/_tmp"

pushd "${LWS_CONTROLLER_DIR}"
  if [ -n "$(git status --porcelain)" ];then
      echo "${LWS_CONTROLLER_DIR} is not a clean git directory" >&2
      exit 2
  fi
  # ensure kustomize exists or download it
  GOFLAGS='-mod=readonly' make kustomize

  ORIGINAL_GIT_BRANCH_OR_COMMIT="$(git branch --show-current)"
  if [[ -z "${ORIGINAL_GIT_BRANCH_OR_COMMIT}" ]]; then
      ORIGINAL_GIT_BRANCH_OR_COMMIT="$(git rev-parse HEAD)"
  fi

  git checkout "${LWS_BRANCH_OR_TAG}"
    # backup kustomization.yaml and edit the default values
    pushd "${LWS_CONTROLLER_DIR}/config/default"
      cp "${LWS_CONTROLLER_DIR}/config/default/kustomization.yaml" "${SCRIPT_ROOT}/_tmp/lws_kustomization.yaml.bak"
      "${LWS_CONTROLLER_DIR}/bin/kustomize" edit add patch --path "webhookcainjection_patch.yaml"
      "${LWS_CONTROLLER_DIR}/bin/kustomize" edit set namespace "${LWS_NAMESPACE}"
      "${LWS_CONTROLLER_DIR}/bin/kustomize" edit add resource "../prometheus"
      "${LWS_CONTROLLER_DIR}/bin/kustomize" edit add resource "../certmanager"
      "${LWS_CONTROLLER_DIR}/bin/kustomize" edit remove resource "../internalcert"
    popd
    pushd "${LWS_CONTROLLER_DIR}/config/crd"
      cp "${LWS_CONTROLLER_DIR}/config/crd/kustomization.yaml" "${SCRIPT_ROOT}/_tmp/lws_crd_kustomization.yaml.bak"
      "${LWS_CONTROLLER_DIR}/bin/kustomize" edit add patch --path "patches/cainjection_in_leaderworkersets.yaml"
    popd
    pushd "${LWS_CONTROLLER_DIR}/config/manager"
      cp "${LWS_CONTROLLER_DIR}//config/manager/kustomization.yaml" "${SCRIPT_ROOT}/_tmp/lws_components_manager_kustomization.yaml.bak"
      "${LWS_CONTROLLER_DIR}/bin/kustomize" edit set image controller='${CONTROLLER_IMAGE}:latest'
    popd
    "${LWS_CONTROLLER_DIR}/bin/kustomize" build config/default -o "${LWS_ASSETS_DIR}"
    # restore back to the original state
    mv "${SCRIPT_ROOT}/_tmp/lws_kustomization.yaml.bak" "${LWS_CONTROLLER_DIR}/config/default/kustomization.yaml"
    mv "${SCRIPT_ROOT}/_tmp/lws_crd_kustomization.yaml.bak" "${LWS_CONTROLLER_DIR}/config/crd/kustomization.yaml"
    mv  "${SCRIPT_ROOT}/_tmp/lws_components_manager_kustomization.yaml.bak" "${LWS_CONTROLLER_DIR}/config/manager/kustomization.yaml"
  git checkout "${ORIGINAL_GIT_BRANCH_OR_COMMIT}"
popd

# post processing
pushd "${LWS_ASSETS_DIR}"
  # we need to modify prometheus rolebinding to use openshift-monitoring namespace
  sed -i 's/namespace: monitoring/namespace: openshift-monitoring/g' rbac.authorization.k8s.io_v1_rolebinding_lws-prometheus-k8s.yaml
  # we supply our own config
  if [ -e "./v1_configmap_lws-manager-config.yaml" ]; then
    rm ./v1_configmap_lws-manager-config.yaml
  fi
  # we don't need the namespace object
  if [ -e "./v1_namespace_openshift-lws-operator.yaml" ]; then
    rm ./v1_namespace_openshift-lws-operator.yaml
  fi

  rm -f "${LWS_DEPLOY_DIR}/02_00_operand_clusterrole.yaml"
  rm -f "${LWS_DEPLOY_DIR}/02_03_operand_role.yaml"

cat >"${LWS_DEPLOY_DIR}/02_00_operand_clusterrole.yaml" <<EOL
kind: ClusterRole
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: openshift-lws-operator-operand
rules:
EOL
cat >"${LWS_DEPLOY_DIR}/02_03_operand_role.yaml" <<EOL
kind: Role
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: openshift-lws-operator-operand
  namespace: openshift-lws-operator
rules:
EOL

  for clusterRole in rbac.authorization.k8s.io_v1_clusterrole_*.yaml; do
    yq -oyaml ".rules" "$clusterRole" >> "${LWS_DEPLOY_DIR}/02_00_operand_clusterrole.yaml"
  done
  for role in rbac.authorization.k8s.io_v1_role_*.yaml; do
    yq -oyaml ".rules" "$role" >> "${LWS_DEPLOY_DIR}/02_03_operand_role.yaml"
  done
popd

