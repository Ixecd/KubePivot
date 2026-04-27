package eventstream

import (
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// ─── Prometheus Metrics 暴露 ───────────────────────────────────────
//
// InformerCollector 把多个 Informer 的运行指标暴露为 Prometheus 格式。
//
// 设计原则（v2.7 Day 5）：
//
//   1. 全局共享 desc（一组描述符服务所有 informer）
//      避免"每个 informer 一个 collector"导致的 desc 冲突
//      不同 informer 通过 resource label 区分
//
//   2. 仅暴露 informer 级别指标（不含 subscriber 级别）
//      理由：subscriber 没有 stable id，作为 label 会导致基数爆炸
//      未来 v2.7.1+ 可考虑暴露聚合后的 SubscriberStats
//
//   3. 不创建 HTTP server
//      调用方负责 promhttp.Handler() 接入 /metrics endpoint
//
//   4. 标签维度受控
//      所有指标统一含 resource label（如 "deployments" / "pods"）
//      events_total 额外含 type label（add/update/delete/resync）
//
// 使用模式：
//
//   单 informer:
//     informer, _ := eventstream.NewInformer(ctx, opts)
//     informer.Start(ctx)
//     eventstream.RegisterInformerMetrics(prometheus.DefaultRegisterer, informer)
//
//   多 informer:
//     deployInformer, _ := eventstream.NewInformer(ctx, deployOpts)
//     podInformer, _ := eventstream.NewInformer(ctx, podOpts)
//     eventstream.RegisterInformerMetrics(reg, deployInformer, podInformer)
//
//   动态增加（运行时）:
//     collector := eventstream.NewInformerCollector()
//     reg.Register(collector)
//     collector.AddInformer(newInformer)

const (
	// metricsNamespace 所有指标的统一前缀
	metricsNamespace = "kubepivot"
	metricsSubsystem = "informer"
)

// InformerCollector 实现 prometheus.Collector 接口。
//
// 持有一组 informer，遍历输出 metrics。
// 使用 resource label 区分不同 informer 的指标。
//
// 并发安全：
//   - informers 列表用 RWMutex 保护
//   - Collect 复制 informers 切片快照后释放锁，避免长持锁
//   - 各 informer 自身的 Stats() 已并发安全
type InformerCollector struct {
	mu        sync.RWMutex
	informers []Informer

	// 描述符（全局共享，启动时一次性创建，不可变）
	cacheSize       *prometheus.Desc
	cacheHotCount   *prometheus.Desc
	cacheWarmCount  *prometheus.Desc
	cacheColdCount  *prometheus.Desc
	eventsTotal     *prometheus.Desc
	watchReconnects *prometheus.Desc
	cacheHitRatio   *prometheus.Desc
	memoryBytes     *prometheus.Desc
	lastResyncTime  *prometheus.Desc
}

// NewInformerCollector 创建一个新的空 collector（未持有任何 informer）。
//
// 使用 AddInformer 添加 informer 后再注册到 registry：
//
//	collector := NewInformerCollector()
//	collector.AddInformer(deployInformer)
//	collector.AddInformer(podInformer)
//	prometheus.MustRegister(collector)
//
// 推荐配合 RegisterInformerMetrics 使用（一行注册多个 informer）。
func NewInformerCollector() *InformerCollector {
	return &InformerCollector{
		cacheSize: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, metricsSubsystem, "cache_size"),
			"Number of objects currently in informer cache.",
			[]string{"resource"}, nil,
		),
		cacheHotCount: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, metricsSubsystem, "cache_hot_count"),
			"Number of objects in cache hot layer (full skeleton + raw object).",
			[]string{"resource"}, nil,
		),
		cacheWarmCount: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, metricsSubsystem, "cache_warm_count"),
			"Number of objects in cache warm layer (skeleton + raw JSON).",
			[]string{"resource"}, nil,
		),
		cacheColdCount: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, metricsSubsystem, "cache_cold_count"),
			"Number of objects in cache cold layer (disk-backed).",
			[]string{"resource"}, nil,
		),
		eventsTotal: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, metricsSubsystem, "events_total"),
			"Total number of events processed by informer, partitioned by type.",
			[]string{"resource", "type"}, nil,
		),
		watchReconnects: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, metricsSubsystem, "watch_reconnects_total"),
			"Total number of watch reconnections (includes server-initiated closes).",
			[]string{"resource"}, nil,
		),
		cacheHitRatio: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, metricsSubsystem, "cache_hit_ratio"),
			"Cache hit ratio (range 0.0-1.0). 0 if no Get calls yet.",
			[]string{"resource"}, nil,
		),
		memoryBytes: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, metricsSubsystem, "memory_bytes"),
			"Estimated memory usage of informer cache in bytes.",
			[]string{"resource"}, nil,
		),
		lastResyncTime: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, metricsSubsystem, "last_resync_timestamp_seconds"),
			"Unix timestamp of last successful full resync. Not exposed before first resync.",
			[]string{"resource"}, nil,
		),
	}
}

// AddInformer 注册一个 informer 到 collector。
//
// 多次调用同一 informer 不去重（多次注册会导致 metrics 重复输出）。
// 调用方应自行保证不重复添加。
func (c *InformerCollector) AddInformer(informer Informer) {
	if informer == nil {
		return
	}
	c.mu.Lock()
	c.informers = append(c.informers, informer)
	c.mu.Unlock()
}

