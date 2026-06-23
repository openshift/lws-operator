# LWS Operator - AI Agent Instructions

This document provides critical context for AI agents (Claude, Copilot, etc.) working on the LeaderWorkerSet (LWS) Operator codebase.

## Critical Rules - READ FIRST

### What NOT to Do

1. **NEVER modify vendored code** - The `vendor/` directory is managed by `go mod vendor` and must not be edited directly
2. **NEVER modify generated code without regenerating** - Files in `pkg/generated/` are auto-generated (clients, informers, listers)
3. **NEVER change the singleton CR name** - The operator expects a CR named `cluster` in namespace `openshift-lws-operator`
4. **NEVER bypass code generation** - Always run `make generate` after modifying APIs or CRDs
5. **NEVER commit without verification** - Run `make verify-codegen` and `make verify-controller-manifests` before commits
6. **NEVER modify cert-manager dependencies** - The operator requires cert-manager to be installed; this is a hard dependency

### What TO Do

1. **Always use the provided Makefile targets** - Don't run tools directly; use `make` targets
2. **Always update manifests after changes** - CRD, RBAC, and deployment changes require regeneration
3. **Always test both operator and operand** - Run both `make test-e2e` and `make test-e2e-operand`
4. **Always respect the OpenShift operator patterns** - This uses library-go conventions
5. **Always update the CSV** - ClusterServiceVersion must be updated for OLM compatibility

### Common Pitfalls

