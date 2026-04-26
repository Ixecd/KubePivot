#!/usr/bin/env bash
# KubePivot v2.6 蓝绿示例 - cleanup.sh

set -o pipefail

NAMESPACE="${NAMESPACE:-kp-demo-bluegreen}"

GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log()  { echo -e "${BLUE}[$(date +%H:%M:%S)] $*${NC}"; }
ok()   { echo -e "${GREEN}✓ $*${NC}"; }
warn() { echo -e "${YELLOW}⚠ $*${NC}"; }

log "清理 demo 资源"

# helm uninstall（如果存在）
if helm status whoami-bluegreen -n "${NAMESPACE}" >/dev/null 2>&1; then
    helm uninstall whoami-bluegreen -n "${NAMESPACE}" >/dev/null
    ok "helm release 已卸载"
fi

# 删 namespace（连带所有资源）
if kubectl get namespace "${NAMESPACE}" >/dev/null 2>&1; then
    log "删除 namespace ${NAMESPACE}"
    kubectl delete namespace "${NAMESPACE}" --ignore-not-found --timeout=60s >/dev/null
    ok "namespace 已删除"
fi

echo
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Cleanup 完成${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo
