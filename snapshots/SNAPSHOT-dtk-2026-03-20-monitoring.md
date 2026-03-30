# kubepivot 快照 — 监控告警骨架完成

> 归档时间：2026-03-20
> 里程碑：monitoring — metrics + prometheus + alertmanager + grafana 完整骨架

---

## 本次新增

### `dtk init` 新增生成内容

**`internal/metrics/metrics.go`**
- HTTPRequestTotal（Counter，method/path/status）
- HTTPRequestDuration（Histogram，method/path，DefBuckets）
- BusinessErrorTotal（Counter，code）
- Init() 注册所有指标

**`monitoring/` 完整目录**

```
monitoring/
├── prometheus/
│   ├── prometheus.yml         # 含 scrape_configs，job 名自动替换为项目名
│   └── rules/<n>.yml         # 3条告警规则
├── alertmanager/
│   └── alertmanager.yml       # Telegram 模板，bot_token 占位符
└── grafana/
    ├── dashboards/<n>.json    # 4个 Panel：请求速率/错误率/P99延迟/业务错误
    └── provisioning/
        ├── dashboards/dashboard.yml
        └── datasources/prometheus.yml
```

**告警规则**

| 告警名 | 级别 | 触发条件 |
|--------|------|---------|
| ServiceDown | critical | up == 0 超过1分钟 |
| HighErrorRate | warning | 5分钟内5xx超过10次 |
| HighLatency | warning | P99延迟超过1秒 |

**其他修复**
- go mod tidy 移到所有 go get 之后，解决依赖拉取失败问题
- configs/components.yaml 清理 helloword 历史遗留条目

---

## `dtk init` 完整生成结构（v当前）

```
<n>/
├── cmd/<n>/main.go
├── internal/
│   ├── api/handler.go, server.go
│   ├── auth/auth.go, middleware.go, rbac.go
│   ├── metrics/metrics.go
│   └── pkg/code/code.go
├── test/e2e/, integration/, smoke/
├── scripts/test_api.sh
├── build/docker/<n>/Dockerfile, build.sh
├── configs/components.yaml, project.env
├── docs/swagger.yaml
├── monitoring/
│   ├── prometheus/prometheus.yml, rules/<n>.yml
│   ├── alertmanager/alertmanager.yml
│   └── grafana/dashboards/<n>.json, provisioning/
├── deployments/<n>/
├── snapshots/README.md
├── .github/workflows/ci.yml
├── go.mod (go 1.25)
└── go.sum
```

## auto go get 列表

- github.com/stretchr/testify@latest
- github.com/golang-jwt/jwt/v5
- golang.org/x/crypto/bcrypt
- github.com/prometheus/client_golang/prometheus
- go mod tidy（最后执行）
