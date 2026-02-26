# Update this value when you upgrade the version of your project.
VERSION ?= v0.0.3

# Set the Operator SDK version to use. By default, what is installed on the system is used.
# This is useful for CI or a project to utilize a specific version of the operator-sdk toolkit.
OPERATOR_SDK_VERSION ?= v1.30.0

# go env vars
GOARCH  := $(shell go env GOARCH)
GOOS    := $(shell go env GOOS)
GOPROXY := $(shell go env GOPROXY)

GO_FILES=$(shell go list ./...)
TOOLS_MOD_DIR := ./hack/tools
TOOLS_DIR := $(abspath ./hack/tools)
TOOLS_BIN_DIR := $(TOOLS_DIR)/bin

# project configuration
ORG_PATH=sigs.k8s.io
PROJECT_NAME := secrets-store-sync-controller
BUILD_COMMIT := $(shell git rev-parse --short HEAD)
REPO_PATH=$(ORG_PATH)/$(PROJECT_NAME)

# build variables
BUILD_TIMESTAMP := $$(date +%Y-%m-%d-%H:%M)
BUILD_TIME_VAR := $(REPO_PATH)/pkg/version.BuildTime
BUILD_VERSION_VAR := $(REPO_PATH)/pkg/version.BuildVersion
VCS_VAR := $(REPO_PATH)/pkg/version.Vcs
LDFLAGS ?= "-X $(BUILD_TIME_VAR)=$(BUILD_TIMESTAMP) -X $(BUILD_VERSION_VAR)=$(VERSION) -X $(VCS_VAR)=$(BUILD_COMMIT)"

## Tool Versions
KUSTOMIZE_VERSION ?= v4.5.7
CONTROLLER_TOOLS_VERSION ?= v0.20.0
K8S_CODEGEN_VERSION ?= v0.34.3
KIND_NODE_IMAGE_VERSION ?= v1.32.2
BATS_VERSION ?= 1.11.0
SHELLCHECK_VER ?= v0.10.0
KIND_VERSION ?= v0.27.0
TRIVY_VERSION ?=  0.52.2

## Tool Binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT := $(TOOLS_BIN_DIR)/golangci-lint
HELM := helm
KIND := kind
ENVSUBST := envsubst
BATS := bats
TRIVY := trivy

# Image URL to use all building/pushing image targets
REGISTRY ?= docker.io
IMAGE_NAME ?= controller
IMAGE_TAG ?= $(REGISTRY)/$(IMAGE_NAME):$(VERSION)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./api/..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: k8s-codegen ## Generate deep copies and clients. Must be run from `realpath .`, not a symlinked dir.
	./hack/update-codegen.sh

##@ Build

.PHONY: build
build:
	GOPROXY=$(GOPROXY) CGO_ENABLED=0 GOOS=$(GOOS) go build -a -ldflags $(LDFLAGS) -o _output/secrets-store-sync-controller ./cmd/main.go

# If you wish built the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64 ). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	docker build -t ${IMAGE_TAG} -f ./docker/Dockerfile .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMAGE_TAG}

##@ Build Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

KUSTOMIZE_INSTALL_SCRIPT ?= "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary. If wrong version is installed, it will be removed before downloading.
$(KUSTOMIZE): $(LOCALBIN)
	@if test -x $(LOCALBIN)/kustomize && ! $(LOCALBIN)/kustomize version | grep -q $(KUSTOMIZE_VERSION); then \
		echo "$(LOCALBIN)/kustomize version is not expected $(KUSTOMIZE_VERSION). Removing it before installing."; \
		rm -rf $(LOCALBIN)/kustomize; \
	fi
	test -s $(LOCALBIN)/kustomize || { curl -Ss $(KUSTOMIZE_INSTALL_SCRIPT) | bash -s -- $(subst v,,$(KUSTOMIZE_VERSION)) $(LOCALBIN); }

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary. If wrong version is installed, it will be overwritten.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen && $(LOCALBIN)/controller-gen --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: k8s-codegen
k8s-codegen:
	cd $(TOOLS_MOD_DIR) && \
		go mod download "k8s.io/code-generator@$(K8S_CODEGEN_VERSION)"

.PHONY: envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary.
$(ENVTEST): $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

## --------------------------------------
## Local Setup
## --------------------------------------

.PHONY: local-setup
local-setup: docker-build setup-kind-cluster helm-manifest-install ## setup and run sync controller locally
	kubectl apply -f ./hack/localsetup/e2e-providerspc.yaml
	kubectl apply -f ./hack/localsetup/e2e-secret-sync.yaml

.PHONY: setup-kind-cluster
setup-kind-cluster:
	kind delete cluster --name sync-controller
	kind create cluster --name sync-controller \
		--image kindest/node:$(KIND_NODE_IMAGE_VERSION)
	kind load docker-image --name sync-controller $(IMAGE_TAG)

.PHONY: helm-manifest-install ## Install Helm manifests
helm-manifest-install:
	cp manifest_staging/charts/secrets-store-sync-controller/values.yaml manifest_staging/charts/secrets-store-sync-controller/temp_values.yaml
	@if [[ "$$(uname)" == "Darwin" ]]; then \
		sed -i '' '/providerContainer:/,/providervol:/s/^#//g' manifest_staging/charts/secrets-store-sync-controller/temp_values.yaml; \
	else \
		sed -i '/providerContainer:/,/providervol:/s/^#//g' manifest_staging/charts/secrets-store-sync-controller/temp_values.yaml; \
	fi
	helm install secrets-store-sync-controller --wait --timeout=5m \
		--namespace secrets-store-sync-controller-system --create-namespace \
		-f manifest_staging/charts/secrets-store-sync-controller/temp_values.yaml \
		--set image.repository=$(REGISTRY)/$(IMAGE_NAME) \
		--set image.tag=$(VERSION) \
		manifest_staging/charts/secrets-store-sync-controller
	rm -f manifest_staging/charts/secrets-store-sync-controller/temp_values.yaml


