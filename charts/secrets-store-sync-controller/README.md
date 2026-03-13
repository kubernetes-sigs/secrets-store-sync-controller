# secrets-store-sync-controller

## Installation

Quick start instructions for the setup and configuration of secrets-store-sync-controller using Helm.

### Prerequisites

- [Helm](https://helm.sh/docs/intro/quickstart/#install-helm)

### Installing the chart

#### Add the chart repo

```bash
helm repo add secrets-store-sync-controller https://kubernetes-sigs.github.io/secrets-store-sync-controller/charts
```

#### Install chart using Helm v3.0+

```bash
helm install secrets-sync-controller secrets-store-sync-controller/secrets-store-sync-controller
```

## Configuration and Parameters
You can customize the installation by modifying values in the `values.yaml` file or by passing parameters to the helm install command using the `--set key=value[,key=value]` argument.

| Parameter Name                                   | Description                                                                                       | Default Value                                                                                                                                                                         |
|--------------------------------------------------|---------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `providerContainer`                              | The container for the Secrets Store Sync Controller.                                              | `[- name: provider-aws-installer ...]`                                                                                                                                                |
| `controllerName`                                 | The name of the Secrets Store Sync Controller.                                                    | `secrets-store-sync-controller-manager`                                                                                                                                               |
| `tokenRequestAudience`                           | The audience for the token request.                                                               | `[]`                                                                                                                                                                                  |
| `logVerbosity`                                   | The log level.                                                                                    | `5`                                                                                                                                                                                   |
| `validatingAdmissionPolicies.applyPolicies`      | Determines whether the Secrets Store Sync Controller should apply policies.                       | `true`                                                                                                                                                                                |
| `validatingAdmissionPolicies.allowedSecretTypes` | The types of secrets that the Secrets Store Sync Controller should allow.                         | `["Opaque", "kubernetes.io/basic-auth", "bootstrap.kubernetes.io/token", "kubernetes.io/dockerconfigjson", "kubernetes.io/dockercfg", "kubernetes.io/ssh-auth", "kubernetes.io/tls"]` |
| `image.repository`                               | The image repository of the Secrets Store Sync Controller.                                        | `registry.k8s.io/secrets-store-sync/controller`                                                                                                                                       |
| `image.pullPolicy`                               | Image pull policy.                                                                                | `IfNotPresent`                                                                                                                                                                        |
| `image.tag`                                      | The specific image tag to use. Overrides the image tag whose default is the chart's `appVersion`. | `v0.0.3`                                                                                                                                                                              |
| `securityContext`                                | Security context for the Secrets Store Sync Controller.                                           | `{ allowPrivilegeEscalation: false, capabilities: { drop: [ALL] } }`                                                                                                                  |
| `resources`                                      | The resource request/limits for the Secrets Store Sync Controller image.                          | `limits: 500m CPU, 128Mi; requests: 10m CPU, 64Mi`                                                                                                                                    |
| `podAnnotations`                                 | Annotations to be added to pods.                                                                  | `{ kubectl.kubernetes.io/default-container: "manager" }`                                                                                                                              |
| `podLabels`                                      | Labels to be added to pods.                                                                       | `{ control-plane: "controller-manager", secrets-store.io/system: "true", app: "secrets-store-sync-controller" }`                                                                      |
| `nodeSelector`                                   | Node labels for pod assignment.                                                                   | `{}`                                                                                                                                                                                  |
| `tolerations`                                    | Tolerations for pod assignment.                                                                   | `[{ operator: "Exists" }]`                                                                                                                                                            |


These parameters offer flexibility in configuring and deploying the Secrets Store Sync Controller according to specific requirements in your Kubernetes environment. Remember to replace values appropriately or use the `--set` flag when installing the chart via Helm.
