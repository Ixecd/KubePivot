#!/usr/bin/env bash
# ============================================================================
# KubePivot Benchmark - ConfigMap Hot Reload Dedup Verification
# ============================================================================
# 目的：验证 v2.3.0 的 sha256 去重热加载机制真的能省掉无意义 reconcile
#
# 测试方法：
#   1. 记录 controller leader 当前 reconcile 次数（grep 日志）
#   2. 100 次 kubectl apply 同一份 ConfigMap（sha256 不变）
#   3. 等 30 秒让 controller 处理完所有 watch 事件
#   4. 再次记录 reconcile 次数
#   5. 验证：reconcile 次数增量 < 5（只有 8s 周期 tick 触发的，不是 ConfigMap 触发）
#
# 关键期望：
#   sha256 比对成功 → "📋 项目状态已更新" 日志最多出现 1 次（首次）
#   后续 99 次 apply 应该只在 watcher 看到事件后比对 sha256，发现一致直接跳过
#
# 用法：
#   bash benchmark/scripts/hot-reload.sh
# ============================================================================

set -euo pipefail

ITERATIONS="${ITERATIONS:-100}"
TARGET_NS="${TARGET_NS:-kp-auth-service}"
RESULTS_DIR="${RESULTS_DIR:-$(pwd)/benchmark/results}"

GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; RED='\033[0;31m'; NC='\033[0m'
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
    -o jsonpath='{.items[*].metadata.name}' | tr ' ' '\n' | head -1)
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
log_file="$run_dir/run.log"
result_file="$run_dir/result.txt"

# ── 提取当前 ConfigMap 全文（保留 sha256 annotation 和 data）────────────────

current_yaml=$(kubectl get configmap kubepivot-resources -n "$TARGET_NS" -o yaml)
echo "$current_yaml" > "$run_dir/configmap-snapshot.yaml"

ok "ConfigMap 快照已保存"

# ── Phase 1: 记录基线 reconcile 次数 ─────────────────────────────────────────

# 我们关注三种日志的出现次数：
# - "📋 项目状态已更新"      表示 sha256 不同 → 真的 reparse 了
# - "🔄 ConfigMap 变化触发全量 reconcile"  表示热加载真的触发
# - "🔁 Reconcile Loop"     周期性 tick（8s 一次，每分钟约 7-8 次）

log "Phase 1: 记录 100 次 apply 之前的基线 reconcile 行为"

before_log_count=$(kubectl logs -n kubepivot-system "$leader_pod" --tail=10000 2>/dev/null | wc -l | tr -d ' ')
before_reload=$(kubectl logs -n kubepivot-system "$leader_pod" --tail=10000 2>/dev/null \
    | grep -c "📋 项目状态已更新.*$TARGET_NS" || true)
before_trigger=$(kubectl logs -n kubepivot-system "$leader_pod" --tail=10000 2>/dev/null \
    | grep -c "🔄 ConfigMap 变化触发全量 reconcile.*$TARGET_NS" || true)

# 清洗（防 grep 多行返回）
before_reload=$(echo "$before_reload" | head -1 | tr -dc '0-9')
before_trigger=$(echo "$before_trigger" | head -1 | tr -dc '0-9')
before_reload="${before_reload:-0}"
before_trigger="${before_trigger:-0}"

ok "基线: 项目状态更新=$before_reload, ConfigMap 触发=$before_trigger"

# ── Phase 2: 跑 N 次 apply ────────────────────────────────────────────────

log "Phase 2: 执行 $ITERATIONS 次幂等 apply（sha256 不变）"

start_ts=$(date +%s)
for ((i = 1; i <= ITERATIONS; i++)); do
    echo "$current_yaml" | kubectl apply -f - >/dev/null 2>&1
    if (( i % 10 == 0 )); then
        log "  已执行 $i/$ITERATIONS 次"
    fi
done
apply_duration=$(($(date +%s) - start_ts))

ok "$ITERATIONS 次 apply 完成（耗时 ${apply_duration}s）"

# ── Phase 3: 等待 controller 处理完事件 ─────────────────────────────────────

log "Phase 3: 等待 30 秒让 controller 处理完所有 watch 事件"
sleep 30

# ── Phase 4: 验证 reconcile 行为 ────────────────────────────────────────────

after_reload=$(kubectl logs -n kubepivot-system "$leader_pod" --tail=20000 2>/dev/null \
    | grep -c "📋 项目状态已更新.*$TARGET_NS" || true)
after_trigger=$(kubectl logs -n kubepivot-system "$leader_pod" --tail=20000 2>/dev/null \
    | grep -c "🔄 ConfigMap 变化触发全量 reconcile.*$TARGET_NS" || true)

after_reload=$(echo "$after_reload" | head -1 | tr -dc '0-9')
after_trigger=$(echo "$after_trigger" | head -1 | tr -dc '0-9')
after_reload="${after_reload:-0}"
after_trigger="${after_trigger:-0}"

reload_delta=$((after_reload - before_reload))
trigger_delta=$((after_trigger - before_trigger))

# ── Phase 5: 输出结果 ──────────────────────────────────────────────────────

cat > "$result_file" << EOF
═══════════════════════════════════════════════════════════════
  ConfigMap Hot Reload Dedup Verification
═══════════════════════════════════════════════════════════════

参数:
  iterations              $ITERATIONS
  target_namespace        $TARGET_NS
  apply_duration          ${apply_duration}s

基线（apply 之前）:
  项目状态更新次数        $before_reload
  ConfigMap 触发 reconcile  $before_trigger

之后（apply + 30s wait 之后）:
  项目状态更新次数        $after_reload
  ConfigMap 触发 reconcile  $after_trigger

增量:
  项目状态更新增量        $reload_delta   (期望 0)
  ConfigMap 触发增量      $trigger_delta   (期望 0)

判定:
EOF

if [[ "$reload_delta" -le 1 && "$trigger_delta" -le 1 ]]; then
    cat >> "$result_file" << EOF
  ✅ sha256 去重完美工作
  $ITERATIONS 次幂等 apply 触发的 reconcile 次数 = $reload_delta（期望 ≤ 1）
  控制器在 sha256 比对层面 99.x% 命中"内容未变"快速跳过
EOF
elif [[ "$reload_delta" -le 5 && "$trigger_delta" -le 5 ]]; then
    cat >> "$result_file" << EOF
  ⚠ sha256 去重大部分工作，少量异常
  $ITERATIONS 次 apply → $reload_delta 次 reconcile（少量但非零）
  可能原因: kubectl apply 偶尔会修改 metadata.resourceVersion 导致 watcher 重新触发
EOF
else
    cat >> "$result_file" << EOF
  ❌ sha256 去重可能失效
  $ITERATIONS 次幂等 apply 引发了 $reload_delta 次 reconcile
  请检查 global_state.go 的 UpsertProject 逻辑
EOF
fi

cat >> "$result_file" << EOF

数据文件:
  $result_file
  $run_dir/configmap-snapshot.yaml
EOF

cat "$result_file"
