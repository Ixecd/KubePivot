package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"sync"

	"github.com/Ixecd/kubepivot/internal/state"
	"gopkg.in/yaml.v3"
)

// projectState 单个项目（namespace）在 controller 内存中的状态
//
// 由 Watcher 填充，由 Task Dispatcher / Worker Pool 读取
type projectState struct {
	Namespace string     // 项目所在 namespace
	Sha256    string     // resources.yaml 内容的 sha256（去 annotation 读取）
	Resources []Resource // 解析后的资源清单
}

// machineEntry 状态机缓存项
//
// 每个 *state.Machine 配一把 mutex，因为 state.Machine.Transition 不是 thread-safe。
// 多个 worker 同时处理同一个 project 的不同资源时，会拿到同一个 machine 实例。
// 用 Lock() 串行化 Transition 调用。
type machineEntry struct {
	machine *state.Machine
	mu      sync.Mutex
}

// GlobalState 全局 controller 的内存状态
//
// 设计要点（v2.3.0 → v2.4.0 演进）：
//   - 读多写少场景：RWMutex 合适
//   - ConfigMap watcher 写入（managed ns 变化 / CM 内容变化）
//   - Task Dispatcher / Worker Pool 读取
//   - sha256 比对在写入层做，避免无意义的 reconcile 唤起
//   - v2.4.0 新增：状态机缓存。每个 namespace 对应一个 *state.Machine 复用，
//     避免每次 reconcile task 都新建。同时给状态机访问做并发同步。
type GlobalState struct {
	mu       sync.RWMutex
	projects map[string]*projectState // key = namespace

	// v2.4.0 新增：状态机缓存
	// key = namespace（v2.3.0 默认 project ≡ namespace）
	// 单独 mutex，独立于 projects 的锁，减小锁粒度
	machinesMu sync.Mutex
	machines   map[string]*machineEntry
}

// NewGlobalState 构造空的全局状态
func NewGlobalState() *GlobalState {
	return &GlobalState{
		projects: make(map[string]*projectState),
		machines: make(map[string]*machineEntry),
	}
}

// UpsertProject 更新或新增一个项目的状态
//
// 参数 resourcesYAML 是 ConfigMap 里 data["resources.yaml"] 的原始内容。
// 返回 changed 告诉调用方是否真的变了（sha256 比对）。
// 如果 yaml 解析失败，保留旧状态但返回 false + err（降级不阻断）。
//
// v2.4.0：成功路径会顺手预创建对应 namespace 的状态机（不影响返回值）
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

	// v2.4.0：项目第一次出现时预热状态机（已有则保留）
	g.ensureMachine(namespace)

	slog.Info("📋 项目状态已更新",
		"namespace", namespace,
		"resources", len(cfg.Resources),
		"sha256", newHash[:8]+"...")
	return true, nil
}

// RemoveProject 移除一个项目（namespace unlabel 或被删除时调用）
//
// v2.4.0：同步移除对应状态机（避免内存累积 + 防止陈旧状态影响 re-enroll）
func (g *GlobalState) RemoveProject(namespace string) bool {
	g.mu.Lock()
	_, ok := g.projects[namespace]
	if !ok {
		g.mu.Unlock()
		return false
	}
	delete(g.projects, namespace)
	g.mu.Unlock()

	// v2.4.0：连带清理状态机
	g.removeMachine(namespace)

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

// ════════════════════════════════════════════════════════════════════════════
//                          v2.4.0 新增：状态机缓存
// ════════════════════════════════════════════════════════════════════════════

// GetOrCreateMachine 获取或创建某个 namespace 的状态机
//
// 返回 (machine, lock, error)：
//   - machine: 已就绪的 state.Machine 实例
//   - lock: 调用方在使用 machine 前必须 lock，使用完 unlock
//          这把锁保护 machine 的 Transition 调用免受并发污染
//   - error: 状态机首次创建失败时返回（etcd / local 都不可用）
//
// 调用模式：
//
//	m, lock, err := gs.GetOrCreateMachine(ns, version)
//	if err != nil { return err }
//	lock.Lock()
//	defer lock.Unlock()
//	// 在锁内安全调用 m.Transition / m.Current 等方法
func (g *GlobalState) GetOrCreateMachine(namespace, version string) (*state.Machine, *sync.Mutex, error) {
	g.machinesMu.Lock()
	if entry, ok := g.machines[namespace]; ok {
		g.machinesMu.Unlock()
		return entry.machine, &entry.mu, nil
	}
	g.machinesMu.Unlock()

	// 不在缓存里 → 创建新的（在缓存外做，避免阻塞其他 namespace）
	store := state.NewAutoStore(os.Getenv("ETCD_ENDPOINTS"))
	sm, err := state.New(store, namespace, namespace, version)
	if err != nil {
		return nil, nil, err
	}

	// 双重检查：另一个 goroutine 可能已经创建好了
	g.machinesMu.Lock()
	defer g.machinesMu.Unlock()
	if entry, ok := g.machines[namespace]; ok {
		return entry.machine, &entry.mu, nil
	}
	entry := &machineEntry{machine: sm}
	g.machines[namespace] = entry
	slog.Info("🧠 状态机已加入缓存",
		"namespace", namespace,
		"current_state", sm.State())
	return entry.machine, &entry.mu, nil
}

// ensureMachine 项目首次出现或重新出现时预热状态机
// 失败时只 warn 不阻断，让后续 GetOrCreateMachine 在 task 路径上重试
func (g *GlobalState) ensureMachine(namespace string) {
	version := os.Getenv("VERSION")
	if version == "" {
		version = "latest"
	}
	if _, _, err := g.GetOrCreateMachine(namespace, version); err != nil {
		slog.Warn("状态机预热失败（task 阶段会重试）",
			"namespace", namespace, "err", err)
	}
}

// removeMachine 项目被移除时同步清理状态机缓存
func (g *GlobalState) removeMachine(namespace string) {
	g.machinesMu.Lock()
	defer g.machinesMu.Unlock()
	if _, ok := g.machines[namespace]; ok {
		delete(g.machines, namespace)
		slog.Info("🧠 状态机已从缓存移除", "namespace", namespace)
	}
}

// MachineCount 当前缓存的状态机数（用于观测）
func (g *GlobalState) MachineCount() int {
	g.machinesMu.Lock()
	defer g.machinesMu.Unlock()
	return len(g.machines)
}

// fingerprint 计算 resources.yaml 内容的 sha256 十六进制
func fingerprint(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}
