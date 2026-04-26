#!/usr/bin/env bash
# ============================================================================
# KubePivot Benchmark - Cleanup Script (v2.5.1)
# ============================================================================
# 目的：清理 setup.sh 创建的所有 mock 项目和资源
#
# v2.5.1 改造（基于 2026-04-26 实测踩坑）：
#   1. 大规模时（≥30 个）自动停 controller，避免 "reconcile vs cleanup" 死锁螺旋
#   2. namespace 删除超时后自动 fallback 到 finalize API 强删
#   3. 进度打印（每 5 秒一报剩余数量）
#   4. 完成后恢复 controller 副本数到原值
#   5. 仅 set -o pipefail（避免 set -e/-u 踩坑，详见 HANDOFF 3.7）
#
# 用法：
#   bash benchmark/scripts/cleanup.sh
#     清理默认 10 个真实命名项目
#
#   PROJECT_COUNT=50 bash benchmark/scripts/cleanup.sh
#     清理 50 个 kp-bench-NNN（>=30 自动停 controller）
#
#   bash benchmark/scripts/cleanup.sh --all
#     同时卸载 controller 和 workspace
#
#   bash benchmark/scripts/cleanup.sh --skip-controller-pause
#     即使大规模也不停 controller（不推荐）
#
#   bash benchmark/scripts/cleanup.sh --force-finalize
#     直接走 finalize API 强删（紧急情况）
# ============================================================================

set -o pipefail   # 仅这一项，参考 HANDOFF.md 3.7

# ── 默认参数 ─────────────────────────────────────────────────────────────────

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

# v2.5.0：PROJECT_COUNT > 10 时改用 kp-bench-NNN 命名
if [[ -n "${PROJECT_COUNT:-}" ]] && [[ "$PROJECT_COUNT" -gt 10 ]]; then
    PROJECTS=()
    for i in $(seq 1 "$PROJECT_COUNT"); do
        PROJECTS+=("$(printf "kp-bench-%03d" "$i")")
    done
fi

WORKSPACE="${BENCHMARK_WORKSPACE:-$HOME/kp-benchmark}"
CLEAN_ALL="${CLEAN_ALL:-false}"
SKIP_CONTROLLER_PAUSE="false"
FORCE_FINALIZE="false"

# 启发式：≥30 项目自动停 controller
LARGE_THRESHOLD="${LARGE_THRESHOLD:-30}"

# ── 参数解析 ─────────────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
    case "$1" in
        --all) CLEAN_ALL="true"; shift ;;
        --skip-controller-pause) SKIP_CONTROLLER_PAUSE="true"; shift ;;
        --force-finalize) FORCE_FINALIZE="true"; shift ;;
        -h|--help)
            grep '^#' "$0" | head -30 | sed 's/^# \?//'
            exit 0 ;;
        *) echo "未知参数: $1" >&2; exit 1 ;;
    esac
done

# ── 颜色 + 日志 ─────────────────────────────────────────────────────────────

GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; RED='\033[0;31m'; NC='\033[0m'
log()  { echo -e "${BLUE}[$(date +%H:%M:%S)] $*${NC}"; }
ok()   { echo -e "${GREEN}✓ $*${NC}"; }
warn() { echo -e "${YELLOW}⚠ $*${NC}"; }
err()  { echo -e "${RED}✗ $*${NC}" >&2; }

# ── 工具函数 ────────────────────────────────────────────────────────────────

# 当前还有多少个 mock ns 存在
count_remaining() {
    local count=0
    for project in "${PROJECTS[@]}"; do
        if kubectl get namespace "$project" >/dev/null 2>&1; then
            count=$((count + 1))
        fi
    done
    echo "$count"
}

# 用 finalize API 强删卡 Terminating 的 namespace
finalize_namespace() {
    local ns="$1"
    kubectl get ns "$ns" -o json 2>/dev/null | \
        jq '.metadata.finalizers = [] | .spec.finalizers = []' 2>/dev/null | \
        kubectl replace --raw "/api/v1/namespaces/${ns}/finalize" -f - >/dev/null 2>&1
}

# ── Phase 1: 大规模时停 controller ──────────────────────────────────────────

original_replicas=""
total="${#PROJECTS[@]}"

# 决策：是否需要停 controller
need_pause="false"
if [[ "$total" -ge "$LARGE_THRESHOLD" ]] && [[ "$SKIP_CONTROLLER_PAUSE" != "true" ]]; then
    need_pause="true"
fi

