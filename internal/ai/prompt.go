package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AIPlan LLM 返回的规划结果
type AIPlan struct {
	Components []AIComponent `json:"components"`
	Reasoning  string        `json:"reasoning"`
}

// AIComponent 单个组件的资源规划
type AIComponent struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Image    string `json:"image"`
	Replicas int    `json:"replicas"`
	CPU      string `json:"cpu"`
	Memory   string `json:"memory"`
	Storage  string `json:"storage"`
}

// BuildPrompt 构建发给 LLM 的 prompt
func BuildPrompt(ctx *RepoContext) string {
	var sb strings.Builder

	sb.WriteString(`你是一个 Go 云原生项目的资源规划专家。
我会给你一个项目的代码仓库信息，请帮我分析项目结构，生成合理的 components.yaml 配置。

## 输出格式要求

只输出 JSON，不要有任何其他内容，不要有 markdown 代码块标记，格式如下：

{
  "components": [
    {
      "name": "服务名（小写，和 cmd/ 下目录名一致）",
      "port": 8080,
      "image": "镜像名（和服务名一致，纯 CLI 工具设为空字符串）",
      "replicas": 1,
      "cpu": "100m",
      "memory": "128Mi",
      "storage": "1Gi"
    }
  ],
  "reasoning": "简短说明你的判断依据，中文，不超过 200 字"
}

## 规划原则

- replicas：默认 1，生产环境高可用服务建议 2+
- cpu：轻量服务 100m，中等业务 200-500m，高负载 1000m+
- memory：最小 64Mi，普通服务 128-256Mi，有缓存或高并发 512Mi+
- storage：有持久化需求时填写，纯无状态服务填 "0"
- image：如果是纯 CLI 工具（不对外提供 HTTP 服务）填空字符串，dtk 会跳过 build/push
- 不要把 postgres/etcd/controller 等基础设施组件列进来，只列业务服务

`)

	// 项目目录结构
	sb.WriteString("## 项目目录结构\n\n```\n")
	sb.WriteString(ctx.DirTree)
	sb.WriteString("```\n\n")

	// go.mod
	if ctx.GoMod != "" {
		sb.WriteString("## go.mod\n\n```\n")
		sb.WriteString(ctx.GoMod)
		sb.WriteString("\n```\n\n")
	}

	// 发现的服务
	if len(ctx.Services) > 0 {
		sb.WriteString("## cmd/ 下发现的服务\n\n")
		for _, svc := range ctx.Services {
			sb.WriteString(fmt.Sprintf("### %s\n\n", svc.Name))
			if svc.MainGo != "" {
				sb.WriteString("```go\n")
				sb.WriteString(svc.MainGo)
				sb.WriteString("\n```\n\n")
			}
		}
	}

	// 现有 components.yaml
	if ctx.ExistingPlan != "" {
		sb.WriteString("## 现有 components.yaml（供参考，可更新）\n\n```yaml\n")
		sb.WriteString(ctx.ExistingPlan)
		sb.WriteString("\n```\n\n")
	}

	// Dockerfile
	if len(ctx.Dockerfiles) > 0 {
		sb.WriteString("## Dockerfile\n\n")
		for _, df := range ctx.Dockerfiles {
			sb.WriteString("```\n")
			sb.WriteString(df)
			sb.WriteString("\n```\n\n")
		}
	}

	// README
	if ctx.ReadmeSummary != "" {
		sb.WriteString("## README（前 50 行）\n\n```\n")
		sb.WriteString(ctx.ReadmeSummary)
		sb.WriteString("\n```\n\n")
	}

	// 用户补充描述
	if ctx.UserDescription != "" {
		sb.WriteString("## 用户补充描述\n\n")
		sb.WriteString(ctx.UserDescription)
		sb.WriteString("\n\n")
	}

	sb.WriteString("请根据以上信息生成 components.yaml 配置，只输出 JSON：")
	return sb.String()
}

// ParsePlan 解析 LLM 返回的 JSON
func ParsePlan(raw string) (*AIPlan, error) {
	// 清理可能的 markdown 代码块
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var plan AIPlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil, fmt.Errorf("解析 LLM 输出失败: %w\n原始输出:\n%s", err, raw)
	}

	if len(plan.Components) == 0 {
		return nil, fmt.Errorf("LLM 没有返回任何组件")
	}

	// 补全默认值
	for i := range plan.Components {
		c := &plan.Components[i]
		if c.Replicas == 0 {
			c.Replicas = 1
		}
		if c.CPU == "" {
			c.CPU = "100m"
		}
		if c.Memory == "" {
			c.Memory = "128Mi"
		}
		if c.Storage == "" {
			c.Storage = "1Gi"
		}
	}

	return &plan, nil
}

// RenderComponentsYAML 把 AIPlan 渲染成 components.yaml 格式
func RenderComponentsYAML(plan *AIPlan) string {
	var sb strings.Builder
	sb.WriteString("# 由 dtk ai-plan 自动生成\n")
	sb.WriteString("# LLM 分析依据：" + plan.Reasoning + "\n\n")
	sb.WriteString("components:\n")

	for _, c := range plan.Components {
		sb.WriteString(fmt.Sprintf("  - name: %s\n", c.Name))
		sb.WriteString(fmt.Sprintf("    port: %d\n", c.Port))
		sb.WriteString(fmt.Sprintf("    image: %s\n", c.Image))
		sb.WriteString(fmt.Sprintf("    replicas: %d\n", c.Replicas))
		sb.WriteString(fmt.Sprintf("    cpu: %s\n", c.CPU))
		sb.WriteString(fmt.Sprintf("    memory: %s\n", c.Memory))
		sb.WriteString(fmt.Sprintf("    storage: %s\n", c.Storage))
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}
