# Secrets Store Sync Controller

This is a Kubernetes controller that watches for changes to a custom resource and syncs the secrets from external secrets-store as Kubernetes secret. This feature is useful for syncing secrets across multiple namespaces and making sure that the secrets are available when the cluster is offline.

> NOTE: This code is in experimental stage and is not recommended for production use.

## Description

This proposal is a diversion from the current design of the Secrets Store CSI driver. Based on feedback, some of the users want the CSI driver to sync the secret store objects as Kubernetes secrets without the mount instead of the tight coupling between the mount and the sync as it is [today](https://secrets-store-csi-driver.sigs.k8s.io/topics/sync-as-kubernetes-secret).

To support this, we have extracted the sync controller from the CSI driver and have provided it as a standalone deployment. Syncing Kubernetes secrets is a cluster-scope operation and doesn’t require the controller or CSI pods to be run on all nodes. The controller watches for Create/Update events for the Secret Sync (SS) and create the Kubernetes secrets by making an RPC call to the provider.

For more information, see the [design proposal](https://docs.google.com/document/d/1Ylwpg-YXNw6kC9-kdHNYD3ZKskj9TTIopwIxz5VUOW4/edit#heading=h.n3xa8h2b1inm).

## Getting Started

> NOTE: You can do a local setup by running `VERSION=e2e make local-setup` from 
> root and [check that the secret is created](#check-that-the-secret-is-created).

You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster.
The helm chart for the controller has validating admission policies that are available for k8s 1.27 and later. If you are using an older version of k8s, you may need to disable the validating admission policies by setting the `validatingAdmissionPolicies.applyPolicies` parameter to `false` in the `secret-sync-controller/secretsync/values.yaml` file, but this is not recommended. We recommend using a k8s version 1.27 or later.

Before you begin, ensure the [following](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/#before-you-begin).

If you're creating a kind cluster, here's a sample config:

```bash
kind create cluster --name sync-controller --image kindest/node:v1.29.2 --config=- <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
featureGates:
  ValidatingAdmissionPolicy: true
runtimeConfig:
  admissionregistration.k8s.io/v1beta1: true
EOF
```

### Deploy the controller

As the controller is still under development, the helm chart is not available in the public repository. You can deploy the controller from the source code by following these steps:

1. Build secrets sync controller docker image locally and load it to the KinD cluster
   ```shell
   VERSION=e2e make docker-build
   
   kind load docker-image --name sync-controller controller:e2e
   ```

1. Configure the provider container

   The helm charts for the controller contain a `providerContainer` parameter that specifies the container(s) for the Secret Sync Provider(s). You can configure the provider container by modifying the `providerContainer` parameter in the `secretsync/values.yaml` file. By default, the `providerContainer` is empty, and failing to specify the provider container will result in an error when deploying the controller. If you would like to test the controller with the e2e-provider, you can comment out the yaml block for the `providerContainer` parameter in the `secretsync/values.yaml` file.

1. From the root of the project, run the following command to deploy the controller using Helm:

    ```bash
    helm install secrets-sync-controller -f manifest_staging/charts/secrets-store-sync-controller/values.yaml manifest_staging/charts/secrets-store-sync-controller
    ```

### Deploy the SecretProviderClass

Deploy the SecretProviderClass. The [SecretProviderClass](https://secrets-store-csi-driver.sigs.k8s.io/concepts#secretproviderclass) object specifies the provider and the parameters for the provider. An example of a SecretProviderClass object for the e2e-provider is shown below:

```yaml
export NAMESPACE=<namespace>
kubectl apply -n ${NAMESPACE} -f - <<EOF
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
 name: e2e-providerspc
spec:
 provider: e2e-provider
 parameters:
  objects: |
    array:
      - |
        objectName: foo
        objectVersion: v1
EOF
```

In the example above, the `e2e-provider` is the provider name, and the `objects` parameter specifies the secret object name and version. Refer to the provider documentation for the specific parameters required for the provider.

### Deploy the SecretSync

Create a SecretSync object. The SecretSync object specifies the secret provider class, and the parameters for the secret. An example of a SecretSync object is shown below:

```yaml
export SERVICE_ACCOUNT_NAME=<service_account_name>
kubectl apply -n ${NAMESPACE} -f - <<EOF
apiVersion: secret-sync.x-k8s.io/v1alpha1
kind: SecretSync
metadata:
 name: sse2esecret  # this is the name of the secret that will be created
spec:
 serviceAccountName: ${SERVICE_ACCOUNT_NAME}
 secretProviderClassName: e2e-providerspc
 secretObject:
  type: Opaque
  data:
  - sourcePath: foo # name of the object in the SecretProviderClass
    targetKey:  bar # name of the key in the Kubernetes secret
EOF
```

### Check that the secret is created

A secret is created in the namespace specified by the SecretSync object, with the same name as the name specified in the SecretSync metadata. For the SecretSync example provided above, a secret named `sse2esecret` is created.

1. You can check that the secret is created by running the following command:
```sh
kubectl get secret sse2esecret -n ${NAMESPACE}
```

## Uninstall controller

Run the following command to uninstall the controller:

```bash
helm delete secrets-sync-controller
```

## Troubleshooting
The validating admission policies are available for k8s 1.27 and later. If you are using an older version of k8s, you may need to disable the validating admission policies by setting the `validatingAdmissionPolicies.applyPolicies` parameter to `false` in the `secret-sync-controller/secretsync/values.yaml` file.
