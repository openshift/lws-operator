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
TMP_DIR=$(mktemp -d)

#LWS_REPO_URL="https://github.com/openshift/kubernetes-sigs-lws.git"
#USE DOWNSTREAM WHEN IT IS SYNCED
LWS_REPO_URL="https://github.com/kubernetes-sigs/lws.git"
export LWS_BRANCH_OR_TAG="${LWS_BRANCH_OR_TAG:-$(cat "${SCRIPT_ROOT}/operand-git-ref")}"
export LWS_CONTROLLER_DIR="${LWS_CONTROLLER_DIR:-${TMP_DIR}/go/src/sigs.k8s.io/lws}"

git clone --branch "$LWS_BRANCH_OR_TAG" "$LWS_REPO_URL" "$LWS_CONTROLLER_DIR"

"${SCRIPT_ROOT}/hack/update-lws-controller-manifests.sh"

rm -rf "${TMP_DIR}"

if [ -n "$(git status --porcelain -- bindata/assets/lws-controller-generated/)" ];then
    echo "assets do not match with the github.com/openshift/kubernetes-sigs-lws $LWS_BRANCH_OR_TAG. Please run update-lws-controller-manifests.sh script" >&2
    exit 2
fi