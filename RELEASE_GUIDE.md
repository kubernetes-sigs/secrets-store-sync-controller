# Release Process

## Overview

The release process consists of three main phases: **versioning**, **building**, and **publishing**.

1. **Versioning**: Update specific files with the new version to ensure consistency across the build and deployment configurations.
2. **Building**: Generate the required artifacts using Docker and Cloud Build for multi-architecture support.
3. **Publishing**: Tag the repository and create a release in GitHub.

## Prerequisites

- Configure `git remote` so that `origin` is your fork and `upstream` is the main repository.
- Install the `gh` tool (GitHub CLI) from [GitHub CLI installation page](https://github.com/cli/cli#installation).
- Ensure GNU `sed` is installed on macOS using `brew install gnu-sed`.

## Versioning

The following files need to be updated with the new version:

1. **Makefile**: Update the `IMAGE_VERSION` variable with the new version.
2. **Helm Chart** (`charts/secrets-store-sync-controller`): Update the version in the chart's configuration to reflect the new release.
3. **Dockerfile** (`docker/Dockerfile`): Ensure the base images and build configurations are compatible with the release requirements.
4. **Cloud Build Configuration** (`docker/cloudbuild.yaml`): Update the `_GIT_TAG` and `_PULL_BASE_REF` substitutions if necessary.
5. **Goreleaser Config** (`.goreleaser.yml`): Check `prerelease` and other release settings to ensure they align with the new release.

## After updating these files, commit and push your changes to create a pull request.

```bash
git checkout -b release-<NEW_VERSION>
git commit -m "release: bump version to <NEW_VERSION>"
git push origin release-<NEW_VERSION>

