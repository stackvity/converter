# --- START OF REVISED FILE Makefile ---
# Makefile for stack-converter development
# Provides targets for common dev tasks, aiming for CI parity.
# Ref: cicd.md#9, backlog-backend.md#6 (SC-STORY-066)
# (No changes needed)

SHELL := /bin/bash
GO := go
GOFMT := gofmt
# Recommendation 1: Define pinned linter version
GOLINT_VERSION := v1.59.1 # Example pinned version
# Ensure GOLINT points to the executable installed by install-tools or available in PATH
# This aims to use the version installed by 'make install-tools'
GOLINT_INSTALLED := $(shell $(GO) env GOPATH)/bin/golangci-lint
GOLINT := $(GOLINT_INSTALLED)
ifeq ($(shell command -v $(GOLINT) 2> /dev/null),)
GOLINT := golangci-lint # Fallback if not in GOPATH/bin but in PATH
endif
BINARY_NAME := stack-converter
CMD_PATH := ./cmd/$(BINARY_NAME)
OUTPUT_DIR := ./bin
# Attempt to get version from git, fallback to "dev"
# Match goreleaser's logic for consistency if possible
VERSION ?= $(shell git describe --tags --always --dirty --match='v*' 2> /dev/null || echo "dev")
# Get commit hash and build date
COMMIT := $(shell git rev-parse --short HEAD 2> /dev/null || echo "none")
DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
# Standard ldflags for version injection and smaller binaries
# Variables match those expected in cmd/stack-converter/root.go
LDFLAGS := "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"
# Read Go version from go.mod for reference (ensure CI uses compatible version)
GO_VERSION_MOD := $(shell grep '^go ' go.mod | awk '{print $$2}')
GO_VERSION_SYS := $(shell $(GO) version | awk '{print $$3}')
# Define coverage file
COVERAGE_FILE := coverage.out

# Targets that don't represent files
.PHONY: all build clean fmt lint run test help check-deps install-tools tidy test-docker cover cover-html

# Default target
all: build

# Check for required tools
check-deps:
	@command -v $(GO) >/dev/null 2>&1 || { echo >&2 "[ERROR] Go is not installed. Please install Go $(GO_VERSION_MOD) or later."; exit 1; }
	@echo "[INFO] Using Go version: $(GO_VERSION_SYS) (go.mod requires >= $(GO_VERSION_MOD))"
	@command -v git >/dev/null 2>&1 || { echo >&2 "[ERROR] Git is not installed. Aborting."; exit 1; }
	@command -v $(GOLINT) >/dev/null 2>&1 || { echo >&2 "[WARN] $(GOLINT) not found. Run 'make install-tools' for linting."; }
	@echo "[INFO] Dependencies checked."

# Install development tools (linters) with pinned version
install-tools:
	@echo "==== Installing Development Tools (golangci-lint@$(GOLINT_VERSION)) ===="
	@command -v $(GO) >/dev/null 2>&1 || { echo >&2 "[ERROR] Go is not installed. Aborting."; exit 1; }
	# Install to GOPATH/bin, ensure GOPATH/bin is in PATH
	@echo "[INFO] Installing golangci-lint $(GOLINT_VERSION) to $(shell $(GO) env GOPATH)/bin..."
	GOBIN=$(shell $(GO) env GOPATH)/bin $(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLINT_VERSION)
	@echo "[INFO] Done. Make sure '$(shell $(GO) env GOPATH)/bin' is in your system PATH."

# Build the binary with version information
build: check-deps fmt tidy
	@echo "==== Building $(BINARY_NAME) v$(VERSION) (Commit: $(COMMIT), Built: $(DATE)) ===="
	@mkdir -p $(OUTPUT_DIR)
	$(GO) build -ldflags=$(LDFLAGS) -o $(OUTPUT_DIR)/$(BINARY_NAME) $(CMD_PATH)
	@echo "[INFO] Binary available at $(OUTPUT_DIR)/$(BINARY_NAME)"

# Clean build artifacts and coverage files
clean:
	@echo "==== Cleaning Build Artifacts ===="
	@rm -rf $(OUTPUT_DIR)
	@rm -f $(COVERAGE_FILE)
	@rm -f coverage.html

