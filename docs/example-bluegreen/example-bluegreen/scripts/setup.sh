#!/usr/bin/env bash
# KubePivot v2.6 蓝绿示例 - setup.sh
# 部署初始 blue 版本 + Ingress
#
# 输出：
#   namespace 创建
#   helm install whoami-bluegreen
#   whoami-blue 1/1 ready
#   whoami-green 1/1 ready（同时部署，初始 weight=0）
#   Ingress 指向 blue

set -o pipefail

NAMESPACE="${NAMESPACE:-kp-demo-bluegreen}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="${SCRIPT_DIR}/../chart"

GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log()  { echo -e "${BLUE}[$(date +%H:%M:%S)] $*${NC}"; }
ok()   { echo -e "${GREEN}✓ $*${NC}"; }
warn() { echo -e "${YELLOW}⚠ $*${NC}"; }

# ── 前置检查 ───────────────────────────────────────────────────────

log "前置检查"

if ! command -v kubectl >/dev/null 2>&1; then
    echo "✗ kubectl 不在 PATH" >&2
    exit 1
fi

if ! command -v helm >/dev/null 2>&1; then
    echo "✗ helm 不在 PATH" >&2
    exit 1
fi

if ! kubectl cluster-info >/dev/null 2>&1; then
    echo "✗ kubectl 无法访问集群" >&2
    exit 1
fi

ok "kubectl / helm / 集群均就绪"

# ── 创建 namespace ─────────────────────────────────────────────────

log "创建 namespace: ${NAMESPACE}"
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
ok "namespace ${NAMESPACE} 就绪"

# ── helm install ──────────────────────────────────────────────────

log "helm install whoami-bluegreen"
helm upgrade --install whoami-bluegreen "${CHART_DIR}" \
    --namespace "${NAMESPACE}" \
    --set namespace="${NAMESPACE}" \
    --wait \
    --timeout 60s >/dev/null

ok "helm install 完成"

# ── 等待 Pod ready ─────────────────────────────────────────────────

log "等待 Pod ready"
kubectl wait --for=condition=Available \
    --namespace "${NAMESPACE}" \
    --timeout=60s \
    deployment/whoami-blue \
    deployment/whoami-green >/dev/null

ok "whoami-blue / whoami-green Pod 全部 ready"

# ── 总结 ───────────────────────────────────────────────────────────

echo
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Setup 完成${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo
echo "  namespace:   ${NAMESPACE}"
echo "  blue Pod:    $(kubectl get pods -n ${NAMESPACE} -l app=whoami,version=blue --no-headers | wc -l | tr -d ' ') ready"
echo "  green Pod:   $(kubectl get pods -n ${NAMESPACE} -l app=whoami,version=green --no-headers | wc -l | tr -d ' ') ready"
echo "  Ingress:     whoami-ingress (backend = whoami-blue)"
echo
echo "  下一步："
echo "    make verify    检查当前流量指向"
echo "    make switch    触发蓝绿切换"
echo
