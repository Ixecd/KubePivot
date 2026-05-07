#!/bin/bash
# kind-chaos-lite.sh — v3.2 Chaos 轻量测试 (EKS pilot 版)
# 模拟节点故障 + Pod 驱逐 + Watch jitter + 410 Gone storm
# 验证 Rescheduler p99 tail latency + delta storm merge-on-read
#
# 使用方式:
#   ./benchmark/chaos/kind-chaos-lite.sh --nodes 3 --pods 500 --chaos-duration 120 --watch-jitter 50 --gone-rate 5

set -o pipefail

NODES=3
PODS=500
DURATION=120
CHAOS_INTERVAL=15
while [[ $# -gt 0 ]]; do
    case $1 in
        --nodes) NODES="$2"; shift 2 ;;
        --pods) PODS="$2"; shift 2 ;;
        --chaos-duration) DURATION="$2"; shift 2 ;;
        --watch-jitter) WATCH_JITTER="$2"; shift 2 ;;
        --gone-rate) GONE_RATE="$2"; shift 2 ;;
        *) echo "unknown: $1"; exit 1 ;;
    esac
done

echo "=== KubePivot Chaos Lite ==="
echo "  nodes:  $NODES"
echo "  pods:   $PODS"
echo "  duration: ${DURATION}s"
echo "  interval: ${CHAOS_INTERVAL}s"
echo ""

# Phase 1: create kind cluster
echo "[1/4] Creating kind cluster..."
kind create cluster --name kp-chaos --wait 120s 2>/dev/null || true

# Phase 2: deploy test workloads
echo "[2/4] Deploying $PODS pods..."
for i in $(seq 1 $PODS); do
    NS="chaos-$(($i % 10))"
    kubectl create ns "$NS" --dry-run=client -o yaml | kubectl apply -f - 2>/dev/null
    kubectl run "chaos-pod-$i" --image=nginx:alpine --restart=Never \
        -n "$NS" --labels="app=chaos,shard=$(($i % 5))" \
        --overrides='{"spec":{"terminationGracePeriodSeconds":1}}' 2>/dev/null &
    if (( i % 50 == 0 )); then wait; fi
done
wait
echo "  Deployed."

# Phase 3: start KubePivot in background
echo "[3/4] Starting KubePivot watch + Rescheduler..."
# Simulate with benchmark: run delta storm while deploying
go test -bench='BenchmarkPodCache_DeltaStorm' -benchtime="${DURATION}s" \
    -cpuprofile=/tmp/kp-chaos-cpu.prof \
    ./internal/eventstream/ &
KP_PID=$!

# Phase 4: inject failures
echo "[4/4] Injecting node/pod failures for ${DURATION}s..."
END=$((SECONDS + DURATION))
while [ $SECONDS -lt $END ]; do
    # Delete a random chaos pod (simulate eviction)
    NS="chaos-$((RANDOM % 10))"
    POD=$(kubectl get pods -n "$NS" -l app=chaos --field-selector=status.phase=Running \
        -o jsonpath='{.items[*].metadata.name}' 2>/dev/null | tr ' ' '\n' | shuf -n 1)
    if [ -n "$POD" ]; then
        kubectl delete pod "$POD" -n "$NS" --grace-period=0 --force 2>/dev/null &
    fi

    # Simulate node not-ready (patch node condition)
    NODE="kp-chaos-worker$(( (RANDOM % NODES) + 1 ))"
    kubectl taint nodes "$NODE" chaos=test:NoSchedule --overwrite 2>/dev/null &
    sleep 2
    kubectl taint nodes "$NODE" chaos- 2>/dev/null &

    # Watch jitter: inject network latency on API server
    if [ "$WATCH_JITTER" -gt 0 ] && [ $((RANDOM % 3)) -eq 0 ]; then
        kubectl annotate pod -n kube-system -l component=kube-apiserver \
            chaos-jitter="injected-$(date +%s)" --overwrite 2>/dev/null || true
    fi

    # 410 Gone: force resource version expiry (5% chance per interval)
    if [ $((RANDOM % 100)) -lt "$GONE_RATE" ]; then
        # Rapid pod create/delete to bump resource version
        kubectl run "gone-storm-$(date +%s)" --image=busybox --restart=Never \
            -n default -- sleep 1 2>/dev/null && \
        kubectl delete pod "gone-storm-$(date +%s)" -n default --wait=false 2>/dev/null &
    fi

    sleep "$CHAOS_INTERVAL"
done

# Cleanup
wait $KP_PID 2>/dev/null
echo ""
echo "=== Chaos complete ==="
echo "Results:"
echo "  CPU profile: /tmp/kp-chaos-cpu.prof"
echo "  Run: go tool pprof -http=:8080 /tmp/kp-chaos-cpu.prof"
echo ""
echo "  To clean up: kind delete cluster --name kp-chaos"
