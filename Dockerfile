FROM brew.registry.redhat.io/rh-osbs/openshift-golang-builder:rhel_9_1.24 as builder
WORKDIR /go/src/github.com/openshift/lws-operator
COPY . .
RUN make build --warn-undefined-variables

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest@sha256:2ddd6e10383981c7d10e4966a7c0edce7159f8ca91b1691cafabc78bae79d8f8
COPY --from=builder /go/src/github.com/openshift/lws-operator/lws-operator /usr/bin/
RUN mkdir /licenses
COPY --from=builder /go/src/github.com/openshift/lws-operator/LICENSE /licenses/.

LABEL com.redhat.component="Leader Worker Set"
LABEL description="LeaderWorkerSet Operator manages the Leader Worker Set."
LABEL name="leader-worker-set/lws-rhel9-operator"
LABEL cpe="cpe:/a:redhat:leader_worker_set:1.0::el9"
LABEL summary="LeaderWorkerSet Operator manages the Leader Worker Set."
LABEL io.k8s.display-name="LeaderWorkerSet Operator" \
      io.k8s.description="This is an operator to manage Leader Worker Set" \
      io.openshift.tags="openshift,lws-operator" \
      com.redhat.delivery.appregistry=true \
      maintainer="AOS workloads team, <aos-workloads-staff@redhat.com>"
USER 1001
