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

## Building

### Docker Build and Push

The Dockerfile is located at `docker/Dockerfile`, which uses a multi-stage build process:

- **Builder Stage**: Uses Golang as the base image to build the application.
- **Final Stage**: Uses a Distroless image for minimal dependencies and security.

To build and push the image:

```bash
cd docker
make build-and-push

# Cloud Build

## The docker/cloudbuild.yaml file configures the Cloud Build environment for multi-arch Docker images. Use this configuration to trigger a build with Google Cloud Build. Update _GIT_TAG and _PULL_BASE_REF as needed before running the job.

```bash
gcloud builds submit --config docker/cloudbuild.yaml

# Publishing

## 1.Create a Git Tag: Tag the release branch with the new version.

```bash
git tag -a v<NEW_VERSION> -m "release: <NEW_VERSION>"
git push origin --tags

## 2. Run Goreleaser: The .goreleaser.yml file is configured to categorize changes and create a release. Run Goreleaser to publish the release.

```bash
goreleaser release

## 3. Create GitHub Release: Once the tag is created, GitHub Actions should automatically generate the release notes and changelog based on the configuration in .goreleaser.yml


