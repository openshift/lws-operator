AGENTS.md

## Quick Context

- **What**: lws-operator - OpenShift second-level operator managing LeaderWorkerSet controller
- **Namespace**: `openshift-lws-operator` (hardcoded)
- **CR name**: `cluster` (singleton, validated in CRD)
- **Hard dependency**: cert-manager (operator degrades without it)
- **Main reconciler**: `pkg/operator/target_config_reconciler.go`
- **Architecture**: See ARCHITECTURE.md for reconciliation flow and design decisions
- **Contributing**: See CONTRIBUTING.md for development workflows