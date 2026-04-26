# KubePivot v2.6 蓝绿部署示例 Makefile
#
# 使用：
#   make setup     初始 blue 部署
#   make verify    curl Ingress 看流量指向
#   make switch    sandbox commit 触发蓝绿切换
#   make cleanup   删除所有资源
#
# 环境变量（可覆盖默认值）：
#   NAMESPACE      默认 kp-demo-bluegreen
#   INGRESS_HOST   默认 whoami.local

NAMESPACE ?= kp-demo-bluegreen
INGRESS_HOST ?= whoami.local
SCRIPT_DIR := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))/scripts

.PHONY: setup verify switch cleanup help

help:
	@echo "KubePivot v2.6 蓝绿部署示例"
	@echo ""
	@echo "  make setup     部署初始 blue 版本 + Ingress"
	@echo "  make verify    验证当前流量指向"
	@echo "  make switch    触发蓝绿切换（blue → green）"
	@echo "  make cleanup   清理所有资源"
	@echo ""
	@echo "环境变量："
	@echo "  NAMESPACE=$(NAMESPACE)"
	@echo "  INGRESS_HOST=$(INGRESS_HOST)"

setup:
	@bash $(SCRIPT_DIR)/setup.sh

verify:
	@echo "═══════════════════════════════════════════"
	@echo "  当前流量指向（直接查 Ingress backend）"
	@echo "═══════════════════════════════════════════"
	@kubectl get ingress whoami-ingress -n $(NAMESPACE) \
		-o jsonpath='{"  Service: "}{.spec.rules[0].http.paths[0].backend.service.name}{"\n"}' \
		2>/dev/null || echo "  Ingress 不存在，先 make setup"
	@echo ""
	@echo "  curl 实际响应（如果 Ingress controller 已配置）:"
	@curl -s -H "Host: $(INGRESS_HOST)" http://localhost/ 2>/dev/null \
		| grep -E "Hostname:" \
		| head -1 \
		|| echo "  curl 失败：可能 Ingress controller 未路由到 localhost"

switch:
	@bash $(SCRIPT_DIR)/switch.sh

cleanup:
	@bash $(SCRIPT_DIR)/cleanup.sh
