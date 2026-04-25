#!/usr/bin/env bash
# ============================================================================
# KubePivot Benchmark - Concurrent Chaos (Self-Healing Latency)
# ============================================================================
# 目的：测量 controller 自愈延迟，覆盖三种并发度
#
# 测试矩阵：
#   1 个删除   baseline，单 task 走完整 reconcile + healRollback 链路
#   3 个并发   worker pool 部分占用
#   10 个并发  worker pool 满载（默认 size=20，10 个不会饱和但够测）
#
# 每档跑 N 轮，记录:
#   T0  kubectl delete 完成时间
#   T1  pod 重新出现时间
#   T2  pod Running 时间
#
# 输出 p50 / p95 / max
#
# 用法：
#   bash benchmark/scripts/concurrent-chaos.sh                    # 默认 5 轮
#   bash benchmark/scripts/concurrent-chaos.sh -r 10              # 10 轮
# ============================================================================

set -euo pipefail

ROUNDS="${ROUNDS:-5}"
COOLDOWN_SEC="${COOLDOWN_SEC:-30}"
RESULTS_DIR="${RESULTS_DIR:-$(pwd)/benchmark/results}"

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

while [[ $# -gt 0 ]]; do
    case "$1" in
        -r|--rounds) ROUNDS="$2"; shift 2 ;;
        -c|--cooldown) COOLDOWN_SEC="$2"; shift 2 ;;
        -h|--help)
            grep '^#' "$0" | head -20 | sed 's/^# \?//'
            exit 0 ;;
        *) echo "未知参数: $1" >&2; exit 1 ;;
    esac
done

GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; RED='\033[0;31m'; NC='\033[0m'
log()  { echo -e "${BLUE}[$(date +%H:%M:%S)] $*${NC}"; }
ok()   { echo -e "${GREEN}✓ $*${NC}"; }
warn() { echo -e "${YELLOW}⚠ $*${NC}"; }
err()  { echo -e "${RED}✗ $*${NC}" >&2; }

# ── 前置检查 ──────────────────────────────────────────────────────────────────

# 全部 mock 项目都得在
for p in "${PROJECTS[@]}"; do
    if ! kubectl get deployment "$p" -n "$p" >/dev/null 2>&1; then
        err "$p Deployment 不在，请先运行 setup.sh"
        exit 1
    fi
done

run_id=$(date +%Y-%m-%d_%H%M%S)
run_dir="$RESULTS_DIR/$run_id-concurrent-chaos"
mkdir -p "$run_dir"

csv_file="$run_dir/healing-latency.csv"
echo "concurrency,round,project,T0_delete_ts,T1_appear_ms,T2_running_ms" > "$csv_file"

ok "结果目录: $run_dir"

# ── 单轮测试函数 ──────────────────────────────────────────────────────────────

