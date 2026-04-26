#!/usr/bin/env bash
# ============================================================================
# KubePivot Benchmark - Performance Cube Matrix (v2.5.1)
# ============================================================================
# 目的：跑 (P, R, N) 三维性能立方体测试，找最优配置
#
#   P  = 项目数      {10, 50, 100, ...}
#   R  = 副本数      {3, 5, 10}
#   N  = 分片数      {R, R*2, R*5, ...}
#
# 用法：
#   bash benchmark/scripts/matrix.sh --phase 1
#     等价于：跑 P=10/50/100, R=3, N=10 三个组合
#
#   bash benchmark/scripts/matrix.sh --phase 2
#     等价于：跑 P=50, R=3/5, N=10 两个组合
#
#   bash benchmark/scripts/matrix.sh --phase 3
#     等价于：跑 P=50, R=3, N=3/10/15 三个组合
#
#   bash benchmark/scripts/matrix.sh --p 100 --r 3 --n 10
#     单点测试
#
#   bash benchmark/scripts/matrix.sh --all
#     完整立方体（耗时 6+ 小时，慎用）
#
# 选项：
#   --skip-setup       项目数没变时跳过 cleanup+setup（快速重测）
#   --steady N         稳态采样秒数（默认 300）
#   --dry-run          只打印计划不执行
#
# 输出：
#   benchmark/results/matrix-{date}/
#     010-3-10/  result.txt + resources.csv
#     050-3-10/  ...
#     100-3-10/  ...
#     summary.csv
# ============================================================================

set -o pipefail

# ── 默认参数 ──────────────────────────────────────────────────────────────────

PHASE=""
SINGLE_P=""
SINGLE_R=""
SINGLE_N=""
ALL_MODE="false"
SKIP_SETUP="false"
STEADY_DURATION="${STEADY_DURATION:-300}"
DRY_RUN="false"

# ── 颜色 ──────────────────────────────────────────────────────────────────────

GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; RED='\033[0;31m'; NC='\033[0m'
log()  { echo -e "${BLUE}[$(date +%H:%M:%S)] $*${NC}"; }
ok()   { echo -e "${GREEN}✓ $*${NC}"; }
warn() { echo -e "${YELLOW}⚠ $*${NC}"; }
err()  { echo -e "${RED}✗ $*${NC}" >&2; }

# ── 参数解析 ──────────────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
    case "$1" in
        --phase) PHASE="$2"; shift 2 ;;
        --p) SINGLE_P="$2"; shift 2 ;;
        --r) SINGLE_R="$2"; shift 2 ;;
        --n) SINGLE_N="$2"; shift 2 ;;
        --all) ALL_MODE="true"; shift ;;
        --skip-setup) SKIP_SETUP="true"; shift ;;
        --steady) STEADY_DURATION="$2"; shift 2 ;;
        --dry-run) DRY_RUN="true"; shift ;;
        -h|--help)
            grep '^#' "$0" | sed 's/^# \?//'
            exit 0 ;;
        *) err "未知参数: $1"; exit 1 ;;
    esac
done

# ── 计算测试矩阵 ──────────────────────────────────────────────────────────────

# 输出格式：每行一个 "P R N" 组合
build_matrix() {
    if [[ -n "$SINGLE_P" && -n "$SINGLE_R" && -n "$SINGLE_N" ]]; then
        echo "$SINGLE_P $SINGLE_R $SINGLE_N"
        return
    fi

    if [[ "$ALL_MODE" == "true" ]]; then
        # 完整立方体
        for p in 10 50 100; do
            for r in 3 5; do
                for n in "$r" $((r * 2)) $((r * 5)); do
                    echo "$p $r $n"
                done
            done
        done
        return
    fi

    case "$PHASE" in
        1)
            # 阶段 1：固定 R=3 N=10，扫 P
            echo "10 3 10"
            echo "50 3 10"
            echo "100 3 10"
            ;;
        2)
            # 阶段 2：固定 P=50 N=10，扫 R
            echo "50 3 10"
            echo "50 5 10"
            ;;
        3)
            # 阶段 3：固定 P=50 R=3，扫 N
            echo "50 3 3"
            echo "50 3 10"
            echo "50 3 15"
            ;;
        *)
            err "必须指定 --phase 1|2|3 或 --p/--r/--n 单点 或 --all"
            exit 1
            ;;
    esac
}

