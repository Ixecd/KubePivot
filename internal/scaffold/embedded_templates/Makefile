# Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
# Use of this source code is governed by a MIT style
# License that can be found in the LICENSE file.

# Build all by default, even if it's not first.
# Targets can be arranged in logical order without forcing 'all' to be the first.
.DEFAULT_GOAL := all

.PHONY := all
all: tidy gen add-copyright format lint cover build frontend.build

# ================================================================
# Build options

ROOT_PACKAGE := github.com/Ixecd/kubepivot

VERSION_PACKAGE := github.com/Ixecd/component-base/pkg/version

ROOT_DIR := $(shell pwd)
VERSION ?= v0.1.0
ARCH ?= amd64
REGISTRY_PREFIX ?= local

# ================================================================
# Frontend options

FRONTEND_DIR ?= $(ROOT_DIR)/frontend

# ================================================================
# Other mk files
include scripts/make-rules/common.mk
include scripts/make-rules/golang.mk
include scripts/make-rules/image.mk
include scripts/make-rules/deploy.mk
include scripts/make-rules/ca.mk
include scripts/make-rules/release.mk
include scripts/make-rules/swagger.mk
include scripts/make-rules/dependencies.mk
include scripts/make-rules/gen.mk
include scripts/make-rules/tools.mk

# ================================================================
# Append frontend tools to CRITICAL_TOOLS
# (common.mk 定义了 CRITICAL_TOOLS，这里追加前端工具)
CRITICAL_TOOLS += node pnpm tsc

# ================================================================
# Usage

define USAGE_OPTIONS
Options:
  DEBUG            Whether to generate debug symbols. Default is 0.
  BINS             The binaries to build. Default is all of cmd.
                   This option is available when using: make build/build.multiarch
                   Example: make build BINS="client server"
  IMAGES           Backend images to make. Default is all of cmd starting with project.name-
                   This option is available when using: make image/image.multiarch/push/push.multiarch
                   Example: make image.multiarch IMAGES="client server"
  REGISTRY_PREFIX  Docker registry prefix. Default is qingchun22. 
                   Example: make push REGISTRY_PREFIX=qingchun22 VERSION=v2.4.1
  PLATFORMS        The multiple platforms to build. Default is linux_amd64 and linux_arm64.
                   This option is available when using: make build.multiarch/image.multiarch/push.multiarch
                   Example: make image.multiarch IMAGES="clinet server" PLATFORMS="linux_amd64 linux_arm64"
  VERSION          The version information compiled into binaries.
                   The default is obtained from gsemver or git.
  V                Set to 1 enable verbose build. Default is 0.
  FRONTEND_DIR     Frontend source directory. Default is $(ROOT_DIR)/frontend.
endef
export USAGE_OPTIONS

# ==============================================================================
# Build targets

## build: Build source code for host platform.
.PHONY: build
build:
	@$(MAKE) go.build

## install: Install kp binary to GOPATH/bin or GOBIN.
.PHONY: install
install:
	@echo "===========> Installing kp"
	@$(GO) install ./cmd/kp
	@ln -sf $(shell go env GOPATH)/bin/kp $(shell go env GOPATH)/bin/kpivot
	@ln -sf $(shell go env GOPATH)/bin/kp $(shell go env GOPATH)/bin/kubepivot
	@$(GO) install ./cmd/kp

## dev: Build, test and install in one shot (fast dev loop).
.PHONY: dev
dev:
	@$(MAKE) go.dev

## build.multiarch: Build source code for multiple platforms.
.PHONY: build.multiarch
build.multiarch:
	@$(MAKE) go.build.multiarch

## image: Build docker images for host arch.
.PHONY: image
image:
	@$(MAKE) image.build

## image.multiarch: Build backend images for multiple platforms.
.PHONY: image.multiarch
image.multiarch:
	@$(MAKE) image.build.multiarch

## push: Push docker images for host arch and push images to registry.
.PHONY: push
push:
	@$(MAKE) image.push

## push.multiarch: Push docker images for multiple platforms to registry.
.PHONY: push.multiarch
push.multiarch:
	@$(MAKE) image.push.multiarch

