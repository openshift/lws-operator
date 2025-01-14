all: build
.PHONY: all

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/images.mk \
	targets/openshift/codegen.mk \
	targets/openshift/deps.mk \
	targets/openshift/crd-schema-gen.mk \
)

# Exclude e2e tests from unit testing
GO_TEST_PACKAGES :=./pkg/... ./cmd/...
GO_BUILD_FLAGS :=-tags strictfipsruntime

IMAGE_REGISTRY := registry.ci.openshift.org

CODEGEN_OUTPUT_PACKAGE :=github.com/openshift/lws-operator/pkg/generated
CODEGEN_API_PACKAGE :=github.com/openshift/lws-operator/pkg/apis
CODEGEN_GROUPS_VERSION :=lwsoperator:v1alpha1

# This will call a macro called "build-image" which will generate image specific targets based on the parameters:
# $0 - macro name
# $1 - target name
# $2 - image ref
# $3 - Dockerfile path
# $4 - context directory for image build
$(call build-image,ocp-lws-operator,$(IMAGE_REGISTRY)/ocp/4.19:lws-operator, ./Dockerfile,.)

$(call verify-golang-versions,Dockerfile)

$(call add-crd-gen,lwsoperator,./pkg/apis/lwsoperator/v1alpha1,./manifests/,./manifests/)

generate: update-codegen-crds generate-clients
.PHONY: generate

generate-clients:
	GO=GO111MODULE=on GOFLAGS=-mod=readonly hack/update-codegen.sh
.PHONY: generate-clients

clean:
	$(RM) ./lws-operator
	$(RM) -r ./_tmp
.PHONY: clean
