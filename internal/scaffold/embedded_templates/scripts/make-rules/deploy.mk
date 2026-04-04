# Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.

KUBECTL := kubectl
HELM    := helm

PROJECT_NAME   ?= demo-svc
KUBE_NAMESPACE ?= $(PROJECT_NAME)
KUBE_CONTEXT   ?=
KUBE_CONFIG    ?=
CHART_DIR      ?= $(ROOT_DIR)/deployments/$(PROJECT_NAME)

NAMESPACE ?= $(KUBE_NAMESPACE)
CONTEXT   ?= $(KUBE_CONTEXT)

KUBECTL_FLAGS := \
	$(if $(strip $(KUBE_CONFIG)),--kubeconfig $(KUBE_CONFIG)) \
	$(if $(strip $(CONTEXT)),--context $(CONTEXT)) \
	--namespace $(NAMESPACE)

HELM_FLAGS := \
	$(if $(strip $(KUBE_CONFIG)),--kubeconfig $(KUBE_CONFIG)) \
	$(if $(strip $(CONTEXT)),--kube-context $(CONTEXT))

DEPLOYS ?= $(if $(IMAGES),$(IMAGES),$(BINS))

define print_ctx
	@echo "  kubeconfig: $(if $(strip $(KUBE_CONFIG)),$(KUBE_CONFIG),(default))"
	@echo "  context   : $(if $(strip $(CONTEXT)),$(CONTEXT),(current))"
	@echo "  namespace : $(NAMESPACE)"
	@echo "  image     : $(REGISTRY_PREFIX)/$(1)-$(ARCH):$(VERSION)"
endef

.PHONY: deploy.full
deploy.full: deploy.build deploy.push deploy.install deploy.run.all

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
				$(ROOT_DIR) \
			|| { echo ""; echo "✘ docker build failed:"; \
			     echo "  image    : $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION)"; \
			     exit 1; }; \
		fi; \
	)

.PHONY: deploy.push
deploy.push:
	@$(foreach img,$(IMAGES), \
		if docker image inspect $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION) > /dev/null 2>&1; then \
			echo "===========> Pushing $(img):$(VERSION)"; \
			docker push $(REGISTRY_PREFIX)/$(img)-$(ARCH):$(VERSION) \
			|| { echo "✘ docker push failed"; exit 1; }; \
		else \
			echo "===========> Skipping push $(img):$(VERSION) (not built locally, already on registry)"; \
		fi; \
	)

.PHONY: deploy.install
deploy.install:
	@echo "===========> Installing chart $(PROJECT_NAME) to $(NAMESPACE)"
	@$(HELM) upgrade --install $(PROJECT_NAME) $(CHART_DIR) \
		$(HELM_FLAGS) \
		--namespace $(NAMESPACE) \
		--create-namespace \
		--set image.repository=$(REGISTRY_PREFIX)/$(firstword $(BINS))-$(ARCH) \
		--set-file controller.resourcesConfig=configs/resources.yaml \
		--set image.tag=$(VERSION) \
		--force-conflicts \
		--wait \
		--timeout 120s \
	|| { echo ""; echo "✘ helm upgrade failed:"; \
	     echo "  kubeconfig: $(if $(strip $(KUBE_CONFIG)),$(KUBE_CONFIG),(default))"; \
	     echo "  context   : $(if $(strip $(CONTEXT)),$(CONTEXT),(current))"; \
	     echo "  namespace : $(NAMESPACE)"; \
	     echo "  chart     : $(CHART_DIR)"; \
	     echo "  hint      : run 'helm status $(PROJECT_NAME) -n $(NAMESPACE)' for details"; \
	     exit 1; }

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
		set image deployment/$* $*=$(REGISTRY_PREFIX)/$*-$(ARCH):$(VERSION) \
	|| { echo ""; echo "✘ kubectl set image failed:"; \
	     echo "  namespace: $(NAMESPACE)"; \
	     echo "  image    : $(REGISTRY_PREFIX)/$*-$(ARCH):$(VERSION)"; \
	     exit 1; }
	@$(KUBECTL) $(KUBECTL_FLAGS) \
		rollout status deployment/$* --timeout=300s \
	|| { echo ""; echo "✘ rollout status failed:"; \
	     echo "  namespace: $(NAMESPACE)"; \
	     exit 1; }
