package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

func writeMonitoringSkeleton(outputDir, name string) error {
	rulesContent := fmt.Sprintf(`groups:
  - name: %s
    rules:
      - alert: ServiceDown
        expr: up{job="%s"} == 0
        for: 1m
        labels:
          severity: critical
          service: %s
        annotations:
          description: "%s 已停止响应，请立即检查"

      - alert: HighErrorRate
        expr: increase(%s_http_request_total{status=~"5.."}[5m]) > 10
        for: 2m
        labels:
          severity: warning
          service: %s
        annotations:
          description: "%s 过去5分钟 5xx 错误超过10次"

      - alert: HighLatency
        expr: histogram_quantile(0.99, rate(%s_http_request_duration_seconds_bucket[5m])) > 1
        for: 2m
        labels:
          severity: warning
          service: %s
        annotations:
          description: "%s P99 延迟超过1秒"
`, name, name, name, name, name, name, name, name, name, name)

	prometheusContent := fmt.Sprintf(`global:
  scrape_interval: 15s
  evaluation_interval: 15s

rule_files:
  - rules/*.yml

alerting:
  alertmanagers:
    - static_configs:
        - targets: ['localhost:9093']

scrape_configs:
  - job_name: 'prometheus'
    static_configs:
      - targets: ['localhost:9090']

  - job_name: '%s'
    scrape_interval: 10s
    metrics_path: '/metrics'
    static_configs:
      - targets: ['localhost:8080']
        labels:
          environment: "local-dev"
          service: "%s"
`, name, name)

	alertmanagerContent := `# Alertmanager 配置
# 注意: bot_token 等敏感信息不要提交到 git

global:
  resolve_timeout: 5m

route:
  receiver: 'telegram'
  group_by: ['alertname', 'service']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 12h

receivers:
  - name: 'telegram'
    telegram_configs:
      - bot_token: '<YOUR_BOT_TOKEN>'
        chat_id: <YOUR_CHAT_ID>
        message: |
          🚨 *{{ .GroupLabels.alertname }}*
          {{ range .Alerts }}
          *状态*: {{ .Status }}
          *服务*: {{ .Labels.service }}
          *描述*: {{ .Annotations.description }}
          {{ end }}
        parse_mode: 'Markdown'
`

	readmeContent := fmt.Sprintf(`# %s 监控告警

## 目录结构

`+"```"+`
monitoring/
├── prometheus/
│   ├── prometheus.yml
│   └── rules/%s.yml
├── alertmanager/
│   └── alertmanager.yml
└── grafana/
    ├── dashboards/%s.json
    └── provisioning/
        ├── dashboards/dashboard.yml
        └── datasources/prometheus.yml
`+"```"+`

## 告警规则

| 告警名 | 级别 | 触发条件 |
|--------|------|---------|
| ServiceDown | critical | 服务不可达超过1分钟 |
| HighErrorRate | warning | 5分钟内5xx超过10次 |
| HighLatency | warning | P99延迟超过1秒 |
`, name, name, name)

	files := map[string]string{
		filepath.Join(outputDir, "monitoring", "prometheus", "rules", name+".yml"): rulesContent,
		filepath.Join(outputDir, "monitoring", "alertmanager", "alertmanager.yml"): alertmanagerContent,
		filepath.Join(outputDir, "monitoring", "prometheus", "prometheus.yml"):     prometheusContent,
		filepath.Join(outputDir, "monitoring", "grafana", "provisioning", "datasources", "prometheus.yml"): `apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    url: http://localhost:9090
    isDefault: true
`,
		filepath.Join(outputDir, "monitoring", "grafana", "provisioning", "dashboards", "dashboard.yml"): `apiVersion: 1
providers:
  - name: default
    folder: ''
    type: file
    options:
      path: /etc/grafana/dashboards
`,
		filepath.Join(outputDir, "monitoring", "grafana", "dashboards", name+".json"): fmt.Sprintf(`{
  "title": "%s Dashboard",
  "uid": "%s",
  "panels": [
    {
      "title": "HTTP 请求总数",
      "type": "stat",
      "targets": [{"expr": "sum(rate(%s_http_request_total[5m]))", "legendFormat": "req/s"}]
    },
    {
      "title": "HTTP 错误率",
      "type": "timeseries",
      "targets": [{"expr": "sum(rate(%s_http_request_total{status=~\"5..\"}[5m]))", "legendFormat": "5xx/s"}]
    },
    {
      "title": "P99 延迟",
      "type": "timeseries",
      "targets": [{"expr": "histogram_quantile(0.99, rate(%s_http_request_duration_seconds_bucket[5m]))", "legendFormat": "P99"}]
    },
    {
      "title": "业务错误",
      "type": "timeseries",
      "targets": [{"expr": "sum by(code) (rate(%s_business_error_total[5m]))", "legendFormat": "{{code}}"}]
    }
  ],
  "schemaVersion": 38,
  "version": 1
}`, name, name, name, name, name, name),
		filepath.Join(outputDir, "monitoring", "README.md"): readmeContent,
	}

	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("gen monitoring file %s: %w", path, err)
		}
	}
	return nil
}