## --------------------------------------
## Testing Binaries
## --------------------------------------

$(HELM): ## Install helm3 if not present
	helm version --short | grep -q v3 || (curl https://raw.githubusercontent.com/helm/helm/master/scripts/get-helm-3 | bash)

$(BATS): ## Install bats for running the tests
	bats --version | grep -q $(BATS_VERSION) || (curl -sSLO https://github.com/bats-core/bats-core/archive/v${BATS_VERSION}.tar.gz && tar -zxvf v${BATS_VERSION}.tar.gz && bash bats-core-${BATS_VERSION}/install.sh /usr/local)

$(ENVSUBST): ## Install envsubst for running the tests
	envsubst -V || (apt-get -o Acquire::Retries=30 update && apt-get -o Acquire::Retries=30 install gettext-base -y)

SHELLCHECK := $(TOOLS_BIN_DIR)/shellcheck-$(SHELLCHECK_VER)
$(SHELLCHECK): OS := $(shell uname | tr '[:upper:]' '[:lower:]')
$(SHELLCHECK): ARCH := $(shell uname -m)
$(SHELLCHECK):
	mkdir -p $(TOOLS_BIN_DIR)
	rm -rf "$(SHELLCHECK)*"
	curl -sfOL "https://github.com/koalaman/shellcheck/releases/download/$(SHELLCHECK_VER)/shellcheck-$(SHELLCHECK_VER).$(OS).$(ARCH).tar.xz"
	tar xf shellcheck-$(SHELLCHECK_VER).$(OS).$(ARCH).tar.xz
	cp "shellcheck-$(SHELLCHECK_VER)/shellcheck" "$(SHELLCHECK)"
	ln -sf "$(SHELLCHECK)" "$(TOOLS_BIN_DIR)/shellcheck"
	chmod +x "$(TOOLS_BIN_DIR)/shellcheck" "$(SHELLCHECK)"
	rm -rf shellcheck*

$(KIND): ## Download and install kind
	kind --version | grep -q $(KIND_VERSION) || (curl -L https://github.com/kubernetes-sigs/kind/releases/download/$(KIND_VERSION)/kind-linux-amd64 --output kind && chmod +x kind && mv kind /usr/local/bin/)

$(TRIVY): ## Install trivy for image vulnerability scan
	trivy -v | grep -q $(TRIVY_VERSION) || (curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh | sh -s -- -b /usr/local/bin v$(TRIVY_VERSION))

## --------------------------------------
## Testing
## --------------------------------------

.PHONY: go-test # Run unit tests
go-test:
	go test -count=1 $(GO_FILES) -v -coverprofile cover.out

.PHONY: image-scan
image-scan: $(TRIVY)
	# show all vulnerabilities
	$(TRIVY) image --severity MEDIUM,HIGH,CRITICAL $(IMAGE_TAG)
	# show vulnerabilities that have been fixed
	$(TRIVY) image --exit-code 1 --ignore-unfixed --severity MEDIUM,HIGH,CRITICAL $(IMAGE_TAG)

## --------------------------------------
## Linting
## --------------------------------------

.PHONY: test-style
test-style: lint lint-charts shellcheck

$(GOLANGCI_LINT): ## Build golangci-lint from tools folder.
	cd $(TOOLS_MOD_DIR) && \
		GOPROXY=$(GOPROXY) go build -o $(TOOLS_BIN_DIR)/golangci-lint github.com/golangci/golangci-lint/v2/cmd/golangci-lint

.PHONY: lint
lint: $(GOLANGCI_LINT)
	# Setting timeout to 5m as default is 1m
	$(GOLANGCI_LINT) run --timeout=5m -v

lint-charts: $(HELM) # Run helm lint tests
	helm lint manifest_staging/charts/secrets-store-sync-controller
	helm lint charts/secrets-store-sync-controller

.PHONY: shellcheck
shellcheck: $(SHELLCHECK)
	find . \( -name '*.sh' -o -name '*.bash' \) | xargs $(SHELLCHECK)

## --------------------------------------
## E2E Testing
## --------------------------------------

.PHONY: e2e-setup ## Setup environment for e2e tests
e2e-setup: $(HELM) $(BATS) $(ENVSUBST) $(KIND)

.PHONY: e2e-bootstrap
e2e-bootstrap: e2e-setup docker-build setup-kind-cluster helm-manifest-install ## Bootstrap the e2e environment

# Run the e2e provider tests
.PHONY: run-e2e-provider-tests
run-e2e-provider-tests:
	bats -t -T test/bats/e2e-provider.bats

## --------------------------------------
## Release
## --------------------------------------
.PHONY: release-manifest
release-manifest:
	$(MAKE) manifests
	@sed -i "s/version: .*/version: ${NEWVERSION}/" manifest_staging/charts/secrets-store-sync-controller/Chart.yaml
	@sed -i "s/appVersion: .*/appVersion: v${NEWVERSION}/" manifest_staging/charts/secrets-store-sync-controller/Chart.yaml
	@sed -i "s/tag: v${CURRENTVERSION}/tag: v${NEWVERSION}/" manifest_staging/charts/secrets-store-sync-controller/values.yaml
	@sed -i "s/v${CURRENTVERSION}/v${NEWVERSION}/" manifest_staging/charts/secrets-store-sync-controller/README.md

.PHONY: promote-staging-manifest
promote-staging-manifest: #promote staging manifests to release dir
	$(MAKE) release-manifest
	@rm -rf charts/secrets-store-sync-controller/
	@cp -r manifest_staging/charts/secrets-store-sync-controller ./charts
