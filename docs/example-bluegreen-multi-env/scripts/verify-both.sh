#!/usr/bin/env bash
# Step 4: 对比两 env 的 traffic 状态
#
# 验证传播链是否成功 - prod 的 ingress 应该跟 staging 一致.

set -euo pipefail

STAGING_NS="${STAGING_NAMESPACE:-kp-demo-bluegreen-staging}"
PROD_NS="${PROD_NAMESPACE:-kp-demo-bluegreen-prod}"

echo "═══════════════════════════════════════════"
echo "  Step 4: 对比两 env traffic 状态"
echo "═══════════════════════════════════════════"
echo ""

print_ingress_backend() {
  local ns="$1"
  local label="$2"
  echo "  ${label} (ns=${ns}):"
  local backend
  backend=$(kubectl get ingress whoami-ingress -n "${ns}" \
    -o jsonpath='{.spec.rules[0].http.paths[0].backend.service.name}' 2>/dev/null)
  if [[ -z "${backend}" ]]; then
    echo "    Ingress 不存在或未就绪"
    return
  fi
  echo "    backend.service.name: ${backend}"
}

print_ingress_backend "${STAGING_NS}" "Staging"
print_ingress_backend "${PROD_NS}"    "Prod   "
echo ""

# 期望两个 backend 相同 (都是 whoami-green)
staging_backend=$(kubectl get ingress whoami-ingress -n "${STAGING_NS}" \
  -o jsonpath='{.spec.rules[0].http.paths[0].backend.service.name}' 2>/dev/null || echo "")
prod_backend=$(kubectl get ingress whoami-ingress -n "${PROD_NS}" \
  -o jsonpath='{.spec.rules[0].http.paths[0].backend.service.name}' 2>/dev/null || echo "")

if [[ -n "${staging_backend}" ]] && [[ "${staging_backend}" == "${prod_backend}" ]]; then
  echo "✅ 两 env traffic 状态一致 (backend=${staging_backend})"
  echo "   传播链验证通过"
else
  echo "⚠️  两 env traffic 状态不一致或 ingress 未就绪"
  echo "    staging: ${staging_backend:-(未就绪)}"
  echo "    prod:    ${prod_backend:-(未就绪)}"
  echo ""
  echo "    可能原因:"
  echo "    - prod sandbox start --from-env 没成功"
  echo "    - prod ingress controller 未刷新"
  echo "    - staging 还没切到 green (sandbox 还在跑)"
fi

echo ""
echo "verified-traffic ConfigMap 对比:"
echo ""
echo "  Staging (生产者):"
kubectl get configmap kubepivot-verified-traffic -n "${STAGING_NS}" \
  -o jsonpath='{.data.traffic\.yaml}' 2>/dev/null | sed 's/^/    /' \
  || echo "    (不存在)"
echo ""
echo "  Prod (5min 稳态后也会成为传播源):"
kubectl get configmap kubepivot-verified-traffic -n "${PROD_NS}" \
  -o jsonpath='{.data.traffic\.yaml}' 2>/dev/null | sed 's/^/    /' \
  || echo "    (尚未生成 - prod 5min 稳态后 controller 会写)"
