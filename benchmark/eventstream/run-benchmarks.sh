#!/usr/bin/env bash
# v2.7 Event Stream Benchmark - 完整 5 项基准
#
# Bench 1: Cache Get 单条读延迟
# Bench 2: Cache List 全量遍历
# Bench 3: Watch 稳态吞吐
# Bench 4: 启动时间 cold start
# Bench 5: 内存放大率（核心差异化指标）
#
# 跑完输出对比报告
# 数据自动归档到 docs/design/eventstream-perf/$(date)/

set -o pipefail

# ─── 切到 benchmark 目录（脚本健壮性）────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${SCRIPT_DIR}"

# ─── 公平性约定（qc 强调）────────────────────────────────────
export GOGC=200
export GOMEMLIMIT=4GiB
export GOMAXPROCS=4

# ─── 输出目录 ─────────────────────────────────────────────────
DATE=$(date +%Y%m%d_%H%M%S)
OUTPUT_DIR="${PROJECT_ROOT}/docs/design/eventstream-perf/${DATE}"
mkdir -p "${OUTPUT_DIR}"

GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log()  { echo -e "${BLUE}[$(date +%H:%M:%S)] $*${NC}"; }
ok()   { echo -e "${GREEN}✓ $*${NC}"; }
warn() { echo -e "${YELLOW}⚠ $*${NC}"; }

log "v2.7 Event Stream Benchmark - Day 1"
log "GOGC=${GOGC} GOMEMLIMIT=${GOMEMLIMIT} GOMAXPROCS=${GOMAXPROCS}"
log "Output: ${OUTPUT_DIR}"

# ─── Bench 1-4 (标准 go bench) ────────────────────────────────
log "运行 Bench 1-4 (Cache Get / List / Cold Start / Watch Throughput)..."
go test -bench=. -benchmem -run=^$ -count=10 -timeout=30m \
    -cpuprofile="${OUTPUT_DIR}/cpu.prof" \
    -memprofile="${OUTPUT_DIR}/mem.prof" \
    | tee "${OUTPUT_DIR}/bench-results.txt"

if [[ $? -ne 0 ]]; then
    warn "Bench 1-4 部分失败，继续 Bench 5"
fi
ok "Bench 1-4 完成"

# ─── Bench 5: 内存放大率 (qc 强调的核心) ─────────────────────
log "运行 Bench 5: 内存放大率 (1w 复杂 Deployment)..."
log "  注：分别跑 client-go / KubePivot 避免内存交叉"

# 单独跑 client-go
log "  → client-go cache..."
go test -run=TestMemoryAmplification_ClientGo -v -timeout=10m \
    | tee "${OUTPUT_DIR}/memory-clientgo.txt"

# 单独跑 KubePivot
log "  → KubePivot cache..."
go test -run=TestMemoryAmplification_KubePivot -v -timeout=10m \
    | tee "${OUTPUT_DIR}/memory-kubepivot.txt"

ok "Bench 5 完成"

# ─── Bench 3: Watch Throughput 对比报告 (events/sec) ──────────
log "运行 Bench 3 对比报告: Watch 稳态吞吐..."

go test -run=TestWatchThroughputComparison -v -timeout=10m \
    | tee "${OUTPUT_DIR}/watch-throughput.txt"

ok "Bench 3 对比报告完成"

# ─── 反序列化对比 (Bench 5 辅助) ─────────────────────────────
log "运行 ParseOnly 对比 (反序列化基线)..."
go test -bench=BenchmarkParseOnly -benchmem -run=^$ -count=5 \
    | tee "${OUTPUT_DIR}/parse-only.txt"

# ─── 总结 ─────────────────────────────────────────────────────
echo
echo -e "${GREEN}═══════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Day 1 Benchmark 完成${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════${NC}"
echo
echo "  数据归档: ${OUTPUT_DIR}/"
echo "    - bench-results.txt           Bench 1-4 (含 Bench 3 BenchmarkXxx)"
echo "    - watch-throughput.txt        Bench 3 对比报告 (events/sec)"
echo "    - memory-clientgo.txt         Bench 5 client-go"
echo "    - memory-kubepivot.txt        Bench 5 KubePivot"
echo "    - parse-only.txt              反序列化对比"
echo "    - cpu.prof / mem.prof         pprof 数据"
echo
echo "  下一步:"
echo "    1. 看 ${OUTPUT_DIR}/bench-results.txt 对比 client-go vs KubePivot"
echo "    2. 看 ${OUTPUT_DIR}/memory-*.txt 对比放大率"
echo "    3. 决策门: 是否走自研路径"
echo "    4. 写 docs/design/eventstream-perf.md 决策文档"
echo
