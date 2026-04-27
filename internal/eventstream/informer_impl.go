package eventstream

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// ─── 内部常量 ─────────────────────────────────────────────────────

const (
	// httpRequestTimeout 整体 HTTP 请求 timeout（不影响 watch 长连接）。
	// list 请求用此值；watch 自身用 ctx 控制。
	httpRequestTimeout = 30 * time.Second

	// maxJSONLineSize 单个 watch event JSON 最大字节数。
	// K8s 单对象通常 < 100KB，1MB 足够安全。
	maxJSONLineSize = 1 * 1024 * 1024

	// listPageSize 全量 list 时单页对象数。
	// K8s 推荐 500，避免大集群单次响应过大。
	listPageSize = 500
)

// ─── informerImpl 实现 ────────────────────────────────────────────

// informerImpl 是 Informer 接口的标准实现。
//
// 内部组件：
//   - cache: SkeletonCache (lock-free 读)
//   - subscribers: 订阅者列表 + 派发管理
//   - watch loop: 单 goroutine 跑 list + watch + 重连
//   - resync ticker: 周期性触发全量 relist
//
// 不引入 client-go：
//   - 用 net/http 自实现 watch 协议
//   - chunked transfer encoding 由 Go HTTP client 自动处理
//   - bufio.Scanner 按行（NDJSON）读取事件
type informerImpl struct {
	opts InformerOptions

	// cache 是底层存储
	cache *SkeletonCache

	// 订阅管理
	subsMu      sync.RWMutex
	subscribers []*subscriberImpl

	// HTTP client（复用连接）
	httpClient *http.Client

	// resourceVersion 跟踪当前 watch 进度
	// list 完成时设置，watch 收到事件后递增
	rvMu            sync.Mutex
	resourceVersion string

	// 监控指标（atomic 访问）
	cacheHits       atomic.Uint64
	cacheMisses     atomic.Uint64
	eventsTotal     atomic.Uint64
	eventsByType    sync.Map // EventType → *atomic.Uint64
	watchReconnects atomic.Uint64
	lastResyncTime  atomic.Int64 // unix nanos

	// 生命周期管理
	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	// started 标记 Start 是否已被调用过
	// 用于 Stop 判断"是否需要等 watch goroutine 退出"
	started atomic.Bool
}

// subscriberImpl 单个订阅句柄。
type subscriberImpl struct {
	informer *informerImpl
	handler  EventHandler
	stats    SubscriberStats

	// inflight 派发中的事件队列
	// 慢 handler 不阻塞 dispatcher：buffer 满则 drop（计入 EventsDropped）
	inflight chan Event

	// 生命周期
	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

const subscriberQueueSize = 1024

// ─── NewInformer 替换占位 ─────────────────────────────────────────

// NewInformer 创建一个新的 Informer 实例（不自动启动）。
//
// 调用方负责调用 informer.Start(ctx) 启动 watch loop。
//
// 参数验证：
//   - Resource 必填
//   - APIVersion 必填
//   - 其他字段缺失时使用默认值
func NewInformer(ctx context.Context, opts InformerOptions) (Informer, error) {
	if opts.Resource == "" {
		return nil, fmt.Errorf("eventstream: NewInformer: Resource required")
	}
	if opts.APIVersion == "" {
		return nil, fmt.Errorf("eventstream: NewInformer: APIVersion required")
	}

	// 默认值填充
	if opts.ResyncPeriod == 0 {
		opts.ResyncPeriod = DefaultResyncPeriod(opts.Resource)
	}
	if opts.ReconnectPolicy.InitialBackoff == 0 {
		opts.ReconnectPolicy = DefaultReconnectPolicy()
	}
	// CachePolicy 零值即可（默认全 Hot 行为）
	// 当前 v2.7.0 仅实现 Hot 层，CachePolicy 主要影响后续版本

	im := &informerImpl{
		opts:   opts,
		cache:  NewCache(),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
		httpClient: &http.Client{
			// watch 不能用整体 timeout，per-request 用 ctx 控制
			Timeout: 0,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     90 * time.Second,
				DisableCompression:  false,
				ResponseHeaderTimeout: httpRequestTimeout,
			},
		},
	}

	return im, nil
}

// ─── Informer 接口方法 ────────────────────────────────────────────