# Format Go code (using gofmt - mimics CI check)
fmt:
	@echo "==== Formatting Go Code (gofmt) ===="
	# Use find to ensure all .go files are checked, excluding vendor
	$(GOFMT) -s -w $$(find . -type f -name '*.go' -not -path "./vendor/*")

# Run linters (using the same config file as CI: .golangci.yaml)
lint: check-deps
	@echo "==== Running Linters ($(GOLINT) - using .golangci.yaml) ===="
	@command -v $(GOLINT) >/dev/null 2>&1 || { echo >&2 "[ERROR] $(GOLINT) not found. Run 'make install-tools'."; exit 1; }
	# Assumes .golangci.yaml exists in the root and configures linters (incl. gosec)
	$(GOLINT) run ./...

# Run the application (passing arguments)
# Example: make run ARGS="-i ./src -o ./docs -v"
ARGS ?= --help
run: build
	@echo "==== Running $(BINARY_NAME) $(ARGS) ===="
	@$(OUTPUT_DIR)/$(BINARY_NAME) $(ARGS)

# Run tests (mimics CI test execution, including race detector and coverage)
# Example: make test PKG=./converter/...
PKG ?= ./...
test: check-deps tidy
	@echo "==== Running Tests (PKG=$(PKG)) with race detector and coverage ===="
	# Add -coverprofile if generating coverage locally
	# Using -v for verbose output, timeout for safety
	$(GO) test -race -v -timeout 5m -coverprofile=$(COVERAGE_FILE) -covermode=atomic $(PKG)

# Generate HTML coverage report
cover-html: test
	@echo "==== Generating HTML Coverage Report ===="
	@$(GO) tool cover -html=$(COVERAGE_FILE) -o coverage.html
	@echo "[INFO] HTML coverage report available at coverage.html"

# Generate text coverage report (useful for CI summary)
cover: test
	@echo "==== Generating Text Coverage Report ===="
	@$(GO) tool cover -func=$(COVERAGE_FILE)

# Tidy Go modules (and optionally vendor)
tidy:
	@echo "==== Tidying Go Modules ===="
	$(GO) mod tidy
	# $(GO) mod vendor # Uncomment if using vendoring

# (Optional) Run tests inside a Docker container mimicking CI environment
# Requires Docker installed and a Dockerfile defining the CI build env
# test-docker:
#	@echo "==== Running Tests inside Docker (CI Parity) ===="
#	@command -v docker >/dev/null 2>&1 || { echo >&2 "[ERROR] Docker is not installed. Aborting."; exit 1; }
#	@docker build -t stack-converter-test-env -f Dockerfile.test . # Assuming Dockerfile.test exists
#	@docker run --rm stack-converter-test-env make test # Run tests inside container

# Display help
help:
	@echo "stack-converter Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make install-tools   Install necessary dev tools (golangci-lint@$(GOLINT_VERSION))."
	@echo "  make build           Build the $(BINARY_NAME) binary with version info."
	@echo "  make test            Run tests locally with race detector and generate coverage profile (coverage.out)."
	@echo "                       (Use PKG=./path/... to target specific packages)."
	@echo "  make cover           Run tests and display text coverage summary."
	@echo "  make cover-html      Run tests and generate an HTML coverage report (coverage.html)."
	@echo "  make lint            Run linters locally (uses .golangci.yaml, mimics CI)."
	@echo "  make fmt             Format Go source code using gofmt."
	@echo "  make run             Run the built binary (use ARGS='...' to pass arguments, defaults to '--help')."
	@echo "  make clean           Remove build artifacts and coverage files."
	@echo "  make tidy            Run go mod tidy (and optionally go mod vendor)."
	@echo "  make check-deps      Verify required tools (Go, Git, linter) are installed."
	@echo "  make help            Show this help message."
	@echo ""
	@echo "CI Parity Notes:"
	@echo " - 'make lint' uses the same .golangci.yaml as the CI workflow."
	@echo " - 'make test' uses '-race' and '-coverprofile' like the CI workflow (where supported)."
	@echo " - Ensure local Go and golangci-lint versions match CI for best results."
# --- END OF REVISED FILE Makefile ---