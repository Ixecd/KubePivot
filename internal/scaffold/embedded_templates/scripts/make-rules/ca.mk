# Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
# Use of this source code is governed by a MIT style
# License that can be found in the LICENSE file.

# ==============================================================================
# Makefile helper functions for create CA files
#

.PHONY: ca.gen.%
ca.gen.%:
	$(eval CA := $(word 1, $(subst ., ,$*)))
	@echo "===========> Generating CA files for $(CA)"
	@${ROOT_DIR}/scripts/gencerts.sh generate-cert $(OUTPUT_DIR)/cert $(CA)

.PHONY: ca.gen
ca.gen: $(addprefix ca.gen., $(CERTIFICATES))
