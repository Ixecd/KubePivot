#!/usr/bin/env bash
# ============================================================================
# KubePivot Benchmark - Watcher Reconnect Robustness
# ============================================================================
# 目的：验证 kubectl --watch 子进程异常退出后，watcher 心跳守卫 + 退避重连机制
#       能否兜住事件不丢失。
#
# 测试方法（不真断网，更可靠）：
#   1. 找到 controller leader pod
#   2. docker exec 进容器，杀掉所有 kubectl --watch 子进程
#   3. 立即在某个 managed namespace 删除一个 Deployment（事件应该被丢失）
#   4. 等 30-60 秒，观察:
#      - 心跳守卫是否触发
#      - watcher 是否重连
#      - 删除事件是否被捕获（业务 Pod 应该被 healing 重建）
#
# 用法：
#   bash benchmark/scripts/watch-reconnect.sh
# ============================================================================

set -euo pipefail

TARGET_NS="${TARGET_NS:-kp-auth-service}"
RECOVERY_TIMEOUT="${RECOVERY_TIMEOUT:-90}"
RESULTS_DIR="${RESULTS_DIR:-$(pwd)/benchmark/results}"

GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; RED='\033[0;31m'; NC='\033[0m'
log()  { echo -e "${BLUE}[$(date +%H:%M:%S)] $*${NC}"; }
ok()   { echo -e "${GREEN}✓ $*${NC}"; }
warn() { echo -e "${YELLOW}⚠ $*${NC}"; }
err()  { echo -e "${RED}✗ $*${NC}" >&2; }

# ── 前置检查 ──────────────────────────────────────────────────────────────────

if ! kubectl get deployment "$TARGET_NS" -n "$TARGET_NS" >/dev/null 2>&1; then
    err "$TARGET_NS Deployment 不存在，请先运行 setup.sh"
    exit 1
fi

# ── 找当前 leader pod（用 kubectl 看 lease）──────────────────────────────────

log "查找 leader pod"

leader_identity=$(kubectl get lease -n kubepivot-system kubepivot-controller-leader \
    -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || echo "")

if [[ -z "$leader_identity" ]]; then
    warn "K8s Lease 不可用，可能用 etcd 或单机模式 - 选第一个 pod 测"
    leader_pod=$(kubectl get pods -n kubepivot-system -l app=kubepivot-controller \
        -o jsonpath='{.items[0].metadata.name}')
else
    # holder identity 形如 kubepivot-controller-79967bf987-7qrbx-bca5296c
    # pod name 是前面的 kubepivot-controller-79967bf987-7qrbx
    leader_pod=$(echo "$leader_identity" | sed 's/-[0-9a-f]\{8\}$//')
fi

if [[ -z "$leader_pod" ]]; then
    err "找不到 leader pod"
    exit 1
fi

ok "当前 Leader pod: $leader_pod"

# ── 准备结果目录 ──────────────────────────────────────────────────────────────

run_id=$(date +%Y-%m-%d_%H%M%S)
run_dir="$RESULTS_DIR/$run_id-watch-reconnect"
mkdir -p "$run_dir"
result_file="$run_dir/result.txt"

# ── Phase 1: 找到 controller 容器 ID ──────────────────────────────────────

container_id=$(docker ps --filter "name=k8s_controller_${leader_pod}_" --format '{{.ID}}' | head -1)
if [[ -z "$container_id" ]]; then
    err "找不到 leader pod 的 docker 容器"
    exit 1
fi
ok "Leader 容器 ID: $container_id"

# 看下杀之前有几个 kubectl 子进程
kubectl_count_before=$(docker top "$container_id" 2>/dev/null | grep -c kubectl || true)
kubectl_count_before="${kubectl_count_before:-0}"
ok "kubectl 子进程数（杀之前）: $kubectl_count_before"

# ── Phase 2: 杀掉所有 kubectl 子进程 ─────────────────────────────────────

log "杀掉 leader 容器内所有 kubectl 子进程（模拟 watcher 异常）"

