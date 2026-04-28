# KubePivot v2.6.1 多环境流量传播 demo Makefile
#
# 使用:
#   make setup-staging        staging: helm install + kp enroll + sandbox start
#   make verify-staging-cm    检查 staging 是否已写出 verified-traffic ConfigMap
#   make propagate-to-prod    prod: helm install + kp sandbox start --from-env staging
#   make verify-both          对比两 env 的 traffic 状态
#   make cleanup              清理两 env
#
# 环境变量 (可覆盖默认值):
#   STAGING_NAMESPACE  默认 kp-demo-bluegreen-staging
#   PROD_NAMESPACE     默认 kp-demo-bluegreen-prod
#   STAGING_ENV_NAME   默认 staging  (KPEnv 名)
#   PROD_ENV_NAME      默认 prod
#
# 前置:
#   1. KubePivot Controller 已部署
#   2. KPEnv 已配置:
#        kp context add --name staging --namespace kp-demo-bluegreen-staging
#        kp context add --name prod    --namespace kp-demo-bluegreen-prod
#   3. chart/ 已复制:
#        cp -r ../example-bluegreen/example-bluegreen/chart .

STAGING_NAMESPACE ?= kp-demo-bluegreen-staging
PROD_NAMESPACE    ?= kp-demo-bluegreen-prod
STAGING_ENV_NAME  ?= staging
PROD_ENV_NAME     ?= prod
SCRIPT_DIR := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))/scripts

.PHONY: setup-staging verify-staging-cm propagate-to-prod verify-both cleanup help

help:
	@echo "KubePivot v2.6.1 多环境流量传播 demo"
	@echo ""
	@echo "  make setup-staging        Step 1: staging 部署 + 蓝绿切换"
	@echo "  make verify-staging-cm    Step 2: 检查 staging verified-traffic ConfigMap (5min 后)"
	@echo "  make propagate-to-prod    Step 3: prod 用 staging 配置部署"
	@echo "  make verify-both          Step 4: 对比两 env traffic 状态"
	@echo "  make cleanup              Step 5: 清理两 env"
	@echo ""
	@echo "完整流程: setup-staging → 等 5min → verify-staging-cm → propagate-to-prod → verify-both"
	@echo ""
	@echo "环境变量:"
	@echo "  STAGING_NAMESPACE=$(STAGING_NAMESPACE)"
	@echo "  PROD_NAMESPACE=$(PROD_NAMESPACE)"
	@echo "  STAGING_ENV_NAME=$(STAGING_ENV_NAME)"
	@echo "  PROD_ENV_NAME=$(PROD_ENV_NAME)"

setup-staging:
	@bash $(SCRIPT_DIR)/setup-staging.sh

verify-staging-cm:
	@echo "═══════════════════════════════════════════"
	@echo "  检查 staging verified-traffic ConfigMap"
	@echo "═══════════════════════════════════════════"
	@kubectl get configmap kubepivot-verified-traffic -n $(STAGING_NAMESPACE) \
		2>/dev/null && echo "" \
		|| (echo "  ConfigMap 不存在. 可能原因:" \
		   && echo "    - staging RUNNING 不到 5min (Step 1 后等 5min)" \
		   && echo "    - Controller 没部署 (kubectl get pods -n kubepivot-system)")
	@echo ""
	@echo "  ConfigMap data 字段 traffic.yaml:"
	@kubectl get configmap kubepivot-verified-traffic -n $(STAGING_NAMESPACE) \
		-o jsonpath='{.data.traffic\.yaml}' 2>/dev/null \
		| sed 's/^/    /' \
		|| echo "    (空)"

propagate-to-prod:
	@bash $(SCRIPT_DIR)/propagate-to-prod.sh

verify-both:
	@bash $(SCRIPT_DIR)/verify-both.sh

cleanup:
	@bash $(SCRIPT_DIR)/cleanup.sh
