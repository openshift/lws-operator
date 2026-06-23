# Contributing to LWS Operator

Thank you for your interest in contributing to the LeaderWorkerSet (LWS) Operator! This guide will help you get started with development, testing, and submitting contributions.

## Table of Contents

- [Getting Started](#getting-started)
- [Development Environment](#development-environment)
- [Building and Testing](#building-and-testing)
- [Making Changes](#making-changes)
- [Submitting Contributions](#submitting-contributions)
- [Code Review Process](#code-review-process)
- [Community and Support](#community-and-support)

## Getting Started

### Prerequisites

Before you begin, ensure you have the following installed:

- **Go 1.25.0+** - Required for building the operator
- **Podman or Docker** - For building container images
- **oc (OpenShift CLI)** or **kubectl** - For deploying to a cluster
- **make** - For running build targets
- **git** - For version control
- **yq v4.45.1+** - YAML processor from mikefarah (see note below)
- **Access to an OpenShift/Kubernetes cluster** - For testing (4.18+ recommended)

### Required: LeaderWorkerSet Controller Repository

The operator's `make generate` command pulls operand manifests from the upstream LeaderWorkerSet controller repository. You **must** have it checked out locally:

**Option 1: Default GOPATH location (recommended)**
```bash
mkdir -p ~/go/src/sigs.k8s.io
git clone https://github.com/openshift/kubernetes-sigs-lws.git ~/go/src/sigs.k8s.io/lws
```

**Option 2: Custom location**
```bash
# Clone to your preferred location
git clone https://github.com/openshift/kubernetes-sigs-lws.git ~/workspace/kubernetes-sigs-lws

# Set environment variable before running make generate
export LWS_CONTROLLER_DIR=~/workspace/kubernetes-sigs-lws
make generate
```

**Why is this needed?**

The script `hack/update-lws-controller-manifests.sh` checks out a specific git ref (from `operand-git-ref` file), runs kustomize to build manifests, and copies them to `bindata/assets/lws-controller-generated/`. This ensures the operand manifests stay in sync with the upstream controller at a pinned version.

If you don't need to regenerate operand manifests, you can skip cloning the LWS repository and just avoid running `make generate` or `make generate-controller-manifests`.

### Important: yq Version

The `make generate` command requires `yq` v4.45.1 from [mikefarah/yq](https://github.com/mikefarah/yq). If you have an older or different `yq` installed (common on Ubuntu/Debian), you need to ensure the correct version is in your PATH:

```bash
# Install the correct yq
go install github.com/mikefarah/yq/v4@v4.45.1

# Ensure ~/go/bin is in your PATH before /usr/bin
export PATH=~/go/bin:$PATH

# Verify correct version
yq --version  # Should show: yq (https://github.com/mikefarah/yq/) version v4.45.1
```

Add `export PATH=~/go/bin:$PATH` to your `~/.bashrc` or `~/.zshrc` to make it permanent.

### Required Cluster Components

- **cert-manager v1.17.0+** - Hard dependency for webhook certificates
  ```bash
  VERSION=v1.17.0
  oc apply -f https://github.com/cert-manager/cert-manager/releases/download/$VERSION/cert-manager.yaml
  oc -n cert-manager wait --for condition=ready pod -l app.kubernetes.io/instance=cert-manager --timeout=2m
  ```

### Fork and Clone

1. Fork the repository on GitHub
2. Clone your fork:
   ```bash
   git clone https://github.com/YOUR_USERNAME/lws-operator.git
   cd lws-operator
   ```
3. Add the upstream repository:
   ```bash
   git remote add upstream https://github.com/openshift/lws-operator.git
   ```

## Development Environment

### Repository Structure

Familiarize yourself with the repository layout:

```
lws-operator/
├── cmd/lws-operator/       # Main entry point
├── pkg/                    # Go source code
│   ├── apis/               # API definitions
│   ├── operator/           # Core operator logic
│   └── generated/          # Generated code (do not edit manually)
├── bindata/                # Embedded assets (auto-generated)
├── deploy/                 # Quick deployment manifests
├── manifests/              # OLM manifests (CSV, CRD)
├── test/e2e/              # End-to-end tests
├── hack/                   # Build and generation scripts
├── Makefile               # Build targets
└── vendor/                # Vendored dependencies (do not edit)
```

### Setting Up Your Environment

1. **Install Go dependencies**:
   ```bash
   go mod download
   go mod vendor
   ```

2. **Verify your environment**:
   ```bash
   make verify-gofmt
   make lint
   ```

3. **Build the operator**:
   ```bash
   make build
   ```

## Building and Testing

### Local Build

```bash
# Build the binary
make build

# Run unit tests
make test-unit

# Run linter
make lint

# Verify generated code is up-to-date
make verify-codegen
make verify-controller-manifests
```

### Building Container Images

**Note**: Building images requires Red Hat registry authentication (`brew.registry.redhat.io`).

```bash
# Build operator image
make images

# Or build specific image
make image-ocp-lws-operator
```

The image is tagged as `registry.ci.openshift.org/ocp/4.20:lws-operator` by the Makefile.

### Deploying for Development

#### Quick Deployment (Fastest)

1. Update the image in `deploy/05_deployment.yaml`:
   ```yaml
   spec:
     template:
       spec:
         containers:
         - image: quay.io/${QUAY_USER}/lws-operator:${IMAGE_TAG}
   ```

2. Deploy:
   ```bash
   oc apply -f deploy/
   ```

3. Create the operator CR:
   ```bash
   oc apply -f - <<EOF
   apiVersion: operator.openshift.io/v1
   kind: LeaderWorkerSetOperator
   metadata:
     name: cluster
     namespace: openshift-lws-operator
   spec:
     managementState: Managed
     logLevel: Debug
     operatorLogLevel: Debug
   EOF
   ```

#### OLM Deployment (For Testing OLM Integration)

1. Build and push bundle image (**requires brew.registry.redhat.io access**):
   ```bash
   # Update image in manifests/lws-operator.clusterserviceversion.yaml first
   podman login brew.registry.redhat.io
   podman build -t quay.io/${QUAY_USER}/lws-operator-bundle:${IMAGE_TAG} -f bundle.Dockerfile .
   podman push quay.io/${QUAY_USER}/lws-operator-bundle:${IMAGE_TAG}
   ```
   
   **Note**: There is no CI alternative for the bundle build. OLM testing requires Red Hat registry access.

2. Build and push index image:
   ```bash
   opm index add --bundles quay.io/${QUAY_USER}/lws-operator-bundle:${IMAGE_TAG} \
     --tag quay.io/${QUAY_USER}/lws-operator-index:${IMAGE_TAG}
   podman push quay.io/${QUAY_USER}/lws-operator-index:${IMAGE_TAG}
   ```

3. Create CatalogSource and install via OperatorHub UI

### Running E2E Tests

```bash
# Set required environment variables
export OPERATOR_IMAGE=quay.io/${QUAY_USER}/lws-operator:${IMAGE_TAG}
export RELATED_IMAGE_OPERAND_IMAGE=<your-lws-controller-image>
export KUBECONFIG=/path/to/your/kubeconfig

# Run operator E2E tests
make test-e2e

# Run operand E2E tests
make test-e2e-operand
```

## Making Changes

### Before You Start

1. **Check existing issues**: Look for related issues or discussions
2. **Create an issue**: For significant changes, create an issue first to discuss the approach
3. **Create a feature branch**:
   ```bash
   git checkout -b feature/my-new-feature
   ```

### Code Guidelines

#### Go Code Style

- Follow standard Go conventions (gofmt, golint)
- Use meaningful variable and function names
- Add comments for exported functions and types
- Keep functions focused and small
- Avoid deep nesting

#### Operator Patterns

- Use `library-go` patterns for resource management
- Follow the established resource application pattern:
  ```go
  required := resourceread.Read<Type>OrDie(bindata.MustAsset("path"))
  required.Namespace = c.namespace
  required.OwnerReferences = []metav1.OwnerReference{ownerReference}
  return resourceapply.Apply<Type>(ctx, client, eventRecorder, required)
  ```
- Set OwnerReferences on all managed resources
- Use strongly-typed clients when possible
- Emit events for important actions

#### Testing

- Add unit tests for new functionality
- Update E2E tests if changing user-facing behavior
- Test on a real cluster before submitting
- Verify both upgrade and fresh installation scenarios

### Common Contribution Scenarios

#### Adding a New Managed Resource

1. Add the manifest to `bindata/assets/lws-controller/` (for operator-managed resources) or `bindata/assets/lws-controller-config/` (for config)
2. Create a `manage<ResourceType>()` method in `pkg/operator/target_config_reconciler.go`
3. Call the method from the `sync()` function
4. Rebuild the operator (assets are embedded at build time via go:embed)
5. Test the changes:
   ```bash
   make verify-codegen
   make verify-controller-manifests
   make test-unit
   ```

**Note**: `bindata/assets/lws-controller-generated/` is populated automatically from upstream - do not add files there manually.

#### Modifying the API

1. Edit `pkg/apis/leaderworkersetoperator/v1/types.go`
2. Add kubebuilder markers as needed
3. Regenerate all code:
   ```bash
   make generate
   ```
4. Verify changes:
   ```bash
   make verify-codegen
   make verify-controller-manifests
   ```
5. Update the CSV if needed:
   - Edit `manifests/lws-operator.clusterserviceversion.yaml`
   - Or regenerate: `make update-cluster-service-version`

#### Updating Dependencies

1. Update `go.mod`:
   ```bash
   go get k8s.io/client-go@v0.x.y
   go mod tidy
   go mod vendor
   ```
2. Regenerate if needed:
   ```bash
   make generate
   ```
3. Test thoroughly:
   ```bash
   make build
   make test-unit
   make test-e2e
   ```

#### Updating Operand Manifests

The operand manifests are sourced from the upstream LeaderWorkerSet repository:

**Prerequisites**: Ensure the LWS controller repository is cloned (see [Required: LeaderWorkerSet Controller Repository](#required-leaderworkerset-controller-repository))

1. Update `operand-git-ref` with the desired commit/tag:
   ```bash
   echo "v0.5.0" > operand-git-ref  # or a commit SHA
   ```

2. Run the update script (requires clean git state in LWS repo):
   ```bash
   # If using custom location:
   export LWS_CONTROLLER_DIR=~/workspace/kubernetes-sigs-lws
   
   # Run the script
   hack/update-lws-controller-manifests.sh
   ```

3. Review the changes in `bindata/assets/lws-controller-generated/`

4. Commit the updated manifests:
   ```bash
   git add bindata/assets/lws-controller-generated/ operand-git-ref deploy/02_*.yaml
   git commit -m "Update operand manifests to $(cat operand-git-ref)"
   ```

5. Test the changes with a full build and E2E tests

### Code Generation

The operator uses code generation extensively. Always run `make generate` after:

- Modifying API types (`pkg/apis/`)
- Changing kubebuilder markers
- Adding/removing resources in `bindata/assets/`
- Updating controller manifests

**Never edit generated files directly**:
- `pkg/generated/` - Generated clients, informers, listers
- `bindata/bindata.go` - Embedded assets

### Running Verification

Before committing, ensure all verifications pass:

```bash
make verify-gofmt        # Code formatting
make verify-codegen      # Generated code is up-to-date
make verify-controller-manifests  # Controller manifests are up-to-date
make lint                # Linter checks
make test-unit          # Unit tests
```

### Commit Messages

Write clear, descriptive commit messages:

```
Short summary (50 chars or less)

More detailed explanatory text, if necessary. Wrap it to about 72
characters. The blank line separating the summary from the body is
critical.

- Bullet points are okay
- Use imperative mood ("Add feature" not "Added feature")
- Reference issues and PRs: "Fixes #123"

Signed-off-by: Your Name <your.email@example.com>
```

## Submitting Contributions

### Pull Request Process

1. **Sync with upstream**:
   ```bash
   git fetch upstream
   git rebase upstream/main
   ```

2. **Push to your fork**:
   ```bash
   git push origin feature/my-new-feature
   ```

3. **Create a Pull Request**:
   - Go to https://github.com/openshift/lws-operator
   - Click "New Pull Request"
   - Select your fork and branch
   - Fill out the PR template

4. **PR Description should include**:
   - What changed and why
   - How to test the changes
   - Any breaking changes or upgrade notes
   - Related issues (use "Fixes #123" to auto-close)

### PR Checklist

Before submitting, ensure:

- [ ] Code builds successfully (`make build`)
- [ ] All tests pass (`make test-unit`)
- [ ] Code is formatted (`make verify-gofmt`)
- [ ] Linter passes (`make lint`)
- [ ] Generated code is up-to-date (`make verify-codegen`)
- [ ] Controller manifests are up-to-date (`make verify-controller-manifests`)
- [ ] E2E tests pass on a real cluster (`make test-e2e`)
- [ ] Commit messages are clear and descriptive
- [ ] Documentation is updated if needed
- [ ] Commits are signed-off (DCO)

### Developer Certificate of Origin (DCO)

This project uses the DCO. All commits must include a `Signed-off-by` line:

```bash
git commit -s -m "Your commit message"
```

This certifies that you have the right to submit the code under the project's license.

## Code Review Process

### What to Expect

1. **Automated Checks**: CI/CD will run tests and verifications
2. **Maintainer Review**: A maintainer will review your code
3. **Feedback**: You may receive comments or change requests
4. **Iteration**: Address feedback and push updates
5. **Approval**: Once approved, a maintainer will merge

### Review Timeline

- Initial response: Usually within 1-2 business days
- Full review: Depends on PR complexity
- Be patient and responsive to feedback

### Addressing Feedback

1. Make requested changes in new commits
2. Push to the same branch
3. Reply to comments when done
4. Do not force-push until after approval (preserves review context)

## Community and Support

### Getting Help

- **Issues**: Check existing issues or create a new one
- **Discussions**: Use GitHub Discussions for questions
- **Slack**: Join the Kubernetes Slack workspace
  - Channel: #sig-apps or #kubeflow (for LeaderWorkerSet questions)

### Reporting Bugs

When reporting bugs, include:

1. **Environment**: OpenShift/Kubernetes version, operator version
2. **Steps to reproduce**: Clear, minimal steps
3. **Expected behavior**: What should happen
4. **Actual behavior**: What actually happens
5. **Logs**: Operator and operand logs
   ```bash
   oc logs -n openshift-lws-operator deployment/lws-operator
   oc logs -n openshift-lws-operator deployment/lws-controller-manager
   ```
6. **CR definition**: Your LeaderWorkerSetOperator CR
7. **Status**: Output of `oc get leaderworkersetoperator cluster -o yaml`

### Suggesting Features

For feature requests:

1. Create a GitHub issue with the "enhancement" label
2. Describe the use case and problem you're solving
3. Propose a solution or approach
4. Be open to discussion and alternative approaches

## Development Tips

### Useful Commands

```bash
# Watch operator logs
oc logs -n openshift-lws-operator deployment/lws-operator -f

# Watch operand logs
oc logs -n openshift-lws-operator deployment/lws-controller-manager -f

# Check operator status
oc get leaderworkersetoperator cluster -o yaml

# List all managed resources
oc get all,cm,secret,sa,role,rolebinding -n openshift-lws-operator

# Force reconciliation (delete and recreate CR)
oc delete leaderworkersetoperator cluster
oc apply -f <your-cr.yaml>
```

### Debugging Tips

1. **Enable debug logging**: Set `logLevel: Debug` in the CR
2. **Check events**: `oc get events -n openshift-lws-operator --sort-by='.lastTimestamp'`
3. **Verify cert-manager**: Ensure cert-manager is running
4. **Check webhook connectivity**: Verify webhook service and certificates
5. **Inspect resources**: Look at generated deployments, secrets, configmaps

### Local Development Workflow

1. Make code changes
2. Build and push image: `podman build -t ... && podman push ...`
3. Update deployment: `oc set image deployment/lws-operator ...`
4. Watch logs: `oc logs -f deployment/lws-operator`
5. Verify behavior: Check operand deployment and logs
6. Iterate as needed

### Testing Changes

Always test on a real cluster:

1. **Fresh install**: Deploy from scratch
2. **Upgrade**: Update an existing installation
3. **Delete and recreate**: Test garbage collection
4. **Configuration changes**: Modify CR and verify reconciliation
5. **Failure scenarios**: 
   - Uninstall cert-manager (should degrade gracefully)
   - Delete certificates (should recreate)
   - Delete operand deployment (should recreate)

## Additional Resources

- [ARCHITECTURE.md](./ARCHITECTURE.md) - Detailed architecture documentation
- [AGENTS.md](./AGENTS.md) - AI agent development guide
- [README.md](./README.md) - Quick start guide
- [Upstream LeaderWorkerSet](https://github.com/openshift/kubernetes-sigs-lws)
- [OpenShift library-go](https://github.com/openshift/library-go)
- [Operator SDK](https://sdk.operatorframework.io/)

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.

---

**Thank you for contributing to the LWS Operator!** Your contributions help make Kubernetes workload management better for everyone.
