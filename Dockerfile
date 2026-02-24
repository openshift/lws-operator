FROM brew.registry.redhat.io/rh-osbs/openshift-golang-builder:rhel_9_1.25 as builder
WORKDIR /go/src/github.com/openshift/lws-operator
COPY . .
RUN make build --warn-undefined-variables

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest@sha256:c7d44146f826037f6873d99da479299b889473492d3c1ab8af86f08af04ec8a0
COPY --from=builder /go/src/github.com/openshift/lws-operator/lws-operator /usr/bin/
RUN mkdir /licenses
COPY --from=builder /go/src/github.com/openshift/lws-operator/LICENSE /licenses/.

LABEL com.redhat.component="Leader Worker Set"
LABEL description="LeaderWorkerSet Operator manages the Leader Worker Set."
LABEL name="leader-worker-set/lws-rhel9-operator"
LABEL cpe="cpe:/a:redhat:leader_worker_set:1.0::el9"
LABEL summary="LeaderWorkerSet Operator manages the Leader Worker Set."
LABEL release="1.0.0"
LABEL version="1.0.0"
LABEL distribution-scope=public
LABEL url="https://github.com/openshift/lws-operator"
LABEL vendor="Red Hat, Inc."
LABEL io.k8s.display-name="LeaderWorkerSet Operator" \
      io.k8s.description="This is an operator to manage Leader Worker Set" \
      io.openshift.tags="openshift,lws-operator" \
      com.redhat.delivery.appregistry=true \
      maintainer="AOS workloads team, <aos-workloads-staff@redhat.com>"
USER 1001