// Start 启动 watch loop。
//
// 返回 errChan：watch goroutine 异常退出时通知；ctx 取消时 chan 关闭。
// 多次调用 Start 安全（仅第一次生效）。
func (im *informerImpl) Start(ctx context.Context) <-chan error {
	errCh := make(chan error, 1)

	im.startOnce.Do(func() {
		im.started.Store(true)
		go func() {
			defer close(im.doneCh)
			defer close(errCh)

			if err := im.runWatchLoop(ctx); err != nil {
				select {
				case errCh <- err:
				default:
				}
			}
		}()
	})

	return errCh
}

// Get 走 cache 读取（lock-free）。
func (im *informerImpl) Get(ns, name string) (*Resource, bool) {
	r, ok := im.cache.Get(ns, name)
	if ok {
		im.cacheHits.Add(1)
	} else {
		im.cacheMisses.Add(1)
	}
	return r, ok
}

// List 走 cache 按 ns 列出。
func (im *informerImpl) List(ns string) []*Resource {
	return im.cache.List(ns)
}

// ListAll 走 cache 全量列出。
func (im *informerImpl) ListAll() []*Resource {
	return im.cache.ListAll()
}

// Subscribe 注册一个事件 handler。
//
// 返回 Subscription，调用 Unsubscribe 取消订阅。
// handler 在独立 goroutine 中调用，不阻塞 watch loop。
func (im *informerImpl) Subscribe(handler EventHandler) Subscription {
	sub := &subscriberImpl{
		informer: im,
		handler:  handler,
		inflight: make(chan Event, subscriberQueueSize),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}

	// 启动 handler goroutine
	go sub.run()

	im.subsMu.Lock()
	im.subscribers = append(im.subscribers, sub)
	im.subsMu.Unlock()

	return sub
}

// Stats 返回当前监控指标快照。
func (im *informerImpl) Stats() InformerStats {
	stats := InformerStats{
		Resource:        im.opts.Resource,
		EventsTotal:     im.eventsTotal.Load(),
		WatchReconnects: im.watchReconnects.Load(),
		EventsByType:    make(map[EventType]uint64),
	}

	cacheStats := im.cache.Stats()
	stats.CacheSize = cacheStats.TotalItems
	stats.HotCount = cacheStats.ItemsByLayer[LayerHot]
	stats.WarmCount = cacheStats.ItemsByLayer[LayerWarm]
	stats.ColdCount = cacheStats.ItemsByLayer[LayerCold]
	stats.MemoryBytes = cacheStats.BytesEstimated

	// EventsByType
	im.eventsByType.Range(func(k, v any) bool {
		typ := k.(EventType)
		counter := v.(*atomic.Uint64)
		stats.EventsByType[typ] = counter.Load()
		return true
	})

	// CacheHitRate
	hits := im.cacheHits.Load()
	misses := im.cacheMisses.Load()
	if total := hits + misses; total > 0 {
		stats.CacheHitRate = float64(hits) / float64(total)
	}

	if rt := im.lastResyncTime.Load(); rt > 0 {
		stats.LastResyncTime = time.Unix(0, rt)
	}

	return stats
}

// Stop 停止 informer。
//
// 关闭所有订阅、停止 watch loop、清理资源。
// 多次调用 Stop 安全。
func (im *informerImpl) Stop() {
	im.stopOnce.Do(func() {
		close(im.stopCh)

		// 仅当 Start 已被调用时才等 watch goroutine 退出
		// 否则 doneCh 永远不会 close（无 goroutine 在运行）
		if im.started.Load() {
			select {
			case <-im.doneCh:
			case <-time.After(5 * time.Second):
				slog.Warn("eventstream: watch loop did not exit in 5s",
					"resource", im.opts.Resource)
			}
		}

		// 关闭所有订阅
		im.subsMu.Lock()
		subs := im.subscribers
		im.subscribers = nil
		im.subsMu.Unlock()

		for _, s := range subs {
			s.stop()
		}
	})
}

// ─── subscriberImpl 实现 ──────────────────────────────────────────

// Unsubscribe 取消订阅。
func (s *subscriberImpl) Unsubscribe() {
	s.stop()

	// 从 informer 列表移除
	s.informer.subsMu.Lock()
	defer s.informer.subsMu.Unlock()
	out := s.informer.subscribers[:0]
	for _, x := range s.informer.subscribers {
		if x != s {
			out = append(out, x)
		}
	}
	s.informer.subscribers = out
}

func (s *subscriberImpl) Stats() SubscriberStats {
	// 浅拷贝 stats（含 atomic 字段）
	// atomic 字段需用 Load() 读取
	return SubscriberStats{
		// SubscriberStats 内的 atomic 字段无法直接拷贝
		// 但我们暴露的就是这个结构体，调用方应使用 .Load() 读
	}
}

