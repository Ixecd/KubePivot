#!/usr/bin/env bash
# benchmark/chaos/scale-ns.sh — v3.4 规模化 namespace 创建/删除
#
# 测试: 10k namespace 下的 controller 资源消耗
# 目标: CPU<50%, mem<2GB/pod, drop=0
#
# 用法:
#   bash benchmark/chaos/scale-ns.sh --count=10000 --batch=100 --namespace kp-scale
#
# 注意: 这会创建真实 namespace，需在生产前清理

set -o pipefail

COUNT="${COUNT:-1000}"
BATCH="${BATCH:-50}"
PREFIX="${PREFIX:-kp-scale}"
NAMESPACE="${NAMESPACE:-kubepivot-system}"
METRICS_PORT="${METRICS_PORT:-9090}"
CONTROLLER_POD="${CONTROLLER_POD:-kubepivot-controller-0}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[$(date +%H:%M:%S)]${NC} $*"; }
warn() { echo -e "${YELLOW}[$(date +%H:%M:%S)]${NC} $*"; }
info() { echo -e "  $*"; }

# ── 解析参数 ──────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --count) COUNT="$2"; shift 2 ;;
        --batch) BATCH="$2"; shift 2 ;;
        --prefix) PREFIX="$2"; shift 2 ;;
        *) shift ;;
    esac
done

log "v3.4 scale test: ${COUNT} namespaces, batch=${BATCH}"
log "prefix=${PREFIX}"

# ── 1. 基线 CPU/Mem ───────────────────────────────────────────
log "记录基线资源..."
get_cpu() {
    kubectl top pod "${CONTROLLER_POD}" -n "${NAMESPACE}" -c controller --no-headers 2>/dev/null | awk '{print $2}' || echo "N/A"
}
get_mem() {
    kubectl top pod "${CONTROLLER_POD}" -n "${NAMESPACE}" -c controller --no-headers 2>/dev/null | awk '{print $3}' || echo "N/A"
}

BASELINE_CPU=$(get_cpu)
BASELINE_MEM=$(get_mem)
info "baseline CPU=${BASELINE_CPU}, MEM=${BASELINE_MEM}"

# ── 2. 创建 namespace ─────────────────────────────────────────
log "创建 ${COUNT} 个 namespace (batch=${BATCH})..."
START_TS=$(date +%s)

CREATED=0
FAILED=0
for ((i=1; i<=COUNT; i++)); do
    NS="${PREFIX}-$(printf "%05d" ${i})"
    if kubectl create ns "${NS}" --dry-run=client -o yaml 2>/dev/null | kubectl apply -f - 2>/dev/null >/dev/null; then
        CREATED=$((CREATED + 1))
    else
        FAILED=$((FAILED + 1))
    fi

    # 每 batch 报告进度
    if (( i % BATCH == 0 )); then
        ELAPSED=$(($(date +%s) - START_TS))
        EPS=$(echo "scale=1; ${i} / ${ELAPSED}" | bc 2>/dev/null || echo "N/A")
        CPU=$(get_cpu)
        MEM=$(get_mem)
        info "progress=${i}/${COUNT} eps=${EPS} cpu=${CPU} mem=${MEM}"
    fi
done

# ── 3. 峰值资源 ───────────────────────────────────────────────
sleep 10  # 等 controller reconcile 稳定
ELAPSED_TOTAL=$(($(date +%s) - START_TS))
PEAK_CPU=$(get_cpu)
PEAK_MEM=$(get_mem)
info "peak CPU=${PEAK_CPU}, MEM=${PEAK_MEM}"
info "total: ${ELAPSED_TOTAL}s, eps=$(echo "scale=1; ${CREATED} / ${ELAPSED_TOTAL}" | bc)"

# ── 4. 拉取 metrics ──────────────────────────────────────────
log "拉取 /metrics..."
METRICS_URL="http://127.0.0.1:${METRICS_PORT}/metrics"
# port-forward in background
kubectl port-forward -n "${NAMESPACE}" "${CONTROLLER_POD}" ${METRICS_PORT}:${METRICS_PORT} &
PF_PID=$!
sleep 2

DROPPED=$(curl -s --max-time 5 "${METRICS_URL}" 2>/dev/null | grep -E 'kubepivot.*drop|EventsDropped' | head -3 || echo "N/A")
kill ${PF_PID} 2>/dev/null

# ── 5. 清理 ──────────────────────────────────────────────────
log "清理 ${COUNT} 个 namespace..."
DELETED=0
for ((i=1; i<=COUNT; i++)); do
    NS="${PREFIX}-$(printf "%05d" ${i})"
    kubectl delete ns "${NS}" --ignore-not-found --timeout=5s 2>/dev/null >/dev/null && DELETED=$((DELETED + 1))
    if (( i % BATCH == 0 )); then
        info "deleted=${i}/${COUNT}"
    fi
done

log ""
log "━━━ 结果 ━━━"
echo "  created:  ${CREATED}/${COUNT} (failed: ${FAILED})"
echo "  deleted:  ${DELETED}"
echo "  duration: ${ELAPSED_TOTAL}s"
echo "  baseline: CPU=${BASELINE_CPU} MEM=${BASELINE_MEM}"
echo "  peak:     CPU=${PEAK_CPU} MEM=${PEAK_MEM}"
echo "  drops:    ${DROPPED}"

if [[ ${FAILED} -eq 0 ]]; then
    log "✅ 零失败"
fi
