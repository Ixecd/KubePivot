#!/usr/bin/env bash
# Step 3: prod 用 staging 配置部署
#
# 完整流程:
#   1. helm install whoami chart 到 prod ns
#   2. kp controller enroll prod (用 resources-prod.yaml, 含 traffic 字段)
#   3. kp sandbox start --from-env staging (核心: 跨集群读 staging verified-traffic)
#
# 关键依赖: staging 已经在 RUNNING + ≥5min 稳态状态, controller 已写出 ConfigMap
# 否则 step 3 fail-fast (Q7).

set -euo pipefail

STAGING_NS="${STAGING_NAMESPACE:-kp-demo-bluegreen-staging}"
STAGING_ENV="${STAGING_ENV_NAME:-staging}"
PROD_NS="${PROD_NAMESPACE:-kp-demo-bluegreen-prod}"
PROD_ENV="${PROD_ENV_NAME:-prod}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEMO_ROOT="$(dirname "${SCRIPT_DIR}")"

echo "═══════════════════════════════════════════"
echo "  Step 3: prod 用 staging 配置部署"
echo "═══════════════════════════════════════════"
echo "  Source (staging): namespace=${STAGING_NS}, env=${STAGING_ENV}"
echo "  Target (prod):    namespace=${PROD_NS},    env=${PROD_ENV}"
echo ""

# 前置检查 1: staging verified-traffic ConfigMap 存在
echo "[预检 1/2] staging verified-traffic ConfigMap..."
if ! kubectl get configmap kubepivot-verified-traffic -n "${STAGING_NS}" >/dev/null 2>&1; then
  echo "❌ staging verified-traffic ConfigMap 不存在"
  echo ""
  echo "   可能原因:"
  echo "     - staging 还在 5min 稳态等待中 (RUNNING 时长不够)"
  echo "     - Controller 没部署 (kubectl get pods -n kubepivot-system)"
  echo "     - Controller 没接管 staging ns"
  echo ""
  echo "   排查命令:"
  echo "     kubectl get configmap kubepivot-verified-traffic -n ${STAGING_NS}"
  echo "     kp status --env ${STAGING_ENV}"
  exit 1
fi
echo "      ✓ ConfigMap 存在"

# 前置检查 2: prod KPEnv
echo "[预检 2/2] prod KPEnv..."
if ! kp context show "${PROD_ENV}" >/dev/null 2>&1; then
  echo "❌ KPEnv ${PROD_ENV} 不存在"
  echo "   请先运行:"
  echo "     kp context add --name ${PROD_ENV} --namespace ${PROD_NS}"
  exit 1
fi
echo "      ✓ KPEnv ${PROD_ENV} 存在"
echo ""

# 1. 创建 prod namespace
echo "[1/3] 创建 namespace ${PROD_NS}..."
kubectl create namespace "${PROD_NS}" --dry-run=client -o yaml | kubectl apply -f -

# 2. helm install 到 prod (跟 staging 一样的 chart)
echo "[2/3] helm install whoami 到 prod..."
helm upgrade --install whoami "${DEMO_ROOT}/chart" \
  --namespace "${PROD_NS}" \
  --create-namespace \
  --wait --timeout 60s

# 3. kp controller enroll prod
echo "[3/3] kp controller enroll prod..."
kp controller enroll \
  --namespace "${PROD_NS}" \
  --resources "${DEMO_ROOT}/resources-prod.yaml"

# 4. kp sandbox start --from-env staging (核心命令)
echo ""
echo "═══════════════════════════════════════════"
echo "  执行 kp sandbox start --from-env staging"
echo "═══════════════════════════════════════════"
echo ""

kp sandbox start \
  --namespace "${PROD_NS}" \
  --from-env "${STAGING_ENV}"

echo ""
echo "✅ prod 部署完成"
echo ""
echo "下一步:"
echo "  对比两 env traffic 状态:"
echo "    make verify-both"
