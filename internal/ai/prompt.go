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
	Name     string   `json:"name"`
	Port     int      `json:"port"`
	Image    string   `json:"image"`
	Replicas int      `json:"replicas"`
	CPU      string   `json:"cpu"`
	Memory   string   `json:"memory"`
	Storage  string   `json:"storage"`
	Profile  string   `json:"profile,omitempty"`
	Deps     []string `json:"deps,omitempty"`
	Source   string   `json:"source,omitempty"`
}

// BuildPrompt 构建发给 LLM 的 prompt。
// v2.0: LLM 角色从"数值决定者"变为"框架构建者"。
func BuildPrompt(ctx *RepoContext) string {
	var sb strings.Builder

	sb.WriteString(`你是一个云原生项目的资源规划专家。
我会给你一个项目的代码仓库信息，请帮我分析项目结构，生成 components.yaml 框架。

## 重要：v2.0 角色转变

你不再负责决定 CPU/Memory/GPU 的具体数值。你的职责是：
1. 组件拓扑：项目有哪些服务、它们的依赖关系
2. 每个组件的 profile（web/batch/db/gpu）
3. 哪些字段应该走自动 sizing

具体数值由 KubePivot 的 sizing 引擎基于 Prometheus 历史数据 + GPU 检测填充。
因此，cpu/memory 字段填 "auto"，不要猜具体数值。

## 输出格式要求

只输出 JSON，不要有任何其他内容，不要有 markdown 代码块标记，格式如下：

{
  "components": [
    {
      "name": "服务名",
      "port": 8080,
      "image": "镜像名",
      "replicas": 1,
      "profile": "web",
      "cpu": "auto",
      "memory": "auto",
      "storage": "1Gi",
      "deps": ["redis"]
    }
  ],
  "reasoning": "简短说明你的判断依据，中文，不超过 200 字"
}

## 字段说明

- name: 服务名（小写，和代码目录一致）
- port: 服务端口
- image: 镜像名（纯 CLI 工具设为空字符串）
- replicas: 副本数（默认 1，你仍可建议）
- profile: web | batch | db | gpu
  - web: HTTP/API 服务，CPU 优先
  - batch: 定时任务/数据处理，内存优先
  - db: 数据库/缓存，内存优先
  - gpu: GPU 推理/训练任务（检查上下文中的 GPU 库）
- cpu/memory: 请不要猜数值，填 "auto" 即可
- storage: 有持久化需求时填写，无状态填 "0"
- deps: 依赖的其他服务名列表

## profile 推断指南

- 提供 HTTP/gRPC API → web
- 定时任务/队列消费 → batch
- 检测到 torch/tensorflow/vllm/jax → gpu
- 不确定 → web

## 注意事项

- 不要把 postgres/etcd/controller 等基础设施组件列进来
- 如果上下文中有 GPU 库检测结果，相关组件 profile 应设为 gpu
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

	// v2.0: GPU 库检测结果
	if len(ctx.GPULibs) > 0 {
		sb.WriteString("## GPU 库检测结果\n\n")
		sb.WriteString("以下 GPU 相关库在项目中检测到：\n")
		for _, lib := range ctx.GPULibs {
			sb.WriteString(fmt.Sprintf("- %s\n", lib))
		}
		sb.WriteString("\n请将使用这些库的服务 profile 设为 gpu。\n\n")
	}

	// v2.0: Docker 基础镜像
	if len(ctx.DockerBaseImages) > 0 {
		sb.WriteString("## Docker 基础镜像\n\n")
		for _, img := range ctx.DockerBaseImages {
			sb.WriteString(fmt.Sprintf("- %s\n", img))
		}
		sb.WriteString("\n")
	}

	// 现有 components.yaml
	if ctx.ExistingPlan != "" {
		sb.WriteString("## 现有 components.yaml（供参考）\n\n```yaml\n")
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

	sb.WriteString("请根据以上信息生成 components.yaml 框架，只输出 JSON：")
	return sb.String()
}

// ParsePlan 解析 LLM 返回的 JSON
func ParsePlan(raw string) (*AIPlan, error) {
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

	// v2.0: 补全默认值。数值字段默认 "auto"（由 sizing 引擎填充），不再硬编码。
	for i := range plan.Components {
		c := &plan.Components[i]
		if c.Replicas == 0 {
			c.Replicas = 1
		}
		if c.CPU == "" {
			c.CPU = "auto"
		}
		if c.Memory == "" {
			c.Memory = "auto"
		}
		if c.Storage == "" {
			c.Storage = "1Gi"
		}
		if c.Profile == "" {
			c.Profile = "web"
		}
		c.Source = "llm-estimated"
	}

	return &plan, nil
}

// RenderComponentsYAML 把 AIPlan 渲染成 components.yaml 格式
func RenderComponentsYAML(plan *AIPlan) string {
	var sb strings.Builder
	sb.WriteString("# 由 kp ai-plan 自动生成\n")
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
		if c.Profile != "" {
			sb.WriteString(fmt.Sprintf("    profile: %s\n", c.Profile))
		}
		if c.Source != "" && c.Source != "llm-estimated" {
			sb.WriteString(fmt.Sprintf("    # source: %s\n", c.Source))
		}
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}
