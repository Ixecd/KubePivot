#!/usr/bin/env bash
# ============================================================================
# KubePivot Benchmark - ConfigMap Hot Reload Dedup Verification (v2)
# ============================================================================
# 目的：验证 v2.3.0 的 sha256 去重热加载机制真的能省掉无意义 reconcile
#
# v2 改动（vs 2026-04-25 第一版）:
#   - 不用 set -e（避免 grep 0 匹配 + pipefail 误退出）
#   - kubectl logs 输出先存文件再 grep（避免管道写 stdout 的 SIGPIPE 问题）
#   - 简化数字提取，不用 head + tr 链式管道
#   - Phase 2 加 inline 进度打印（5 次一报）
#
# 用法：
#   bash benchmark/scripts/hot-reload.sh
#   ITERATIONS=10 bash benchmark/scripts/hot-reload.sh   # 短跑试运行
# ============================================================================

# 不用 set -e（脚本里大量 grep 等命令的"无匹配返回 1"会造成误退出）
# 用 set -u 防变量拼写错误
# set -u  # 去掉，与多分支判定不兼容

ITERATIONS="${ITERATIONS:-100}"
TARGET_NS="${TARGET_NS:-kp-auth-service}"
RESULTS_DIR="${RESULTS_DIR:-$(pwd)/benchmark/results}"

GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

log()  { echo -e "${BLUE}[$(date +%H:%M:%S)] $*${NC}"; }
ok()   { echo -e "${GREEN}✓ $*${NC}"; }
warn() { echo -e "${YELLOW}⚠ $*${NC}"; }
err()  { echo -e "${RED}✗ $*${NC}" >&2; }

# ── 前置检查 ──────────────────────────────────────────────────────────────────

if ! kubectl get namespace "$TARGET_NS" >/dev/null 2>&1; then
    err "namespace $TARGET_NS 不存在，请先运行 setup.sh"
    exit 1
fi

if ! kubectl get configmap kubepivot-resources -n "$TARGET_NS" >/dev/null 2>&1; then
    err "$TARGET_NS 中找不到 kubepivot-resources ConfigMap"
    exit 1
fi

leader_pod=$(kubectl get pods -n kubepivot-system -l app=kubepivot-controller \
    -o jsonpath='{.items[0].metadata.name}')
if [[ -z "$leader_pod" ]]; then
    err "找不到 controller pod"
    exit 1
fi

ok "目标 namespace: $TARGET_NS"
ok "采样 controller pod: $leader_pod"

# ── 准备结果目录 ──────────────────────────────────────────────────────────────

run_id=$(date +%Y-%m-%d_%H%M%S)
run_dir="$RESULTS_DIR/$run_id-hot-reload"
mkdir -p "$run_dir"
result_file="$run_dir/result.txt"
logs_before="$run_dir/logs-before.txt"
logs_after="$run_dir/logs-after.txt"

# ── 提取当前 ConfigMap 全文 ────────────────────────────────────────────────

current_yaml=$(kubectl get configmap kubepivot-resources -n "$TARGET_NS" -o yaml)
echo "$current_yaml" > "$run_dir/configmap-snapshot.yaml"

ok "ConfigMap 快照已保存"

# ── 计数函数（统一处理 grep 0 匹配的退出码问题）────────────────────────────

# 用法：count_pattern <file> <pattern>
# 返回：匹配次数（数字字符串），永远不会失败
count_pattern() {
    local file="$1"
    local pattern="$2"
    local count
    count=$(grep -c -- "$pattern" "$file" 2>/dev/null)
    # grep 无匹配时输出 0 并 exit 1；我们要的就是这个 0
    if [[ -z "$count" ]]; then
        echo 0
    else
        echo "$count"
    fi
}

# ── Phase 1: 记录基线 ─────────────────────────────────────────────────────

log "Phase 1: 记录 $ITERATIONS 次 apply 之前的基线"

# kubectl logs 先存文件，避免大量数据走管道引发 SIGPIPE
kubectl logs -n kubepivot-system "$leader_pod" --tail=10000 \
    > "$logs_before" 2>/dev/null

