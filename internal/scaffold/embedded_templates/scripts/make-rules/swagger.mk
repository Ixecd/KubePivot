# Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
# Use of this source code is governed by a MIT style
# License that can be found in the LICENSE file.

# ==============================================================================
# Makefile helper functions for swagger
#

.PHONY: swagger.validate
swagger.validate: tools.verify.swagger
	@echo "===========> Validating swagger API docs"
	@swagger validate $(ROOT_DIR)/docs/swagger.yaml

.PHONY: swagger.serve
swagger.serve: tools.verify.swagger
	@echo "===========> Serving swagger UI"
	@swagger serve -F=redoc --no-open --port 36666 $(ROOT_DIR)/docs/swagger.yaml