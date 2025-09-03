# LeaderWorkerSet Operator

The LeaderWorkerSet Operator provides the ability to deploy a
[LWS](https://github.com/openshift/kubernetes-sigs-lws) in OpenShift.

## Deploy the Operator

### Prerequisites

cert-manager is installed:

```sh
VERSION=v1.17.0
oc apply -f https://github.com/cert-manager/cert-manager/releases/download/$VERSION/cert-manager.yaml
oc -n cert-manager wait --for condition=ready pod -l app.kubernetes.io/instance=cert-manager --timeout=2m

```

### Quick Development

1. Build and push the operator image to a registry:
   ```sh
   export QUAY_USER=${your_quay_user_id}
   export IMAGE_TAG=${your_image_tag}
   podman build -t quay.io/${QUAY_USER}/lws-operator:${IMAGE_TAG} .
   podman login quay.io -u ${QUAY_USER}
   podman push quay.io/${QUAY_USER}/lws-operator:${IMAGE_TAG}
   ```

2. Update the image spec under `.spec.template.spec.containers[0].image` field in the `deploy/05_deployment.yaml` Deployment to point to the newly built image

3. Apply the manifests from `deploy` directory:
   ```sh
   oc apply -f deploy/
   ```

### OperatorHub install with custom index image

This process refers to building the operator in a way that it can be installed locally via the OperatorHub with a custom index image

1. Build and push the operator image to a registry:
   ```sh
   export QUAY_USER=${your_quay_user_id}
   export IMAGE_TAG=${your_image_tag}
   podman build -t quay.io/${QUAY_USER}/lws-operator:${IMAGE_TAG} .
   podman login quay.io -u ${QUAY_USER}
   podman push quay.io/${QUAY_USER}/lws-operator:${IMAGE_TAG}
   ```

2. Update the `.spec.install.spec.deployments[0].spec.template.spec.containers[0].image` field in the LWS CSV under `manifests/lws-operator.clusterserviceversion.yaml` to point to the newly built image.

3. Build and push the metadata image to a registry (e.g. https://quay.io):
   ```sh
   podman build -t quay.io/${QUAY_USER}/lws-operator-bundle:${IMAGE_TAG} -f bundle.Dockerfile .
   podman push quay.io/${QUAY_USER}/lws-operator-bundle:${IMAGE_TAG}
   ```

4. Build and push image index for operator-registry (pull and build https://github.com/operator-framework/operator-registry/ to get the `opm` binary)
   ```sh
   opm index add --bundles quay.io/${QUAY_USER}/lws-operator-bundle:${IMAGE_TAG} --tag quay.io/${QUAY_USER}/lws-operator-index:${IMAGE_TAG}
   podman push quay.io/${QUAY_USER}/lws-operator-index:${IMAGE_TAG}
   ```

   Don't forget to increase the number of open files, .e.g. `ulimit -n 100000` in case the current limit is insufficient.

5. Create and apply catalogsource manifest (notice to change <<QUAY_USER>> and <<IMAGE_TAG>> to your own values)::
   ```yaml
   apiVersion: operators.coreos.com/v1alpha1
   kind: CatalogSource
   metadata:
     name: lws-operator
     namespace: openshift-marketplace
   spec:
     sourceType: grpc
     image: quay.io/<<QUAY_USER>>/lws-operator-index:<<IMAGE_TAG>>
   ```

6. Create `openshift-lws-operator` namespace:
   ```
   $ oc create ns openshift-lws-operator
   ```

7. Open the console Operators -> OperatorHub, search for  `Leader Worker Set` and install the operator

8. Create CR for the LeaderWorkerSet Operator in the console:
```yaml
apiVersion: operator.openshift.io/v1
kind: LeaderWorkerSetOperator
metadata:
  name: cluster
  namespace: openshift-lws-operator
spec:
  managementState: Managed
  logLevel: Normal
  operatorLogLevel: Normal
```

## Sample CR

A sample CR definition looks like below (the operator expects `cluster` CR under `openshift-lws-operator` namespace):

```yaml
apiVersion: operator.openshift.io/v1
kind: LeaderWorkerSetOperator
metadata:
  name: cluster
  namespace: openshift-lws-operator
spec:
  managementState: Managed
  logLevel: Normal
  operatorLogLevel: Normal
```

## E2E Test
Set kubeconfig to point to a OCP cluster

Set OPERATOR_IMAGE to point to your operator image

Set RELATED_IMAGE_OPERAND_IMAGE to point to your lws image you want to test

[Optional] Set ARTIFACT_DIR to /path/to/dir for junit_report.xml

Run operator e2e test
```sh
make test-e2e
```
Run operand e2e test
```sh
make test-e2e-operand
```