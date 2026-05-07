// internal/scheduler/cba.go — v3.2: Cell-based Architecture
//
// CBA 将故障处理按 Workload-class 差异化：
//   - Stateless Cell: v2.5 接管模式（快速接管，故障不可见）
//   - Stateful Cell: 就地隔离/自愈（故障可见但数据完整）
//
// v3.2 交付: 类型系统 + Workload-class 判定 + Cell-to-Pod 映射 + Fencing 协议

package scheduler

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"
)

// ─── Cell 类型 ────────────────────────────────────────────────────

// CellClass 表示 workload 的有状态程度。
type CellClass string

const (
	CellStateless CellClass = "stateless" // Deployment/Service/ConfigMap，可安全接管
	CellStateful  CellClass = "stateful"  // StatefulSet/PVC/DB，需 Fencing 协议
)

// Cell 表示一个故障隔离单元。
type Cell struct {
	Name      string            // Cell 标识
	Class     CellClass         // Stateless / Stateful
	Shard     int               // 所属分片
	Resources []string          // 管理的 K8s 资源名列表
	Labels    map[string]string // 匹配标签
}

// ─── Workload-class 自动判定 ─────────────────────────────────────

// ClassifyWorkload 根据 K8s 资源类型 + PVC 挂载判定 CellClass。
// 规则：
//   - 包含 StatefulSet → Stateful
//   - 包含 PersistentVolumeClaim → Stateful
//   - 其他 → Stateless
func ClassifyWorkload(resources []string, hasPVC bool) CellClass {
	for _, r := range resources {
		lower := strings.ToLower(r)
		if strings.Contains(lower, "statefulset") {
			return CellStateful
		}
	}
	if hasPVC {
		return CellStateful
	}
	return CellStateless
}

// ─── Fencing 协议 ────────────────────────────────────────────────

// FencingPhase 表示 Fencing 协议的阶段。
type FencingPhase string

const (
	FencingNone      FencingPhase = ""           // 未进入 Fencing
	FencingRequested FencingPhase = "requested"  // 已请求 Fencing（打 annotation）
	FencingDraining  FencingPhase = "draining"   // 业务容器排空中
	FencingPaused    FencingPhase = "paused"     // Dual-Path: 超时暂挂，不 flush WAL
	FencingReady     FencingPhase = "ready"      // 排空完成，可以安全终止
	FencingComplete  FencingPhase = "complete"   // Fencing 完成，旧 Pod 可删除
)

// SignalProtocol 抽象 Fencing 信号传递方式。
// v3.2: 支持 AnnotationWatch + Webhook。
// v3.3: 扩展 gRPC + SIGTERM drain fallback。
type SignalProtocol string

const (
	SignalAnnotationWatch SignalProtocol = "annotation-watch" // Pod annotation Watch（默认）
	SignalWebhook         SignalProtocol = "webhook"          // HTTP POST 到业务容器 /fence
	SignalSIGTERM         SignalProtocol = "sigterm"          // Fallback: SIGTERM drain（通用，不依赖 etcd learner）
)

// FencingConfig 配置 Fencing 信号方式与重试策略。
type FencingConfig struct {
	Protocol     SignalProtocol // 首选信号方式
	RetryMax     int           // 最大重试次数（默认 3）
	RetryBackoff time.Duration // 重试间隔（默认 5s）
	Timeout      time.Duration // 软超时（默认 15min），超时→FencingPaused
	HardTimeout  time.Duration // 硬超时（默认 60min），超时→强制释放，防止 Draining 卡死永久锁定 Cell
}

// FallbackChain returns the ordered fallback protocol chain.
// AnnotationWatch → Webhook → SIGTERM.
// If the primary protocol fails after RetryMax attempts, the caller should
// try the next protocol in the chain.
func (fc *FencingConfig) FallbackChain() []SignalProtocol {
	switch fc.Protocol {
	case SignalAnnotationWatch:
		return []SignalProtocol{SignalAnnotationWatch, SignalWebhook, SignalSIGTERM}
	case SignalWebhook:
		return []SignalProtocol{SignalWebhook, SignalSIGTERM}
	case SignalSIGTERM:
		return []SignalProtocol{SignalSIGTERM}
	default:
		return []SignalProtocol{SignalAnnotationWatch, SignalWebhook, SignalSIGTERM}
	}
}

