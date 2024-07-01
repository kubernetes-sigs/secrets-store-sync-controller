# e2e-provider
This folder contains the scripts for the end to end provider.

To run the tests, you must first deploy the end to end provider as outlined in the [top level README](../../README.md).


# Running the tests
1. To run the tests, from the root directory run:
```shell
make run-e2e-provider-tests
```

# Testing

This doc lists the different Secret Sync scenarios tested as part of CI.

## E2E tests

| Test Description                                                                                       | E2E  |
| ------------------------------------------------------------------------------------------------------- | ---- |
| Check if `secretproviderclasses` CRD is established                                                     | ✔️   |
| Check if `secretsyncs` CRD is established                                                               | ✔️   |
| Test if RBAC roles and role bindings exist                                                              | ✔️   |
| Deploy `e2e-providerspc` SecretProviderClass CRD                                                        | ✔️   |
| Deploy `e2e-providerspc` SecretSync CRD                                                                 | ✔️   |
| Deploy SecretProviderClass and SecretSync in different namespaces and check that no secret is created   | ✔️   |
| Deploy SecretProviderClass and SecretSync, ensure secret is created, then delete SecretSync and verify  | ✔️   |
