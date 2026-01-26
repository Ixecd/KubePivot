# Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
# Use of this source code is governed by a MIT style
# License that can be found in the LICENSE file.

# ==============================================================================
# Makefile helper functions for deploy
#

KUBECTL := kubectl
HELM := helm
PROJECT_NAME ?= dev-toolkit
KUBE_NAMESPACE ?= $(PROJECT_NAME)
KUBE_CONTEXT ?= qingchun22.dev
CHART_DIR ?= $(if $(wildcard $(ROOT_DIR)/deployments/$(PROJECT_NAME)),$(ROOT_DIR)/deployments/project)

NAMESPACE ?= $(KUBE_NAMESPACE)
CONTEXT ?= $(KUBE_CONTEXT)

DEPLOYS ?= $(if $(IMAGES),$(IMAGES),$(BINS))

.PHONY: deploy.run.all
deploy.run.all:
	@echo "===========> Deploying all components"
	@$(MAKE) deploy.run

.PHONY: deploy.install
deploy.install:
	@echo "===========> Installing chart $(PROJECT_NAME) to $(NAMESPACE)"
	@$(HELM) upgrade --install $(PROJECT_NAME) $(CHART_DIR) --namespace $(NAMESPACE) --create-namespace

.PHONY: deploy.full
deploy.full: deploy.install deploy.run.all

.PHONY: deploy.run
deploy.run: $(addprefix deploy.run., $(DEPLOYS))

.PHONY: deploy.run.%
deploy.run.%:
	$(eval ARCH := $(word 2,$(subst _, ,$(PLATFORM))))
	@echo "===========> Deploying $* $(VERSION) on $(ARCH)"
	@$(KUBECTL) --context $(CONTEXT) --namespace $(NAMESPACE) set image deployment/$* $*=$(REGISTRY_PREFIX)/$*-$(ARCH):$(VERSION) --record
	@$(KUBECTL) --context $(CONTEXT) --namespace $(NAMESPACE) rollout status deployment/$* --timeout=300s