// NextProtocol returns the next fallback protocol after current.
// ok=false means current is the last resort (no further fallback).
func (fc *FencingConfig) NextProtocol(current SignalProtocol) (next SignalProtocol, ok bool) {
	chain := fc.FallbackChain()
	for i, p := range chain {
		if p == current && i+1 < len(chain) {
			return chain[i+1], true
		}
	}
	return "", false
}

// FencingState 表示一个 Stateful Cell 迁移的 Fencing 状态。
type FencingState struct {
	PodNS      string
	PodName    string
	Phase      FencingPhase
	StartedAt  string // RFC3339
	ReadyAt    string // fencing-ready annotation 时间
}

// FencingAnnotationBundle 返回 Stateful Cell Fencing 的 annotation 集合。
func FencingAnnotationBundle(state *FencingState) map[string]string {
	return map[string]string{
		"kubepivot.io/fencing-phase": string(state.Phase),
		"kubepivot.io/fencing-ready": boolToString(state.Phase == FencingReady),
	}
}

// IsPointOfNoReturn 判断是否已超过不可逆点。
// Stateful 迁移一旦进入 Draining 阶段，原则上不允许自动回滚。
func (f *FencingState) IsPointOfNoReturn() bool {
	return f.Phase == FencingDraining || f.Phase == FencingReady || f.Phase == FencingComplete
}

