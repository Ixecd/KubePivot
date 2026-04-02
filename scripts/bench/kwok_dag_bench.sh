#!/bin/bash
# kwok_dag_bench.sh — KWOK 集群 DAG 规划 + Apiserver 延迟压测
# 用法：./scripts/bench/kwok_dag_bench.sh [节点数] [服务数]
# 依赖：kwokctl, kubectl, kp

set -e

NODES=${1:-500}
SERVICES=${2:-20}
CONTEXT="kwok-kwok-stress"
BENCH_DIR=$(mktemp -d)

echo "========================================"
echo " KubePivot KWOK 压测"
echo " 节点数: ${NODES}  服务数: ${SERVICES}"
echo "========================================"
echo ""

# ── 1. Apiserver P99 延迟 ────────────────────────────────────────────────────
echo "▶ 测试 Apiserver 延迟（采样 10 次）"
latencies=()
for i in $(seq 1 10); do
    start=$(date +%s%N)
    kubectl --context "$CONTEXT" get nodes -o name > /dev/null 2>&1
    end=$(date +%s%N)
    ms=$(( (end - start) / 1000000 ))
    latencies+=($ms)
    printf "  采样 %2d: %dms\n" $i $ms
done

# 计算 P50/P99
sorted=($(printf '%s\n' "${latencies[@]}" | sort -n))
p50=${sorted[4]}
p99=${sorted[9]}
echo ""
echo "  P50=${p50}ms  P99=${p99}ms"

if [ "$p99" -gt 2000 ]; then
    echo "  ❌ P99 > 2s，建议 --parallelism=1"
elif [ "$p99" -gt 500 ]; then
    echo "  ⚠️  P99 > 500ms，建议 --parallelism=4"
else
    echo "  ✅ 延迟正常，可使用 --parallelism=8"
fi
echo ""

# ── 2. 生成测试用 components.yaml ────────────────────────────────────────────
echo "▶ 生成 ${SERVICES} 服务的拓扑（DAG 规划测试）"
COMP_FILE="${BENCH_DIR}/components.yaml"

cat > "$COMP_FILE" << YAML
components:
  - name: svc-db
    type: statefulset
    port: 5432
    image: ""
  - name: svc-cache
    type: statefulset
    port: 6379
    image: ""
  - name: svc-core
    type: deployment
    port: 8001
    image: "svc-core"
    depends_on: [svc-db, svc-cache]
  - name: svc-api
    type: deployment
    port: 8002
    image: "svc-api"
    depends_on: [svc-core]
YAML

# 追加剩余服务（并行层）
for i in $(seq 5 $SERVICES); do
    cat >> "$COMP_FILE" << YAML
  - name: svc-$(printf '%02d' $i)
    type: deployment
    port: $(( 8000 + i ))
    image: "svc-$(printf '%02d' $i)"
    depends_on: [svc-core]
YAML
done

echo "  生成完成：${COMP_FILE}"
echo ""

# ── 3. DAG 规划耗时 ───────────────────────────────────────────────────────────
echo "▶ 测试 DAG 规划耗时（kp deploy --dry-run，5 次）"
dag_times=()
for i in $(seq 1 5); do
    start=$(date +%s%N)
    kp deploy --dry-run --components "$COMP_FILE" > /dev/null 2>&1 || true
    end=$(date +%s%N)
    ms=$(( (end - start) / 1000000 ))
    dag_times+=($ms)
    printf "  运行 %d: %dms\n" $i $ms
done

dag_sorted=($(printf '%s\n' "${dag_times[@]}" | sort -n))
dag_p50=${dag_sorted[2]}
dag_p99=${dag_sorted[4]}
echo ""
echo "  DAG 规划 P50=${dag_p50}ms  P99=${dag_p99}ms（${SERVICES} 服务）"
echo ""

# ── 4. 汇总 ───────────────────────────────────────────────────────────────────
echo "========================================"
echo " 压测结果汇总"
echo "========================================"
echo "  集群节点数:        ${NODES}"
echo "  测试服务数:        ${SERVICES}"
echo "  Apiserver P50:     ${p50}ms"
echo "  Apiserver P99:     ${p99}ms"
echo "  DAG 规划 P50:      ${dag_p50}ms"
echo "  DAG 规划 P99:      ${dag_p99}ms"
echo ""

# 清理
rm -rf "$BENCH_DIR"
