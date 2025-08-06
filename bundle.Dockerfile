FROM brew.registry.redhat.io/rh-osbs/openshift-golang-builder:rhel_9_1.23 as builder
WORKDIR /go/src/github.com/openshift/lws-operator
COPY . .

ARG OPERAND_IMAGE=registry.redhat.io/leader-worker-set/lws-rhel9@sha256:c6c825ba5bf39ab1e2e38190bef3f3d40eef3702ffb372ccf973e31aec043e80
ARG REPLACED_OPERAND_IMG=\${OPERAND_IMAGE}

# Replace the operand image in deploy/05_deployment.yaml with the one specified by the OPERAND_IMAGE build argument.
RUN hack/replace-image.sh deploy $REPLACED_OPERAND_IMG $OPERAND_IMAGE
RUN hack/replace-image.sh manifests $REPLACED_OPERAND_IMG $OPERAND_IMAGE

ARG OPERATOR_IMAGE=registry.redhat.io/leader-worker-set/lws-rhel9-operator@sha256:17ebdb5c47f6ccb8f4fa50daa871d439d825df55a6d474ed098987f6d237ffc4
ARG REPLACED_OPERATOR_IMG=\${OPERATOR_IMAGE}

# Replace the operand image in deploy/05_deployment.yaml with the one specified by the OPERATOR_IMAGE build argument.
RUN hack/replace-image.sh deploy $REPLACED_OPERATOR_IMG $OPERATOR_IMAGE
RUN hack/replace-image.sh manifests $REPLACED_OPERATOR_IMG $OPERATOR_IMAGE

RUN mkdir licenses
COPY LICENSE licenses/.

FROM registry.redhat.io/rhel9-4-els/rhel-minimal:9.4

LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=leader-worker-set
LABEL operators.operatorframework.io.bundle.channels.v1=stable
LABEL operators.operatorframework.io.bundle.channel.default.v1=stable
LABEL operators.operatorframework.io.metrics.builder=operator-sdk-v1.34.2
LABEL operators.operatorframework.io.metrics.mediatype.v1=metrics+v1

COPY --from=builder /go/src/github.com/openshift/lws-operator/manifests /manifests
COPY --from=builder /go/src/github.com/openshift/lws-operator/metadata /metadata
COPY --from=builder /go/src/github.com/openshift/lws-operator/licenses /licenses

LABEL com.redhat.component="Leader Worker Set"
LABEL description="LeaderWorkerSet: An API for deploying a group of pods as a unit of replication. It aims to address common deployment patterns of AI/ML inference workloads, especially multi-host inference workloads where the LLM will be sharded and run across multiple devices on multiple nodes."
LABEL distribution-scope="public"
LABEL name="lws-operator-bundle"
LABEL release="1.0.0"
LABEL version="1.0.0"
LABEL url="https://github.com/openshift/lws-operator"
LABEL vendor="Red Hat, Inc."
LABEL name="lws-operator-bundle"
LABEL summary="LeaderWorkerSet: An API for deploying a group of pods as a unit of replication"
LABEL io.k8s.display-name="Leader Worker Set" \
      io.k8s.description="This is an operator to manage Leader Worker Set" \
      io.openshift.tags="openshift,lws-operator" \
      com.redhat.delivery.appregistry=true \
      maintainer="AOS workloads team, <aos-workloads-staff@redhat.com>"
USER 1001