# 参数：concurrency_level round_idx
# 删除指定数量的 Deployment（前 N 个），并行测量恢复延迟
run_round() {
    local conc="$1"
    local round="$2"

    log "[conc=$conc round=$round/$ROUNDS] 开始"

    # 选要删的项目（前 N 个）
    local targets=("${PROJECTS[@]:0:$conc}")

    # 记录每个项目的删除起始时间（毫秒）
    declare -A T0_ms

    # 并发删除（用 background）
    for proj in "${targets[@]}"; do
        T0_ms[$proj]=$(($(date +%s%N) / 1000000))
        kubectl delete deployment "$proj" -n "$proj" --wait=false >/dev/null 2>&1 &
    done
    wait

    # 等所有目标都重新出现 → Running
    for proj in "${targets[@]}"; do
        local t0=${T0_ms[$proj]}
        local t1_appear=""
        local t2_running=""

        # 等 deployment 重新出现（最多 60 秒）
        local poll_deadline=$(($(date +%s) + 60))
        while [[ $(date +%s) -lt $poll_deadline ]]; do
            if kubectl get deployment "$proj" -n "$proj" >/dev/null 2>&1; then
                t1_appear=$(($(date +%s%N) / 1000000 - t0))
                break
            fi
            sleep 0.5
        done

        if [[ -z "$t1_appear" ]]; then
            warn "[conc=$conc round=$round] $proj 60 秒内未重新出现"
            echo "$conc,$round,$proj,$t0,-1,-1" >> "$csv_file"
            continue
        fi

        # 等 Running（最多再 60 秒）
        poll_deadline=$(($(date +%s) + 60))
        while [[ $(date +%s) -lt $poll_deadline ]]; do
            local ready=$(kubectl get deployment "$proj" -n "$proj" \
                -o jsonpath='{.status.readyReplicas}' 2>/dev/null)
            if [[ "$ready" == "1" ]]; then
                t2_running=$(($(date +%s%N) / 1000000 - t0))
                break
            fi
            sleep 0.5
        done

        if [[ -z "$t2_running" ]]; then
            warn "[conc=$conc round=$round] $proj 60 秒内未 Running"
            t2_running="-1"
        fi

        echo "$conc,$round,$proj,$t0,$t1_appear,$t2_running" >> "$csv_file"
    done

    ok "[conc=$conc round=$round] 完成"
}

# ── 主循环：3 档 × N 轮 ────────────────────────────────────────────────────

for conc in 1 3 10; do
    log "═══ 测试档位：concurrency=$conc ═══"
    for round in $(seq 1 $ROUNDS); do
        run_round "$conc" "$round"
        if [[ $round -lt $ROUNDS ]] || [[ $conc -lt 10 ]]; then
            log "冷却 ${COOLDOWN_SEC}s"
            sleep "$COOLDOWN_SEC"
        fi
    done
done

# ── 计算分位数（awk 实现）────────────────────────────────────────────────

log "计算 p50 / p95 / max"

stats_file="$run_dir/stats.txt"
cat > "$stats_file" << EOF
═══════════════════════════════════════════════════════════════
  Self-Healing Latency Benchmark
  rounds: $ROUNDS, cooldown: ${COOLDOWN_SEC}s, projects: ${#PROJECTS[@]}
═══════════════════════════════════════════════════════════════

EOF

# 对每个 concurrency level 算分位数
for conc in 1 3 10; do
    awk -F, -v c="$conc" '
        NR > 1 && $1 == c && $5 > 0 && $6 > 0 {
            t1[NR] = $5
            t2[NR] = $6
            n++
        }
        END {
            if (n == 0) {
                printf "concurrency=%d  无有效数据\n\n", c
                exit
            }

            # 把 t1 / t2 各自排序
            split("", t1_sorted); split("", t2_sorted)
            i = 1
            for (k in t1) { t1_sorted[i] = t1[k]; i++ }
            i = 1
            for (k in t2) { t2_sorted[i] = t2[k]; i++ }
            asort(t1_sorted); asort(t2_sorted)

            p50_t1 = t1_sorted[int(n * 0.5)]
            p95_t1 = t1_sorted[int(n * 0.95) > 0 ? int(n * 0.95) : 1]
            max_t1 = t1_sorted[n]

            p50_t2 = t2_sorted[int(n * 0.5)]
            p95_t2 = t2_sorted[int(n * 0.95) > 0 ? int(n * 0.95) : 1]
            max_t2 = t2_sorted[n]

            printf "concurrency=%d  样本=%d\n", c, n
            printf "  T1 (重新出现):  p50=%dms  p95=%dms  max=%dms\n", p50_t1, p95_t1, max_t1
            printf "  T2 (Running):   p50=%dms  p95=%dms  max=%dms\n", p50_t2, p95_t2, max_t2
            print ""
        }
    ' "$csv_file" >> "$stats_file"
done

cat "$stats_file"

echo
ok "原始数据: $csv_file"
ok "统计结果: $stats_file"
