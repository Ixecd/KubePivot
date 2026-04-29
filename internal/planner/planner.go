package planner

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Component 组件定义
type Component struct {
	Name        string
	Type        string // deployment（默认）/ statefulset
	Image       string
	Port        int
	Replicas    int
	CPU         string
	Memory      string
	Storage     string
	DependsOn   []string // 依赖的服务名列表
	Strategy    string   // rolling（默认）/ blue-green / canary
	MinReplicas int
	MaxReplicas int
	TargetCPU   int    // HPA 目标 CPU 使用率（0=不启用）
	APIVersion  string // 服务对外 API 版本，用于 kp compat 依赖检查
	Namespace   string
	CrossNsDeps []string // 跨 namespace 依赖，格式 "other-ns/svc-name"

	// YAML tag 用 sizing 保持配置文件可读性
	// 指针类型：nil = 不启用，使用默认行为 (mode=manual)
	Sizing *SizingConfig `yaml:"sizing,omitempty" json:"sizing,omitempty"`
}

// SizingConfig 定义资源优化策略
type SizingConfig struct {
	// Mode: auto (部署前自动计算并建议) / manual (用户手动配置，默认)
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"` // "auto" | "manual"
	// Profile: 业务模板，驱动得分权重 (web / batch / db / default)
	Profile string `yaml:"profile,omitempty" json:"profile,omitempty"`
	// Force: 是否自动应用建议 (不等待用户 git diff 确认)
	// ⚠️ 生产环境慎用，默认 false
	Force bool `yaml:"force,omitempty" json:"force,omitempty"`
	// Samples: 采样次数 (2-10)，默认 5
	Samples *int `yaml:"samples,omitempty" json:"samples,omitempty"`
	// Interval: 采样间隔，默认 2s
	Interval *string `yaml:"interval,omitempty" json:"interval,omitempty"` // "2s", "500ms", etc.
	// Window: Prometheus 查询时间窗口，默认 "7d"
	// 格式: duration string (e.g., "1h", "7d", "30d")
	Window *string `yaml:"window,omitempty" json:"window,omitempty"`
	// Step: Prometheus 查询步长，默认 "15m"
	// 格式: duration string (e.g., "1m", "5m", "15m")
	Step *string `yaml:"step,omitempty" json:"step,omitempty"`
	// Threshold: 置信度阈值，默认 0.7
	// 范围: 0.0-1.0, 低于此值不自动应用建议
	Threshold *float64 `yaml:"threshold,omitempty" json:"threshold,omitempty"`
	// AutoProfile: 是否自动推荐业务模板 (基于资源使用模式分析)
	// 默认: false (保持向后兼容，用户需显式开启)
	AutoProfile *bool `yaml:"auto_profile,omitempty" json:"auto_profile,omitempty"`
	// WeightLearning: 是否启用自适应权重学习 (基于历史利用率 CV)
	// 默认: false (保持向后兼容)
	WeightLearning *bool `yaml:"weight_learning,omitempty" json:"weight_learning,omitempty"`
}

// Plan 单个组件的部署计划
type Plan struct {
	Name        string
	Type        string
	Replicas    int
	CPU         string
	Memory      string
	Storage     string
	Image       string
	Port        int
	DependsOn   []string
	Strategy    string // rolling（默认）/ blue-green / canary
	MinReplicas int
	MaxReplicas int
	TargetCPU   int // HPA 目标 CPU 使用率（0=不启用）
	APIVersion  string
	Namespace   string
	CrossNsDeps []string
	Sizing      *SizingConfig `yaml:"-" json:"-"` // 不序列化，仅内存传递
}

// Layer 拓扑排序后的一层（同层可并行部署）
type Layer []Plan

