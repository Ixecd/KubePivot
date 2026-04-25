#!/usr/bin/env bash
# ============================================================================
# KubePivot v2.3.0 Benchmark - Setup Script
# ============================================================================
# 目的：准备 10 个 mock 项目用于性能测试
#
# 生成策略 (混合模式) ：
#   - 第 1 个 kp-auth-service 用 kp init 生成完整骨架 (真实锚点，证明兼容) 
#   - 所有 10 个走 kubectl apply (批量快速生成，测试主力) 
#
# 每个项目资源：
#   - 独立 namespace (同名) 
#   - 1 个 Deployment (pause:3.9，1 副本，mount PVC 到 /data) 
#   - 1 个 Service (ClusterIP) 
#   - 1 个 PVC (500Mi，真实绑定 local-path) 
#   - ns label: kubepivot.io/managed=true
#   - ConfigMap: kubepivot-resources (带 sha256 annotation) 
#
# 用法：
#   bash benchmark/scripts/setup.sh                 # 完整 10 项目 setup
#   bash benchmark/scripts/setup.sh --skip-kp-init  # 跳过 kp-init 锚点
# ============================================================================

set -euo pipefail

# ── 配置 ──────────────────────────────────────────────────────────────────────

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

ANCHOR_PROJECT="kp-auth-service"
WORKSPACE="${BENCHMARK_WORKSPACE:-$HOME/kp-benchmark}"
SKIP_KP_INIT="${SKIP_KP_INIT:-false}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --skip-kp-init) SKIP_KP_INIT="true"; shift ;;
        --workspace) WORKSPACE="$2"; shift 2 ;;
        -h|--help)
            grep '^#' "$0" | head -30 | sed 's/^# \?//'
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

log "前置检查"
command -v kp      >/dev/null || { err "kp 未安装"; exit 1; }
command -v kubectl >/dev/null || { err "kubectl 未安装"; exit 1; }
kubectl cluster-info --request-timeout=5s >/dev/null 2>&1 || { err "K8s 集群不可达"; exit 1; }
if ! kubectl get deployment -n kubepivot-system kubepivot-controller >/dev/null 2>&1; then
    warn "kubepivot-controller 未安装，请先运行: kp controller install"
    exit 1
fi
ok "kp / kubectl / 集群 / controller 全部就绪"

# ── 准备 workspace ────────────────────────────────────────────────────────────

log "准备 workspace: $WORKSPACE"
mkdir -p "$WORKSPACE"
cd "$WORKSPACE"

# ── 步骤 1：真实锚点项目 (kp init)  ──────────────────────────────────────────

if [[ "$SKIP_KP_INIT" != "true" ]]; then
    if [[ -d "$WORKSPACE/$ANCHOR_PROJECT" ]]; then
        warn "锚点项目已存在，跳过 kp init"
    else
        log "生成真实锚点项目: $ANCHOR_PROJECT (kp init) "
        kp init \
            --name "$ANCHOR_PROJECT" \
            --module "github.com/bench/$ANCHOR_PROJECT" \
            >/dev/null
        ok "锚点骨架已生成: $WORKSPACE/$ANCHOR_PROJECT"
    fi
    # 说明：锚点项目不 kp deploy (避免 build/push/helm 耗时) 
    #      只证明 kp init 生成的真实骨架和 benchmark 的 mock 资源能并存
fi

# ── 步骤 2：批量生成 10 个 namespace + mock 资源 ──────────────────────────────

log "批量生成 10 个 mock namespace + 资源 (含 PVC mount) "

