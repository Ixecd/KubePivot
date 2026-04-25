#!/usr/bin/env bash
# ============================================================================
# KubePivot Benchmark - Cleanup Script
# ============================================================================
# 目的：清理 setup.sh 创建的所有 mock 项目和资源
#
# 用法：
#   bash benchmark/scripts/cleanup.sh           # 清理 mock 项目（保留 controller + workspace）
#   bash benchmark/scripts/cleanup.sh --all     # 同时清理 controller 和 workspace
# ============================================================================

set -euo pipefail

PROJECTS=(
    "kp-auth-service"
    "kp-gateway"
    "kp-user-profile"
    "kp-order-api"
    "kp-payment-worker"
    "kp-notification"
    "kp-search-engine"
    "kp-analytics"
    "kp-media-processor"
    "kp-admin-dashboard"
)

# v2.5.0：支持自定义项目数（压测大规模分片用）
# 不传 PROJECT_COUNT → 用上面 10 个真实命名（兼容旧用法）
# PROJECT_COUNT=50  → 改用 kp-bench-001 ... kp-bench-050
if [[ -n "${PROJECT_COUNT:-}" ]] && [[ "$PROJECT_COUNT" -gt 10 ]]; then
    PROJECTS=()
    for i in $(seq 1 "$PROJECT_COUNT"); do
        PROJECTS+=("$(printf "kp-bench-%03d" "$i")")
    done
fi

WORKSPACE="${BENCHMARK_WORKSPACE:-$HOME/kp-benchmark}"
CLEAN_ALL="${CLEAN_ALL:-false}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --all) CLEAN_ALL="true"; shift ;;
        -h|--help)
            grep '^#' "$0" | head -10 | sed 's/^# \?//'
            exit 0 ;;
        *) echo "未知参数: $1" >&2; exit 1 ;;
    esac
done

GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log()  { echo -e "${BLUE}[$(date +%H:%M:%S)] $*${NC}"; }
ok()   { echo -e "${GREEN}✓ $*${NC}"; }
warn() { echo -e "${YELLOW}⚠ $*${NC}"; }

# ── 删除所有 mock namespace（连带资源）─────────────────────────────────────

log "开始清理 ${#PROJECTS[@]} 个 mock namespace"

deleted=0
for project in "${PROJECTS[@]}"; do
    if kubectl get namespace "$project" >/dev/null 2>&1; then
        kubectl delete namespace "$project" --ignore-not-found --timeout=60s >/dev/null 2>&1 &
        deleted=$((deleted + 1))
    fi
done

# 等所有删除并发完成
log "等待 namespace 完全删除（最多 90 秒）"
wait

# 验证清理完成
remaining=0
for project in "${PROJECTS[@]}"; do
    if kubectl get namespace "$project" >/dev/null 2>&1; then
        remaining=$((remaining + 1))
        warn "$project 仍未删除"
    fi
done

ok "删除完成: $((deleted - remaining))/${#PROJECTS[@]}"

# ── --all 模式：连同 controller 和 workspace ─────────────────────────────

if [[ "$CLEAN_ALL" == "true" ]]; then
    log "清理 kubepivot-system controller"
    kp controller uninstall --force 2>/dev/null || \
        kubectl delete namespace kubepivot-system --ignore-not-found --timeout=60s
    ok "controller 已卸载"

    if [[ -d "$WORKSPACE" ]]; then
        log "清理 workspace: $WORKSPACE"
        rm -rf "$WORKSPACE"
        ok "workspace 已删除"
    fi
fi

echo
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  清理完成${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo
if [[ "$CLEAN_ALL" != "true" ]]; then
    echo "  Controller 仍在运行（kubepivot-system namespace）"
    echo "  benchmark/results/ 数据保留（git ignored）"
    echo "  下次跑 setup.sh 重新生成 mock 项目即可"
    echo
fi