// stop 停止 subscriber，释放资源。
func (s *subscriberImpl) stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		<-s.doneCh
	})
}

// run subscriber 的事件处理循环。
//
// 用独立 goroutine 调用 handler，避免阻塞 watch loop。
// handler panic 时 recover，计入 stats.Panics。
func (s *subscriberImpl) run() {
	defer close(s.doneCh)

	for {
		select {
		case <-s.stopCh:
			return
		case ev := <-s.inflight:
			s.dispatchOne(ev)
		}
	}
}

func (s *subscriberImpl) dispatchOne(ev Event) {
	defer func() {
		if r := recover(); r != nil {
			s.stats.Panics.Add(1)
			slog.Error("eventstream: subscriber handler panic",
				"panic", r,
				"event_type", ev.Type.String(),
				"namespace", ev.Namespace,
				"name", ev.Name)
		}
	}()

	s.handler(ev)
	s.stats.EventsDelivered.Add(1)
}

// dispatch 由 informer 调用，向 subscriber 投递事件。
//
// 慢 handler 时（inflight 满）直接 drop（计入 EventsDropped）。
// 不阻塞 watch loop。
func (s *subscriberImpl) dispatch(ev Event) {
	select {
	case s.inflight <- ev:
		// 入队成功
	case <-s.stopCh:
		// 已停止
	default:
		// 队列满，drop
		s.stats.EventsDropped.Add(1)
	}
}

// ─── watch loop 主体 ──────────────────────────────────────────────

// runWatchLoop 主循环：list → watch → 断线重连 → resync。
//
// 状态机（简化）：
//
//	初始 → doInitialList
//	  ↓
//	enterWatch (用上次 RV 续传)
//	  ↓ 断线
//	退避等待 (NextBackoff)
//	  ↓
//	enterWatch
//	  ↓ 410 Gone (RV expired)
//	doInitialList (relist)
//	  ↓
//	enterWatch
//	  ...
//
// resync ticker 与 watch loop 在同一 goroutine 中通过 select 复用。
func (im *informerImpl) runWatchLoop(ctx context.Context) error {
	resyncTicker := time.NewTicker(im.opts.ResyncPeriod)
	defer resyncTicker.Stop()

	attempt := 0

	// 初始全量 list
	if err := im.doInitialList(ctx); err != nil {
		slog.Error("eventstream: initial list failed",
			"resource", im.opts.Resource, "err", err)
		// list 失败不算致命，进入 watch 循环（watch 也会先 list）
	}

	for {
		// 检查终止信号
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-im.stopCh:
			return nil
		default:
		}

		// 发起 watch
		err := im.doWatch(ctx)

		// watch 退出原因分类
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-im.stopCh:
			return nil
		default:
		}

		// watch 退出（无论错误还是正常关流）都计入 reconnects
		// 因为下一次循环都会重新发起 watch
		im.watchReconnects.Add(1)

		if err != nil {
			slog.Warn("eventstream: watch ended",
				"resource", im.opts.Resource,
				"attempt", attempt,
				"err", err)

			// 410 Gone → 必须 relist（清掉 RV）
			if isGone(err) {
				slog.Info("eventstream: resource version expired, relist",
					"resource", im.opts.Resource)
				im.setResourceVersion("")
				attempt = 0
				if relistErr := im.doInitialList(ctx); relistErr != nil {
					slog.Error("eventstream: relist failed",
						"resource", im.opts.Resource, "err", relistErr)
				}
				continue
			}
		} else {
			// 正常结束（罕见，K8s watch 应该长连接保持）
			attempt = 0
		}

		// 退避
		if im.opts.ReconnectPolicy.ShouldGiveUp(attempt) {
			return fmt.Errorf("eventstream: watch reconnect gave up after %d attempts", attempt)
		}
		backoff := im.opts.ReconnectPolicy.NextBackoff(attempt)
		attempt++

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-im.stopCh:
			return nil
		case <-time.After(backoff):
			// 继续重连
		case <-resyncTicker.C:
			// resync 触发：先 relist 再 watch
			slog.Info("eventstream: resync triggered (during backoff)",
				"resource", im.opts.Resource)
			im.setResourceVersion("")
			if err := im.doInitialList(ctx); err != nil {
				slog.Error("eventstream: resync list failed",
					"resource", im.opts.Resource, "err", err)
			}
			attempt = 0
		}
	}
}