// yamlComponents 对应 components.yaml 的结构
type yamlComponents struct {
	Components []struct {
		Name        string   `yaml:"name"`
		Type        string   `yaml:"type"`
		Image       string   `yaml:"image"`
		Port        int      `yaml:"port"`
		Replicas    int      `yaml:"replicas"`
		CPU         string   `yaml:"cpu"`
		Memory      string   `yaml:"memory"`
		Storage     string   `yaml:"storage"`
		Strategy    string   `yaml:"strategy"`
		MinReplicas int      `yaml:"min_replicas"`
		MaxReplicas int      `yaml:"max_replicas"`
		TargetCPU   int      `yaml:"target_cpu"`
		DependsOn   []string `yaml:"depends_on"`
		APIVersion  string   `yaml:"api_version"`
		Namespace   string   `yaml:"namespace"`
	} `yaml:"components"`
}

func LoadComponents(path string) ([]Component, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open components config: %w", err)
	}

	var raw yamlComponents
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse components config: %w", err)
	}

	components := make([]Component, 0, len(raw.Components))
	for _, c := range raw.Components {
		t := c.Type
		if t == "" {
			t = "deployment"
		}
		var localDeps []string
		var crossDeps []string
		for _, dep := range c.DependsOn {
			if strings.Contains(dep, "/") {
				crossDeps = append(crossDeps, dep)
			} else {
				localDeps = append(localDeps, dep)
			}
		}
		components = append(components, Component{
			Name:        c.Name,
			Type:        t,
			Image:       c.Image,
			Port:        c.Port,
			Replicas:    c.Replicas,
			CPU:         c.CPU,
			Memory:      c.Memory,
			Storage:     c.Storage,
			Strategy:    c.Strategy,
			MinReplicas: c.MinReplicas,
			MaxReplicas: c.MaxReplicas,
			TargetCPU:   c.TargetCPU,
			Namespace:   c.Namespace,
			DependsOn:   localDeps, // 只有同 namespace 的
			CrossNsDeps: crossDeps, // 跨 namespace 的
		})
	}
	return components, nil
}

func EstimateReplicas(component Component) int {
	if component.Replicas > 0 {
		return component.Replicas
	}
	return 1
}

func EstimateResources(component Component) (cpu string, memory string, storage string) {
	cpu = component.CPU
	memory = component.Memory
	storage = component.Storage
	if cpu == "" {
		cpu = "100m"
	}
	if memory == "" {
		memory = "128Mi"
	}
	if storage == "" {
		storage = "1Gi"
	}
	return cpu, memory, storage
}

// BuildPlan 构建部署计划（扁平列表，保持原有接口兼容）
func BuildPlan(path string) ([]Plan, error) {
	layers, err := BuildLayers(path)
	if err != nil {
		return nil, err
	}
	var plans []Plan
	for _, layer := range layers {
		plans = append(plans, layer...)
	}
	return plans, nil
}

// BuildLayers 构建拓扑分层部署计划
// 返回按依赖顺序排列的层级，同层可并行部署，层间必须串行
func BuildLayers(path string) ([]Layer, error) {
	components, err := LoadComponents(path)
	if err != nil {
		return nil, err
	}

	// 构建 Plan 列表
	planMap := make(map[string]Plan, len(components))
	for _, c := range components {
		cpu, memory, storage := EstimateResources(c)
		planMap[c.Name] = Plan{
			Name:        c.Name,
			Type:        c.Type,
			Replicas:    EstimateReplicas(c),
			CPU:         cpu,
			Memory:      memory,
			Storage:     storage,
			Image:       c.Image,
			Port:        c.Port,
			DependsOn:   c.DependsOn,
			Strategy:    c.Strategy,
			APIVersion:  c.APIVersion,
			Namespace:   c.Namespace,
			CrossNsDeps: c.CrossNsDeps,
			Sizing:      c.Sizing,
		}
	}

	// 验证 depends_on 里的服务名都存在
	for name, plan := range planMap {
		for _, dep := range plan.DependsOn {
			if _, ok := planMap[dep]; !ok {
				return nil, fmt.Errorf("服务 %s 依赖 %s，但 %s 未在 components.yaml 中定义", name, dep, dep)
			}
		}
	}

	// 拓扑排序（Kahn 算法），同时检测循环依赖
	return topoSort(components, planMap)
}

