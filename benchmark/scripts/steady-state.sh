#!/usr/bin/env bash
# ============================================================================
# KubePivot v2.3.0 Benchmark - Steady State Sampling
# ============================================================================
# 目的：10 个 mock 项目稳定运行下，每 N 秒采样 controller 的资源占用，
#       持续 M 分钟，输出 CSV 便于后续画图分析。
#
# 采样指标：
#   - CPU 百分比（docker stats）
#   - 内存使用 MiB（docker stats）
#   - kubectl/helm 子进程数（docker top）
#
# 用法：
#   bash benchmark/scripts/steady-state.sh                # 默认 30 分钟，10s 间隔
#   bash benchmark/scripts/steady-state.sh -d 600 -i 5    # 10 分钟，5s 间隔
# ============================================================================

set -euo pipefail

DURATION_SEC="${DURATION_SEC:-1800}"
INTERVAL_SEC="${INTERVAL_SEC:-10}"
RESULTS_DIR="${RESULTS_DIR:-$(pwd)/benchmark/results}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        -d|--duration) DURATION_SEC="$2"; shift 2 ;;
        -i|--interval) INTERVAL_SEC="$2"; shift 2 ;;
        -o|--output)   RESULTS_DIR="$2"; shift 2 ;;
        -h|--help)
            grep '^#' "$0" | head -25 | sed 's/^# \?//'
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

command -v docker  >/dev/null || { err "docker 未安装"; exit 1; }
command -v kubectl >/dev/null || { err "kubectl 未安装"; exit 1; }

if ! kubectl get deployment -n kubepivot-system kubepivot-controller >/dev/null 2>&1; then
    err "kubepivot-controller 未运行"
    exit 1
fi

if ! docker ps --format "{{.Names}}" | grep -q "kubepivot-controller"; then
    err "docker 看不到 kubepivot-controller 容器 (orbstack 未启动?)"
    exit 1
fi

# ── 准备输出 ──────────────────────────────────────────────────────────────────

run_id=$(date +%Y-%m-%d_%H%M%S)
run_dir="$RESULTS_DIR/$run_id-steady-state"
mkdir -p "$run_dir"

ok "结果目录: $run_dir"

csv_resources="$run_dir/resources.csv"
csv_subprocess="$run_dir/subprocess.csv"

echo "timestamp,pod_short,cpu_percent,memory_mib" > "$csv_resources"
echo "timestamp,pod_short,kubectl_count,helm_count,total_processes" > "$csv_subprocess"

# ── 元信息 ────────────────────────────────────────────────────────────────────

managed_count=$(kubectl get ns -l kubepivot.io/managed=true --no-headers 2>/dev/null | wc -l | tr -d ' ')
controller_pods=($(kubectl get pods -n kubepivot-system -l app=kubepivot-controller -o jsonpath='{.items[*].metadata.name}'))
kp_version=$(kp version 2>/dev/null | head -1 || echo "unknown")

cat > "$run_dir/meta.txt" << EOF
═══════════════════════════════════════════════════════════════════
  KubePivot Steady-State Benchmark - $run_id
═══════════════════════════════════════════════════════════════════

参数:
  duration         $DURATION_SEC sec ($((DURATION_SEC / 60)) min)
  interval         $INTERVAL_SEC sec
  expected_samples $((DURATION_SEC / INTERVAL_SEC))

