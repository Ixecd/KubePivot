#!/usr/bin/env bash
# 清理两 env 资源
#
# 清理顺序:
#   1. helm uninstall (两 env)
#   2. kubectl delete namespace (两 env)
#   3. controller 自动 unenroll (namespace 删除时触发)

set -euo pipefail

STAGING_NS="${STAGING_NAMESPACE:-kp-demo-bluegreen-staging}"
PROD_NS="${PROD_NAMESPACE:-kp-demo-bluegreen-prod}"

echo "═══════════════════════════════════════════"
echo "  清理 multi-env demo 资源"
echo "═══════════════════════════════════════════"
echo ""

cleanup_ns() {
  local ns="$1"
  local label="$2"
  echo "[${label}] 清理 ns=${ns}..."

  # helm uninstall
  if helm list -n "${ns}" 2>/dev/null | grep -q whoami; then
    helm uninstall whoami -n "${ns}" || true
  fi

  # 删除 namespace (controller 自动 unenroll)
  kubectl delete namespace "${ns}" --ignore-not-found --wait=false || true

  echo "      ✓ ${ns} 清理已触发"
}

cleanup_ns "${STAGING_NS}" "Staging"
cleanup_ns "${PROD_NS}"    "Prod   "
echo ""

echo "✅ 清理已触发 (namespace 删除是异步的)"
echo ""
echo "如需清理 KPEnv 配置:"
echo "  kp context remove staging"
echo "  kp context remove prod"