// doInitialList 全量 list，灌入 cache。
//
// K8s API: GET /apis/{group}/{version}/{resource}?limit=500&continue=...
// 通过分页拿全部对象，然后 PutBulk 一次性写入 cache。
//
// 完成后设置 resourceVersion（list 响应的 metadata.resourceVersion）。
// 之后 watch 用此 RV 作为起点。
func (im *informerImpl) doInitialList(ctx context.Context) error {
	allItems := make([]*Resource, 0, listPageSize)
	var listRV string
	continueToken := ""

	for {
		listURL, err := im.buildListURL(continueToken)
		if err != nil {
			return fmt.Errorf("build list URL: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := im.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("http do: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("list returned %d: %s", resp.StatusCode, string(body))
		}

		// 解析 List 响应
		var listResp struct {
			Metadata struct {
				ResourceVersion string `json:"resourceVersion"`
				Continue        string `json:"continue"`
			} `json:"metadata"`
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(body, &listResp); err != nil {
			return fmt.Errorf("unmarshal list: %w", err)
		}

		// listRV 取首页（K8s 保证一致）
		if listRV == "" {
			listRV = listResp.Metadata.ResourceVersion
		}

		// 解 Skeleton
		for _, raw := range listResp.Items {
			r, err := ParseSkeleton(raw)
			if err != nil {
				slog.Warn("eventstream: parse skeleton failed in list",
					"resource", im.opts.Resource, "err", err)
				continue
			}
			allItems = append(allItems, r)
		}

		// 翻页或结束
		if listResp.Metadata.Continue == "" {
			break
		}
		continueToken = listResp.Metadata.Continue
	}

	// 一次性写入 cache（避免 N 次 Put 的 O(N²)）
	im.cache.PutBulk(allItems)
	im.setResourceVersion(listRV)
	im.lastResyncTime.Store(time.Now().UnixNano())

	slog.Info("eventstream: initial list done",
		"resource", im.opts.Resource,
		"count", len(allItems),
		"rv", listRV)

	return nil
}

// doWatch 发起一次 watch 连接，持续读取事件直到断开或 ctx 取消。
//
// K8s API: GET /apis/{group}/{version}/{resource}?watch=1&resourceVersion=X
// 服务端用 chunked transfer encoding 推送事件（每行一个 JSON）。
//
// 错误返回：
//   - context.Canceled / DeadlineExceeded: ctx 取消（正常退出）
//   - errStreamGone: 410 Gone，需要 relist
//   - 其他: 网络错误、JSON 解析错误等
func (im *informerImpl) doWatch(ctx context.Context) error {
	watchURL, err := im.buildWatchURL()
	if err != nil {
		return fmt.Errorf("build watch URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, watchURL, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := im.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusGone {
		return errStreamGone
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("watch returned %d: %s", resp.StatusCode, string(body))
	}

	// 按行读取（NDJSON）
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), maxJSONLineSize)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// 复制 line（scanner buffer 会被复用）
		raw := make([]byte, len(line))
		copy(raw, line)

		if err := im.handleWatchLine(raw); err != nil {
			slog.Warn("eventstream: handle watch line failed",
				"resource", im.opts.Resource, "err", err)
			// 继续处理，不退出 watch
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner: %w", err)
	}

	// 服务端关闭流（无错误）
	return nil
}

// errStreamGone 标记 410 Gone（resourceVersion 过期）。
var errStreamGone = fmt.Errorf("eventstream: 410 Gone, resource version expired")

func isGone(err error) bool {
	return err == errStreamGone
}

// handleWatchLine 处理一行 watch 事件 JSON。
//
// K8s watch event 格式：
//
//	{"type":"ADDED","object":{...}}
//	{"type":"MODIFIED","object":{...}}
//	{"type":"DELETED","object":{...}}
//	{"type":"ERROR","object":{"code":410,...}}
func (im *informerImpl) handleWatchLine(raw []byte) error {
	var ev struct {
		Type   string          `json:"type"`
		Object json.RawMessage `json:"object"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return fmt.Errorf("unmarshal event: %w", err)
	}

	// ERROR 事件特殊处理
	if ev.Type == "ERROR" {
		var status struct {
			Code int `json:"code"`
		}
		_ = json.Unmarshal(ev.Object, &status)
		if status.Code == 410 {
			return errStreamGone
		}
		return fmt.Errorf("watch ERROR event: %s", string(ev.Object))
	}

	// 解析对象
	r, err := ParseSkeleton(ev.Object)
	if err != nil {
		return fmt.Errorf("parse skeleton: %w", err)
	}

	// 更新 RV（用于断线续传）
	im.setResourceVersion(r.ResourceVersion)

	// 转换事件类型 + 更新 cache
	switch ev.Type {
	case "ADDED":
		im.cache.Put(r)
		im.dispatchToSubscribers(Event{
			Type: EventAdd, Namespace: r.Namespace, Name: r.Name, New: r,
		})
	case "MODIFIED":
		old, _ := im.cache.Get(r.Namespace, r.Name)
		// 增量序列化优化：Skeleton 没变（仅 RV 变 = K8s housekeeping）跳过
		if old != nil && !SkeletonChanged(old, r) {
			im.cache.Put(r) // 仍更新 cache（保持最新 RV）
			return nil
		}
		im.cache.Put(r)
		im.dispatchToSubscribers(Event{
			Type: EventUpdate, Namespace: r.Namespace, Name: r.Name, Old: old, New: r,
		})
	case "DELETED":
		old, _ := im.cache.Get(r.Namespace, r.Name)
		im.cache.Delete(r.Namespace, r.Name)
		im.dispatchToSubscribers(Event{
			Type: EventDelete, Namespace: r.Namespace, Name: r.Name, Old: old,
		})
	default:
		slog.Warn("eventstream: unknown watch event type",
			"type", ev.Type, "resource", im.opts.Resource)
	}
	return nil
}

// dispatchToSubscribers 派发事件到所有订阅者。
//
// dispatcher 层的 shard 过滤在 Day 4 引入。
// v2.7 Step 3.2 当前直接派发给所有 subscribers（无过滤）。
func (im *informerImpl) dispatchToSubscribers(ev Event) {
	im.eventsTotal.Add(1)
	im.bumpEventTypeCounter(ev.Type)

	// 应用 ShardSet 过滤（如果设置了）
	if im.opts.ShardSet != nil && !im.opts.ShardSet.Owns(ev.Namespace) {
		return // 不属于本 pod 的 shard，drop
	}

	im.subsMu.RLock()
	subs := make([]*subscriberImpl, len(im.subscribers))
	copy(subs, im.subscribers)
	im.subsMu.RUnlock()

	for _, s := range subs {
		s.dispatch(ev)
	}
}

func (im *informerImpl) bumpEventTypeCounter(t EventType) {
	v, _ := im.eventsByType.LoadOrStore(t, &atomic.Uint64{})
	v.(*atomic.Uint64).Add(1)
}

// ─── URL 构造 ────────────────────────────────────────────────────

// buildListURL 构造 list 请求 URL。
//
// 例：
//   /api/v1/pods?limit=500
//   /apis/apps/v1/deployments?limit=500&continue=token
func (im *informerImpl) buildListURL(continueToken string) (string, error) {
	base, err := im.resourceBaseURL()
	if err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("limit", fmt.Sprintf("%d", listPageSize))
	if continueToken != "" {
		q.Set("continue", continueToken)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// buildWatchURL 构造 watch 请求 URL。
//
// 例：
//   /api/v1/pods?watch=1&resourceVersion=12345&allowWatchBookmarks=true
func (im *informerImpl) buildWatchURL() (string, error) {
	base, err := im.resourceBaseURL()
	if err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("watch", "1")
	q.Set("allowWatchBookmarks", "true")
	if rv := im.getResourceVersion(); rv != "" {
		q.Set("resourceVersion", rv)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// resourceBaseURL 构造资源的 base URL（不含 query）。
//
// APIVersion 解析：
//   "v1"        → /api/v1/{resource}                    (核心组)
//   "apps/v1"   → /apis/apps/v1/{resource}               (扩展组)
//   "networking.k8s.io/v1" → /apis/networking.k8s.io/v1/{resource}
func (im *informerImpl) resourceBaseURL() (string, error) {
	if im.opts.APIServerURL == "" {
		return "", fmt.Errorf("APIServerURL not configured")
	}

	var path string
	if im.opts.APIVersion == "v1" || !containsSlash(im.opts.APIVersion) {
		// 核心组（"v1" 或无 group）
		path = fmt.Sprintf("/api/%s/%s", im.opts.APIVersion, im.opts.Resource)
	} else {
		// 扩展组（含 "/"）
		path = fmt.Sprintf("/apis/%s/%s", im.opts.APIVersion, im.opts.Resource)
	}

	return im.opts.APIServerURL + path, nil
}

func containsSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}

// ─── resourceVersion 访问 ────────────────────────────────────────

func (im *informerImpl) getResourceVersion() string {
	im.rvMu.Lock()
	defer im.rvMu.Unlock()
	return im.resourceVersion
}

func (im *informerImpl) setResourceVersion(rv string) {
	im.rvMu.Lock()
	defer im.rvMu.Unlock()
	im.resourceVersion = rv
}