## deploy: Deploy updated components to deployment env.
.PHONY: deploy
deploy:
	@$(MAKE) deploy.run

## clean: Remove all files that are created during build.
.PHONY: clean
clean:
	@echo "===========> Cleaning all build output"
	@-rm -vrf $(OUTPUT_DIR)
	@$(MAKE) frontend.clean

## lint: Check syntax and styling of go sources.
.PHONY: lint
lint:
	@$(MAKE) go.lint

## test: Run unit tests.
.PHONY: test
test:
	@$(MAKE) go.test

## cover: Run unit tests and generate code coverage report.
.PHONY: cover
cover:
	@$(MAKE) go.test.cover

.PHONY: release
release:
	@$(MAKE) release.run

## format: Gofmt (reformat) package sources (exclude vendor dir if existed).
.PHONY: format
format: tools.verify.golines tools.verify.goimports
	@echo "===========> Formating codes"
	@$(FIND) -type f -name '*.go' | $(XARGS) gofmt -s -w
	@$(FIND) -type f -name '*.go' | $(XARGS) goimports -w -local $(ROOT_PACKAGE)
	@$(FIND) -type f -name '*.go' | $(XARGS) golines -w --max-len=120 --reformat-tags --shorten-comments --ignore-generated .
	@$(GO) mod edit -fmt

## verify-copyright: Verify the boilerplate headers for all files.
.PHONY: verify-copyright
verify-copyright: 
	@$(MAKE) copyright.verify

## add-copyright: Ensures source code files have copyright license headers.
.PHONY: add-copyright
add-copyright:
	@$(MAKE) copyright.add

## gen: Generate all necessary files, such as error code files.
.PHONY: gen
gen: 
	@$(MAKE) gen.run

## ca: Generate CA files for all iam components.
.PHONY: ca
ca:
	@$(MAKE) gen.ca

## swagger: Generate swagger document.
.PHONY: swagger
swagger:
	@$(MAKE) swagger.run

## server-swagger: Serve swagger spec and docs.
.PHONY: swagger.serve
serve-swagger:
	@$(MAKE) swagger.Serve

## dependencies: Install necessary dependencies.
.PHONY: dependencies
dependencies:
	@$(MAKE) dependencies.run

## tools: Install necessary tools (including frontend tools).
.PHONY: tools
tools:
	@$(MAKE) tools.install

.PHONY: check-updates
check-updates:
	@$(MAKE) go.updates

.PHONY: tidy
tidy:
	@$(GO) mod tidy

# ==============================================================================
# Frontend targets (proxied from tools.mk)

## frontend.build: Build frontend for production.
.PHONY: frontend.build
frontend.build:
	@$(MAKE) -f $(firstword $(MAKEFILE_LIST)) _frontend.build

.PHONY: _frontend.build
_frontend.build: tools.verify.node install.frontend-deps
	@echo "===========> Building frontend"
	@cd $(FRONTEND_DIR) && npm run build

## frontend.dev: Start frontend development server.
.PHONY: frontend.dev
frontend.dev: tools.verify.node install.frontend-deps
	@echo "===========> Starting frontend dev server (http://localhost:5173)"
	@cd $(FRONTEND_DIR) && npm run dev

## frontend.typecheck: Run TypeScript type check (no emit).
.PHONY: frontend.typecheck
frontend.typecheck: tools.verify.node install.frontend-deps
	@echo "===========> Type checking frontend"
	@cd $(FRONTEND_DIR) && npx tsc --noEmit

## frontend.clean: Remove frontend build output and node_modules.
.PHONY: frontend.clean
frontend.clean:
	@if [ -d "$(FRONTEND_DIR)" ]; then \
		echo "===========> Cleaning frontend"; \
		rm -rf $(FRONTEND_DIR)/dist $(FRONTEND_DIR)/node_modules; \
	fi

## help: Show this help info
.PHONY: help
help:
	@printf "\nUsage: make <TARGETS> <OPTIONS> ...\n\nTargets:\n"
	@sed -n 's/^##//p' $< | column -t -s ':' | sed -e 's/^/ /'
	@echo "$$USAGE_OPTIONS"
