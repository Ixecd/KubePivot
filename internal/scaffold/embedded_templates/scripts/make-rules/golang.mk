# Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
# Use of this source code is governed by a MIT style
# License that can be found in the LICENSE file.

# ==============================================================================
# Makefile helper functions for generate files
#

# = 和 := 有重要区别
# = 延迟展开，在使用时展开
# := 立即展开，在定义时展开

GO := go
# set go linker flags
# -X 在编译时设置包级别变量的值，编译时注入值
# -w 省略 DWARF 调试信息，减少二进制文件大小
# -s 省略符号表，减少二进制文件大小
# -extldflags "-static" 静态链接，生成的二进制文件不依赖于系统库
GO_LDFLAGS += -X $(VERSION_PACKAGE).GitVersion=$(VERSION) \
	-X $(VERSION_PACKAGE).GitCommit=$(GIT_COMMIT) \
	-X $(VERSION_PACKAGE).GitTreeState=$(GIT_TREE_STATE) \
	-X $(VERSION_PACKAGE).BuildDate=$(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
# 调试模式的条件判断
ifneq ($(DLV),)
# 调试模式下，禁用优化，开启调试符号
	GO_BUILD_FLAGS += -gcflags "all=-N -l"
	LDFLAGS = ""
endif
GO_BUILD_FLAGS += -ldflags "$(GO_LDFLAGS)"

# windows 下，需要添加 .exe 后缀
ifeq ($(GOOS),windows)
	GO_OUT_EXT = ".exe"
endif

ifeq ($(ROOT_PACKAGE),)
	$(error the variable ROOT_PACKAGE must be set prior to including golang.mk)
endif

GOPATH := $(shell go env GOPATH)
ifeq ($(origin GOBIN), undefined)
	GOBIN := $(GOPATH)/bin
endif

COMMANDS ?= $(filter-out %.md, $(wildcard ${ROOT_DIR}/cmd/*))
BINS ?= $(foreach cmd, $(COMMANDS), $(notdir $(cmd)))

ifeq (${COMMANDS},)
	$(error Could not determine COMMANDS, set ROOT_DIR or run in source dir)
endif
ifeq (${BINS},)
	$(error Could not determine BINS, set ROOT_DIR or run in source dir)
endif

EXCLUDE_TESTS=github.com/Ixecd/kubepivot/test github.com/Ixecd/kubepivot/pkg/log github.com/Ixecd/kubepivot/third_party github.com/Ixecd/kubepivot/internal/pkg/logger

.PHONY: go.build.verify
go.build.verify:

.PHONY: go.build.%
go.build.%:
	$(eval COMMAND := $(word 2, $(subst ., ,$*)))
	$(eval PLATFORM := $(word 1, $(subst ., , $*)))
	$(eval OS := $(word 1,$(subst _, ,$(PLATFORM))))
	$(eval ARCH := $(word 2,$(subst _, ,$(PLATFORM))))
	@echo "===========> Building binary $(COMMAND) $(VERSION) for $(OS) $(ARCH)"
	@mkdir -p $(OUTPUT_DIR)/platforms/$(OS)/$(ARCH)
	@CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) $(GO) build $(GO_BUILD_FLAGS) -o $(OUTPUT_DIR)/platforms/$(OS)/$(ARCH)/$(COMMAND)$(GO_OUT_EXT) $(ROOT_PACKAGE)/cmd/$(COMMAND)

.PHONY: go.build
go.build: go.build.verify $(addprefix go.build., $(addprefix $(PLATFORM)., $(BINS)))

.PHONY: go.build.multiarch
go.build.multiarch: go.build.verify $(foreach p,$(PLATFORMS),$(addprefix go.build., $(addprefix $(p)., $(BINS))))

.PHONY: go.clean
go.clean:
	@echo "===========> Cleaning all build output"
	@rm -vrf $(OUTPUT_DIR)

.PHONY: go.lint
go.lint: tools.verify.golangci-lint
	@echo "===========> Run golangci to lint source codes"
	@golangci-lint run -c $(ROOT_DIR)/.golangci.yaml $(ROOT_DIR)/...

.PHONY: go.test
go.test: tools.verify.go-junit-report
	@echo "===========> Run unit test"
	@set -o pipefail;$(GO) test -race -cover -coverprofile=$(OUTPUT_DIR)/coverage.out \
		-timeout=10m -shuffle=on -short -v `go list ./...|\
		egrep -v $(subst $(SPACE),'|',$(sort $(EXCLUDE_TESTS)))` 2>&1 | \
		tee >(go-junit-report --set-exit-code >$(OUTPUT_DIR)/report.xml)
	@sed -i.bak '/mock_.*.go/d' $(OUTPUT_DIR)/coverage.out && rm -f $(OUTPUT_DIR)/coverage.out.bak
	@$(GO) tool cover -html=$(OUTPUT_DIR)/coverage.out -o $(OUTPUT_DIR)/coverage.html

.PHONY: go.test.cover
go.test.cover: go.test
	@$(GO) tool cover -func=$(OUTPUT_DIR)/coverage.out | \
		awk -v target=$(COVERAGE) -f $(ROOT_DIR)/scripts/coverage.awk

.PHONY: go.updates
go.updates: tools.verify.go-mod-outdated
	@$(GO) list -u -m -json all | go-mod-outdated -update -direct

.PHONY: go.dev
go.dev:
	@echo "===========> Build + Test + Install (dev loop)"
	@$(GO) build ./...
	@$(GO) test ./... -race
	@$(MAKE) install
