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
LWS_DEPLOY_DIR="${SCRIPT_ROOT}/deploy"
CLUSTER_SERVICE_VERSION_FILE="${SCRIPT_ROOT}/manifests/leader-worker-set.clusterserviceversion.yaml"

# Ensure yq is installed
if ! command -v yq &> /dev/null; then
    echo "yq is not installed. Installing yq..."
    go install -mod=readonly github.com/mikefarah/yq/v4@v4.45.1
fi

yq -i '.spec.install.spec.clusterPermissions[0].rules = load("'"${LWS_DEPLOY_DIR}"'/04_00_clusterrole.yaml").rules' "${CLUSTER_SERVICE_VERSION_FILE}"
yq -i '.spec.install.spec.clusterPermissions[0].rules += load("'"${LWS_DEPLOY_DIR}"'/02_00_operand_clusterrole.yaml").rules' "${CLUSTER_SERVICE_VERSION_FILE}"
yq -i '.spec.install.spec.permissions[0].rules = load("'"${LWS_DEPLOY_DIR}"'/03_00_role.yaml").rules' "${CLUSTER_SERVICE_VERSION_FILE}"
yq -i '.spec.install.spec.permissions[0].rules += load("'"${LWS_DEPLOY_DIR}"'/02_03_operand_role.yaml").rules' "${CLUSTER_SERVICE_VERSION_FILE}"

yq -i '.spec.install.spec.deployments[0].spec = load("'"${LWS_DEPLOY_DIR}"'/05_deployment.yaml").spec' "${CLUSTER_SERVICE_VERSION_FILE}"
