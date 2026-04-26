#!/usr/bin/env bash
# KubePivot v2.6 蓝绿示例 - switch.sh
# 触发蓝绿切换：blue → green
#
# 这个脚本演示 v2.6 的"配置驱动切换"模式：
#   1. 修改 resources.yaml 的 routes weight（blue: 100→0, green: 0→100）
#   2. 调用 kp sandbox commit 触发完整 LOCKED → ... → COMMITTING → RUNNING 链路
#   3. KubePivot 自动：helm upgrade（已最新）+ 流量切换 + Pod ready 验证

set -o pipefail

NAMESPACE="${NAMESPACE:-kp-demo-bluegreen}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEMO_ROOT="$(dirname "${SCRIPT_DIR}")"
RESOURCES_YAML="${DEMO_ROOT}/resources.yaml"

GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log()  { echo -e "${BLUE}[$(date +%H:%M:%S)] $*${NC}"; }
ok()   { echo -e "${GREEN}✓ $*${NC}"; }
warn() { echo -e "${YELLOW}⚠ $*${NC}"; }

# ── 前置检查 ───────────────────────────────────────────────────────

log "前置检查"

if ! command -v kp >/dev/null 2>&1; then
    echo "✗ kp 不在 PATH（需要 KubePivot v2.6.0+）" >&2
    exit 1
fi

CURRENT_BACKEND=$(kubectl get ingress whoami-ingress -n "${NAMESPACE}" \
    -o jsonpath='{.spec.rules[0].http.paths[0].backend.service.name}' 2>/dev/null)

if [[ -z "${CURRENT_BACKEND}" ]]; then
    echo "✗ Ingress whoami-ingress 不存在，先 make setup" >&2
    exit 1
fi

log "当前 Ingress backend: ${CURRENT_BACKEND}"

# ── 决定切换目标 ──────────────────────────────────────────────────

if [[ "${CURRENT_BACKEND}" == "whoami-blue" ]]; then
    TARGET="whoami-green"
    SOURCE="whoami-blue"
elif [[ "${CURRENT_BACKEND}" == "whoami-green" ]]; then
    TARGET="whoami-blue"
    SOURCE="whoami-green"
else
    echo "✗ 当前 backend (${CURRENT_BACKEND}) 不是 blue 也不是 green，无法切换" >&2
    exit 1
fi

log "切换目标: ${SOURCE} → ${TARGET}"

# ── 直接调 kubectl 模拟切换（demo 简化版）─────────────────────────────
#
# 注：完整的 v2.6 流程是 kp sandbox commit，但本 demo 为了简单演示
# 直接用 kubectl 修改 Ingress backend.service.name。
# 这是 KubePivot 内部 IngressProvider.ApplyRoutes 在做的事的简化版。
#
# 真实业务项目里应该：
#   1. 编辑 configs/resources.yaml 修改 routes weight
#   2. kp sandbox start --namespace XXX
#   3. kp sandbox commit
#
# 这样会走完整 LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING → RUNNING

log "切换 Ingress backend 到 ${TARGET}"

kubectl patch ingress whoami-ingress -n "${NAMESPACE}" --type=json -p="[
    {\"op\": \"replace\", \"path\": \"/spec/rules/0/http/paths/0/backend/service/name\", \"value\": \"${TARGET}\"}
]" >/dev/null

ok "Ingress backend 已切换"

# ── 等待 ${TARGET} Pod ready ──────────────────────────────────────

log "等待 ${TARGET} Pod ready (timeout=60s)"
kubectl rollout status deployment "${TARGET}" \
    -n "${NAMESPACE}" \
    --timeout=60s >/dev/null

ok "${TARGET} 全部 ready"

# ── 总结 ───────────────────────────────────────────────────────────

echo
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  蓝绿切换完成${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo
echo "  从:  ${SOURCE}"
echo "  到:  ${TARGET}"
echo
echo "  下一步："
echo "    make verify    确认流量已指向 ${TARGET}"
echo "    make switch    再切回 ${SOURCE}"
echo "    make cleanup   清理"
echo
