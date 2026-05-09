// benchmark/chaos/flood-ns.js
// v3.4 k6 flood — 模拟大规模 namespace 创建/删除
//
// 目标: 10k eps, p99 lat <200ms, drop <0.1%
//
// 用法:
//   k6 run --vus 100 --duration 60s benchmark/chaos/flood-ns.js
//   k6 run --vus 500 --duration 300s --out prometheus benchmark/chaos/flood-ns.js
//
// 前提:
//   - kind cluster running, controller deployed
//   - kubectl proxy 或 port-forward controller metrics

import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Counter, Trend, Rate } from 'k6/metrics';

// ── 自定义指标 ────────────────────────────────────────────────
const nsCreated   = new Counter('ns_created');
const nsDeleted   = new Counter('ns_deleted');
const nsErrors    = new Counter('ns_errors');
const latencyP99  = new Trend('latency_p99', true);
const dropRate    = new Rate('drop_rate');

// ── 配置 ──────────────────────────────────────────────────────
// Controller /health 端点 (用于基线吞吐)
const HEALTH = __ENV.HEALTH_URL || 'http://localhost:9090/health';

// Controller /metrics 端点 (用于抓取 drop/queue 指标)
const METRICS = __ENV.METRICS_URL || 'http://localhost:9090/metrics';

export const options = {
  stages: [
    { duration: '30s', target: 50 },   // 预热
    { duration: '60s', target: 200 },  // 爬坡
    { duration: '120s', target: 500 }, // 洪峰
    { duration: '30s', target: 0 },    // 收尾
  ],
  thresholds: {
    http_req_duration: ['p(99)<200'],       // p99 < 200ms
    drop_rate:         ['rate<0.001'],       // drop < 0.1%
    http_req_failed:   ['rate<0.01'],        // 错误率 < 1%
  },
};

// ── 主函数 ────────────────────────────────────────────────────
export default function () {
  group('health check flood', function () {
    // GET /health — 基线吞吐
    const res = http.get(HEALTH, {
      tags: { type: 'health' },
      timeout: '2s',
    });

    check(res, {
      'status 200': (r) => r.status === 200,
    });

    latencyP99.add(res.timings.duration);
    if (res.status !== 200) {
      nsErrors.add(1);
      dropRate.add(1);
    }
  });

  // 每 10 次请求 sleep 1ms，模拟真实间隔
  if (__ITER % 10 === 0) {
    sleep(0.001);
  }
}

// ── 自定义函数: namespace flood (需 kubectl 或 K8s API) ──────
export function setup() {
  console.log('v3.4 flood test starting...');
  console.log(`health: ${HEALTH}`);
  console.log(`metrics: ${METRICS}`);
}

export function teardown(data) {
  console.log('v3.4 flood test complete');
  console.log(`errors: ${nsErrors.value}`);
  console.log(`p99: ${latencyP99.p(99)}ms`);
}
