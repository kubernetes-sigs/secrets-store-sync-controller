#!/usr/bin/env bash

# Copyright 2024 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

TASK=$1

LDFLAGS="-X sigs.k8s.io/secrets-store-sync-controller/pkg/version.BuildVersion=${IMAGE_VERSION} \
 -X sigs.k8s.io/secrets-store-sync-controller/pkg/version.Vcs=${BUILD_COMMIT} \
 -X sigs.k8s.io/secrets-store-sync-controller/pkg/version.BuildTime=${BUILD_TIMESTAMP} -extldflags '-static'"

# This function will build and push the image for all the architectures supported via PLATFORMS var.
build_and_push() {
	# Enable execution of multi-architecture containers
  docker buildx create --name img-builder --use --bootstrap
  # List builder instances
  docker buildx ls
  trap "docker buildx ls && docker buildx rm img-builder" EXIT

  echo "Building image for platforms ${PLATFORMS}..."
  docker buildx build --no-cache --pull --push \
        --platform "${PLATFORMS}" \
        -t "${IMAGE_TAG}" \
        --build-arg LDFLAGS="${LDFLAGS}" \
        -f "Dockerfile" ..
}

shift
eval "${TASK}"
