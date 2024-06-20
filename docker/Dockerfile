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

# Build the manager binary
FROM golang:1.22@sha256:a66eda637829ce891e9cf61ff1ee0edf544e1f6c5b0e666c7310dce231a66f28 as builder
ARG TARGETOS
ARG TARGETARCH
ARG LDFLAGS

WORKDIR /workspace
# Copy the Go Modules manifests
COPY ../go.mod go.mod
COPY ../go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY ../cmd/main.go cmd/main.go
COPY ../api api/
COPY ../internal/controller internal/controller/
COPY ../pkg pkg/

# Build
# The GOARCH does not have a default value to allow the binary to be built according to the host where the command was
# called. For example, if we call make docker-build in a local environment with the Apple Silicon, the Docker
# BUILDPLATFORM argument will be linux/arm64. For Apple x86, it will be linux/amd64. Therefore, by leaving it empty,
# we can ensure that the container and binary shipped with it will have the same platform.
RUN CGO_ENABLED=0 \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH} \
    go build -a -ldflags "${LDFLAGS}" -o secrets-store-sync-controller cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:latest
WORKDIR /
COPY --from=builder /workspace/secrets-store-sync-controller .

ENTRYPOINT ["/secrets-store-sync-controller"]
