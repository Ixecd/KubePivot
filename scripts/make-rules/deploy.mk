# Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
# Use of this source code is governed by a MIT style
# License that can be found in the LICENSE file.

# ==============================================================================
# Makefile helper functions for deploy
#

KUBECTL := kubectl
HELM    := helm

# PROJECT_NAME: dtk init 时自动替换成 --name 的值
# KUBE_CONTEXT: 留空 = 使用当前 kubectl context，不能写 ""，否则 $(if $(strip)) 判断失效
PROJECT_NAME   ?= demo-svc
KUBE_NAMESPACE ?= $(PROJECT_NAME)
KUBE_CONTEXT   ?=
CHART_DIR      ?= $(ROOT_DIR)/deployments/$(PROJECT_NAME)

NAMESPACE ?= $(KUBE_NAMESPACE)
CONTEXT   ?= $(KUBE_CONTEXT)

# 公共 flag，避免每个 target 重复写 $(if $(CONTEXT),...)
KUBECTL_FLAGS := $(if $(strip $(CONTEXT)),--context $(CONTEXT)) --namespace $(NAMESPACE)
HELM_FLAGS    := $(if $(strip $(CONTEXT)),--kube-context $(CONTEXT))

# DEPLOYS: 优先用 IMAGES，没有则用 BINS（golang.mk 自动扫 cmd/ 生成）
DEPLOYS ?= $(if $(IMAGES),$(IMAGES),$(BINS))

# ==============================================================================
# deploy.full: 完整部署流程（build → push → install → rollout）
# 通常直接执行这个，dtk deploy 调用的就是它
# ==============================================================================
.PHONY: deploy.full
deploy.full: deploy.build deploy.push deploy.install deploy.run.all

# ==============================================================================
# deploy.build: 构建 Docker 镜像
# 先检查远端是否已有同 tag 镜像，有则跳过，避免 VERSION 不变时重复构建
# ==============================================================================
.PHONY: deploy.build
deploy.build:
	@$(foreach img,$(IMAGES), \
		echo "===========> Checking image $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION)"; \
		if docker manifest inspect $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION) > /dev/null 2>&1; then \
			echo "===========> Image already exists, skipping build"; \
		else \
			echo "===========> Building $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION)"; \
			docker build \
				-t $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION) \
				-f $(ROOT_DIR)/build/docker/$(img)/Dockerfile \
				--build-arg SERVICE_NAME=$(img) \
				$(ROOT_DIR); \
		fi; \
	)

# ==============================================================================
# deploy.push: 推送 Docker 镜像到 registry
# 同样先检查远端是否已有同 tag，有则跳过
# ==============================================================================
.PHONY: deploy.push
deploy.push:
	@$(foreach img,$(IMAGES), \
		echo "===========> Checking image $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION)"; \
		if docker manifest inspect $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION) > /dev/null 2>&1; then \
			echo "===========> Image already pushed, skipping push"; \
		else \
			echo "===========> Pushing $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION)"; \
			docker push $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION); \
		fi; \
	)

# ==============================================================================
# deploy.install: Helm 安装/升级 chart
# --force-conflicts: 防止 kubectl set 等操作产生的 SSA field manager 冲突
# --wait: 等待 deployment ready 后再返回，确保后续 rollout 能找到 pod
# image.repository 用 $(firstword $(BINS)) 而不是 $(PROJECT_NAME)，
#   因为实际镜像名来自 cmd/ 目录扫描，两者可能不同（如 PROJECT_NAME=dev-toolkit，BINS=dtk）
# ==============================================================================
.PHONY: deploy.install
deploy.install:
	@echo "===========> Installing chart $(PROJECT_NAME) to $(NAMESPACE)"
	@$(HELM) upgrade --install $(PROJECT_NAME) $(CHART_DIR) \
		$(HELM_FLAGS) \
		--namespace $(NAMESPACE) \
		--create-namespace \
		--set image.repository=$(REGISTRY_PREFIX)/$(firstword $(BINS))-$(ARCH) \
		--set image.tag=$(VERSION) \
		--force-conflicts \
		--wait \
		--timeout 120s

# ==============================================================================
# deploy.run.all / deploy.run / deploy.run.%: 滚动更新已部署的 deployment
# 用于 VERSION 不变但需要强制重新拉镜像的场景（如 latest tag）
# 正常走 deploy.full 时 deploy.install --wait 已经完成更新，这里是补充手段
# ==============================================================================
.PHONY: deploy.run.all
deploy.run.all:
	@echo "===========> Deploying all components"
	@$(MAKE) deploy.run

.PHONY: deploy.run
deploy.run: $(addprefix deploy.run., $(DEPLOYS))

.PHONY: deploy.run.%
deploy.run.%:
	@echo "===========> Deploying $* $(VERSION) on $(ARCH)"
	@$(KUBECTL) $(KUBECTL_FLAGS) \
		set image deployment/$* $*=$(REGISTRY_PREFIX)/$*-$(ARCH):$(VERSION)
	@$(KUBECTL) $(KUBECTL_FLAGS) \
		rollout status deployment/$* --timeout=300s