# ── 当前集群状态查询 ──────────────────────────────────────────────────────────

get_current_replicas() {
    kubectl get deployment kubepivot-controller \
        -n kubepivot-system \
        -o jsonpath='{.spec.replicas}' 2>/dev/null
}

get_current_shards() {
    kubectl get configmap kubepivot-controller-config \
        -n kubepivot-system \
        -o jsonpath='{.data.shards}' 2>/dev/null
}

get_current_project_count() {
    kubectl get ns -l kubepivot.io/managed=true \
        --no-headers 2>/dev/null | wc -l | tr -d ' '
}

# ── 集群状态调整 ──────────────────────────────────────────────────────────────

adjust_replicas() {
    local target="$1"
    local current
    current=$(get_current_replicas)

    if [[ "$current" == "$target" ]]; then
        ok "副本数已经是 $target"
        return
    fi

    log "调整 controller 副本数: $current → $target"
    kubectl scale deployment kubepivot-controller \
        -n kubepivot-system --replicas="$target" >/dev/null

    log "等 rollout 完成"
    kubectl rollout status -n kubepivot-system \
        deployment/kubepivot-controller --timeout=120s >/dev/null

    # 等 lease 重新分布稳定
    log "等 shard lease 重新分布（30 秒）"
    sleep 30
    ok "副本数现在是 $target"
}

adjust_shards() {
    local target="$1"
    local current
    current=$(get_current_shards)

    if [[ "$current" == "$target" ]]; then
        ok "分片数已经是 $target"
        return
    fi

    log "调整分片数: $current → $target"
    kubectl patch configmap kubepivot-controller-config \
        -n kubepivot-system \
        --type=merge \
        -p "{\"data\":{\"shards\":\"$target\"}}" >/dev/null

    # ConfigMap 修改不会自动 rollout pod，要主动 restart
    log "重启 controller 让新分片数生效"
    kubectl rollout restart -n kubepivot-system \
        deployment/kubepivot-controller >/dev/null
    kubectl rollout status -n kubepivot-system \
        deployment/kubepivot-controller --timeout=120s >/dev/null

    log "等 shard lease 重新分布（30 秒）"
    sleep 30
    ok "分片数现在是 $target"
}

adjust_projects() {
    local target="$1"
    local current
    current=$(get_current_project_count)

    if [[ "$current" == "$target" && "$SKIP_SETUP" != "true" ]]; then
        log "项目数已是 $target，但仍需重 setup 保持一致性"
    fi

    if [[ "$current" -gt 0 ]]; then
        log "清理当前 $current 个项目"
        # cleanup.sh 已支持 PROJECT_COUNT 但要传当前的数量
        PROJECT_COUNT="$current" bash benchmark/scripts/cleanup.sh >/dev/null 2>&1
        sleep 30
    fi

    log "Setup $target 个项目"
    PROJECT_COUNT="$target" bash benchmark/scripts/setup.sh >/dev/null 2>&1

    log "等 controller 把所有项目 enroll（60 秒）"
    sleep 60

    local actual
    actual=$(get_current_project_count)
    if [[ "$actual" != "$target" ]]; then
        warn "项目数不符：期望 $target，实际 $actual"
    fi
    ok "项目数现在是 $actual"
}

# ── 跑单个组合 ────────────────────────────────────────────────────────────────

