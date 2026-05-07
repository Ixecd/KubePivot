// internal/eventstream/cache_layer.go — v3.2: PodCache Hot/Warm 分层
//
// 自研轻量分层（不引入 ristretto 外部依赖）。
// Hot 层：访问频率 >3/min → 保持在 PodEntry 完整结构
// Warm 层：低频访问 → 可压缩为 PodEntryCompact（仅保留 Namespace/Name/NodeName/Phase）
//
// 激活条件：生产 pprof 显示 Go GC >10% CPU 时启用 Warm 压缩。
// 默认关闭（分层开销 > 当前 3.7x 内存优势的边际收益）。

package eventstream

import (
	"sync"
	"time"
)

// AccessTracker tracks per-key access frequency for Hot/Warm tiering.
type AccessTracker struct {
	mu       sync.Mutex
	accesses map[string]*accessEntry // key → access record
}

type accessEntry struct {
	count      int
	lastAccess time.Time
	promoted   bool // true = Hot tier
}

// NewAccessTracker creates an access tracker. Not active until StartGC is called.
func NewAccessTracker() *AccessTracker {
	return &AccessTracker{accesses: make(map[string]*accessEntry)}
}

// Record marks a key as accessed. Returns true if the key should be promoted to Hot.
func (t *AccessTracker) Record(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.accesses[key]
	if !ok {
		t.accesses[key] = &accessEntry{count: 1, lastAccess: time.Now()}
		return false
	}
	e.count++
	e.lastAccess = time.Now()
	if !e.promoted && e.count >= 3 {
		e.promoted = true
		return true // promote to Hot
	}
	return false
}

// DemoteCold returns keys that haven't been accessed in the last 30 minutes.
// These can be compressed to Warm tier (PodEntryCompact).
func (t *AccessTracker) DemoteCold(now time.Time) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	coldThreshold := now.Add(-30 * time.Minute)
	var cold []string
	for k, e := range t.accesses {
		if e.lastAccess.Before(coldThreshold) {
			cold = append(cold, k)
		}
	}
	for _, k := range cold {
		delete(t.accesses, k)
	}
	return cold
}

// PodEntryCompact is a compressed PodEntry for Warm tier.
// Only keeps identity + scheduling-critical fields.
// Labels are dropped (rebuilt from CommonLabelKeys on re-promotion).
type PodEntryCompact struct {
	Namespace string
	Name      string
	NodeName  string
	Phase     string
	Requests  ResourceRequest
	RV        int64
}

// Compact compresses a PodEntry to Warm tier.
func (e *PodEntry) Compact() *PodEntryCompact {
	return &PodEntryCompact{
		Namespace: e.Namespace,
		Name:      e.Name,
		NodeName:  e.NodeName,
		Phase:     e.Phase,
		Requests:  e.Requests,
		RV:        e.RV,
	}
}

// Expand restores a PodEntry from Warm tier (labels are empty — caller should re-fetch if needed).
func (c *PodEntryCompact) Expand() *PodEntry {
	return &PodEntry{
		Namespace: c.Namespace,
		Name:      c.Name,
		NodeName:  c.NodeName,
		Phase:     c.Phase,
		Requests:  c.Requests,
		RV:        c.RV,
	}
}
