# Architecture

## Overview

LeaderWorkerSet Operator is an OpenShift operator that deploys and manages the upstream [LeaderWorkerSet (LWS) controller](https://github.com/openshift/kubernetes-sigs-lws) on OpenShift clusters. LeaderWorkerSet is a Kubernetes API for deploying groups of pods as a unit of replication, primarily targeting AI/ML inference workloads — especially multi-host inference where an LLM is sharded across multiple devices/nodes.

The operator follows the operator-operand pattern: it manages the lifecycle of the LWS controller (the "operand"), which in turn manages `LeaderWorkerSet` custom resources created by end users.

The operator's primary responsibilities:
- Watch the `LeaderWorkerSetOperator` CR and reconcile the operand lifecycle
- Deploy and manage the LWS controller Deployment with TLS certificates and configuration
- Create cert-manager Issuer and Certificate resources for webhook and metrics TLS
- Manage RBAC (ClusterRoles, ClusterRoleBindings, Roles, RoleBindings) for the operand
- Manage MutatingWebhookConfiguration and ValidatingWebhookConfiguration with cert-manager CA injection
- Manage the LeaderWorkerSet CRD (the operand's CRD, with conversion webhook)
- Support configurable node placement for the operand deployment
- Provide Prometheus monitoring via ServiceMonitor

## Data Flow

```text
  LeaderWorkerSetOperator CR (operator.openshift.io/v1)
  (name: cluster, cluster-scoped)
              |
              v
  +------------------------------------------------------+
  |    TargetConfigReconciler (pkg/operator)               |
  |  (sequential sync: RBAC -> Certs -> Config ->          |
  |   CRD -> Webhooks -> ServiceMonitor -> Deployment)     |
  +------------------+-----------------------------------+
                     |
      +--------------+------------------+
      v              v                  v
  Deployment    ServiceAccount     MutatingWebhook +
  (lws-        + ClusterRoles     ValidatingWebhook
   controller-  + Roles           Configurations
   manager)     + Bindings        (cert-manager CA)
      |              ^
      v              |
  Controller    TLS Certs
  Pod(s)        (cert-manager
                 Issuer +
                 Certificate CRs)
      |
      v
  LeaderWorkerSet CRs (leaderworkerset.x-k8s.io)
  created by end users for AI/ML workloads
```

Users create a `LeaderWorkerSetOperator` CR. The operator deploys and manages the LWS controller as a Deployment in the `openshift-lws-operator` namespace. End users then create `LeaderWorkerSet` CRs to deploy groups of pods as a unit of replication.

## Operator Startup

Entry point: `cmd/lws-operator/main.go` -> `pkg/cmd/operator/cmd.go` -> `pkg/operator/starter.go`.

Startup sequence:
1. Create clients (Kubernetes, dynamic, apiextensions, discovery, operator CR client)
2. Set up informers for the operator namespace and cluster-wide resources
3. Create a `LeaderWorkerSetClient` (implements `v1helpers.OperatorClient` for library-go compatibility)
4. Create two controllers:
   - **TargetConfigReconciler** — the main reconciliation controller
   - **logLevelController** — manages operator log level settings
5. Start informers
6. Run controllers
7. Block until context cancellation

The operator uses OpenShift's `library-go` `controllercmd` framework, which provides leader election, health checks, and graceful shutdown.

## Custom Resource

The `LeaderWorkerSetOperator` CRD (`operator.openshift.io/v1`) is cluster-scoped and defines:

- **Spec fields** (embeds `operatorv1.OperatorSpec`):
  - Standard operator fields: `managementState`, `logLevel`, `operatorLogLevel`, `unsupportedConfigOverrides`, `observedConfig`
  - `nodePlacement` (optional) — controls scheduling of operand pods:
    - `nodeSelector` (map[string]string) — replaces the operand deployment's nodeSelector
    - `tolerations` ([]Toleration) — replaces the operand deployment's tolerations
- **Status fields** (embeds `operatorv1.OperatorStatus`):
  - `conditions[]`, `generations[]`, `observedGeneration`, `readyReplicas`

The CR must be named `cluster` (enforced via CEL validation).

## TargetConfigReconciler

`pkg/operator/target_config_reconciler.go` implements the main reconciliation loop. On each sync, it performs the following steps sequentially:

1. **ManagementState check** — reads operator spec; skips if not `Managed`
2. **Availability condition** — checks if the operand Deployment exists and is available
3. **cert-manager check** — verifies `cert-manager.io/v1/Issuer` is registered via discovery; sets `Degraded` with message if missing
4. **ClusterRoles** — applies manager, metrics-reader, proxy ClusterRoles from embedded assets
5. **ClusterRoleBindings** — applies manager, metrics-reader, proxy ClusterRoleBindings with namespace substitution on subjects
6. **Roles** — applies leader-election and prometheus-k8s Roles
7. **RoleBindings** — applies leader-election and prometheus-k8s RoleBindings
8. **Services** — applies webhook service and controller-manager-metrics service
9. **cert-manager Issuer** — creates self-signed Issuer CR via dynamic client
10. **cert-manager Certificates** — creates webhook serving cert and metrics cert with DNS name substitution (`SERVICE_NAME`, `SERVICE_NAMESPACE`)
11. **Secret readiness** — verifies webhook and metrics TLS secrets have `tls.crt` and `tls.key` populated; tracks resource versions in spec annotations
12. **ConfigMap** — applies controller configuration ConfigMap
13. **CRD** — applies LeaderWorkerSet CRD with conversion webhook namespace substitution and cert-manager CA injection; preserves existing CA bundle
14. **ServiceAccount** — applies controller-manager ServiceAccount
15. **Controller Service** — applies metrics service
16. **Webhooks** — applies MutatingWebhookConfiguration and ValidatingWebhookConfiguration with namespace and cert-manager CA injection
17. **ServiceMonitor** — applies Prometheus ServiceMonitor with TLS config using mounted client certs
18. **Deployment** — applies operand Deployment with:
    - Image from `RELATED_IMAGE_OPERAND_IMAGE` env var (replaces `${CONTROLLER_IMAGE}:latest` placeholder)
    - Spec annotations from secret/configmap resource versions for rolling updates
    - `--zap-log-level` arg mapped from operator logLevel (Normal=2, Debug=4, Trace=6, TraceAll=9)
    - `--config=/controller_manager_config.yaml` arg
    - NodePlacement from CR spec applied to pod template
19. **Status update** — sets deployment generation, ready replicas, available condition, clears degraded

The controller uses `factory.New()` from library-go with informers on the operator CR, deployments, configmaps, and secrets, resyncing every 5 minutes.

## Certificate Management

The operator uses **cert-manager** for TLS certificate management:

- **Self-signed Issuer** — `lws-selfsigned-issuer` created in the operator namespace
- **Webhook Certificate** — `lws-serving-cert` for the webhook server TLS
- **Metrics Certificate** — `lws-metrics-cert` for the metrics endpoint TLS
- **CA injection** — The `cert-manager.io/inject-ca-from` annotation is set on MutatingWebhookConfiguration, ValidatingWebhookConfiguration, and CRD resources, pointing to the webhook certificate in the operator namespace
- **DNS names** — Certificate DNS names include `SERVICE_NAME.SERVICE_NAMESPACE.svc` and `SERVICE_NAME.SERVICE_NAMESPACE.svc.cluster.local`, with placeholders substituted at runtime

The operator requires cert-manager to be installed on the cluster and checks for it at each reconciliation cycle.

## Embedded Asset Construction

Operand Kubernetes resources are read from embedded YAML assets in `bindata/assets/lws-controller-generated/`:

| Resource | Asset File | Purpose |
|----------|-----------|---------|
| Deployment | `apps_v1_deployment_lws-controller-manager.yaml` | LWS controller, with image/args/placement customization |
| ServiceAccount | `v1_serviceaccount_lws-controller-manager.yaml` | Identity for the controller pods |
| ClusterRole (manager) | `rbac..._clusterrole_lws-manager-role.yaml` | RBAC for managing LeaderWorkerSet resources |
| ClusterRole (metrics) | `rbac..._clusterrole_lws-metrics-reader.yaml` | RBAC for metrics access |
| ClusterRole (proxy) | `rbac..._clusterrole_lws-proxy-role.yaml` | RBAC for kube-rbac-proxy |
| ClusterRoleBindings | `rbac..._clusterrolebinding_lws-*.yaml` | Bindings for the above roles |
| Role (election) | `rbac..._role_lws-leader-election-role.yaml` | Leader election RBAC |
| Role (monitoring) | `rbac..._role_lws-prometheus-k8s.yaml` | Prometheus monitoring RBAC |
| RoleBindings | `rbac..._rolebinding_lws-*.yaml` | Bindings for the above roles |
| Service (webhook) | `v1_service_lws-webhook-service.yaml` | Webhook endpoint |
| Service (metrics) | `v1_service_lws-controller-manager-metrics-service.yaml` | Metrics endpoint |
| MutatingWebhookConfig | `admissionregistration..._mutatingwebhookconfiguration_*.yaml` | Webhook registration |
| ValidatingWebhookConfig | `admissionregistration..._validatingwebhookconfiguration_*.yaml` | Validation webhook registration |
| CRD | `apiextensions..._customresourcedefinition_leaderworkersets.*.yaml` | LeaderWorkerSet CRD with conversion webhook |
| ServiceMonitor | `monitoring..._servicemonitor_*.yaml` | Prometheus monitoring config |
| Issuer | `cert-manager..._issuer_lws-selfsigned-issuer.yaml` | Self-signed cert-manager Issuer |
| Certificate (webhook) | `cert-manager..._certificate_lws-serving-cert.yaml` | Webhook TLS certificate |
| Certificate (metrics) | `cert-manager..._certificate_lws-metrics-cert.yaml` | Metrics TLS certificate |

These assets are generated from the upstream LWS kustomize manifests via `make generate-controller-manifests` (`hack/update-lws-controller-manifests.sh`). The upstream git ref is tracked in the `operand-git-ref` file.

## Build System

Uses `build-machinery-go`. Key targets:

| Target | Description |
|--------|-------------|
| `make build` | Build operator binary (with `-tags strictfipsruntime`) |
| `make test` | Run unit tests (`./pkg/... ./cmd/...`) |
| `make test-e2e` | Run operator E2E tests (requires cluster) |
| `make test-e2e-operand` | Run upstream LWS operand E2E tests |
| `make verify` | Formatting, vetting, golang version checks |
| `make lint` | Run golangci-lint (v2) |
| `make regen-crd` | Regenerate CRD from Go types using `controller-gen` |
| `make generate` | Run all codegen (CRD + clients + controller manifests + CSV) |
| `make generate-clients` | Generate typed clients/informers/listers |
| `make generate-controller-manifests` | Pull upstream LWS manifests via kustomize |
| `make generate-bundle` | Generate OLM bundle via operator-sdk |
| `make clean` | Remove build artifacts |

**Base images:** Production uses `brew.registry.redhat.io` builder and `ubi9/ubi-minimal` runtime. CI uses `registry.ci.openshift.org` images.

**Go version:** see `go.mod`.

## Testing

**Unit tests**: Co-located `*_test.go` files in `pkg/operator/`. Coverage includes node placement application logic.

**E2E tests** (`test/e2e/`): Uses Ginkgo/Gomega framework. Tests include:
- Operator condition verification — no degraded condition, available condition is true
- Operand pod delete and recovery — verifies Deployment recreates pods
- NodePlacement propagation — sets `nodeSelector` and `tolerations` on CR, verifies they appear on operand Deployment
- ManagementState transitions:
  - Unmanaged: allows manual scaling of operand deployment
  - Removed: allows manual scaling of operand deployment
  - Restore to Managed: verifies original replica count is restored

**Operand E2E tests**: `make test-e2e-operand` clones the upstream LWS repository and runs its test suite against the deployed operand.

Run E2E with: `make test-e2e` (requires `OPERATOR_IMAGE`, `RELATED_IMAGE_OPERAND_IMAGE`, `KUBECONFIG` env vars). The `hack/e2e-test.sh` script deploys cert-manager, then the operator via `oc apply -f deploy/`, then runs the Ginkgo test suite.

## Namespace

The operator and operand run in `openshift-lws-operator` (constant `operatorNamespace` in `pkg/operator/starter.go`). The operator Deployment, operand Deployment, RBAC, Services, ConfigMap, Secrets, cert-manager resources, and ServiceMonitor all live here. The namespace has the label `openshift.io/cluster-monitoring: "true"` for Prometheus integration.

## Directory Structure

| Directory / File | Purpose |
|-----------------|---------|
| `cmd/lws-operator/` | Main operator entry point |
| `pkg/apis/leaderworkersetoperator/v1/` | LeaderWorkerSetOperator CRD types |
| `pkg/cmd/operator/` | Cobra command factory |
| `pkg/operator/` | Core operator logic (startup, reconciler, helpers) |
| `pkg/operator/operatorclient/` | Operator client adapter implementing `v1helpers.OperatorClient` |
| `pkg/generated/` | Auto-generated clientset, informers, listers, applyconfigs |
| `pkg/version/` | Build version info |
| `pkg/dependencymagnet/` | Build dependency imports |
| `bindata/` | Embedded operand manifests (Go embed) |
| `deploy/` | Manual (non-OLM) deployment manifests |
| `manifests/` | OLM bundle manifests (CSV, CRD) |
| `metadata/` | OLM metadata annotations |
| `hack/` | Code generation and helper scripts |
| `test/e2e/` | End-to-end test suite |
| `test/e2e/testutils/` | Test client setup and helper functions |
| `vendor/` | Vendored dependencies (don't modify directly) |
| `.tekton/` | Tekton/Konflux CI pipeline definitions |
| `operand-git-ref` | Upstream LWS git ref for manifest generation |

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| `library-go` controller framework | Consistent with other OpenShift operators; provides battle-tested leader election, health checks, and factory pattern |
| Sequential sync steps (not handler chain) | Single `sync()` method with sequential resource management calls; simpler than a formal handler chain while maintaining clear ordering |
| Embedded YAML assets via `//go:embed` | Upstream LWS manifests are generated from kustomize and embedded; changes to operand manifests go through `make generate-controller-manifests` |
| cert-manager for TLS | Delegates certificate lifecycle management to cert-manager rather than implementing self-signed cert generation; requires cert-manager as a prerequisite |
| Deployment (not DaemonSet) for operand | LWS controller runs as a standard Deployment, not a DaemonSet — appropriate for a controller-manager workload |
| Resource version annotations for rollouts | Secret and ConfigMap resource versions stored as Deployment spec annotations trigger rolling updates when certificate or config content changes |
| NodePlacement support | Allows cluster admins to control operand scheduling via the CR spec, useful for dedicated infra/control-plane nodes |
| Image placeholder substitution | The embedded deployment manifest uses `${CONTROLLER_IMAGE}:latest` as a placeholder, replaced at runtime from `RELATED_IMAGE_OPERAND_IMAGE` env var — standard OLM pattern for disconnected environments |
| Singleton CR via CEL validation | `LeaderWorkerSetOperator` CR name must be `cluster`, enforced by CEL validation rule on the CRD |
| Operand CRD managed by operator | The operator installs and manages the upstream LeaderWorkerSet CRD, including conversion webhook configuration |