created_count=0
for project in "${PROJECTS[@]}"; do
    resources_yaml=$(cat <<EOF
resources:
  - kind: Deployment
    name: ${project}
    on-missing: auto-heal
    max-retry: 3
  - kind: Service
    name: ${project}
    on-missing: alert
  - kind: PersistentVolumeClaim
    name: ${project}-data
    on-missing: alert
EOF
)
    sha256_hex=$(echo -n "$resources_yaml" | shasum -a 256 | awk '{print $1}')

    manifest=$(cat <<EOF
---
apiVersion: v1
kind: Namespace
metadata:
  name: ${project}
  labels:
    kubepivot.io/managed: "true"
    benchmark.kubepivot.io/run: "v2.3.0"
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${project}-data
  namespace: ${project}
  labels:
    app: ${project}
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 500Mi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${project}
  namespace: ${project}
  labels:
    app: ${project}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ${project}
  template:
    metadata:
      labels:
        app: ${project}
    spec:
      containers:
        - name: main
          image: registry.k8s.io/pause:3.9
          resources:
            requests:
              cpu: "10m"
              memory: "16Mi"
            limits:
              cpu: "50m"
              memory: "32Mi"
          volumeMounts:
            - name: data
              mountPath: /data
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: ${project}-data
---
apiVersion: v1
kind: Service
metadata:
  name: ${project}
  namespace: ${project}
  labels:
    app: ${project}
spec:
  selector:
    app: ${project}
  ports:
    - port: 80
      targetPort: 8080
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: kubepivot-resources
  namespace: ${project}
  labels:
    kubepivot.io/managed: "true"
    app.kubernetes.io/managed-by: kp
  annotations:
    kubepivot.io/sha256: "${sha256_hex}"
data:
  resources.yaml: |
$(echo "$resources_yaml" | sed 's/^/    /')
EOF
)

    echo "$manifest" | kubectl apply -f - >/dev/null
    created_count=$((created_count + 1))
    ok "[$created_count/${#PROJECTS[@]}] $project"
done

# ── 步骤 3：等待所有 Deployment Available ────────────────────────────────────

log "等待所有 Deployment Available (最多 120 秒，PVC 真实绑定需要时间) "
ready_count=0
for project in "${PROJECTS[@]}"; do
    if kubectl wait --for=condition=Available \
                    --timeout=120s \
                    -n "$project" deployment/"$project" \
                    >/dev/null 2>&1; then
        ready_count=$((ready_count + 1))
    else
        warn "$project 未 Available"
    fi
done
ok "Deployment 就绪: $ready_count/${#PROJECTS[@]}"

# ── 步骤 4：验证 PVC 真实绑定 ────────────────────────────────────────────────

log "验证 PVC 绑定状态"
bound_count=0
for project in "${PROJECTS[@]}"; do
    phase=$(kubectl get pvc "${project}-data" -n "$project" -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")
    if [[ "$phase" == "Bound" ]]; then
        bound_count=$((bound_count + 1))
    else
        warn "$project PVC phase=$phase"
    fi
done
ok "PVC Bound: $bound_count/${#PROJECTS[@]}"

# ── 步骤 5：验证 controller 发现了所有 managed 项目 ──────────────────────────

log "等待 controller 发现所有 managed 项目 (最多 30 秒) "
expected=${#PROJECTS[@]}
for i in {1..30}; do
    actual=$(kp controller projects 2>/dev/null | grep -c '^  ✓' || true)
    # 清洗：去换行去非数字，fallback 0
    actual=$(echo "$actual" | head -1 | tr -dc '0-9')
    actual="${actual:-0}"
    if [[ "$actual" -ge "$expected" ]]; then
        ok "controller 已发现全部 $actual 个项目"
        break
    fi
    sleep 1
done

# ── 汇总 ──────────────────────────────────────────────────────────────────────

echo
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Setup 完成${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo
echo "  锚点项目目录:    $WORKSPACE/$ANCHOR_PROJECT"
echo "  mock 项目数量:    ${#PROJECTS[@]}"
echo "  Deployment Ready: $ready_count/${#PROJECTS[@]}"
echo "  PVC Bound:        $bound_count/${#PROJECTS[@]}"
echo
echo "  下一步："
echo "    bash benchmark/scripts/steady-state.sh     # 稳态采样 (30 分钟) "
echo "    bash benchmark/scripts/concurrent-chaos.sh # 并发故障测试"
echo "    bash benchmark/scripts/cleanup.sh          # 清理全部"
echo
