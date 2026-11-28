FROM registry.redhat.io/openshift/golang-builder:golang-builder-v1.26-rhel9 AS builder
WORKDIR /go/src/github.com/openshift/lws-operator
COPY . .

ARG OPERAND_IMAGE=registry.redhat.io/leader-worker-set/lws-rhel9@sha256:6b47b8efc6759e072dbced5e7165ce5ebc4932c4a3b4bfe6092fe6767f9a3b6e
ARG REPLACED_OPERAND_IMG=\${OPERAND_IMAGE}

# Replace the operand image in deploy/05_deployment.yaml with the one specified by the OPERAND_IMAGE build argument.
RUN hack/replace-image.sh deploy $REPLACED_OPERAND_IMG $OPERAND_IMAGE
RUN hack/replace-image.sh manifests $REPLACED_OPERAND_IMG $OPERAND_IMAGE

ARG OPERATOR_IMAGE=registry.redhat.io/leader-worker-set/lws-rhel9-operator@sha256:11dddfe9def2bc3ee4a8db6d30e401713b3abc2693415900092fe4d27955c7e0
ARG REPLACED_OPERATOR_IMG=\${OPERATOR_IMAGE}

# Replace the operand image in deploy/05_deployment.yaml with the one specified by the OPERATOR_IMAGE build argument.
RUN hack/replace-image.sh deploy $REPLACED_OPERATOR_IMG $OPERATOR_IMAGE
RUN hack/replace-image.sh manifests $REPLACED_OPERATOR_IMG $OPERATOR_IMAGE

RUN mkdir licenses
COPY LICENSE licenses/.

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest@sha256:08d4c70f1baf6537a0728656cf7942d561e74a35916509d205b6519861266352

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
LABEL cpe="cpe:/a:redhat:leader_worker_set:1.1::el9"
LABEL description="Leader Worker Set Operator is based on the [LWS](https://lws.sigs.k8s.io/docs/) open source project. LeaderWorkerSet: An API for deploying a group of pods as a unit of replication. It aims to address common deployment patterns of AI/ML inference workloads, especially multi-host inference workloads where the LLM will be sharded and run across multiple devices on multiple nodes."
LABEL distribution-scope="public"
LABEL name="leader-worker-set/lws-operator-bundle"
LABEL release="1.1.0"
LABEL version="1.1.0"
LABEL distribution-scope=public
LABEL url="https://github.com/openshift/lws-operator"
LABEL vendor="Red Hat, Inc."
LABEL summary="LeaderWorkerSet: An API for deploying a group of pods as a unit of replication"
LABEL io.k8s.display-name="Leader Worker Set" \
      io.k8s.description="This is an operator to manage Leader Worker Set" \
      io.openshift.tags="openshift,lws-operator" \
      com.redhat.delivery.appregistry=true \
      maintainer="AOS workloads team, <aos-workloads-staff@redhat.com>"
USER 1001
