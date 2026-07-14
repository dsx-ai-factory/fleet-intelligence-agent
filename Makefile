GO ?= go
INSTALL ?= install
GOLANGCI_LINT ?= golangci-lint
GOFMT ?= gofmt
GOTEST ?= go test
GOFLAGS ?= -trimpath
DOCKER ?= docker

# Container image build settings
IMAGE ?= fleet-intelligence-agent:dev
DOCKER_BUILDKIT ?= 1
DOCKER_BUILD_PROGRESS ?= auto
DOCKER_BUILD_EXTRA_FLAGS ?=

# Root directory of the project (absolute path).
ROOTDIR=$(dir $(abspath $(lastword $(MAKEFILE_LIST))))

BUILD_TIMESTAMP ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
VERSION ?= $(shell git describe --match 'v[0-9]*' --dirty='.m' --always)
REVISION ?= $(shell git rev-parse HEAD)$(shell if ! git diff --no-ext-diff --quiet --exit-code; then echo .m; fi)
PACKAGE=github.com/NVIDIA/fleet-intelligence-agent

ifneq "$(strip $(shell command -v $(GO) 2>/dev/null))" ""
	GOOS ?= $(shell $(GO) env GOOS)
	GOARCH ?= $(shell $(GO) env GOARCH)
else
	ifeq ($(GOOS),)
		# approximate GOOS for the platform if we don't have Go and GOOS isn't
		# set. We leave GOARCH unset, so that may need to be fixed.
		ifeq ($(OS),Windows_NT)
			GOOS = windows
		else
			UNAME_S := $(shell uname -s)
			ifeq ($(UNAME_S),Linux)
				GOOS = linux
			endif
			ifeq ($(UNAME_S),Darwin)
				GOOS = darwin
			endif
			ifeq ($(UNAME_S),FreeBSD)
				GOOS = freebsd
			endif
		endif
	else
		GOOS ?= $$GOOS
		GOARCH ?= $$GOARCH
	endif
endif

ifndef GODEBUG
	EXTRA_LDFLAGS += -s -w
	DEBUG_GO_GCFLAGS :=
	DEBUG_TAGS :=
else
	DEBUG_GO_GCFLAGS := -gcflags=all="-N -l"
	DEBUG_TAGS := static_build
endif

RELEASE=fleetint-$(VERSION:v%=%)-${GOOS}-${GOARCH}

COMMANDS=fleetint

GO_BUILD_FLAGS=-ldflags '-s -X $(PACKAGE)/internal/version.BuildTimestamp=$(BUILD_TIMESTAMP) -X $(PACKAGE)/internal/version.Version=$(VERSION) -X $(PACKAGE)/internal/version.Revision=$(REVISION) -X $(PACKAGE)/internal/version.Package=$(PACKAGE)'

ifdef BUILDTAGS
    GO_BUILDTAGS = ${BUILDTAGS}
endif
GO_BUILDTAGS ?=
GO_BUILDTAGS += ${DEBUG_TAGS}
ifneq ($(STATIC),)
	GO_BUILDTAGS += osusergo netgo static_build
endif
GO_TAGS=$(if $(GO_BUILDTAGS),-tags "$(strip $(GO_BUILDTAGS))",)

PACKAGES=$(shell $(GO) list ${GO_TAGS} ./... | grep -v /vendor/)

GOPATHS=$(shell echo ${GOPATH} | tr ":" "\n" | tr ";" "\n")

BINARIES=$(addprefix bin/,$(COMMANDS))

.PHONY: clean all binaries fleetint lint test fmt help package-snapshot docker-build docker-test
.DEFAULT: help

help: ## show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

all: binaries ## build all binaries

FORCE:

define BUILD_BINARY
@echo "Building $@"
@CGO_ENABLED=1 $(GO) build $(GOFLAGS) ${GO_BUILD_FLAGS} ${DEBUG_GO_GCFLAGS} -o $@ ${GO_TAGS}  ./$<
endef

# Build a binary from a cmd.
bin/%: cmd/% FORCE
	$(call BUILD_BINARY)

binaries: $(BINARIES) ## build binaries
	@echo "Built binaries: $(BINARIES)"

# Build container image (BuildKit enabled by default)
docker-build: ## build container image (IMAGE=fleet-intelligence-agent:dev)
	@echo "Building container image: $(IMAGE)"
	@DOCKER_BUILDKIT=$(DOCKER_BUILDKIT) $(DOCKER) build \
		--progress=$(DOCKER_BUILD_PROGRESS) \
		-t $(IMAGE) \
		$(DOCKER_BUILD_EXTRA_FLAGS) \
		.

# Build test image and run tests in container
TEST_IMAGE ?= fleet-intelligence-agent:test
docker-test: ## build test image and run tests in container
	@echo "Building test image: $(TEST_IMAGE)"
	@DOCKER_BUILDKIT=$(DOCKER_BUILDKIT) $(DOCKER) build -f Dockerfile.citests \
		-t $(TEST_IMAGE) \
		.
	@echo "Running tests..."
	@$(DOCKER) run --rm -e VERSION=$(VERSION) -e REVISION=$(REVISION) $(TEST_IMAGE)

# Specific target for fleetint (your main binary)
fleetint: bin/fleetint ## build fleetint binary
	@echo "fleetint built successfully at bin/fleetint"

lint: ## run linting tools
	@echo "Running linting..."
	@if command -v $(GOLANGCI_LINT) >/dev/null 2>&1; then \
		$(GOLANGCI_LINT) run ./...; \
	else \
		echo "golangci-lint not found, running basic checks..."; \
		$(GOFMT) -l -s . | tee /tmp/gofmt.out; \
		if [ -s /tmp/gofmt.out ]; then \
			echo "Code formatting issues found. Run 'make fmt' to fix."; \
			exit 1; \
		fi; \
		go vet ./...; \
	fi

fmt: ## format Go code
	@echo "Formatting code..."
	@$(GOFMT) -l -s -w .

test: ## run tests with coverage
	@echo "Running tests..."
	@mkdir -p coverage
	@$(GOTEST) $(GOFLAGS) -race -coverprofile=coverage/coverage.out -covermode=atomic ./...
	@$(GO) tool cover -html=coverage/coverage.out -o coverage/coverage.html
	@echo "Coverage report generated: coverage/coverage.html"
	@$(GO) tool cover -func=coverage/coverage.out | tail -1

vuln: ## run vulnerability check
	@echo "Running vulnerability check..."
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "Installing govulncheck..."; \
		$(GO) install golang.org/x/vuln/cmd/govulncheck@latest; \
	fi
	@govulncheck ./...

clean: ## clean up binaries and build artifacts
	@echo "Cleaning up..."
	@rm -f $(BINARIES)
	@rm -rf dist/
	@rm -rf coverage/
	@rm -f /tmp/gofmt.out

package-snapshot: ## package snapshot
	@echo "Packaging snapshot..."
	@goreleaser release --snapshot --clean --config .goreleaser.yaml
