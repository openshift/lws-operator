FROM registry.redhat.io/openshift/golang-builder:golang-builder-v1.26-rhel9 AS builder
WORKDIR /go/src/github.com/openshift/lws-operator
COPY . .
RUN make build --warn-undefined-variables

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest@sha256:08d4c70f1baf6537a0728656cf7942d561e74a35916509d205b6519861266352
COPY --from=builder /go/src/github.com/openshift/lws-operator/lws-operator /usr/bin/
RUN mkdir /licenses
COPY --from=builder /go/src/github.com/openshift/lws-operator/LICENSE /licenses/.

LABEL com.redhat.component="Leader Worker Set"
LABEL description="LeaderWorkerSet Operator manages the Leader Worker Set."
LABEL name="leader-worker-set/lws-rhel9-operator"
LABEL cpe="cpe:/a:redhat:leader_worker_set:1.1::el9"
LABEL summary="LeaderWorkerSet Operator manages the Leader Worker Set."
LABEL release="1.1.0"
LABEL version="1.1.0"
LABEL distribution-scope=public
LABEL url="https://github.com/openshift/lws-operator"
LABEL vendor="Red Hat, Inc."
LABEL io.k8s.display-name="LeaderWorkerSet Operator" \
      io.k8s.description="This is an operator to manage Leader Worker Set" \
      io.openshift.tags="openshift,lws-operator" \
      com.redhat.delivery.appregistry=true \
      maintainer="AOS workloads team, <aos-workloads-staff@redhat.com>"
USER 1001
