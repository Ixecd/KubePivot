package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sync"

	"gopkg.in/yaml.v3"
)

// projectState 单个项目（namespace）在 controller 内存中的状态
//
// 由 Watcher 填充，由 Task Dispatcher / Worker Pool 读取
type projectState struct {
	Namespace string     // 项目所在 namespace
	Sha256    string     // resources.yaml 内容的 sha256（去 annotation 读取）
	Resources []Resource // 解析后的资源清单
	// 未来可扩展：Version / EnrolledAt / LastReconciled / ...
}

// GlobalState 全局 controller 的内存状态
//
// 设计要点：
//   - 读多写少场景：RWMutex 合适
//   - ConfigMap watcher 写入（managed ns 变化 / CM 内容变化）
//   - Task Dispatcher / Worker Pool 读取
//   - sha256 比对在写入层做，避免无意义的 reconcile 唤起
type GlobalState struct {
	mu       sync.RWMutex
	projects map[string]*projectState // key = namespace
}

// NewGlobalState 构造空的全局状态
func NewGlobalState() *GlobalState {
	return &GlobalState{
		projects: make(map[string]*projectState),
	}
}

// UpsertProject 更新或新增一个项目的状态
//
// 参数 resourcesYAML 是 ConfigMap 里 data["resources.yaml"] 的原始内容。
// 返回 changed 告诉调用方是否真的变了（sha256 比对）。
// 如果 yaml 解析失败，保留旧状态但返回 false + err（降级不阻断）。
func (g *GlobalState) UpsertProject(namespace, resourcesYAML string) (changed bool, err error) {
	if IsProtectedNamespace(namespace) {
		slog.Warn("🛡 拒绝对 protected namespace 建立状态",
			"namespace", namespace)
		return false, nil
	}

	newHash := fingerprint(resourcesYAML)

	g.mu.RLock()
	old, exists := g.projects[namespace]
	g.mu.RUnlock()

	// 指纹相同 → 内容未变 → 不用 reparse
	if exists && old.Sha256 == newHash {
		return false, nil
	}

	var cfg ResourcesConfig
	if err := yaml.Unmarshal([]byte(resourcesYAML), &cfg); err != nil {
		slog.Warn("resources.yaml 解析失败，保留旧状态",
			"namespace", namespace, "err", err)
		return false, err
	}

	// 补全 namespace 字段
	for i := range cfg.Resources {
		if cfg.Resources[i].Namespace == "" {
			cfg.Resources[i].Namespace = namespace
		}
	}

	g.mu.Lock()
	g.projects[namespace] = &projectState{
		Namespace: namespace,
		Sha256:    newHash,
		Resources: cfg.Resources,
	}
	g.mu.Unlock()

	slog.Info("📋 项目状态已更新",
		"namespace", namespace,
		"resources", len(cfg.Resources),
		"sha256", newHash[:8]+"...")
	return true, nil
}

// RemoveProject 移除一个项目（namespace unlabel 或被删除时调用）
func (g *GlobalState) RemoveProject(namespace string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.projects[namespace]; !ok {
		return false
	}
	delete(g.projects, namespace)
	slog.Info("🗑 项目状态已移除", "namespace", namespace)
	return true
}

// ListProjects 返回所有被管理的 namespace 列表（排序不保证）
func (g *GlobalState) ListProjects() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make([]string, 0, len(g.projects))
	for ns := range g.projects {
		result = append(result, ns)
	}
	return result
}

// GetProject 返回某个项目的资源清单副本（线程安全，返回的是新切片）
func (g *GlobalState) GetProject(namespace string) ([]Resource, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p, ok := g.projects[namespace]
	if !ok {
		return nil, false
	}
	// 返回副本，防止外部修改
	result := make([]Resource, len(p.Resources))
	copy(result, p.Resources)
	return result, true
}

// ProjectCount 当前被管理的项目数
func (g *GlobalState) ProjectCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.projects)
}

// GetSha256 返回某个项目当前的 sha256（用于 watcher 对比 ConfigMap annotation 快速 skip）
func (g *GlobalState) GetSha256(namespace string) (string, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p, ok := g.projects[namespace]
	if !ok {
		return "", false
	}
	return p.Sha256, true
}

// fingerprint 计算 resources.yaml 内容的 sha256 十六进制
func fingerprint(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}