// topoSort Kahn 算法拓扑排序，返回分层结果
func topoSort(components []Component, planMap map[string]Plan) ([]Layer, error) {
	// 计算每个节点的入度
	inDegree := make(map[string]int, len(components))
	// 反向邻接表：dep → 依赖 dep 的服务列表
	dependents := make(map[string][]string, len(components))

	for _, c := range components {
		if _, ok := inDegree[c.Name]; !ok {
			inDegree[c.Name] = 0
		}
		for _, dep := range c.DependsOn {
			inDegree[c.Name]++
			dependents[dep] = append(dependents[dep], c.Name)
		}
	}

	// 初始队列：入度为 0 的节点（无依赖）
	var queue []string
	// 保持原始顺序
	for _, c := range components {
		if inDegree[c.Name] == 0 {
			queue = append(queue, c.Name)
		}
	}

	var layers []Layer
	visited := 0

	for len(queue) > 0 {
		// 当前队列的所有节点形成一层（可并行）
		layer := make(Layer, 0, len(queue))
		for _, name := range queue {
			layer = append(layer, planMap[name])
			visited++
		}
		layers = append(layers, layer)

		// 处理当前层的所有节点，更新下一层
		var nextQueue []string
		for _, name := range queue {
			for _, dependent := range dependents[name] {
				inDegree[dependent]--
				if inDegree[dependent] == 0 {
					nextQueue = append(nextQueue, dependent)
				}
			}
		}
		queue = nextQueue
	}

	// 如果 visited < 总节点数，说明有循环依赖
	if visited < len(components) {
		var cycle []string
		for name, deg := range inDegree {
			if deg > 0 {
				cycle = append(cycle, name)
			}
		}
		return nil, fmt.Errorf("检测到循环依赖，涉及服务：%v", cycle)
	}

	return layers, nil
}

// Downstream 找出 target 服务的所有下游服务（含自身），逆拓扑顺序
// 用于级联 rollback：失败服务 + 所有依赖它的服务
func Downstream(layers []Layer, target string) []string {
	// 构建服务 → 其下游的映射
	dependents := make(map[string][]string)
	for _, layer := range layers {
		for _, plan := range layer {
			for _, dep := range plan.DependsOn {
				dependents[dep] = append(dependents[dep], plan.Name)
			}
		}
	}

	// BFS 找出所有下游
	visited := map[string]bool{target: true}
	queue := []string{target}
	var result []string

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		result = append(result, cur)
		for _, d := range dependents[cur] {
			if !visited[d] {
				visited[d] = true
				queue = append(queue, d)
			}
		}
	}

	// 逆序（先 rollback 最下游）
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// resolveSizingParams 解析 SizingConfig 参数，返回可用值 + 错误
// 优先级: YAML 字段 > 命令行 flag > 硬编码默认
// 返回值: (window, step time.Duration), (threshold float64), error
func resolveSizingParams(cfg *SizingConfig, flagWindow, flagStep time.Duration, flagThreshold float64) (time.Duration, time.Duration, float64, error) {
	// 默认值 (硬编码兜底)
	defaultWindow := 7 * 24 * time.Hour // 7d
	defaultStep := 15 * time.Minute     // 15m
	defaultThreshold := 0.7             // 70% 置信度

	// 解析 Window
	window := defaultWindow
	if cfg != nil && cfg.Window != nil {
		if d, err := time.ParseDuration(*cfg.Window); err == nil {
			window = d
		}
	}
	if flagWindow > 0 {
		window = flagWindow // 命令行覆盖 YAML
	}

	// 解析 Step
	step := defaultStep
	if cfg != nil && cfg.Step != nil {
		if d, err := time.ParseDuration(*cfg.Step); err == nil {
			step = d
		}
	}
	if flagStep > 0 {
		step = flagStep
	}

	// 解析 Threshold
	threshold := defaultThreshold
	if cfg != nil && cfg.Threshold != nil {
		if *cfg.Threshold >= 0.0 && *cfg.Threshold <= 1.0 {
			threshold = *cfg.Threshold
		}
	}
	if flagThreshold > 0.0 {
		threshold = flagThreshold
	}

	// 校验: window >= step
	if window < step {
		return 0, 0, 0, fmt.Errorf("window (%v) must be >= step (%v)", window, step)
	}

	return window, step, threshold, nil
}
