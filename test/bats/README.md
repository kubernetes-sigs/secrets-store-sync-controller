# Testing Secrets Store Sync Controller with e2e provider

This directory contains e2e test scripts for the Secrets Store Sync Controller. Before running the e2e tests, install
the Secrets Store Sync Controller with the `e2e provider` as outlined [here](../../README.md#getting-started).

## Running the tests

1. To run the tests, from the root directory run:

    ```shell
    make run-e2e-provider-tests
    ```

## Testing

This doc lists the different Secret Sync scenarios tested as part of CI.

## E2E tests

| Test Description                                                                                       | E2E |
|--------------------------------------------------------------------------------------------------------|-----|
| Check if `secretproviderclasses` CRD is established                                                    | ✔️  |
| Check if `secretsyncs` CRD is established                                                              | ✔️  |
| Test if RBAC roles and role bindings exist                                                             | ✔️  |
| Deploy `e2e-providerspc` SecretProviderClass CRD                                                       | ✔️  |
| Deploy `e2e-providerspc` SecretSync CRD                                                                | ✔️  |
| Deploy SecretProviderClass and SecretSync in different namespaces and check that no secret is created  | ✔️  |
| Deploy SecretProviderClass and SecretSync, ensure secret is created, then delete SecretSync and verify | ✔️  |
| Validating Admission Policy blocks secret creation when its type is not in the allowed list            | ✔️  |
| Validating Admission Policy blocks secret creation when its type is in the disallowed list             | ✔️  |
| Validating Admission Policy blocks secretsync creation when an annotation is invalid                   | ✔️  |
| Validating Admission Policy blocks secretsync creation when a label is invalid                         | ✔️  |
