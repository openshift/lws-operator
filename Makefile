all: build
.PHONY: all

SOURCE_GIT_TAG ?=$(shell git describe --long --tags --abbrev=7 --match 'v[0-9]*' || echo 'v1.0.0-$(SOURCE_GIT_COMMIT)')
SOURCE_GIT_COMMIT ?=$(shell git rev-parse --short "HEAD^{commit}" 2>/dev/null)

# Use go.mod go version as a single source of truth of Ginkgo version.
GINKGO_VERSION ?= $(shell go list -m -f '{{.Version}}' github.com/onsi/ginkgo/v2)

# OS_GIT_VERSION is populated by ART
# If building out of the ART pipeline, fallback to SOURCE_GIT_TAG
ifndef OS_GIT_VERSION
	OS_GIT_VERSION = $(SOURCE_GIT_TAG)
endif

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/images.mk \
	targets/openshift/deps.mk \
	targets/openshift/crd-schema-gen.mk \
)

# Exclude e2e tests from unit testing
GO_TEST_PACKAGES :=./pkg/... ./cmd/...
GO_BUILD_FLAGS :=-tags strictfipsruntime

IMAGE_REGISTRY := registry.ci.openshift.org

# This will call a macro called "build-image" which will generate image specific targets based on the parameters:
# $0 - macro name
# $1 - target name
# $2 - image ref
# $3 - Dockerfile path
# $4 - context directory for image build
$(call build-image,ocp-lws-operator,$(IMAGE_REGISTRY)/ocp/4.19:lws-operator, ./Dockerfile,.)

$(call verify-golang-versions,Dockerfile)

regen-crd:
	go build -o _output/tools/bin/controller-gen ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen
	rm manifests/leaderworkerset-operator.crd.yaml
	./_output/tools/bin/controller-gen crd paths=./pkg/apis/leaderworkersetoperator/v1/... output:crd:dir=./manifests
	mv manifests/operator.openshift.io_leaderworkersetoperators.yaml manifests/leaderworkerset-operator.crd.yaml
	cp manifests/leaderworkerset-operator.crd.yaml deploy/00_lws-operator.crd.yaml

generate: generate-clients regen-crd generate-controller-manifests
.PHONY: generate

generate-clients:
	GO=GO111MODULE=on GOFLAGS=-mod=readonly hack/update-codegen.sh
.PHONY: generate-clients

generate-controller-manifests:
	hack/update-lws-controller-manifests.sh
.PHONY: generate-controller-manifests

verify-codegen:
	hack/verify-codegen.sh
.PHONY: verify-codegen

verify-controller-manifests:
	hack/verify-lws-controller-manifests.sh
.PHONY: verify-controller-manifests

clean:
	$(RM) ./lws-operator
	$(RM) -r ./_tmp
	$(RM) -r ./_output
.PHONY: clean

GINKGO = $(shell pwd)/_output/tools/bin/ginkgo
.PHONY: ginkgo
ginkgo: ## Download ginkgo locally if necessary.
	test -s $(shell pwd)/_output/tools/bin/ginkgo || GOFLAGS=-mod=readonly GOBIN=$(shell pwd)/_output/tools/bin go install github.com/onsi/ginkgo/v2/ginkgo@$(GINKGO_VERSION)

test-e2e: ginkgo
	RUN_OPERATOR_TEST=true GINKGO=$(GINKGO) hack/e2e-test.sh
.PHONY: test-e2e

test-e2e-operand: ginkgo
	RUN_OPERAND_TEST=true GINKGO=$(GINKGO) hack/e2e-test.sh
.PHONY: test-e2e
