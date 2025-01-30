all: build
.PHONY: all

SOURCE_GIT_TAG ?=$(shell git describe --long --tags --abbrev=7 --match 'v[0-9]*' || echo 'v1.0.0-$(SOURCE_GIT_COMMIT)')
SOURCE_GIT_COMMIT ?=$(shell git rev-parse --short "HEAD^{commit}" 2>/dev/null)

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
	./_output/tools/bin/controller-gen crd paths=./pkg/apis/leaderworkersetoperator/v1/... schemapatch:manifests=./manifests output:crd:dir=./manifests
	mv manifests/operator.openshift.io_leaderworkersetoperators.yaml manifests/leaderworkerset-operator.crd.yaml
	cp manifests/operator.openshift.io_leaderworkersetoperators.yaml deploy/00_lws-operator.crd.yaml

generate: update-codegen-crds generate-clients
.PHONY: generate

generate-clients:
	GO=GO111MODULE=on GOFLAGS=-mod=readonly hack/update-codegen.sh
.PHONY: generate-clients

clean:
	$(RM) ./lws-operator
	$(RM) -r ./_tmp
.PHONY: clean