# scratch 镜像没 killall，用 ps + kill 自己实现
# 但 scratch 也没 ps - 直接用 docker top 拿 pid，docker exec kill
pids=$(docker top "$container_id" -o pid,comm 2>/dev/null | grep kubectl | awk '{print $1}' || true)

if [[ -z "$pids" ]]; then
    err "没找到 kubectl 子进程"
    exit 1
fi

log "找到 kubectl 子进程 PIDs: $(echo $pids | tr '\n' ' ')"

# Note: scratch 容器里没 kill 命令，所以从宿主侧 kill
# orbstack 容器进程在宿主能直接看到
killed=0
for pid in $pids; do
    if kill -9 "$pid" 2>/dev/null; then
        killed=$((killed + 1))
    fi
done
ok "杀掉 $killed 个 kubectl 子进程"

# ── Phase 3: 立即触发一个删除事件（应该被丢失）─────────────────────────────

log "立即删除 $TARGET_NS Deployment（触发应该被丢失的事件）"
delete_ts=$(date +%s)
kubectl delete deployment "$TARGET_NS" -n "$TARGET_NS" --wait=false >/dev/null 2>&1
ok "删除请求已发出"

# ── Phase 4: 监控恢复 ────────────────────────────────────────────────────────

log "监控 watcher 恢复 + 自愈（最多 ${RECOVERY_TIMEOUT}s）"

recovery_ts=""
poll_deadline=$(($(date +%s) + RECOVERY_TIMEOUT))

while [[ $(date +%s) -lt $poll_deadline ]]; do
    # 看 controller 日志里是否有"watcher 重连"或"心跳守卫"日志
    # 或者最直接：看 deployment 是否被重建
    if kubectl get deployment "$TARGET_NS" -n "$TARGET_NS" >/dev/null 2>&1; then
        recovery_ts=$(date +%s)
        break
    fi
    sleep 1
done

# ── Phase 5: 收集证据 ────────────────────────────────────────────────────────

# 杀完之后到现在的子进程数
kubectl_count_after=$(docker top "$container_id" 2>/dev/null | grep -c kubectl || true)
kubectl_count_after="${kubectl_count_after:-0}"

# leader 日志里搜重连/心跳关键字
log "提取 leader 日志（最近 100 行）"
recovery_log=$(kubectl logs -n kubepivot-system "$leader_pod" --tail=100 2>/dev/null \
    | grep -E "Watcher|心跳|重连|reconnect|资源缺失|启动自愈" || true)
echo "$recovery_log" > "$run_dir/leader-log.txt"

# ── 输出结果 ──────────────────────────────────────────────────────────────────

cat > "$result_file" << EOF
═══════════════════════════════════════════════════════════════
  Watcher Reconnect Robustness Verification
═══════════════════════════════════════════════════════════════

测试方法:
  Phase 1  找 leader pod          $leader_pod
  Phase 2  杀 kubectl 子进程       $killed 个
  Phase 3  立即删 Deployment      $TARGET_NS
  Phase 4  监控自愈               最多 ${RECOVERY_TIMEOUT}s

子进程数:
  之前    $kubectl_count_before
  之后    $kubectl_count_after  (重连后应该恢复到接近原值)

恢复时间:
EOF

if [[ -n "$recovery_ts" ]]; then
    elapsed=$((recovery_ts - delete_ts))
    cat >> "$result_file" << EOF
  ✅ Deployment 在 ${elapsed}s 内被自愈重建
  心跳守卫 + 退避重连机制工作正常
EOF
else
    cat >> "$result_file" << EOF
  ❌ ${RECOVERY_TIMEOUT}s 内 Deployment 未被重建
  心跳守卫或重连机制可能有问题
  （也可能是 healing 失败，需查日志）
EOF
fi

cat >> "$result_file" << EOF

Leader 日志（最近 100 行的相关条目）:
$(cat "$run_dir/leader-log.txt" | head -30)

完整数据:
  $result_file
  $run_dir/leader-log.txt
EOF

cat "$result_file"