// ShouldForceRelease 判断是否应该强制释放（硬超时）。
// 当处于 Draining/Paused 超过 hardTimeout 时，允许跳过正常排空流程，
// 避免死 Pod 永久阻塞调度流水线。
func (f *FencingState) ShouldForceRelease(cfg *FencingConfig) bool {
	if cfg == nil || cfg.HardTimeout <= 0 {
		return false
	}
	if f.Phase != FencingDraining && f.Phase != FencingPaused {
		return false
	}
	startedAt, err := time.Parse(time.RFC3339, f.StartedAt)
	if err != nil {
		return true // 时间戳损坏，保守：强制释放
	}
	return time.Since(startedAt) > cfg.HardTimeout
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// ─── Cell-to-Pod 映射（一致性哈希）────────────────────────────

// ringNode is a point on the consistent hash ring.
type ringNode struct {
	hash uint32 // FNV32a hash position
	pod  string // controller pod name
}

// HashRing 真一致性哈希环。
// 每个 Pod 映射到 vnodes 个虚拟节点散布在环上。
// 当 Pod 列表变化时，仅 ~1/N 的 Cell 所有权发生漂移。
type HashRing struct {
	vnodes int        // 每个 Pod 的虚拟节点数（默认 40）
	nodes  []ringNode // 按 hash 排序的环节点
}

// NewHashRing 创建一致性哈希环（等权）。
func NewHashRing(pods []string, vnodes int) *HashRing {
	weights := make(map[string]int, len(pods))
	for _, p := range pods {
		weights[p] = vnodes
	}
	return NewWeightedHashRing(pods, weights, vnodes)
}

// NewWeightedHashRing 创建加权一致性哈希环。
// weights[pod] 决定该 Pod 的虚拟节点数比例。H100 (权重 80) 比 A100 (权重 40) 多一倍虚拟节点，
// 被分配到的 Cell 比例也高一倍，减少 GPU 密集型 Cell 的跨代迁移。
// 未出现在 weights 中的 Pod 默认为 1×baseVnodes。
func NewWeightedHashRing(pods []string, weights map[string]int, baseVnodes int) *HashRing {
	if baseVnodes <= 0 {
		baseVnodes = 40
	}
	r := &HashRing{vnodes: baseVnodes}
	r.buildWeighted(pods, weights)
	return r
}

func (r *HashRing) build(pods []string) {
	weights := make(map[string]int, len(pods))
	for _, p := range pods {
		weights[p] = r.vnodes
	}
	r.buildWeighted(pods, weights)
}

func (r *HashRing) buildWeighted(pods []string, weights map[string]int) {
	if len(pods) == 0 {
		r.nodes = nil
		return
	}
	total := 0
	for _, p := range pods {
		w := weights[p]
		if w <= 0 {
			w = r.vnodes
		}
		total += w
	}
	r.nodes = make([]ringNode, 0, total)
	for _, pod := range pods {
		w := weights[pod]
		if w <= 0 {
			w = r.vnodes
		}
		for i := 0; i < w; i++ {
			h := fnv.New32a()
			h.Write([]byte(fmt.Sprintf("%s#v%d", pod, i)))
			r.nodes = append(r.nodes, ringNode{hash: h.Sum32(), pod: pod})
		}
	}
	sort.Slice(r.nodes, func(i, j int) bool {
		return r.nodes[i].hash < r.nodes[j].hash
	})
}

// GetPod 返回 key 在环上顺时针找到的第一个 Pod。
func (r *HashRing) GetPod(key string) string {
	if len(r.nodes) == 0 {
		return ""
	}
	h := fnv.New32a()
	h.Write([]byte(key))
	hash := h.Sum32()

	// Binary search: first node with hash >= keyHash
	idx := sort.Search(len(r.nodes), func(i int) bool {
		return r.nodes[i].hash >= hash
	})
	if idx == len(r.nodes) {
		idx = 0 // wrap around
	}
	return r.nodes[idx].pod
}

// DiffCells 计算 Pod 列表变更后哪些 Cell 的所有权会发生漂移。
// 返回需要 Takeover 的 Cell 名列表。
func (r *HashRing) DiffCells(cells []*Cell, newPods []string) []string {
	newRing := NewHashRing(newPods, r.vnodes)
	var moved []string
	for _, c := range cells {
		oldPod := r.GetPod(c.Name)
		newPod := newRing.GetPod(c.Name)
		if oldPod != newPod && newPod != "" {
			moved = append(moved, c.Name)
		}
	}
	return moved
}

// CellPodMapping 将 Cell 映射到 Controller Pod。
type CellPodMapping struct {
	CellName  string // Cell 标识
	PodName   string // 负责该 Cell 的 Controller Pod 名
	IsPrimary bool   // 是否为主 Pod（接管优先）
}

// MapCellsToPods 将一组 Cell 通过一致性哈希环分配到 Pod 列表。
// 每个 Pod 分配 40 个虚拟节点，Pod 扩缩容时仅 ~1/N Cell 漂移。
func MapCellsToPods(cells []*Cell, podNames []string) []*CellPodMapping {
	if len(cells) == 0 || len(podNames) == 0 {
		return nil
	}

	ring := NewHashRing(podNames, 40)
	mappings := make([]*CellPodMapping, 0, len(cells))
	for _, cell := range cells {
		mappings = append(mappings, &CellPodMapping{
			CellName:  cell.Name,
			PodName:   ring.GetPod(cell.Name),
			IsPrimary: true,
		})
	}
	return mappings
}

// CellHealth 表示一个 Cell 的健康状态。
type CellHealth struct {
	CellName       string
	Healthy        bool
	FaultCount     int       // 故障次数
	LastFaultAt    string    // 最后一次故障时间 RFC3339
	OwnerPod       string    // 当前负责 Pod
	TakeoverCount  int       // 被接管次数
}

// ShouldTakeover 判断当前 Pod 是否应该接管该 Cell。
// Stateless Cell: 任何其他 Pod 可接管（秒级）
// Stateful Cell: 仅在原 Pod 失效且 fencing 完成后接管（分钟级）
func ShouldTakeover(cell *Cell, health *CellHealth, myPod string, allPods []string) bool {
	if health.OwnerPod == myPod {
		return false // 已经是我的
	}
	if !health.Healthy {
		if cell.Class == CellStateless {
			return true // 秒级接管
		}
		// Stateful: 等 fencing 完成（由外部 MigrationManager 状态决定）
		return false
	}
	return false
}