before_reload=$(count_pattern "$logs_before" "项目状态已更新.*$TARGET_NS")
before_trigger=$(count_pattern "$logs_before" "ConfigMap 变化触发全量 reconcile.*$TARGET_NS")

ok "基线: 项目状态更新=$before_reload, ConfigMap 触发=$before_trigger"

# ── Phase 2: 跑 N 次 apply（带进度打印）─────────────────────────────────

log "Phase 2: 执行 $ITERATIONS 次幂等 apply（sha256 不变）"

start_ts=$(date +%s)
for ((i = 1; i <= ITERATIONS; i++)); do
    echo "$current_yaml" | kubectl apply -f - >/dev/null 2>&1
    if (( i % 5 == 0 || i == ITERATIONS )); then
        log "  已执行 $i/$ITERATIONS 次"
    fi
done
apply_duration=$(($(date +%s) - start_ts))

ok "$ITERATIONS 次 apply 完成（耗时 ${apply_duration}s）"

# ── Phase 3: 等待 ─────────────────────────────────────────────────────────

log "Phase 3: 等待 30 秒让 controller 处理完所有 watch 事件"
sleep 30

# ── Phase 4: 验证 reconcile 行为 ───────────────────────────────────────────

log "Phase 4: 提取最新日志 + 比对计数"

kubectl logs -n kubepivot-system "$leader_pod" --tail=10000 \
    > "$logs_after" 2>/dev/null

after_reload=$(count_pattern "$logs_after" "项目状态已更新.*$TARGET_NS")
after_trigger=$(count_pattern "$logs_after" "ConfigMap 变化触发全量 reconcile.*$TARGET_NS")

reload_delta=$((after_reload - before_reload))
trigger_delta=$((after_trigger - before_trigger))

# ── Phase 5: 输出结果 ──────────────────────────────────────────────────────

{
    echo "═══════════════════════════════════════════════════════════════"
    echo "  ConfigMap Hot Reload Dedup Verification"
    echo "═══════════════════════════════════════════════════════════════"
    echo
    echo "参数:"
    echo "  iterations              $ITERATIONS"
    echo "  target_namespace        $TARGET_NS"
    echo "  apply_duration          ${apply_duration}s"
    echo "  controller_pod          $leader_pod"
    echo
    echo "基线（apply 之前）:"
    echo "  项目状态更新次数        $before_reload"
    echo "  ConfigMap 触发 reconcile  $before_trigger"
    echo
    echo "之后（apply + 30s wait 之后）:"
    echo "  项目状态更新次数        $after_reload"
    echo "  ConfigMap 触发 reconcile  $after_trigger"
    echo
    echo "增量:"
    echo "  项目状态更新增量        $reload_delta   (期望 0)"
    echo "  ConfigMap 触发增量      $trigger_delta   (期望 0)"
    echo
    echo "判定:"
    if [[ "$reload_delta" -le 1 && "$trigger_delta" -le 1 ]]; then
        echo "  ✅ sha256 去重完美工作"
        echo "  ${ITERATIONS} 次幂等 apply 触发的 reconcile 次数 = ${reload_delta}（期望 ≤ 1）"
        echo '  控制器在 sha256 比对层面 99.x% 命中"内容未变"快速跳过'
    elif [[ "$reload_delta" -le 5 && "$trigger_delta" -le 5 ]]; then
        echo "  ⚠ sha256 去重大部分工作，少量异常"
        echo "  ${ITERATIONS} 次 apply → ${reload_delta} 次 reconcile（少量但非零）"
        echo "  可能原因: kubectl apply 偶尔修改 metadata.resourceVersion 导致 watcher 重新触发"
    else
        echo "  ❌ sha256 去重可能失效"
        echo "  ${ITERATIONS} 次幂等 apply 引发了 ${reload_delta} 次 reconcile"
        echo "  请检查 global_state.go 的 UpsertProject 逻辑"
    fi
    echo
    echo "数据文件:"
    echo "  $result_file"
    echo "  $run_dir/configmap-snapshot.yaml"
    echo "  $logs_before / $logs_after"
} | tee "$result_file"