if [[ "$need_pause" == "true" ]]; then
    log "项目数 ${total} >= ${LARGE_THRESHOLD}，先停 controller 防 reconcile 死锁"

    original_replicas=$(kubectl get deployment kubepivot-controller \
        -n kubepivot-system -o jsonpath='{.spec.replicas}' 2>/dev/null)

    if [[ -z "$original_replicas" ]]; then
        warn "找不到 kubepivot-controller deployment，跳过暂停"
        need_pause="false"
    else
        kubectl scale deployment kubepivot-controller \
            -n kubepivot-system --replicas=0 >/dev/null

        # 等所有 controller pod 退出
        local_deadline=$((SECONDS + 30))
        while [[ $SECONDS -lt $local_deadline ]]; do
            running=$(kubectl get pods -n kubepivot-system \
                -l app=kubepivot-controller --no-headers 2>/dev/null | wc -l | tr -d ' ')
            if [[ "$running" == "0" ]]; then
                break
            fi
            sleep 2
        done
        ok "Controller 已停（原副本数 ${original_replicas}）"
    fi
fi

# ── Phase 2: 触发 namespace 删除 ────────────────────────────────────────────

log "开始删除 $total 个 mock namespace"

deleted=0
for project in "${PROJECTS[@]}"; do
    if kubectl get namespace "$project" >/dev/null 2>&1; then
        kubectl delete namespace "$project" --ignore-not-found --wait=false \
            >/dev/null 2>&1 &
        deleted=$((deleted + 1))
    fi
done
wait

# ── Phase 3: 等待删除完成 + 进度打印 ────────────────────────────────────────

if [[ "$FORCE_FINALIZE" == "true" ]]; then
    log "--force-finalize 模式：跳过等待，直接走 finalize API"
else
    log "等待 namespace 完全删除（最多 90 秒）"

    deadline=$((SECONDS + 90))
    last_print=0
    while [[ $SECONDS -lt $deadline ]]; do
        remaining=$(count_remaining)

        if [[ "$remaining" == "0" ]]; then
            ok "全部 ${total} 个 namespace 已删除"
            break
        fi

        # 每 5 秒报一次进度
        if (( SECONDS - last_print >= 5 )); then
            done_count=$((total - remaining))
            log "进度: ${done_count} / ${total} 已删除（剩余 ${remaining}）"
            last_print=$SECONDS
        fi

        sleep 2
    done
fi

# ── Phase 4: Finalize API fallback（如果还有卡住的）─────────────────────────

remaining=$(count_remaining)
if [[ "$remaining" != "0" ]]; then
    warn "${remaining} 个 namespace 卡 Terminating，启动 finalize API 强删"

    finalized=0
    for project in "${PROJECTS[@]}"; do
        if kubectl get namespace "$project" >/dev/null 2>&1; then
            if finalize_namespace "$project"; then
                finalized=$((finalized + 1))
            fi
        fi
    done

    log "已对 ${finalized} 个 namespace 调用 finalize API"
    sleep 5

    final_remaining=$(count_remaining)
    if [[ "$final_remaining" == "0" ]]; then
        ok "Finalize API 强删完成"
    else
        err "仍有 ${final_remaining} 个 namespace 未清理，需手动检查"
        kubectl get ns | grep "kp-bench\|kp-auth\|kp-gateway\|kp-user\|kp-order\|kp-payment\|kp-notification\|kp-search\|kp-analytics\|kp-media\|kp-admin"
    fi
fi

# ── Phase 5: 恢复 controller ────────────────────────────────────────────────

if [[ "$need_pause" == "true" ]] && [[ -n "$original_replicas" ]]; then
    log "恢复 controller 副本数到 ${original_replicas}"
    kubectl scale deployment kubepivot-controller \
        -n kubepivot-system --replicas="$original_replicas" >/dev/null

    kubectl rollout status -n kubepivot-system \
        deployment/kubepivot-controller --timeout=60s >/dev/null

    ok "Controller 已恢复 ${original_replicas} 副本"
fi

# ── Phase 6: --all 模式（连同 controller 和 workspace）──────────────────────

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

# ── Phase 7: 总结 ──────────────────────────────────────────────────────────

cleaned=$((total - $(count_remaining)))

echo
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  清理完成${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo
echo "  目标项目数:  $total"
echo "  实际清理:    $cleaned"
if [[ "$need_pause" == "true" ]]; then
    echo "  Controller:  已恢复 $original_replicas 副本"
fi
if [[ "$CLEAN_ALL" != "true" ]]; then
    echo
    echo "  Controller 仍在运行（kubepivot-system namespace）"
    echo "  benchmark/results/ 数据保留（git ignored）"
    echo "  下次跑 setup.sh 重新生成 mock 项目即可"
fi
echo
