# Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
# Use of this source code is governed by a MIT style
# License that can be found in the LICENSE file.

# ==============================================================================
# Makefile helper functions for deploy
#

KUBECTL := kubectl
NAMESPACE ?= dev-toolkit
CONTEXT ?= qingchun22.dev

DEPLOYS = helloword

.PHONY: deploy.run.all
deploy.run.all:
	@echo "===========> Deploying all components"
	@$(MAKE) deploy.run

.PHONY: deploy.run
deploy.run: $(addprefix deploy.run., $(DEPLOYS))

.PHONY: deploy.run.%
deploy.run.%:
	$(eval ARCH := $(word 2,$(subst _, ,$(PLATFORM))))
	@echo "===========> Deploying $* $(VERSION) on $(ARCH)"
	@$(KUBECTL) --context $(CONTEXT) --namespace $(NAMESPACE) set image deployment/$* $*=$(REGISTRY_PREFIX)/$*-$(ARCH):$(VERSION) --record
	@$(KUBECTL) --context $(CONTEXT) --namespace $(NAMESPACE) rollout status deployment/$* --timeout=300s