// RemoveInformer 移除一个 informer。
//
// 通过引用相等判断，移除后此 informer 的 metrics 不再输出。
// 未找到时不报错（幂等）。
func (c *InformerCollector) RemoveInformer(informer Informer) {
	if informer == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.informers[:0]
	for _, i := range c.informers {
		if i != informer {
			out = append(out, i)
		}
	}
	c.informers = out
}

// Describe 实现 prometheus.Collector 接口。
//
// 一次性输出所有指标的描述符。Prometheus 在注册时调用一次。
func (c *InformerCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.cacheSize
	ch <- c.cacheHotCount
	ch <- c.cacheWarmCount
	ch <- c.cacheColdCount
	ch <- c.eventsTotal
	ch <- c.watchReconnects
	ch <- c.cacheHitRatio
	ch <- c.memoryBytes
	ch <- c.lastResyncTime
}

// Collect 实现 prometheus.Collector 接口。
//
// 遍历所有 informer，从各自的 Stats() 读取数据并输出。
// 不同 informer 通过 resource label 区分。
//
// 行为说明：
//   - cache_size / hot/warm/cold_count: 始终输出
//   - events_total: 仅输出 EventsByType 中已有计数的事件类型
//   - last_resync_timestamp_seconds: 仅在已有过 resync 时输出
//     避免首次 resync 前出现 1970-01-01 的怪异数据
func (c *InformerCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	informers := make([]Informer, len(c.informers))
	copy(informers, c.informers)
	c.mu.RUnlock()

	for _, informer := range informers {
		c.collectOne(ch, informer)
	}
}

func (c *InformerCollector) collectOne(ch chan<- prometheus.Metric, informer Informer) {
	stats := informer.Stats()
	resource := stats.Resource

	// Cache 层级指标（Gauge，始终输出）
	ch <- prometheus.MustNewConstMetric(
		c.cacheSize, prometheus.GaugeValue,
		float64(stats.CacheSize), resource,
	)
	ch <- prometheus.MustNewConstMetric(
		c.cacheHotCount, prometheus.GaugeValue,
		float64(stats.HotCount), resource,
	)
	ch <- prometheus.MustNewConstMetric(
		c.cacheWarmCount, prometheus.GaugeValue,
		float64(stats.WarmCount), resource,
	)
	ch <- prometheus.MustNewConstMetric(
		c.cacheColdCount, prometheus.GaugeValue,
		float64(stats.ColdCount), resource,
	)

	// 事件计数器（按类型 partition）
	for typ, count := range stats.EventsByType {
		ch <- prometheus.MustNewConstMetric(
			c.eventsTotal, prometheus.CounterValue,
			float64(count),
			resource, strings.ToLower(typ.String()),
		)
	}

	// Watch 重连计数（Counter）
	ch <- prometheus.MustNewConstMetric(
		c.watchReconnects, prometheus.CounterValue,
		float64(stats.WatchReconnects), resource,
	)

	// Cache 命中率（Gauge，0.0-1.0）
	ch <- prometheus.MustNewConstMetric(
		c.cacheHitRatio, prometheus.GaugeValue,
		stats.CacheHitRate, resource,
	)

	// 内存占用（Gauge，bytes）
	ch <- prometheus.MustNewConstMetric(
		c.memoryBytes, prometheus.GaugeValue,
		float64(stats.MemoryBytes), resource,
	)

	// 最后 resync 时间戳（仅在已有过 resync 时输出）
	if !stats.LastResyncTime.IsZero() {
		ch <- prometheus.MustNewConstMetric(
			c.lastResyncTime, prometheus.GaugeValue,
			float64(stats.LastResyncTime.Unix()), resource,
		)
	}
}

// RegisterInformerMetrics 一行代码注册多个 informer 的所有指标到给定 registry。
//
// 单 informer:
//
//	RegisterInformerMetrics(prometheus.DefaultRegisterer, informer)
//
// 多 informer（一次性）:
//
//	RegisterInformerMetrics(reg, deployInformer, podInformer, serviceInformer)
//
// 内部创建一个 InformerCollector，添加所有 informer，注册到 registry。
// 后续如需动态增减 informer，应使用 NewInformerCollector + AddInformer 模式。
//
// 错误情形：
//   - reg 为 nil → ErrNilRegisterer
//   - informers 为空 → ErrNilInformer
//   - registry 内部错误 → 透传
func RegisterInformerMetrics(reg prometheus.Registerer, informers ...Informer) error {
	if reg == nil {
		return ErrNilRegisterer
	}
	if len(informers) == 0 {
		return ErrNilInformer
	}
	for _, i := range informers {
		if i == nil {
			return ErrNilInformer
		}
	}

	collector := NewInformerCollector()
	for _, i := range informers {
		collector.AddInformer(i)
	}
	return reg.Register(collector)
}

// ─── 错误定义 ─────────────────────────────────────────────────────

// ErrNilRegisterer RegisterInformerMetrics 调用时 registerer 参数为 nil。
var ErrNilRegisterer = &metricsError{msg: "eventstream: registerer is nil"}

// ErrNilInformer RegisterInformerMetrics 调用时未提供任何 informer。
var ErrNilInformer = &metricsError{msg: "eventstream: no informer provided or informer is nil"}

type metricsError struct {
	msg string
}

func (e *metricsError) Error() string {
	return e.msg
}