1. **Editing generated code** - Never manually edit `pkg/generated/` or `vendor/` directories
2. **Forgetting to regenerate** - Always run `make generate` after API changes, then verify with `make verify-codegen`
3. **Missing LWS controller repo** - `make generate` requires the upstream LWS repo cloned (see [Code Generation](#code-generation))
4. **Wrong yq version** - Must use mikefarah's yq v4.45.1, not the Python-based yq
5. **Adding to wrong assets directory** - Add to `lws-controller/` or `lws-controller-config/`, NOT `lws-controller-generated/`

## Repository Structure

```
lws-operator/
├── cmd/lws-operator/           # Main entry point
├── pkg/
│   ├── apis/                   # API definitions (v1)
│   │   └── leaderworkersetoperator/
│   │       └── v1/             # LeaderWorkerSetOperator CRD types
│   ├── cmd/operator/           # Operator command setup
│   ├── generated/              # Generated clients, informers, listers [AUTO-GENERATED]
│   ├── operator/               # Core operator logic
│   │   ├── starter.go          # Operator initialization
│   │   ├── target_config_reconciler.go  # Main reconciliation logic
│   │   └── operatorclient/     # Operator client interfaces
│   └── version/                # Version information
├── bindata/                    # Embedded assets (using go:embed)
│   ├── assets.go               # Embed wrapper (manual)
│   └── assets/                 # Manifests embedded at build time
│       ├── lws-controller/     # Controller manifests
│       └── lws-controller-generated/  # Operand manifests (updated by script)
├── deploy/                     # Quick deployment manifests
├── manifests/                  # OLM manifests (CSV, CRD)
├── test/e2e/                   # End-to-end tests
├── hack/                       # Build and generation scripts
└── vendor/                     # Vendored dependencies [DO NOT EDIT]
```

## Key Components

### Operator Architecture

The lws-operator is a **second-level operator** that manages the LeaderWorkerSet controller as an operand:

- **Operator**: lws-operator (this repo) - manages deployment and lifecycle
- **Operand**: lws-controller-manager - the actual LeaderWorkerSet controller
- **Namespace**: `openshift-lws-operator` (hardcoded)
- **CR Name**: `cluster` (singleton, validated in CRD)

### Main Reconciler

`pkg/operator/target_config_reconciler.go` is the heart of the operator:
- Manages RBAC (ClusterRoles, ClusterRoleBindings, Roles, RoleBindings)
- Manages cert-manager resources (Issuer, Certificates)
- Manages webhooks (MutatingWebhookConfiguration, ValidatingWebhookConfiguration)
- Manages the operand Deployment
- Manages Services, ConfigMaps, Secrets
- Manages CRD (LeaderWorkerSet)
- Manages ServiceMonitor for Prometheus metrics
- Updates status conditions (Available, Degraded)

**See**: ARCHITECTURE.md - Reconciliation Flow for detailed sync loop

### Key Files

- `pkg/operator/target_config_reconciler.go` - Main reconciliation logic
- `pkg/operator/starter.go` - Operator bootstrap and controller setup
- `pkg/apis/leaderworkersetoperator/v1/types.go` - API types
- `cmd/lws-operator/main.go` - Entry point
- `Makefile` - All build, test, and generation targets

## Build Commands

### Development Build

```bash
# Build the operator binary
make build

# Build operator image (requires brew.registry.redhat.io access)
make images

# Or build specific image
make image-ocp-lws-operator
```

**Note**: `make images` uses `Dockerfile` which requires authentication to `brew.registry.redhat.io`. For builds without registry access, use `Dockerfile.ci` manually.

### Code Generation

**Prerequisite**: `make generate` requires the LWS controller repository. Clone it to the default location:

```bash
mkdir -p ~/go/src/sigs.k8s.io
git clone https://github.com/openshift/kubernetes-sigs-lws.git ~/go/src/sigs.k8s.io/lws
```

Or clone to a custom location and set `LWS_CONTROLLER_DIR`:

```bash
git clone https://github.com/openshift/kubernetes-sigs-lws.git ~/custom/path/lws
export LWS_CONTROLLER_DIR=~/custom/path/lws
```

```bash
# Generate all (clients, CRD, manifests, CSV)
make generate

# Generate only clients
make generate-clients

# Generate only CRD
make regen-crd

# Generate only controller manifests
make generate-controller-manifests

# Update ClusterServiceVersion
make update-cluster-service-version
```

### Verification

```bash
# Verify generated code is up-to-date
make verify-codegen

# Verify controller manifests are up-to-date
make verify-controller-manifests

# Run linter
make lint

# Run unit tests
make test-unit
```

### E2E Testing

```bash
# Run operator E2E tests
export OPERATOR_IMAGE=quay.io/${QUAY_USER}/lws-operator:${IMAGE_TAG}
export RELATED_IMAGE_OPERAND_IMAGE=<lws-controller-image>
make test-e2e

# Run operand E2E tests
make test-e2e-operand
```

## Key Patterns

### Asset Management

Assets are embedded at build time using Go's `//go:embed` directive:
- Source: `bindata/assets/` (YAML manifests)
- Wrapper: `bindata/assets.go` (manual code using go:embed)
- Usage: `bindata.MustAsset("assets/path/to/resource.yaml")`
- The assets/ directory is embedded into the binary, no external files needed at runtime

### Resource Application

Uses `library-go` resource application pattern:
```go
required := resourceread.Read<Type>OrDie(bindata.MustAsset("path"))
required.Namespace = c.namespace
required.OwnerReferences = []metav1.OwnerReference{ownerReference}
return resourceapply.Apply<Type>(ctx, client, eventRecorder, required)
```

### Status Updates

Uses `v1helpers.UpdateStatus()` with condition functions:
```go
v1helpers.UpdateStatus(ctx, client, v1helpers.UpdateConditionFn(condition))
```

### Owner References

All managed resources have OwnerReferences pointing to the LeaderWorkerSetOperator CR:
- Enables garbage collection
- Links resources to the operator lifecycle

### Cert-Manager Integration

Critical dependency:
- Checks for cert-manager via discovery API
- Creates Issuer (self-signed)
- Creates Certificates (webhook and metrics)
- Injects CA via `cert-manager.io/inject-ca-from` annotation

## Environment Variables

- `RELATED_IMAGE_OPERAND_IMAGE` - The LeaderWorkerSet controller image to deploy
- `OS_GIT_VERSION` - Version set by OpenShift ART pipeline (falls back to `SOURCE_GIT_TAG`)

## Constants

From `pkg/operator/starter.go`:
```go
operatorNamespace = "openshift-lws-operator"
operandName       = "lws-controller-manager"
```

From `pkg/operator/target_config_reconciler.go`:
```go
MetricsCertificateSecretName  = "metrics-server-cert"
WebhookCertificateSecretName  = "webhook-server-cert"
WebhookCertificateName        = "lws-serving-cert"
CertManagerInjectCaAnnotation = "cert-manager.io/inject-ca-from"
PrometheusClientCertsPath     = "/etc/prometheus/secrets/metrics-client-certs/"
```

## API Constraints

The LeaderWorkerSetOperator CRD has a **singleton validation**:
```yaml
+kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="LeaderWorkerSetOperator is a singleton, .metadata.name must be 'cluster'"
```

Only one CR is allowed, and it must be named `cluster`.

## Common Workflows

### Adding a New Resource to Reconciliation

1. Add the manifest to `bindata/assets/lws-controller/` (for operator-managed resources) or `bindata/assets/lws-controller-config/` (for config)
2. Create a `manage<ResourceType>()` method in `target_config_reconciler.go`
3. Call the method from `sync()`
4. Add OwnerReference to enable garbage collection
5. Rebuild the operator (assets are embedded at build time via go:embed)

**Note**: `bindata/assets/lws-controller-generated/` is populated automatically from upstream - do not add files there manually.

**See**: CONTRIBUTING.md - Adding a New Managed Resource for detailed steps with testing

### Updating the Operand Image

1. Update `RELATED_IMAGE_OPERAND_IMAGE` environment variable
2. The operator reads it via `os.Getenv("RELATED_IMAGE_OPERAND_IMAGE")`
3. Image is injected into the deployment in `manageDeployments()`

### Modifying the API

1. Edit `pkg/apis/leaderworkersetoperator/v1/types.go`
2. Run `make generate` to update:
   - Generated clients
   - CRD manifests
   - ClusterServiceVersion
3. Run `make verify-codegen` to ensure consistency

**See**: CONTRIBUTING.md - Modifying the API for full workflow with testing

## Troubleshooting

### Generation Issues

If `make generate` fails with "is not a valid directory":

**Problem**: The script `hack/update-lws-controller-manifests.sh` expects the upstream LeaderWorkerSet controller repository at `~/go/src/sigs.k8s.io/lws`

**Solution**:
```bash
# Clone the LWS controller repo
mkdir -p ~/go/src/sigs.k8s.io
git clone https://github.com/openshift/kubernetes-sigs-lws.git ~/go/src/sigs.k8s.io/lws

# OR set custom location
export LWS_CONTROLLER_DIR=/path/to/your/kubernetes-sigs-lws
```

If `make generate` fails for other reasons:
1. Check Go version matches `go.mod` (go 1.25.0)
2. Ensure `vendor/` is up-to-date: `go mod vendor`
3. Check for syntax errors in API types
4. Verify controller-gen markers are valid
5. Ensure LWS controller repo has clean git state (no uncommitted changes)

### yq Version Mismatch

If `make generate` or `make verify-controller-manifests` fails with "jq: Unknown option -oaml" or similar:

**Problem**: Wrong version of `yq` is being used (likely the old Python-based yq instead of mikefarah's Go-based yq)

**Solution**:
```bash
# Install the correct yq version
go install github.com/mikefarah/yq/v4@v4.45.1

# Ensure ~/go/bin is first in PATH
export PATH=~/go/bin:$PATH

# Verify
yq --version  # Should show: yq (https://github.com/mikefarah/yq/) version v4.45.1
```

Make it permanent by adding to `~/.bashrc` or `~/.zshrc`:
```bash
export PATH=$HOME/go/bin:$PATH
```

### Test Failures

If E2E tests fail:
1. Verify cert-manager is installed
2. Check operator namespace exists: `openshift-lws-operator`
3. Verify KUBECONFIG points to valid cluster
4. Check images are accessible
5. Review operator logs: `oc logs -n openshift-lws-operator deployment/lws-operator`

### Reconciliation Issues

If resources aren't being created:
1. Check operator CR exists: `oc get leaderworkersetoperator cluster`
2. Verify ManagementState is `Managed`
3. Check cert-manager dependency: operator will degrade if cert-manager is missing
4. Review operator status conditions
5. Check event recorder messages

## Dependencies

### Required Cluster Components

- **cert-manager**: Hard dependency, checked at runtime
- **OpenShift 4.18-4.22**: Target OCP versions
- **Kubernetes 1.33+**: Minimum k8s version

### Go Dependencies

Key libraries:
- `github.com/openshift/library-go` - OpenShift operator patterns
- `k8s.io/client-go` - Kubernetes client
- `k8s.io/apiextensions-apiserver` - CRD handling
- `github.com/openshift/api` - OpenShift API types

## Useful Commands Reference

```bash
# Quick dev deployment
oc apply -f deploy/

# Check operator status
oc get leaderworkersetoperator cluster -o yaml

# View operator logs
oc logs -n openshift-lws-operator deployment/lws-operator -f

# View operand logs
oc logs -n openshift-lws-operator deployment/lws-controller-manager -f

# Check all managed resources
oc get all,cm,secret,clusterrole,clusterrolebinding,role,rolebinding,crd,mutatingwebhookconfiguration,validatingwebhookconfiguration -n openshift-lws-operator

# Clean up
oc delete -f deploy/
```

## Code Style

- Follow standard Go conventions
- Use `library-go` patterns for operator code
- Include kubebuilder markers for CRD generation
- Add appropriate RBAC markers where needed
- Use `resourceread` and `resourceapply` for manifest handling
- Prefer strongly-typed clients over dynamic clients when possible

## Additional Resources

- [LeaderWorkerSet upstream](https://github.com/openshift/kubernetes-sigs-lws)
- [OpenShift library-go](https://github.com/openshift/library-go)
- [Operator Lifecycle Manager](https://olm.operatorframework.io/)
- [cert-manager](https://cert-manager.io/)
