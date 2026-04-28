#!/usr/bin/env bash
# Step 1: staging 部署 + 蓝绿切换
#
# 完整流程:
#   1. helm install whoami chart 到 staging ns
#   2. kp controller enroll (写 resources.yaml 到 ConfigMap, controller 接管)
#   3. kp sandbox start (蓝绿切换 blue → green=100%)
#
# 之后等 5min, controller verifiedTrafficWriter 自动写出 verified-traffic ConfigMap.

set -euo pipefail

NAMESPACE="${STAGING_NAMESPACE:-kp-demo-bluegreen-staging}"
ENV_NAME="${STAGING_ENV_NAME:-staging}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEMO_ROOT="$(dirname "${SCRIPT_DIR}")"

echo "═══════════════════════════════════════════"
echo "  Step 1: staging 部署 + 蓝绿切换"
echo "═══════════════════════════════════════════"
echo "  Namespace: ${NAMESPACE}"
echo "  KPEnv:     ${ENV_NAME}"
echo ""

# 检查 chart 是否已复制
if [[ ! -d "${DEMO_ROOT}/chart" ]]; then
  echo "❌ chart/ 目录不存在"
  echo "   请先运行:"
  echo "     cd ${DEMO_ROOT}"
  echo "     cp -r ../example-bluegreen/example-bluegreen/chart ."
  exit 1
fi

# 检查 KPEnv 配置
if ! kp context show "${ENV_NAME}" >/dev/null 2>&1; then
  echo "❌ KPEnv ${ENV_NAME} 不存在"
  echo "   请先运行:"
  echo "     kp context add --name ${ENV_NAME} --namespace ${NAMESPACE}"
  exit 1
fi

# 1. 创建 namespace
echo "[1/4] 创建 namespace ${NAMESPACE}..."
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

# 2. helm install
echo "[2/4] helm install whoami..."
helm upgrade --install whoami "${DEMO_ROOT}/chart" \
  --namespace "${NAMESPACE}" \
  --create-namespace \
  --wait --timeout 60s

# 3. kp controller enroll (用 staging resources.yaml)
echo "[3/4] kp controller enroll..."
kp controller enroll \
  --namespace "${NAMESPACE}" \
  --resources "${DEMO_ROOT}/resources-staging.yaml"

# 4. kp sandbox start (触发蓝绿切换)
echo "[4/4] kp sandbox start (蓝绿切换)..."
kp sandbox start \
  --namespace "${NAMESPACE}"

echo ""
echo "✅ staging 部署完成"
echo ""
echo "下一步:"
echo "  等 5 分钟 (controller 5min 稳态判定):"
echo "    sleep 300"
echo "  验证 verified-traffic ConfigMap 已写出:"
echo "    make verify-staging-cm"
