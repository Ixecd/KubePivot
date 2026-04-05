# ==============================================================================
# Makefile helper functions for code generation
#

.PHONY: gen.run
gen.run: gen.errcode

.PHONY: gen.errcode
gen.errcode: gen.errcode.code gen.errcode.doc

.PHONY: gen.errcode.code
gen.errcode.code: tools.verify.codegen
	@echo "===========> Generating error code go source files"
	@codegen -type=ErrorCode $(ROOT_DIR)/internal/pkg/code

.PHONY: gen.errcode.doc
gen.errcode.doc: tools.verify.codegen
	@echo "===========> Generating error code markdown documentation"
	@mkdir -p $(ROOT_DIR)/docs/guide/zh-CN/api
	@codegen -type=ErrorCode -doc \
		-output $(ROOT_DIR)/docs/guide/zh-CN/api/error_code_generated.md \
		$(ROOT_DIR)/internal/pkg/code

.PHONY: gen.clean
gen.clean:
	@$(FIND) -type f -name '*_generated.go' -delete
