package eventstream

import (
	"context"
	"errors"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ═══════════════════════════════════════════════════════════════════
// fakeK8sServer 模拟 K8s API server 用于 watch loop 测试
// ═══════════════════════════════════════════════════════════════════

// fakeK8sServer 是 watch + list 的最小 K8s API server 模拟。
//
// 支持：
//   - GET /apis/{group}/{version}/{resource}     → list 响应
//   - GET /apis/{group}/{version}/{resource}?watch=1 → chunked watch
//   - 测试可注入 list 响应、watch 事件序列、强制断开
//
// 不支持（v2.7 段 B 范围外）：
//   - 真实的 resourceVersion 校验
//   - 分页 continue token（list 单页内一次返回全部）
//   - bookmark 事件
type fakeK8sServer struct {
	server *httptest.Server

	mu sync.Mutex

	// listResponse 下一次 list 请求的响应内容
	listResponse []byte

	// watchEvents watch 时按序推送的事件
	// 每个 string 是一个完整 JSON 行（不含末尾 \n）
	watchEvents []string

	// watchStarted watch 请求到达计数（用于测试可见性）
	watchStarted atomic.Uint64

	// listCalled list 请求到达计数
	listCalled atomic.Uint64

	// watchHoldCh 控制 watch 推送完事件后是否立即关闭
	// nil = 推送完立即关闭流
	// 非 nil = 等此 channel 关闭后才关流（允许测试在 watch 中插入断开）
	watchHoldCh chan struct{}

	// forceCloseAfterEvents 推送多少事件后强制关闭流（模拟断线）
	// 0 = 推完所有事件后正常关闭
	forceCloseAfterEvents int

	// listForceStatus list 请求的 HTTP 状态（0 = 200）
	listForceStatus int

	// watchForceStatus watch 请求的初始 HTTP 状态（0 = 200）
	// 设为 410 用于测试 errStreamGone
	watchForceStatus int
}

func newFakeK8sServer() *fakeK8sServer {
	f := &fakeK8sServer{}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeK8sServer) URL() string {
	return f.server.URL
}

func (f *fakeK8sServer) Close() {
	f.server.Close()
}

func (f *fakeK8sServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("watch") == "1" {
		f.handleWatch(w, r)
		return
	}
	f.handleList(w, r)
}

func (f *fakeK8sServer) handleList(w http.ResponseWriter, r *http.Request) {
	f.listCalled.Add(1)

	f.mu.Lock()
	body := f.listResponse
	status := f.listForceStatus
	f.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure"}`))
		return
	}

	if body == nil {
		// 默认空 list
		body = []byte(`{"metadata":{"resourceVersion":"0"},"items":[]}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (f *fakeK8sServer) handleWatch(w http.ResponseWriter, r *http.Request) {
	f.watchStarted.Add(1)

	f.mu.Lock()
	events := f.watchEvents
	holdCh := f.watchHoldCh
	closeAfter := f.forceCloseAfterEvents
	status := f.watchForceStatus
	f.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		// 410 Gone 时返回 status 对象（Go HTTP 客户端会读到状态码）
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)

	for i, ev := range events {
		_, err := fmt.Fprintln(w, ev)
		if err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}

		// 强制断开：发完 N 个事件就退出
		if closeAfter > 0 && i+1 >= closeAfter {
			return
		}
	}

	// 等 hold（测试控制何时关流）
	if holdCh != nil {
		<-holdCh
	}
	// 否则推完事件就关流
}

// SetList 配置下次 list 响应。
func (f *fakeK8sServer) SetList(items []map[string]interface{}, resourceVersion string) {
	resp := map[string]interface{}{
		"metadata": map[string]interface{}{
			"resourceVersion": resourceVersion,
		},
		"items": items,
	}
	body, _ := json.Marshal(resp)
	f.mu.Lock()
	f.listResponse = body
	f.mu.Unlock()
}

// SetWatchEvents 配置 watch 推送的事件序列。
//
// events 中每个元素是 K8s watch event：
//
//	{"type":"ADDED","object":{...}}
func (f *fakeK8sServer) SetWatchEvents(events []map[string]interface{}) {
	lines := make([]string, len(events))
	for i, e := range events {
		b, _ := json.Marshal(e)
		lines[i] = string(b)
	}
	f.mu.Lock()
	f.watchEvents = lines
	f.mu.Unlock()
}

// SetWatchHold 设置 watch hold channel（推完事件后等此 ch 关闭才关流）。
func (f *fakeK8sServer) SetWatchHold(ch chan struct{}) {
	f.mu.Lock()
	f.watchHoldCh = ch
	f.mu.Unlock()
}

// SetForceCloseAfter 推送 N 个事件后强制关流。
func (f *fakeK8sServer) SetForceCloseAfter(n int) {
	f.mu.Lock()
	f.forceCloseAfterEvents = n
	f.mu.Unlock()
}

// SetWatchStatus 强制 watch 请求返回指定 HTTP 状态。
func (f *fakeK8sServer) SetWatchStatus(status int) {
	f.mu.Lock()
	f.watchForceStatus = status
	f.mu.Unlock()
}

// ═══════════════════════════════════════════════════════════════════
// 测试辅助
// ═══════════════════════════════════════════════════════════════════

// makeDeploy 构造一个测试用 Deployment 对象（map 形式）。
func makeDeploy(ns, name, rv string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"namespace":       ns,
			"name":            name,
			"uid":             ns + "-" + name + "-uid",
			"resourceVersion": rv,
			"generation":      1,
		},
		"spec": map[string]interface{}{
			"replicas": 3,
		},
		"status": map[string]interface{}{
			"phase":         "Running",
			"readyReplicas": 3,
		},
	}
}

// watchEvent 构造一个 watch 事件（map 形式）。
func watchEvent(eventType string, obj map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":   eventType,
		"object": obj,
	}
}

// startInformer 启动 informer 并返回（含自动清理）。
func startInformer(t *testing.T, fake *fakeK8sServer) (Informer, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	informer, err := NewInformer(ctx, InformerOptions{
		Resource:     "deployments",
		APIVersion:   "apps/v1",
		APIServerURL: fake.URL(),
		ResyncPeriod: 1 * time.Hour, // 测试中默认不触发 resync
	})
	if err != nil {
		cancel()
		t.Fatalf("NewInformer: %v", err)
	}
	_ = informer.Start(ctx)

	t.Cleanup(func() {
		cancel()
		informer.Stop()
	})

	return informer, cancel
}

// waitFor 轮询条件直到 true 或超时。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitFor timeout: %s", msg)
}

// ═══════════════════════════════════════════════════════════════════
// 测试：未 Start 直接 Stop（修复 bug 验证）
// ═══════════════════════════════════════════════════════════════════

// TestNewInformer_AuthResolveFailure 验证 NewInformer 真的调 resolveK8sConfig。
//
// 当三种 auth 路径都失败时（不传 APIServerURL + 不传 KubeConfig + in-cluster
// 文件不可读），NewInformer 应返回 error。
//
// 此测试确保 auth.go 真的接入了 informer_impl，没有变成"代码挂在那但没人调"。
func TestNewInformer_AuthResolveFailure(t *testing.T) {
	// mock readTokenFile 返回 error，模拟 in-cluster 路径失败
	oldRead := readTokenFile
	readTokenFile = func() ([]byte, error) {
		return nil, errors.New("simulated in-cluster failure")
	}
	defer func() { readTokenFile = oldRead }()

	_, err := NewInformer(context.Background(), InformerOptions{
		Resource:   "deployments",
		APIVersion: "apps/v1",
		// 不传 APIServerURL，不传 KubeConfig → 走路径 3 → 应失败
	})
	if err == nil {
		t.Fatal("expected error when all auth paths fail")
	}
	// 错误应包含 NewInformer prefix（来自 informer_impl）+ auth prefix（来自 auth.go）
	if !strings.Contains(err.Error(), "NewInformer") {
		t.Errorf("error 应含 NewInformer prefix，得到: %v", err)
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("error 应含 auth prefix（说明真的调了 resolveK8sConfig），得到: %v", err)
	}
}

func TestInformer_StopWithoutStart(t *testing.T) {
	informer, err := NewInformer(context.Background(), InformerOptions{
		Resource:     "deployments",
		APIVersion:   "apps/v1",
		APIServerURL: "http://localhost:8001",
	})
	if err != nil {
		t.Fatalf("NewInformer: %v", err)
	}

	start := time.Now()
	informer.Stop()
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Errorf("Stop without Start 应立即返回，实际等了 %v", elapsed)
	}
}

func TestInformer_StopMultipleTimes(t *testing.T) {
	informer, err := NewInformer(context.Background(), InformerOptions{
		Resource:     "pods",
		APIVersion:   "v1",
		APIServerURL: "http://localhost:8001",
	})
	if err != nil {
		t.Fatalf("NewInformer: %v", err)
	}

	informer.Stop()
	informer.Stop() // 第二次不应 panic / hang
	informer.Stop()
}

// ═══════════════════════════════════════════════════════════════════
// 测试：初始 list 灌入 cache
// ═══════════════════════════════════════════════════════════════════

func TestInformer_InitialListPopulatesCache(t *testing.T) {
	fake := newFakeK8sServer()
	defer fake.Close()

	fake.SetList([]map[string]interface{}{
		makeDeploy("default", "app1", "100"),
		makeDeploy("default", "app2", "101"),
		makeDeploy("kube-system", "kube-dns", "50"),
	}, "200")

	informer, _ := startInformer(t, fake)

	// 等 list 完成
	waitFor(t, 2*time.Second, func() bool {
		return informer.Stats().CacheSize == 3
	}, "cache 应灌入 3 个对象")

	// 验证内容
	r, ok := informer.Get("default", "app1")
	if !ok {
		t.Fatal("Get(default, app1) 应返回 ok=true")
	}
	if r.Kind != "Deployment" {
		t.Errorf("Kind = %q, want Deployment", r.Kind)
	}

	if got := len(informer.List("default")); got != 2 {
		t.Errorf("List(default) len = %d, want 2", got)
	}
	if got := len(informer.List("kube-system")); got != 1 {
		t.Errorf("List(kube-system) len = %d, want 1", got)
	}
}

// ═══════════════════════════════════════════════════════════════════
// 测试：watch 事件分发到订阅者
// ═══════════════════════════════════════════════════════════════════

func TestInformer_WatchDispatchesEvents(t *testing.T) {
	fake := newFakeK8sServer()
	defer fake.Close()

	fake.SetList(nil, "100") // 空 list
	fake.SetWatchEvents([]map[string]interface{}{
		watchEvent("ADDED", makeDeploy("default", "new-app", "101")),
		watchEvent("MODIFIED", func() map[string]interface{} {
			d := makeDeploy("default", "new-app", "102")
			d["metadata"].(map[string]interface{})["generation"] = 2
			return d
		}()),
		watchEvent("DELETED", makeDeploy("default", "new-app", "103")),
	})

	informer, _ := startInformer(t, fake)

	// 收集事件
	var (
		eventsMu sync.Mutex
		received []Event
	)
	informer.Subscribe(func(e Event) {
		eventsMu.Lock()
		received = append(received, e)
		eventsMu.Unlock()
	})

	waitFor(t, 2*time.Second, func() bool {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		return len(received) >= 3
	}, "应收到 3 个事件")

	eventsMu.Lock()
	defer eventsMu.Unlock()

	if got := received[0].Type; got != EventAdd {
		t.Errorf("event[0].Type = %v, want EventAdd", got)
	}
	if got := received[1].Type; got != EventUpdate {
		t.Errorf("event[1].Type = %v, want EventUpdate", got)
	}
	if got := received[2].Type; got != EventDelete {
		t.Errorf("event[2].Type = %v, want EventDelete", got)
	}
}

// ═══════════════════════════════════════════════════════════════════
// 测试：Skeleton 未变（仅 RV 变）不触发 EventUpdate
// ═══════════════════════════════════════════════════════════════════

func TestInformer_SkipsHousekeepingUpdates(t *testing.T) {
	fake := newFakeK8sServer()
	defer fake.Close()

	fake.SetList([]map[string]interface{}{
		makeDeploy("default", "stable-app", "100"),
	}, "100")

	// MODIFIED 事件但仅 RV 变（K8s housekeeping）
	modified := makeDeploy("default", "stable-app", "101")
	fake.SetWatchEvents([]map[string]interface{}{
		watchEvent("MODIFIED", modified),
	})

	informer, _ := startInformer(t, fake)

	var updates atomic.Int64
	informer.Subscribe(func(e Event) {
		if e.Type == EventUpdate {
			updates.Add(1)
		}
	})

	// 等 watch 处理
	waitFor(t, 2*time.Second, func() bool {
		return fake.watchStarted.Load() >= 1
	}, "watch 应被发起")

	// 给 dispatch 一点时间
	time.Sleep(200 * time.Millisecond)

	if got := updates.Load(); got != 0 {
		t.Errorf("仅 RV 变化的 MODIFIED 不应触发 EventUpdate，got %d", got)
	}
}

// ═══════════════════════════════════════════════════════════════════
// 测试：watch 断线后自动重连
// ═══════════════════════════════════════════════════════════════════

func TestInformer_ReconnectsAfterDisconnect(t *testing.T) {
	fake := newFakeK8sServer()
	defer fake.Close()

	fake.SetList(nil, "100")
	fake.SetWatchEvents([]map[string]interface{}{
		watchEvent("ADDED", makeDeploy("default", "app1", "101")),
	})
	// 推送 1 个事件后强制关流（模拟断线）
	fake.SetForceCloseAfter(1)

	ctx, cancel := context.WithCancel(context.Background())
	informer, err := NewInformer(ctx, InformerOptions{
		Resource:     "deployments",
		APIVersion:   "apps/v1",
		APIServerURL: fake.URL(),
		ResyncPeriod: 1 * time.Hour,
		ReconnectPolicy: ReconnectPolicy{
			InitialBackoff: 50 * time.Millisecond, // 测试中加速
			MaxBackoff:     200 * time.Millisecond,
			BackoffFactor:  2.0,
			Jitter:         0,
		},
	})
	if err != nil {
		cancel()
		t.Fatalf("NewInformer: %v", err)
	}
	_ = informer.Start(ctx)
	defer func() {
		cancel()
		informer.Stop()
	}()

	// 等待至少 2 次 watch 调用（首次 + 重连）
	waitFor(t, 2*time.Second, func() bool {
		return fake.watchStarted.Load() >= 2
	}, "应触发至少一次重连")

	if got := informer.Stats().WatchReconnects; got < 1 {
		t.Errorf("WatchReconnects = %d, want >= 1", got)
	}
}

// ═══════════════════════════════════════════════════════════════════
// 测试：410 Gone 触发 relist
// ═══════════════════════════════════════════════════════════════════

func TestInformer_RelistOn410Gone(t *testing.T) {
	fake := newFakeK8sServer()
	defer fake.Close()

	fake.SetList([]map[string]interface{}{
		makeDeploy("default", "old-app", "100"),
	}, "100")

	// watch 立即返回 410
	fake.SetWatchStatus(http.StatusGone)

	ctx, cancel := context.WithCancel(context.Background())
	informer, err := NewInformer(ctx, InformerOptions{
		Resource:     "deployments",
		APIVersion:   "apps/v1",
		APIServerURL: fake.URL(),
		ResyncPeriod: 1 * time.Hour,
		ReconnectPolicy: ReconnectPolicy{
			InitialBackoff: 50 * time.Millisecond,
			MaxBackoff:     200 * time.Millisecond,
			BackoffFactor:  2.0,
			Jitter:         0,
		},
	})
	if err != nil {
		cancel()
		t.Fatalf("NewInformer: %v", err)
	}
	_ = informer.Start(ctx)
	defer func() {
		cancel()
		informer.Stop()
	}()

	// 410 应触发 relist：listCalled 应 >= 2（初始 + relist）
	waitFor(t, 3*time.Second, func() bool {
		return fake.listCalled.Load() >= 2
	}, "410 Gone 应触发 relist")
}

// ═══════════════════════════════════════════════════════════════════
// 测试：参数验证
// ═══════════════════════════════════════════════════════════════════

func TestNewInformer_ApplyDefaultResyncPeriod(t *testing.T) {
	informer, err := NewInformer(context.Background(), InformerOptions{
		Resource:     "pods",
		APIVersion:   "v1",
		APIServerURL: "http://localhost:8001",
		// 不设 ResyncPeriod
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer informer.Stop()

	// 内部检查：从 informerImpl 取出来
	im, ok := informer.(*informerImpl)
	if !ok {
		t.Fatal("type assertion failed")
	}
	if im.opts.ResyncPeriod != ResyncPeriodHigh {
		t.Errorf("pods 应默认 ResyncPeriodHigh (10min), got %v", im.opts.ResyncPeriod)
	}
}

// ═══════════════════════════════════════════════════════════════════
// 测试：URL 构造
// ═══════════════════════════════════════════════════════════════════

func TestInformerImpl_BuildListURL_CoreGroup(t *testing.T) {
	im := &informerImpl{
		opts: InformerOptions{
			Resource:     "pods",
			APIVersion:   "v1",
			APIServerURL: "http://localhost:8001",
		},
	}

	got, err := im.buildListURL("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.HasPrefix(got, "http://localhost:8001/api/v1/pods?") {
		t.Errorf("CoreGroup URL = %q, want prefix /api/v1/pods", got)
	}
}

func TestInformerImpl_BuildListURL_NamedGroup(t *testing.T) {
	im := &informerImpl{
		opts: InformerOptions{
			Resource:     "deployments",
			APIVersion:   "apps/v1",
			APIServerURL: "http://localhost:8001",
		},
	}

	got, err := im.buildListURL("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.HasPrefix(got, "http://localhost:8001/apis/apps/v1/deployments?") {
		t.Errorf("NamedGroup URL = %q, want prefix /apis/apps/v1/deployments", got)
	}
}

func TestInformerImpl_BuildWatchURL_IncludesResourceVersion(t *testing.T) {
	im := &informerImpl{
		opts: InformerOptions{
			Resource:     "pods",
			APIVersion:   "v1",
			APIServerURL: "http://localhost:8001",
		},
	}
	im.setResourceVersion("12345")

	got, err := im.buildWatchURL()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "watch=1") {
		t.Errorf("URL 应含 watch=1, got %q", got)
	}
	if !strings.Contains(got, "resourceVersion=12345") {
		t.Errorf("URL 应含 resourceVersion=12345, got %q", got)
	}
}

// ═══════════════════════════════════════════════════════════════════
// 测试：Subscribe / Unsubscribe
// ═══════════════════════════════════════════════════════════════════

func TestInformer_UnsubscribeStopsDispatch(t *testing.T) {
	fake := newFakeK8sServer()
	defer fake.Close()

	fake.SetList(nil, "100")

	holdCh := make(chan struct{})
	defer close(holdCh)
	fake.SetWatchHold(holdCh)

	informer, _ := startInformer(t, fake)

	var count atomic.Int64
	sub := informer.Subscribe(func(e Event) {
		count.Add(1)
	})

	// Unsubscribe 后再推事件
	sub.Unsubscribe()

	fake.SetWatchEvents([]map[string]interface{}{
		watchEvent("ADDED", makeDeploy("default", "after-unsub", "101")),
	})

	// 给 watch loop 时间处理（但 sub 已经移除，count 不应增加）
	time.Sleep(200 * time.Millisecond)

	if got := count.Load(); got != 0 {
		t.Errorf("Unsubscribe 后 handler 不应再被调用，got %d", got)
	}
}

// ═══════════════════════════════════════════════════════════════════
// 测试：ShardSet 过滤
// ═══════════════════════════════════════════════════════════════════

type fakeShardSet struct {
	owns map[string]bool
}

func (f *fakeShardSet) Owns(ns string) bool {
	return f.owns[ns]
}

func TestInformer_ShardSetFiltersEvents(t *testing.T) {
	fake := newFakeK8sServer()
	defer fake.Close()

	fake.SetList(nil, "100")
	fake.SetWatchEvents([]map[string]interface{}{
		watchEvent("ADDED", makeDeploy("owned-ns", "app1", "101")),
		watchEvent("ADDED", makeDeploy("not-owned-ns", "app2", "102")),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	informer, err := NewInformer(ctx, InformerOptions{
		Resource:     "deployments",
		APIVersion:   "apps/v1",
		APIServerURL: fake.URL(),
		ResyncPeriod: 1 * time.Hour,
		ShardSet: &fakeShardSet{
			owns: map[string]bool{"owned-ns": true},
		},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	_ = informer.Start(ctx)
	defer informer.Stop()

	var (
		eventsMu sync.Mutex
		received []Event
	)
	informer.Subscribe(func(e Event) {
		eventsMu.Lock()
		received = append(received, e)
		eventsMu.Unlock()
	})

	// 给两个事件都到达 dispatcher 的时间（owned-ns 应通过、not-owned-ns 应被 drop）
	time.Sleep(500 * time.Millisecond)

	eventsMu.Lock()
	defer eventsMu.Unlock()

	if len(received) != 1 {
		t.Errorf("ShardSet 应过滤 not-owned-ns，got %d events: %v", len(received), received)
	}
	if len(received) >= 1 && received[0].Namespace != "owned-ns" {
		t.Errorf("应只收到 owned-ns 事件，got ns=%q", received[0].Namespace)
	}
}

// ═══════════════════════════════════════════════════════════════════
// 测试：context cancel 触发 watch 退出
// ═══════════════════════════════════════════════════════════════════

func TestInformer_ContextCancelExitsWatch(t *testing.T) {
	fake := newFakeK8sServer()
	defer fake.Close()

	fake.SetList(nil, "100")
	holdCh := make(chan struct{})
	defer close(holdCh)
	fake.SetWatchHold(holdCh)

	ctx, cancel := context.WithCancel(context.Background())
	informer, err := NewInformer(ctx, InformerOptions{
		Resource:     "deployments",
		APIVersion:   "apps/v1",
		APIServerURL: fake.URL(),
		ResyncPeriod: 1 * time.Hour,
	})
	if err != nil {
		cancel()
		t.Fatalf("err: %v", err)
	}
	_ = informer.Start(ctx)

	// 等 watch 起来
	waitFor(t, 2*time.Second, func() bool {
		return fake.watchStarted.Load() >= 1
	}, "watch 应启动")

	// cancel ctx → watch 应该退出
	cancel()

	// Stop 应该快速返回（< 1s，因为 ctx 已 cancel）
	start := time.Now()
	informer.Stop()
	elapsed := time.Since(start)

	if elapsed > 1*time.Second {
		t.Errorf("ctx cancel 后 Stop 应快速返回，实际 %v", elapsed)
	}
}