环境:
  kp_version        $kp_version
  managed_projects  $managed_count
  controller_pods   ${#controller_pods[@]}
  pod_names         ${controller_pods[*]}
  date              $(date)
  hostname          $(hostname)
EOF

cat "$run_dir/meta.txt"
echo

# ── 单次采样函数 ──────────────────────────────────────────────────────────────

sample_once() {
    local ts=$(date +%s)

    # 1. docker stats - 用 awk 直接处理，避免 while+pipe 子 shell 问题
    docker stats --no-stream --format '{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}' 2>/dev/null \
        | tr -d '\r' \
        | awk -F'|' -v ts="$ts" -v out="$csv_resources" '
            /k8s_controller_kubepivot-controller-/ {
                # $1=name $2=cpu(45.5%) $3=mem(49.01MiB / 512MiB)
                split($1, a, "_")
                pod = a[3] "-" a[4] "-" a[5]   # 拼回 pod 名
                # 实际 a[3]=kubepivot a[4]=controller a[5]=hashpart-suffix
                # 处理：去掉前缀 k8s_controller_，去掉后缀 _kubepivot-system_xxx_0
                pod = $1
                sub("^k8s_controller_", "", pod)
                sub("_kubepivot-system_.*", "", pod)

                cpu = $2; gsub("%", "", cpu)

                # mem 形如 "49.01MiB / 512MiB"
                split($3, mparts, " / ")
                mem = mparts[1]
                if (mem ~ /KiB$/) { gsub("KiB", "", mem); mem = mem / 1024 }
                else if (mem ~ /GiB$/) { gsub("GiB", "", mem); mem = mem * 1024 }
                else { gsub("MiB", "", mem) }

                printf "%s,%s,%s,%s\n", ts, pod, cpu, mem >> out
            }
        ' 

    # 2. 子进程数（docker top）
    for pod in "${controller_pods[@]}"; do
        container_id=$(docker ps --filter "name=k8s_controller_${pod}_" --format '{{.ID}}' | head -1)
        [[ -z "$container_id" ]] && continue

        ps_output=$(docker top "$container_id" 2>/dev/null || true)
        # grep -c 在 0 匹配时返回 1，加 || true 防止 set -e 杀脚本
        kubectl_count=$(echo "$ps_output" | grep -c "kubectl" 2>/dev/null || true)
        helm_count=$(echo "$ps_output" | grep -c "/usr/local/bin/helm" 2>/dev/null || true)
        total=$(echo "$ps_output" | tail -n +2 | wc -l | tr -d ' ')

        echo "$ts,$pod,$kubectl_count,$helm_count,$total" >> "$csv_subprocess"
    done
}

# ── 主循环 ────────────────────────────────────────────────────────────────────

log "开始稳态采样: $((DURATION_SEC / 60)) 分钟，每 ${INTERVAL_SEC}s 一次"
log "中途 Ctrl-C 也能拿到截至那一刻的数据"
echo

start_ts=$(date +%s)
end_ts=$((start_ts + DURATION_SEC))
sample_count=0

_summary() {
    echo
    echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}  采样完成${NC}"
    echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
    echo
    echo "  样本数:    $sample_count"
    echo "  实际时长:  $(($(date +%s) - start_ts)) sec"
    echo "  结果目录:  $run_dir"
    echo
    if [[ $sample_count -gt 0 ]]; then
        echo "  快速统计:"
        awk -F, 'NR>1 && $3!="" {sum+=$3; n++} END {if(n>0) printf "    avg CPU per pod:   %.2f%%\n", sum/n}' "$csv_resources"
        awk -F, 'NR>1 && $4!="" {sum+=$4; n++} END {if(n>0) printf "    avg memory per pod: %.2f MiB\n", sum/n}' "$csv_resources"
        awk -F, 'NR>1 && $3!="" {if($3+0>max)max=$3+0} END {printf "    peak CPU:           %.2f%%\n", max}' "$csv_resources"
        echo
    fi
}
trap _summary EXIT INT TERM

while [[ $(date +%s) -lt $end_ts ]]; do
    sample_once
    sample_count=$((sample_count + 1))

    if (( sample_count % 6 == 0 )); then
        elapsed=$(($(date +%s) - start_ts))
        remaining=$((DURATION_SEC - elapsed))
        log "已采样 $sample_count 次，剩余 $((remaining / 60))m $((remaining % 60))s"
    fi

    sleep "$INTERVAL_SEC"
done
