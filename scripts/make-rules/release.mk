# Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
# Use of this source code is governed by a MIT style
# License that can be found in the LICENSE file.

# ================================================================
# Makefile helper functions for release
#
#

.PHONY: release.run
release.run: release.verify release.ensure-tag
	@scripts/releash.sh

.PHONY: release.verify
release.verify: tools.verify.git-chglog tools.verify.github-release tools.verify.coscmd

# Git 标签发布自动化
# .PHONY: release.tag
# release.tag: tools.verify.gsemver release.ensure-tag
# 	@git push origin `git describe --tags --abbrev=0`
.PHONY: release.tag
release.tag:
	@if [ -z "$(VERSION)" ]; then echo "VERSION req"; exit 1; fi
	@git tag -a "$(VERSION)" -m "release $(VERSION)"
	@git push origin "$(VERSION)"

.PHONY: release.ensure-tag
release.ensure-tag: tools.verify.gsemver
	@scripts/ensure-tag.sh