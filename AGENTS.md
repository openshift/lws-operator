# AI Agent Guide for LeaderWorkerSet Operator

This file provides guidance for AI agents working with the OpenShift LeaderWorkerSet Operator repository.

## Overview

**What is LeaderWorkerSet Operator?**
An OpenShift operator that deploys and manages the upstream [LeaderWorkerSet (LWS) controller](https://github.com/openshift/kubernetes-sigs-lws) on OpenShift clusters. LeaderWorkerSet is a Kubernetes API for deploying groups of pods as a unit of replication, primarily targeting AI/ML inference workloads — especially multi-host inference where an LLM is sharded across multiple devices/nodes.

The operator follows the **operator-operand pattern**: the operator itself manages the lifecycle of the LWS controller (the "operand"), which in turn manages `LeaderWorkerSet` custom resources created by end users. The operand runs as a Deployment (`lws-controller-manager`) in the `openshift-lws-operator` namespace.

The operator uses OpenShift's `library-go` controller framework (not `controller-runtime` or `operator-sdk`). It relies on **cert-manager** for TLS certificate management rather than managing its own certificates.

## Build and Test

```bash
make build        # Build all binaries (with -tags strictfipsruntime)
make test         # Unit tests (pkg/... cmd/...)
make verify       # Formatting, vetting, golang version checks
make lint         # Run golangci-lint (v2)
make test-e2e     # Operator E2E tests (requires cluster, OPERATOR_IMAGE, RELATED_IMAGE_OPERAND_IMAGE, KUBECONFIG)
make test-e2e-operand  # Upstream LWS operand E2E tests
make generate     # Run all codegen (clients, CRD, controller manifests, CSV)
make regen-crd    # Regenerate CRD from Go types using controller-gen
make generate-clients          # Generate typed clients/informers/listers
make generate-controller-manifests  # Pull upstream LWS manifests via kustomize
```

Go version: see `go.mod`.

## Project Structure

| Directory / File | Purpose |
|-----------------|---------|
| `cmd/lws-operator/` | Main operator binary entry point |
| `pkg/apis/leaderworkersetoperator/v1/` | `LeaderWorkerSetOperator` CRD type definitions |
| `pkg/cmd/operator/cmd.go` | Cobra command factory, wires `controllercmd` framework |
| `pkg/operator/starter.go` | `RunOperator()` — creates clients, informers, starts all controllers |
| `pkg/operator/target_config_reconciler.go` | Core `TargetConfigReconciler` — main reconciliation logic |
| `pkg/operator/resource.go` | ServiceMonitor helpers |
| `pkg/operator/node_placement.go` | NodePlacement application logic |
| `pkg/operator/operatorclient/` | Operator client adapter — constants, `LeaderWorkerSetClient` implementing `v1helpers.OperatorClient` |
| `pkg/generated/` | Auto-generated clientset, informers, listers, applyconfigs — **do not modify directly** |
| `pkg/version/version.go` | Build version info + Prometheus metric |
| `bindata/` | `//go:embed` assets for operand manifests |
| `bindata/assets/lws-controller-generated/` | Kustomize-generated upstream LWS manifests (CRD, deployments, RBAC, webhooks, certs, services) |
| `bindata/assets/lws-controller-config/` | Operator-supplied controller configuration |
| `bindata/assets/lws-controller/` | ConfigMap template |
| `deploy/` | Manual (non-OLM) deployment manifests (numbered `00_` through `07_`) |
| `manifests/` | OLM bundle manifests (CSV, CRD) |
| `metadata/` | OLM metadata annotations |
| `test/e2e/` | E2E test suite — Ginkgo/Gomega framework |
| `test/e2e/testutils/` | Test client setup and helper functions |
| `hack/` | Code generation, verification, and helper scripts |
| `vendor/` | Vendored dependencies — **do not modify directly** |
| `.tekton/` | Tekton/Konflux CI pipeline definitions |
| `Dockerfile` | Production container image build (brew.registry.redhat.io base) |
| `Dockerfile.ci` | CI container image build (registry.ci.openshift.org base) |
| `bundle.Dockerfile` | OLM operator bundle image |
| `Makefile` | Build targets (includes `build-machinery-go`) |
| `go.mod` | Go module dependencies |
| `operand-git-ref` | Git ref for upstream LWS operand |

## Controller Pattern

The operator runs two controllers, wired in `pkg/operator/starter.go` via the library-go controller framework:

**`TargetConfigReconciler`** — the main reconciliation controller (`pkg/operator/target_config_reconciler.go`). Each sync cycle performs the following steps sequentially:

1. **ManagementState check** — skips reconciliation if not `Managed`
2. **Availability check** — checks if the operand Deployment is available and sets the `Available` condition
3. **cert-manager verification** — checks that `cert-manager.io/v1/Issuer` is registered; sets `Degraded` if missing
4. **ClusterRoles** — manager, metrics-reader, proxy roles
5. **ClusterRoleBindings** — manager, metrics-reader, proxy bindings (with namespace substitution on subjects)
6. **Roles** — leader-election, prometheus-k8s roles
7. **RoleBindings** — leader-election, prometheus-k8s bindings
8. **Services** — webhook service, controller-manager-metrics service
9. **cert-manager Issuer** — self-signed issuer CR
10. **cert-manager Certificates** — webhook serving cert, metrics cert (with DNS name substitution)
11. **Secret readiness checks** — verifies webhook and metrics TLS secrets are populated
12. **ConfigMap** — controller configuration
13. **CRD** — LeaderWorkerSet CRD (with conversion webhook namespace substitution and cert-manager CA injection)
14. **ServiceAccount** — controller-manager service account
15. **Webhooks** — MutatingWebhookConfiguration and ValidatingWebhookConfiguration (with namespace and cert-manager CA injection)
16. **ServiceMonitor** — Prometheus monitoring (with TLS config for metrics client certs)
17. **Deployment** — operand Deployment with image substitution, log level args, node placement, and spec annotations for rolling updates
18. **Status update** — sets deployment generation, ready replicas, available condition, clears degraded

**`logLevelController`** — (from library-go) manages log level settings based on the CR spec.

**Key Concepts:**
- **Embedded assets via `//go:embed`:** Upstream LWS manifests are embedded in `bindata/assets/` and read at runtime via `bindata.MustAsset()`.
- **Server-side apply:** Uses `resourceapply.Apply*` functions for declarative resource management.
- **Owner references:** All managed resources get owner references pointing to the `LeaderWorkerSetOperator` CR.
- **Dynamic namespace substitution:** Namespace placeholders (`SERVICE_NAMESPACE`, `CERTIFICATE_NAMESPACE`) in embedded manifests are replaced at runtime.
- **cert-manager integration:** TLS certificates are managed by cert-manager, not self-signed by the operator. The operator creates Issuer and Certificate CRs and injects the `cert-manager.io/inject-ca-from` annotation into webhooks and CRDs.
- **Resource version tracking:** Secret and ConfigMap resource versions are stored as annotations on the operand Deployment spec to trigger rolling updates when content changes.

## Key Conventions

- **Namespace:** The operator and operand both run in `openshift-lws-operator`. Constant in `pkg/operator/starter.go`.
- **CR name:** Must be `cluster` (constant `OperatorConfigName` in `pkg/operator/operatorclient/interfaces.go`).
- **Operand deployment name:** `lws-controller-manager` (constant `operandName` in `pkg/operator/starter.go`).
- **Operand image:** Injected via `RELATED_IMAGE_OPERAND_IMAGE` environment variable.
- **Logging:** `k8s.io/klog/v2` with verbosity levels. The operand uses zap with `--zap-log-level` mapped from operator's `logLevel` spec field.
- **Error handling:** Wrap with `fmt.Errorf("context: %w", err)`; return errors for retry, return `nil` for non-retriable conditions.
- **CRD changes:** Modify `pkg/apis/leaderworkersetoperator/v1/types.go`, then run `make regen-crd` and `make generate-clients`.
- **Build tags:** `strictfipsruntime` is set for all Go builds.
- **Owner tracking:** Resources are tracked via OwnerReferences pointing to the `LeaderWorkerSetOperator` CR.

## Critical Rules

### DO NOT
1. **Don't modify CRD definitions** in `pkg/apis/leaderworkersetoperator/v1/` without understanding backward compatibility implications
2. **Don't modify `vendor/`** — always use `go mod tidy && go mod vendor`
3. **Don't modify `pkg/generated/`** — always use `make generate-clients`
4. **Don't modify `zz_generated.deepcopy.go`** — always use `make generate`
5. **Don't skip `make verify`** before considering work complete
6. **Don't log secrets** — TLS certificates, private keys, or auth tokens must never appear in logs
7. **Don't modify OWNERS files** without explicit direction from maintainers
8. **Don't introduce controller-runtime controllers** — use the library-go controller factory pattern

### DO
1. **Run `make verify`** before submitting any changes
2. **Run `make test`** to ensure unit tests pass
3. **Run `make lint`** to ensure linting passes
4. **Use structured logging** via klog with appropriate verbosity levels
5. **Follow Kubernetes API conventions** for CRD status conditions
6. **Handle errors gracefully** and return meaningful error messages
7. **Use the library-go controller factory pattern** for any new controllers
8. **Keep `deploy/` and `manifests/` in sync** — CRD is copied between these locations (use `make regen-crd`)
9. **Document architectural decisions** in ARCHITECTURE.md

## Non-Obvious Internals

- **`controllercmd` framework:** The entry point chain (`cmd/lws-operator/` → `pkg/cmd/operator/` → `pkg/operator/starter.go`) passes through library-go's `controllercmd.ControllerCommandConfig`, which handles leader election, signal handling, health checks, and serving info.
- **Namespace fallback:** In `starter.go`, if `cc.OperatorNamespace` is `"openshift-config-managed"` (the library-go default when running outside a cluster), it falls back to `"openshift-lws-operator"`.
- **Embedded manifest approach:** All operand Kubernetes resources are read from embedded YAML assets in `bindata/assets/lws-controller-generated/` via `resourceread.Read*OrDie()`. These assets are generated from upstream LWS manifests via `make generate-controller-manifests`.
- **cert-manager dependency:** The operator requires cert-manager to be installed. It checks for the `cert-manager.io/v1/Issuer` resource via discovery and sets a `Degraded` condition with a clear message if cert-manager is missing. The operator creates `Issuer` and `Certificate` CRs as unstructured resources via the dynamic client.
- **Image substitution:** The operand deployment template uses `${CONTROLLER_IMAGE}:latest` as a placeholder image. The reconciler replaces it with the actual image from the `RELATED_IMAGE_OPERAND_IMAGE` environment variable.
- **Log level mapping:** The operator maps OpenShift `logLevel` values (Normal, Debug, Trace, TraceAll) to zap numeric levels (2, 4, 6, 9) for the operand's `--zap-log-level` arg.
- **LeaderWorkerSetClient:** The custom client in `pkg/operator/operatorclient/interfaces.go` implements `v1helpers.OperatorClient` for library-go compatibility, with both informer-cached and direct API reads.
- **CRD lives in two places:** `manifests/` is the source of truth. `make regen-crd` copies to `deploy/00_lws-operator.crd.yaml`.
- **Conversion webhook:** The operand CRD has a conversion webhook that requires namespace substitution and cert-manager CA bundle injection.
- **ServiceMonitor customization:** The reconciler modifies ServiceMonitor endpoints to use mounted Prometheus client certs from `/etc/prometheus/secrets/metrics-client-certs/` instead of the upstream cert references.
- **NodePlacement support:** The CR spec supports `nodePlacement` with `nodeSelector` and `tolerations` fields, which are applied to the operand deployment pod template via `applyNodePlacement()`.

### Updating Dependencies

1. Update `go.mod`: `go get <module>@<version> && go mod tidy`
2. Vendor: `go mod vendor`
3. Verify: `make verify && make test`

### Updating Upstream LWS Manifests

1. Update the `operand-git-ref` file with the desired upstream git ref
2. Run `make generate-controller-manifests`
3. Verify: `make verify`

## Testing

- **Unit tests:** Co-located `*_test.go` files in `pkg/operator/`, run with `make test` or `go test ./pkg/... ./cmd/...`.
- **E2E tests:** `test/e2e/` — uses Ginkgo/Gomega framework. Tests include:
  - Operator condition verification (no degraded, available condition)
  - Operand pod delete and recovery
  - NodePlacement propagation to operand deployment
  - ManagementState transitions (Managed/Unmanaged/Removed) with scaling
- **Operand E2E tests:** `make test-e2e-operand` — clones upstream LWS repo and runs its test suite against the deployed operand.
- Run operator E2E with: `make test-e2e` (requires `OPERATOR_IMAGE`, `RELATED_IMAGE_OPERAND_IMAGE`, `KUBECONFIG` env vars).

## Additional Resources

- [ARCHITECTURE.md](ARCHITECTURE.md) — Complete system design, components, and technical details
- [README.md](README.md) — User-facing documentation and getting started guide
- [OpenShift library-go](https://github.com/openshift/library-go) — Controller factory patterns and operator helpers
- [kubernetes-sigs/lws](https://github.com/kubernetes-sigs/lws) — Upstream LeaderWorkerSet controller
- [openshift/kubernetes-sigs-lws](https://github.com/openshift/kubernetes-sigs-lws) — OpenShift fork of LWS
- [cert-manager](https://cert-manager.io/) — TLS certificate management used by this operator
