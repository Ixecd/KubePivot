# Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
# Use of this source code is governed by a MIT style
# License that can be found in the LICENSE file.

SHELL := /bin/bash

# include the common make file
# MAKEFILE_LIST - make 内置变量，包含所有被包含的 Makefile 路径列表
# 是 make 已经处理过的 Makefile 文件路径
# 按照 include/解析的顺序排列
# 包含主 Makefile 和被 include 的 Makefile
COMMON_SELF_DIR := $(dir $(lastword $(MAKEFILE_LIST)))

# ROOT_DIR is the root directory of the project.
ifeq ($(origin ROOT_DIR), undefined)
# 确保是绝对路径
  ROOT_DIR := $(abspath $(shell cd $(COMMON_SELF_DIR)/../.. && pwd -P))
endif	

# Load project config if present (KEY=VALUE format).
-include $(ROOT_DIR)/configs/project.env

# Load components config if present (simple YAML list).
COMPONENTS_FILE ?= $(ROOT_DIR)/configs/components.yaml
ifneq ("$(wildcard $(COMPONENTS_FILE))","")
COMPONENT_NAMES ?= $(shell find $(ROOT_DIR)/cmd -maxdepth 1 -mindepth 1 -type d -exec basename {} \;)
COMPONENT_IMAGES ?= $(shell awk -F': *' '/^ *image:/{gsub(/"/,"",$$2); if ($$2 != "") print $$2}' $(COMPONENTS_FILE))
endif

ifneq ($(strip $(COMPONENT_NAMES)),)
COMMANDS ?= $(foreach c,$(COMPONENT_NAMES),$(ROOT_DIR)/cmd/$(c))
endif

ifneq ($(strip $(COMPONENT_IMAGES)),)
IMAGES ?= $(COMPONENT_IMAGES)
endif

# OUTPUT_DIR is the output directory of the project.
ifeq ($(origin OUTPUT_DIR),undefined)
OUTPUT_DIR := $(ROOT_DIR)/_output
$(shell mkdir -p $(OUTPUT_DIR))
endif

# TOOLS_DIR is the tools directory of the project.
ifeq ($(origin TOOLS_DIR),undefined)
TOOLS_DIR := $(OUTPUT_DIR)/tools
$(shell mkdir -p $(TOOLS_DIR))
endif

# TMP_DIR is the tmp directory of the project.
ifeq ($(origin TMP_DIR),undefined)
TMP_DIR := $(OUTPUT_DIR)/tmp
$(shell mkdir -p $(TMP_DIR))
endif

# set the version number. you should not need to do this
# for the majority of scenarios.
ifeq ($(origin VERSION), undefined)
VERSION := $(shell git describe --tags --always --match='v*')
endif

# Check if the tree is dirty.  default to dirty
GIT_TREE_STATE:="dirty"
ifeq (, $(shell git status --porcelain 2>/dev/null))
	GIT_TREE_STATE="clean"
endif
GIT_COMMIT:=$(shell git rev-parse HEAD)

# Minimum test coverage
ifeq ($(origin COVERAGE),undefined)
COVERAGE := 60
endif

# The OS must be linux when building docker images
PLATFORMS ?= linux_amd64 linux_arm64
# The OS can be linux/windows/darwin when building binaries
# PLATFORMS ?= darwin_amd64 windows_amd64 linux_amd64 linux_arm64

# Set a specific PLATFORM
ifeq ($(origin PLATFORM), undefined)
	ifeq ($(origin GOOS), undefined)
		GOOS := $(shell go env GOOS)
	endif
	ifeq ($(origin GOARCH), undefined)
		GOARCH := $(shell go env GOARCH)
	endif
	PLATFORM := $(GOOS)_$(GOARCH)
	# Use linux as the default OS when building images
	IMAGE_PLAT := linux_$(GOARCH)
else
	GOOS := $(word 1, $(subst _, ,$(PLATFORM)))
	GOARCH := $(word 2, $(subst _, ,$(PLATFORM)))
	IMAGE_PLAT := $(PLATFORM)
endif

# Linux Command settings, from ROOT_DIR start find all files
# FIND := find $(ROOT_DIR) ! -path '$(ROOT_DIR)/third_party/*' ! -path '$(ROOT_DIR)/vendor/*'
FIND := find $(ROOT_DIR) \
    \( \
        -path $(ROOT_DIR)/third_party \
     -o -path $(ROOT_DIR)/vendor \
     -o -path $(ROOT_DIR)/.git \
     -o -path $(ROOT_DIR)/_output \
     -o -path $(ROOT_DIR)/tmp \
    \) -prune -o -print
XARGS := xargs -r -0

# Makefile settings
ifndef V
MAKEFLAGS += --no-print-directory
endif

# Copy githook scripts when execute makefile, maybe not use, better use a target instead
COPY_GITHOOK:=$(shell [ -d .githooks ] && cp -f .githooks/* .git/hooks/ || true)

# Specify components which need certificate
ifeq ($(origin CERTIFICATES),undefined)
CERTIFICATES=apiserver authz-server admin
endif

# Specify tools severity, include: BLOCKER_TOOLS, CRITICAL_TOOLS, TRIVIAL_TOOLS.
# Missing BLOCKER_TOOLS can cause the CI flow execution failed, i.e. `make all` failed.
# Missing CRITICAL_TOOLS can lead to some necessary operations failed. i.e. `make release` failed.
# TRIVIAL_TOOLS are Optional tools, missing these tool have no affect.
BLOCKER_TOOLS ?= gsemver golines go-junit-report golangci-lint addlicense goimports
CRITICAL_TOOLS ?= swagger mockgen gotests git-chglog github-release coscmd go-mod-outdated protoc-gen-go cfssl go-gitlint
TRIVIAL_TOOLS ?= depth go-callvis gothanks richgo rts kube-score

# COMMA value is ','
# SPACE value is ' '
COMMA := ,
SPACE :=
SPACE +=
