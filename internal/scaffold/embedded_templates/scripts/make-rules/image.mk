# Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
# Use of this source code is governed by a MIT style
# License that can be found in the LICENSE file.

# ==============================================================================
# Makefile helper functions for docker image
#

DOCKER := docker
DOCKER_PULL ?= true
DOCKER_SUPPORTED_API_VERSION ?= 1.51

REGISTRY_PREFIX ?= $(PROJECT_NAME)
BASE_IMAGE = alpine:3.18

# 按需传入，例如：make image EXTRA_ARGS="--no-cache" 强制重新构建避免缓存污染
EXTRA_ARGS ?=
_DOCKER_BUILD_EXTRA_ARGS :=

ifdef HTTP_PROXY
_DOCKER_BUILD_EXTRA_ARGS += --build-arg HTTP_PROXY=$(HTTP_PROXY)
endif

ifneq ($(EXTRA_ARGS),)
_DOCKER_BUILD_EXTRA_ARGS += $(EXTRA_ARGS)
endif

# Determine image files by looking into build/docker/*/Dockerfile
IMAGES_DIR ?= $(wildcard ${ROOT_DIR}/build/docker/*)

# Determine image names by stripping out the dir names
IMAGES ?= $(filter-out tools,$(foreach image,${IMAGES_DIR},$(notdir ${image})))

ifeq ($(IMAGES),)
  $(error Could not determine IMAGES, set ROOT_DIR or run in source dir)
endif

.PHONY: image.verify
image.verify:
	$(eval API_VERSION := $(shell $(DOCKER) version --format '{{.Server.APIVersion}}' 2>/dev/null))
	$(eval PASS := $(shell echo "$(API_VERSION) > $(DOCKER_SUPPORTED_API_VERSION)" | bc))
	@if [ "$(PASS)" != "1" ]; then \
		echo "Docker API version $(API_VERSION) is not supported, required $(DOCKER_SUPPORTED_API_VERSION) or higher"; \
		exit 1; \
	fi

.PHONY: image.daemon.verify
image.daemon.verify:
	$(eval DOCKER_DAEMON := $(shell $(DOCKER) info --format '{{.ServerVersion}}' 2>/dev/null))
	@if [ "$(DOCKER_DAEMON)" = "" ]; then \
		echo "Docker daemon is not running"; \
		exit 1; \
	fi

.PHONY: image.build
image.build: image.verify go.build.verify $(addprefix image.build., $(addprefix $(IMAGE_PLAT)., $(IMAGES)))

.PHONY: image.build.multiarch
image.build.multiarch: image.verify go.build.verify $(foreach p, $(PLATFORMS),$(addprefix image.build., $(addprefix $(p)., $(IMAGES))))

.PHONY: image.build.%
image.build.%: go.build.%
	$(eval IMAGE := $(COMMAND))
	$(eval IMAGE_PLAT := $(subst _,/,$(PLATFORM)))
	@echo "===========> Building docker image $(IMAGE) $(VERSION) for $(IMAGE_PLAT)"
	@mkdir -p $(TMP_DIR)/$(IMAGE)
	@cat $(ROOT_DIR)/build/docker/$(IMAGE)/Dockerfile\
		| sed "s#BASE_IMAGE#$(BASE_IMAGE)#g" >$(TMP_DIR)/$(IMAGE)/Dockerfile
	@cp $(OUTPUT_DIR)/platforms/$(IMAGE_PLAT)/$(IMAGE) $(TMP_DIR)/$(IMAGE)/
	@DST_DIR=$(TMP_DIR)/$(IMAGE) $(ROOT_DIR)/build/docker/$(IMAGE)/build.sh 2>/dev/null || true
	$(eval BUILD_SUFFIX := $(_DOCKER_BUILD_EXTRA_ARGS) $(if $(filter true,$(DOCKER_PULL)),--pull) -t $(REGISTRY_PREFIX)/$(IMAGE)-$(ARCH):$(VERSION) $(TMP_DIR)/$(IMAGE))
	@if [ $(shell $(GO) env GOARCH) != $(ARCH) ] ; then \
		$(MAKE) image.daemon.verify ;\
		$(DOCKER) build --platform $(IMAGE_PLAT) $(BUILD_SUFFIX) ; \
	else \
		$(DOCKER) build $(BUILD_SUFFIX) ; \
	fi
	@rm -rf $(TMP_DIR)/$(IMAGE)

.PHONY: image.manifest.push
image.manifest.push:
	@$(foreach img,$(IMAGES), \
		if [ "$(words $(PLATFORMS))" -gt "1" ]; then \
			docker buildx imagetools create -t $(REGISTRY_PREFIX)/$(img):$(VERSION) \
			$(foreach plat,$(PLATFORMS),$(REGISTRY_PREFIX)/$(img)-$(subst /,-,$(subst _,/,$(plat))):$(VERSION)); \
		fi; \
	)

.PHONY: image.push
image.push: image.verify go.build.verify $(addprefix image.push., $(addprefix $(IMAGE_PLAT)., $(IMAGES)))

.PHONY: image.push.multiarch
image.push.multiarch: image.verify go.build.verify $(foreach p, $(PLATFORMS),$(addprefix image.push., $(addprefix $(p)., $(IMAGES)))) image.manifest.push

.PHONY: image.push.%
image.push.%: image.build.%
	@echo "===========> Pushing docker image $(IMAGE) $(VERSION) for $(IMAGE_PLAT) to $(REGISTRY_PREFIX)"
	$(DOCKER) push $(REGISTRY_PREFIX)/$(IMAGE)-$(ARCH):$(VERSION)

# .PHONY: image.manifest.push
# image.manifest.push: export DOCKER_CLI_EXPERIMENTAL=enabled
# image.manifest.push: image.verify go.build.verify \
# $(addprefix image.manifest.push., $(addprefix $(IMAGE_PLAT)., $(IMAGES)))

# .PHONY: image.manifest.push.multiarch
# image.manifest.push.multiarch: image.push.multiarch $(addprefix image.manifest.push.multiarch., $(IMAGES))

# .PHONY: image.manifest.push.multiarch.%
# image.manifest.push.multiarch.%:
# 	@echo "===========> Pushing manifest $* $(VERSION) to $(REGISTRY_PREFIX) and then remove the local manifest list"
# 	REGISTRY_PREFIX=$(REGISTRY_PREFIX) PLATFROMS="$(PLATFORMS)" IMAGE=$* VERSION=$(VERSION) DOCKER_CLI_EXPERIMENTAL=enabled \
# 	  $(ROOT_DIR)/build/lib/create-manifest.sh
