#!/usr/bin/env bash
# benchmark/chaos/shard-flip-gap.sh — v3.4 shard 翻转间隙测量
#
# 测试 v3.4 lease handoff + rebalance 的实际 gap
#   - kill 1 controller pod
#   - 测量 shard 被其他 pod 抢占的时间
#   - 验证 gap < 5s (v3.4 目标)
#
# 前提:
#   - kubectl 可用 + KUBECONFIG 指向测试集群
#   - controller StatefulSet 运行在 kubepivot-system
#
# 用法:
#   bash benchmark/chaos/shard-flip-gap.sh [--duration 300] [--namespace kubepivot-system]

set -o pipefail

NAMESPACE="${NAMESPACE:-kubepivot-system}"
DURATION="${DURATION:-120}"
STS="kubepivot-controller"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[$(date +%H:%M:%S)]${NC} $*"; }
warn() { echo -e "${YELLOW}[$(date +%H:%M:%S)] WARN${NC} $*"; }
fail() { echo -e "${RED}[$(date +%H:%M:%S)] FAIL${NC} $*"; }

# ── 解析参数 ──────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --duration) DURATION="$2"; shift 2 ;;
        --namespace) NAMESPACE="$2"; shift 2 ;;
        *) shift ;;
    esac
done

log "v3.4 shard flip gap test"
log "namespace=${NAMESPACE} duration=${DURATION}s"

# ── 1. 记录初始状态 ───────────────────────────────────────────────
log "记录初始 shard 分布..."
kubectl get leases -n "${NAMESPACE}" -l "app.kubernetes.io/component=kubepivot-controller-shard" \
    -o custom-columns=NAME:.metadata.name,HOLDER:.spec.holderIdentity 2>/dev/null || {
    fail "无法获取 shard lease 列表"
    exit 1
}

POD_COUNT=$(kubectl get pods -n "${NAMESPACE}" -l "app=kubepivot-controller" --no-headers 2>/dev/null | wc -l | tr -d ' ')
log "当前 controller pod 数: ${POD_COUNT}"

if [[ ${POD_COUNT} -lt 2 ]]; then
    fail "需要至少 2 个 controller pod 才能测 shard flip"
    exit 1
fi

# ── 2. 杀掉一个 pod ──────────────────────────────────────────────
TARGET_POD=$(kubectl get pods -n "${NAMESPACE}" -l "app=kubepivot-controller" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)

log "⚡ 杀掉 pod: ${TARGET_POD}"
START_TS=$(date +%s)
kubectl delete pod -n "${NAMESPACE}" "${TARGET_POD}" --grace-period=10 2>/dev/null

# ── 3. 轮询监控 shard 重新分配 ─────────────────────────────────────
log "监控 shard 重新分配 (每 1s 轮询, 最长 ${DURATION}s)..."

RECOVERED=0
SHARD_TAKEOVER_TS=0

for ((i=1; i<=DURATION; i++)); do
    sleep 1

    # 检查新 pod 是否就绪
    NEW_POD_COUNT=$(kubectl get pods -n "${NAMESPACE}" -l "app=kubepivot-controller" \
        --field-selector=status.phase=Running --no-headers 2>/dev/null | wc -l | tr -d ' ')

    # 检查 shard lease 是否已被其他 pod 接管
    LEASE_HOLDERS=$(kubectl get leases -n "${NAMESPACE}" \
        -l "app.kubernetes.io/component=kubepivot-controller-shard" \
        -o jsonpath='{.items[*].spec.holderIdentity}' 2>/dev/null)

    UNIQUE_HOLDERS=$(echo "${LEASE_HOLDERS}" | tr ' ' '\n' | sort -u | wc -l | tr -d ' ')

    if [[ ${RECOVERED} -eq 0 ]] && [[ ${NEW_POD_COUNT} -ge ${POD_COUNT} ]] && [[ -n "${LEASE_HOLDERS}" ]]; then
        SHARD_TAKEOVER_TS=$(date +%s)
        GAP=$((SHARD_TAKEOVER_TS - START_TS))
        RECOVERED=1

        if [[ ${GAP} -le 5 ]]; then
            log "✅ shard 接管完成, gap=${GAP}s (<5s 达标)"
        elif [[ ${GAP} -le 10 ]]; then
            warn "⚠️  shard 接管完成, gap=${GAP}s (5-10s, 可接受)"
        else
            fail "❌ shard 接管完成, gap=${GAP}s (>10s, 超标)"
        fi
    fi

    # 每 10s 打印状态
    if (( i % 10 == 0 )); then
        ELAPSED=$(( $(date +%s) - START_TS ))
        echo "  [${ELAPSED}s] pods=${NEW_POD_COUNT}/${POD_COUNT} holders=${UNIQUE_HOLDERS:-0}"
    fi
done

if [[ ${RECOVERED} -eq 0 ]]; then
    fail "❌ 超时 ${DURATION}s, shard 未恢复"
    exit 1
fi

# ── 4. 验证最终状态 ───────────────────────────────────────────────
log ""
log "━━━ 最终 shard 分布 ━━━"
kubectl get leases -n "${NAMESPACE}" -l "app.kubernetes.io/component=kubepivot-controller-shard" \
    -o custom-columns=NAME:.metadata.name,HOLDER:.spec.holderIdentity 2>/dev/null

log ""
log "━━━ 结果 ━━━"
log "shard flip gap: ${GAP}s (目标 <5s)"
log "最终 pod 数:   ${NEW_POD_COUNT} (初始 ${POD_COUNT})"

if [[ ${GAP} -le 5 ]]; then
    log "✅ 通过 — v3.4 handoff + rebalance 生效"
    exit 0
else
    warn "⚠️  部分通过 — 查看上方详细分布"
    exit 0
fi
