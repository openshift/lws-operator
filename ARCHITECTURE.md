# LWS Operator Architecture

This document describes the architecture, design decisions, and reconciliation flow of the LeaderWorkerSet (LWS) Operator.

## Overview

The lws-operator is a **second-level operator** for OpenShift that manages the lifecycle of the LeaderWorkerSet controller. It follows the OpenShift operator pattern using the [library-go](https://github.com/openshift/library-go) framework.

### Operator vs Operand

- **Operator (lws-operator)**: This repository - manages installation, configuration, and lifecycle
- **Operand (lws-controller-manager)**: The LeaderWorkerSet controller that manages LeaderWorkerSet custom resources
- **End-user CR**: LeaderWorkerSet resources created by cluster users to deploy leader-worker workloads

```
User creates LeaderWorkerSet CR
         ↓
lws-controller-manager (operand) reconciles it
         ↓
Creates Pods with leader-worker topology
         ↑
lws-operator (this repo) manages the controller deployment
```

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│ OpenShift Cluster                                       │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │ openshift-lws-operator namespace                 │  │
│  │                                                  │  │
│  │  ┌────────────────────────────────────────────┐ │  │
│  │  │ LeaderWorkerSetOperator CR (singleton)     │ │  │
│  │  │ metadata.name: cluster                     │ │  │
│  │  │ spec:                                      │ │  │
│  │  │   managementState: Managed                 │ │  │
│  │  │   logLevel: Normal                         │ │  │
│  │  └────────────────────────────────────────────┘ │  │
│  │                    ↓ watches                   │  │
│  │  ┌────────────────────────────────────────────┐ │  │
│  │  │ lws-operator (Deployment)                  │ │  │
│  │  │ - TargetConfigReconciler                   │ │  │
│  │  │ - LogLevelController                       │ │  │
│  │  └────────────────────────────────────────────┘ │  │
│  │                    ↓ manages                   │  │
│  │  ┌────────────────────────────────────────────┐ │  │
│  │  │ lws-controller-manager (Deployment)        │ │  │
│  │  │ - Reconciles LeaderWorkerSet CRs           │ │  │
│  │  │ - Webhook server                           │ │  │
│  │  │ - Metrics server                           │ │  │
│  │  └────────────────────────────────────────────┘ │  │
│  │                                                  │  │
│  │  Supporting Resources (all owned by CR):        │  │
│  │  - RBAC (ClusterRoles, Bindings, Roles)         │  │
│  │  - Services (webhook, metrics)                  │  │
│  │  - Certificates (cert-manager)                  │  │
│  │  - ConfigMaps                                   │  │
│  │  - Secrets (TLS certs)                          │  │
│  │  - Webhooks (Mutating, Validating)              │  │
│  │  - ServiceMonitor (Prometheus)                  │  │
│  └──────────────────────────────────────────────────┘  │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │ Cluster-scoped Resources                         │  │
│  │ - CRD: leaderworkersets.leaderworkerset.x-k8s.io │  │
│  │ - ClusterRoles & ClusterRoleBindings             │  │
│  └──────────────────────────────────────────────────┘  │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │ cert-manager (Required Dependency)               │  │
│  │ - Issuer: lws-selfsigned-issuer                  │  │
│  │ - Certificate: lws-serving-cert (webhook)        │  │
│  │ - Certificate: lws-metrics-cert (metrics)        │  │
│  └──────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

## Core Components

### 1. TargetConfigReconciler

**File**: `pkg/operator/target_config_reconciler.go`

The main reconciliation controller that ensures the desired state of all managed resources.

#### Responsibilities

1. **RBAC Management**: Creates and maintains all necessary roles and bindings
2. **Certificate Management**: Manages cert-manager resources for webhook and metrics TLS
3. **Deployment Management**: Manages the operand (lws-controller-manager) deployment
4. **CRD Management**: Ensures LeaderWorkerSet CRD is installed and configured
5. **Webhook Management**: Manages mutating and validating webhook configurations
6. **Service Management**: Manages services for webhook and metrics endpoints
7. **Monitoring Setup**: Creates ServiceMonitor for Prometheus integration
8. **Status Management**: Updates operator status conditions (Available, Degraded)

#### Key Fields

```go
type TargetConfigReconciler struct {
    targetImage                   string              // Operand image from env
    operatorClient                interface{}         // For CR updates
    dynamicClient                 dynamic.Interface   // For unstructured resources
    discoveryClient               discovery.Interface // For API discovery
    leaderWorkerSetOperatorClient *operatorclient     // Status updates
    kubeClient                    kubernetes.Interface
    apiextensionClient            *apiextclientv1     // For CRD management
    eventRecorder                 events.Recorder
    kubeInformersForNamespaces    v1helpers.KubeInformersForNamespaces
    secretLister                  v1.SecretLister
    deploymentsLister             appsv1lister.DeploymentLister
    namespace                     string              // "openshift-lws-operator"
    resourceCache                 resourceapply.ResourceCache
}
```

### 2. Operator Client

**File**: `pkg/operator/operatorclient/interfaces.go`

Provides a typed interface for interacting with the LeaderWorkerSetOperator CR.

### 3. Starter

**File**: `pkg/operator/starter.go`

Bootstrap logic that:
- Initializes all Kubernetes clients
- Sets up informers with 5-minute resync
- Creates and starts controllers
- Handles namespace resolution (defaults to `openshift-lws-operator`)

## Reconciliation Flow

### Main Sync Loop

The `sync()` method in TargetConfigReconciler executes on:
- LeaderWorkerSetOperator CR changes
- Deployment changes in the operator namespace
- ConfigMap or Secret changes in the operator namespace
- Every 5 minutes (resync period)

```
sync() called
    ↓
1. Check managementState (skip if not "Managed")
    ↓
2. Check operand deployment availability
    ↓ Update Available condition
    ↓
3. Verify cert-manager is installed (discovery API)
    ↓ If missing, set Degraded=true and return error
    ↓
4. Get LeaderWorkerSetOperator CR
    ↓
5. Create OwnerReference
    ↓
6. Reconcile all RBAC resources (cluster-scoped)
   - ClusterRole: lws-manager-role
   - ClusterRole: lws-metrics-reader
   - ClusterRole: lws-proxy-role
   - ClusterRoleBinding: lws-manager-rolebinding
   - ClusterRoleBinding: lws-metrics-reader-rolebinding
   - ClusterRoleBinding: lws-proxy-rolebinding
    ↓
7. Reconcile namespace-scoped RBAC
   - Role: lws-leader-election-role
   - Role: lws-prometheus-k8s
   - RoleBinding: lws-leader-election-rolebinding
   - RoleBinding: lws-prometheus-k8s
    ↓
8. Reconcile Services
   - lws-webhook-service
   - lws-controller-manager-metrics-service
    ↓
9. Reconcile cert-manager resources
   - Issuer: lws-selfsigned-issuer (self-signed)
   - Certificate: lws-serving-cert (webhook TLS)
   - Certificate: lws-metrics-cert (metrics TLS)
    ↓
10. Wait for webhook certificate secret
    ↓ Add to specAnnotations
    ↓
11. Wait for metrics certificate secret
    ↓ Add to specAnnotations
    ↓
12. Reconcile ConfigMap
    ↓ Add to specAnnotations
    ↓
13. Reconcile CRD
    - leaderworkersets.leaderworkerset.x-k8s.io
    - Inject cert-manager CA annotation
    - Preserve existing CA bundle
    ↓
14. Reconcile ServiceAccount
    - lws-controller-manager
    ↓
15. Reconcile Webhooks
    - MutatingWebhookConfiguration
    - ValidatingWebhookConfiguration
    - Inject cert-manager CA annotation
    ↓
16. Reconcile ServiceMonitor
    - Configure TLS with client certs from Prometheus
    ↓
17. Reconcile Deployment
    - Inject target image (RELATED_IMAGE_OPERAND_IMAGE)
    - Inject log level based on CR spec
    - Add specAnnotations to trigger rollout on cert/config changes
    ↓
18. Update Status
    - Set deployment generation
    - Set ready replicas
    - Update Available condition
    - Set Degraded=false
```

### Resource Application Pattern

All resources follow this pattern:

```go
func (c *TargetConfigReconciler) manageResource(ctx context.Context, ownerReference metav1.OwnerReference) (*ResourceType, bool, error) {
    // 1. Read required state from embedded asset
    required := resourceread.Read<Type>OrDie(bindata.MustAsset("assets/path/to/resource.yaml"))
    
    // 2. Set namespace (for namespaced resources)
    required.Namespace = c.namespace
    
    // 3. Set owner reference for garbage collection
    required.OwnerReferences = []metav1.OwnerReference{ownerReference}
    
    // 4. Apply any runtime modifications (e.g., image substitution, namespace injection)
    // ...
    
    // 5. Apply to cluster (creates or updates)
    return resourceapply.Apply<Type>(ctx, client, c.eventRecorder, required)
}
```

### Status Conditions

The operator maintains these conditions on the LeaderWorkerSetOperator CR:

1. **Available**: `true` when operand deployment has desired replicas available
2. **Degraded**: `true` when cert-manager is missing or other critical errors occur
3. **Progressing**: Managed by library-go based on deployment rollout status

## Design Decisions

### 1. Singleton Pattern

**Decision**: The LeaderWorkerSetOperator CR must be named `cluster` (enforced via CRD validation).

**Rationale**:
- Follows OpenShift operator conventions for cluster-scoped operators
- Simplifies discovery and status checking
- Prevents conflicts from multiple operator instances
- Aligns with other OpenShift operators (authentication, ingress, etc.)

**Implementation**: Kubebuilder validation rule in CRD:
```go
+kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="LeaderWorkerSetOperator is a singleton, .metadata.name must be 'cluster'"
```

### 2. cert-manager Dependency

**Decision**: cert-manager is a hard, required dependency.

**Rationale**:
- Webhooks require TLS certificates
- cert-manager provides automatic rotation
- Self-signed issuer is sufficient (no external CA needed)
- Industry standard for Kubernetes certificate management

**Implementation**:
- Runtime check via discovery API
- Operator sets `Degraded=true` if cert-manager is missing
- Clear error message guides users to install cert-manager

### 3. OwnerReference-Based Garbage Collection

**Decision**: All managed resources have OwnerReferences to the LeaderWorkerSetOperator CR.

**Rationale**:
- Automatic cleanup when CR is deleted
- Clear ownership hierarchy
- Standard Kubernetes pattern
- Prevents orphaned resources

**Caveat**: Cluster-scoped resources (CRD, ClusterRoles) are also owned by the CR, which is namespace-scoped. This works because OwnerReferences don't block deletion, they just trigger cascading deletion.

### 4. Embedded Assets via go:embed

**Decision**: All Kubernetes manifests are embedded in the binary using Go's `//go:embed` directive.

**Rationale**:
- Single binary deployment (no external files needed)
- Version-locked manifests
- Simplifies deployment and distribution
- Native Go feature (no external tools needed)

**Implementation**: The `bindata/assets.go` file uses `//go:embed assets/*` to embed all YAML files from `bindata/assets/` at compile time.

### 5. Dynamic Client for cert-manager Resources

**Decision**: Use `dynamic.Interface` for cert-manager CRs (Issuer, Certificate).

**Rationale**:
- Avoids compile-time dependency on cert-manager API types
- Operator can build without cert-manager installed
- More flexible for cross-version compatibility

**Implementation**: Resources are read as `unstructured.Unstructured` and applied via `resourceapply.ApplyUnstructuredResourceImproved()`.

### 6. Annotation-Driven Deployment Rollout

**Decision**: ConfigMap and Secret versions are added to Deployment pod annotations.

**Rationale**:
- Forces rollout when certificates or configuration change
- Kubernetes doesn't automatically detect mounted volume changes
- Ensures operand picks up new certs/config promptly

**Implementation**:
```go
specAnnotations := make(map[string]string)
specAnnotations["secrets/"+webhookSecret.Name] = webhookSecret.ResourceVersion
specAnnotations["secrets/"+metricsSecret.Name] = metricsSecret.ResourceVersion
specAnnotations["configmaps/"+configMap.Name] = configMap.ResourceVersion
resourcemerge.MergeMap(ptr.To(false), &required.Spec.Template.Annotations, specAnnotations)
```

### 7. Namespace Hardcoding

**Decision**: Operator namespace is hardcoded to `openshift-lws-operator`.

**Rationale**:
- Aligns with OpenShift conventions (openshift-* namespace prefix)
- Simplifies documentation and support
- Consistent with other OpenShift operators
- Prevents namespace conflicts

**Fallback**: If run outside the cluster, falls back from `openshift-config-managed` to the hardcoded namespace.

### 8. Log Level Configuration

**Decision**: Operator translates CR log levels to zap log levels.

**Rationale**:
- User-friendly log level names in API (Normal, Debug, Trace, TraceAll)
- Operand uses zap logger with numeric levels
- Operator bridges the gap

**Mapping**:
```
Normal   → --zap-log-level=2
Debug    → --zap-log-level=4
Trace    → --zap-log-level=6
TraceAll → --zap-log-level=9
```

### 9. Prometheus Integration

**Decision**: ServiceMonitor uses client certificates from Prometheus secret mount.

**Rationale**:
- OpenShift Prometheus requires mTLS for metrics scraping
- Certificates are mounted at `/etc/prometheus/secrets/metrics-client-certs/`
- ServiceMonitor configures TLS with these paths
- Enables secure metrics collection

**Implementation**: Metrics endpoint uses the metrics-server-cert for server TLS, and Prometheus uses its client cert for authentication.

## Controller Informers

The TargetConfigReconciler watches these resources:

1. **LeaderWorkerSetOperator** (in all namespaces, but only one should exist)
2. **Deployments** (in `openshift-lws-operator` namespace)
3. **ConfigMaps** (in `openshift-lws-operator` namespace)
4. **Secrets** (in `openshift-lws-operator` namespace)

Resync: Every 5 minutes

## Error Handling

### Degraded Conditions

The operator sets `Degraded=true` for:
- cert-manager not installed
- Certificate secrets not ready (tls.crt or tls.key missing)

### Retries

- library-go's `WithSyncDegradedOnError()` automatically:
  - Sets `Degraded=true` on sync errors
  - Retries with exponential backoff
  - Clears `Degraded=false` on successful sync

### ManagementState

When `managementState != Managed`:
- Reconciliation loop exits early
- Resources are left as-is (not created or deleted)
- Status is not updated

## Security Considerations

### TLS Everywhere

- Webhook server uses TLS (lws-serving-cert)
- Metrics server uses TLS (lws-metrics-cert)
- Both use cert-manager for issuance and rotation

### RBAC Principle of Least Privilege

- Manager role: Full access to LeaderWorkerSet CRs and dependent resources
- Metrics reader: Read-only access to metrics endpoint
- Proxy role: Token review for authentication
- Leader election role: ConfigMap access for leader election
- Prometheus role: Read secrets for mTLS

### Certificate Rotation

- cert-manager handles automatic rotation
- Deployment rollout triggered by certificate ResourceVersion change
- No manual intervention needed

## Monitoring and Observability

### Metrics

- Endpoint: `lws-controller-manager-metrics-service:8443/metrics`
- TLS: Enabled with mTLS
- Scraped by: OpenShift Prometheus via ServiceMonitor

### Events

- All resource applications generate events
- Events recorded with reason and message
- Viewable via: `oc get events -n openshift-lws-operator`

### Logs

- Operator logs: `oc logs -n openshift-lws-operator deployment/lws-operator`
- Operand logs: `oc logs -n openshift-lws-operator deployment/lws-controller-manager`
- Log level controlled via CR `spec.logLevel` and `spec.operatorLogLevel`

## Upgrade and Lifecycle

### Operator Upgrades

OLM (Operator Lifecycle Manager) handles:
- Deployment updates
- CSV transitions
- CRD upgrades

The operator's job:
- Reconcile operand to match new desired state
- Handle CRD schema migrations (preserve existing data)
- Maintain backward compatibility

### Operand Upgrades

Triggered by:
- `RELATED_IMAGE_OPERAND_IMAGE` environment variable change
- OLM updates the operator deployment with new env var
- Operator reconciles and updates operand deployment image

### Graceful Degradation

If cert-manager is removed:
- Operator sets `Degraded=true`
- Existing operand continues to run (with existing certs)
- New deployments or rollouts will fail
- Clear error message directs user to install cert-manager

## Future Considerations

### Potential Enhancements

1. **Multi-tenancy**: Support for multiple LWS controller instances in different namespaces
2. **Custom CA**: Support for user-provided CA instead of self-signed
3. **High Availability**: Leader election for operator itself (currently single replica)
4. **Observability**: Custom metrics from operator (not just operand)
5. **Configuration Validation**: Webhook for LeaderWorkerSetOperator CR validation

### Architectural Constraints

1. **cert-manager dependency**: Cannot be removed without major redesign
2. **Namespace**: Changing namespace would require migration tooling
3. **Singleton CR**: Removing this would require significant API changes
4. **OpenShift-specific**: Uses OpenShift API types and conventions

## Related Documentation

- [AGENTS.md](./AGENTS.md) - AI agent instructions and development guide
- [CONTRIBUTING.md](./CONTRIBUTING.md) - Contribution guidelines
- [README.md](./README.md) - Quick start and installation
