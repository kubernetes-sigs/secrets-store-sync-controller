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
ARG BASEIMAGE=registry.k8s.io/build-image/debian-base:bookworm-v1.0.7@sha256:368abceecc1308e0913a6fd5ab89a513ee4268becefc2a82dbe616462b29a46b

FROM golang:1.26.1@sha256:e2ddb153f786ee6210bf8c40f7f35490b3ff7d38be70d1a0d358ba64225f6428 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Copy the go source
ADD . .

RUN export GOOS=$TARGETOS && \
    export GOARCH=$TARGETARCH && \
    make build

FROM $BASEIMAGE
WORKDIR /
COPY --from=builder /workspace/_output/secrets-store-sync-controller .

ENTRYPOINT ["/secrets-store-sync-controller"]