run_combo() {
    local p="$1"
    local r="$2"
    local n="$3"
    local matrix_dir="$4"

    log "════════════════════════════════════════════════════════"
    log "  组合：P=$p  R=$r  N=$n"
    log "════════════════════════════════════════════════════════"

    if [[ "$DRY_RUN" == "true" ]]; then
        log "[DRY RUN] 跳过实际执行"
        return
    fi

    # 1. 调整副本数（先调副本，再调分片，最后调项目数——状态切换最少）
    adjust_replicas "$r"

    # 2. 调整分片数
    adjust_shards "$n"

    # 3. 调整项目数
    adjust_projects "$p"

    # 4. 跑稳态
    log "稳态采样 $STEADY_DURATION 秒"
    bash benchmark/scripts/steady-state.sh -d "$STEADY_DURATION" -i 5 >/dev/null

    # 5. 找最近的 results 目录归档
    local latest_run
    latest_run=$(ls -1d benchmark/results/*-steady-state 2>/dev/null | tail -1)
    if [[ -z "$latest_run" ]]; then
        err "找不到 steady-state 结果目录"
        return 1
    fi

    # 归档到 matrix-{date}/ 下
    local combo_dir
    combo_dir=$(printf "%s/%03d-%d-%d" "$matrix_dir" "$p" "$r" "$n")
    mkdir -p "$combo_dir"
    cp -r "$latest_run"/* "$combo_dir/"

    # 把 (P, R, N) 写入 metadata
    {
        echo "P=$p"
        echo "R=$r"
        echo "N=$n"
        echo "timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    } > "$combo_dir/matrix-meta.txt"

    ok "组合 P=$p R=$r N=$n 完成 → $combo_dir"
}

# ── 计算单组合的 quick stats（从 resources.csv） ─────────────────────────────

compute_stats() {
    local csv="$1"
    if [[ ! -f "$csv" ]]; then
        echo ",,,,,"
        return
    fi

    awk -F, 'NR>1 {
        cpu_sum+=$3; mem_sum+=$4; n++
        if ($3>cpu_max) cpu_max=$3
    }
    END {
        if (n == 0) { print ",,,,,"; exit }
        printf "%.2f,%.2f,%.2f", cpu_sum/n, cpu_max, mem_sum/n
    }' "$csv"
}

# ── 汇总到 summary.csv ────────────────────────────────────────────────────────

summarize() {
    local matrix_dir="$1"
    local summary="$matrix_dir/summary.csv"

    echo "P,R,N,avg_CPU_per_pod,peak_CPU,avg_MEM_per_pod,total_CPU" > "$summary"

    for combo_dir in "$matrix_dir"/*/; do
        local meta="$combo_dir/matrix-meta.txt"
        local csv="$combo_dir/resources.csv"

        if [[ ! -f "$meta" ]]; then continue; fi

        local p r n
        p=$(grep '^P=' "$meta" | cut -d= -f2)
        r=$(grep '^R=' "$meta" | cut -d= -f2)
        n=$(grep '^N=' "$meta" | cut -d= -f2)

        local stats
        stats=$(compute_stats "$csv")
        local avg_cpu peak_cpu avg_mem
        IFS=',' read -r avg_cpu peak_cpu avg_mem <<< "$stats"

        # 总 CPU = avg_cpu × R
        local total_cpu="0"
        if [[ -n "$avg_cpu" && -n "$r" ]]; then
            total_cpu=$(awk -v a="$avg_cpu" -v b="$r" 'BEGIN { printf "%.2f", a*b }')
        fi

        echo "$p,$r,$n,$avg_cpu,$peak_cpu,$avg_mem,$total_cpu" >> "$summary"
    done

    log "Summary 已生成: $summary"
    echo
    column -t -s, "$summary"
    echo
}

# ── 主流程 ────────────────────────────────────────────────────────────────────

main() {
    # 前置检查
    if ! command -v kubectl >/dev/null; then
        err "kubectl 不在 PATH"
        exit 1
    fi
    if ! kubectl get ns kubepivot-system >/dev/null 2>&1; then
        err "kubepivot-system namespace 不存在，先安装 controller"
        exit 1
    fi

    # 计算矩阵
    local matrix
    matrix=$(build_matrix)
    local count
    count=$(echo "$matrix" | wc -l | tr -d ' ')

    log "本次将跑 $count 个组合"
    echo "$matrix" | while read p r n; do
        echo "  - P=$p R=$r N=$n"
    done

    if [[ "$DRY_RUN" == "true" ]]; then
        log "[DRY RUN] 退出"
        exit 0
    fi

    # 创建 matrix 结果目录
    local run_id
    run_id=$(date +%Y-%m-%d_%H%M%S)
    local matrix_dir="benchmark/results/matrix-$run_id"
    mkdir -p "$matrix_dir"

    log "结果根目录: $matrix_dir"

    # 顺序执行每个组合
    local idx=1
    while read -r p r n; do
        log ""
        log "=== [$idx/$count] ==="
        run_combo "$p" "$r" "$n" "$matrix_dir"
        idx=$((idx + 1))
    done <<< "$matrix"

    # 汇总
    log ""
    log "════════════════════════════════════════════════════════"
    log "  全部组合完成 ✓"
    log "════════════════════════════════════════════════════════"
    summarize "$matrix_dir"
}

main "$@"
