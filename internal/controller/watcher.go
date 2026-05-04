package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// WatchAction 表示 watch 事件类型
type WatchAction string

const (
	WatchAdded    WatchAction = "ADDED"
	WatchModified WatchAction = "MODIFIED"
	WatchDeleted  WatchAction = "DELETED"
	WatchBookmark WatchAction = "BOOKMARK"
)

// WatchEvent 一条 watch 事件
type WatchEvent struct {
	Action WatchAction
	Object map[string]interface{} // 原始对象（metadata / spec / status 等）
}

// ObjectMeta 从 WatchEvent.Object 中提取元数据的便捷方法
type ObjectMeta struct {
	Name        string
	Namespace   string
	Labels      map[string]string
	Annotations map[string]string
}

// Meta 提取 metadata 字段
func (e *WatchEvent) Meta() ObjectMeta {
	meta := ObjectMeta{Labels: map[string]string{}, Annotations: map[string]string{}}
	if e.Object == nil {
		return meta
	}
	m, _ := e.Object["metadata"].(map[string]interface{})
	if m == nil {
		return meta
	}
	if s, ok := m["name"].(string); ok {
		meta.Name = s
	}
	if s, ok := m["namespace"].(string); ok {
		meta.Namespace = s
	}
	if lbls, ok := m["labels"].(map[string]interface{}); ok {
		for k, v := range lbls {
			if sv, ok := v.(string); ok {
				meta.Labels[k] = sv
			}
		}
	}
	if ans, ok := m["annotations"].(map[string]interface{}); ok {
		for k, v := range ans {
			if sv, ok := v.(string); ok {
				meta.Annotations[k] = sv
			}
		}
	}
	return meta
}

// Watcher 抽象接口：watch 某种资源，通过 onEvent 回调推送事件
//
// 保留接口是为了未来 v3.x 可以平替成 client-go informer 实现，
// v2.3.0 的 KubectlWatcher 是默认实现。
type Watcher interface {
	Watch(ctx context.Context, onEvent func(WatchEvent)) error
}

// KubectlWatcher 基于 exec kubectl --watch 的 Watcher 实现
//
// 实现要点：
//   - List-then-Watch：启动时先 kubectl get 做全量 snapshot（不产生 ADDED 事件，
//     由调用方决定是否回放；这样避免 controller 启动时把所有资源都当新增处理）
//   - exec.CommandContext 绑定生命周期，ctx 取消 → kubectl 子进程被 SIGKILL
//   - 心跳守卫：30s 无事件 → 触发探活，探活失败 → 断开重连
//   - 指数退避重连：1s → 2s → 4s → 8s → 30s 封顶
//   - json.NewDecoder 流式解析（kubectl --watch --output-watch-events=true 单行 JSON）
//   - 降级不阻断：err 只日志，不 panic，ctx.Done 才真正退出
type KubectlWatcher struct {
	Resource      string   // deployment / configmap / namespace / ...
	Namespace     string   // 空字符串表示 all-namespaces（-A）
	LabelSelector string   // kubepivot.io/managed=true 等
	FieldSelector string   // metadata.name=xxx 等（可选）
	Kubeconfig    string   // 可选
	ExtraArgs     []string // 额外参数（一般不用）

	// 心跳守卫：多久无事件视为连接可能已僵死
	HeartbeatTimeout time.Duration

	// 最后一次事件时间戳（原子操作，watcher + guard 共享）
	lastEventNano atomic.Int64
}

// NewKubectlWatcher 构造一个 watcher，应用默认值
func NewKubectlWatcher(resource string, labelSelector string) *KubectlWatcher {
	return &KubectlWatcher{
		Resource:         resource,
		LabelSelector:    labelSelector,
		HeartbeatTimeout: 30 * time.Second,
	}
}

// Watch 启动 watch 循环，阻塞直到 ctx.Done()
//
// 内部 2 个 goroutine：
//  1. streamEvents：维护 kubectl --watch 子进程，解析 JSON 推送事件
//  2. heartbeatGuard：30s 无事件 → 主动探活，探活失败 → 强制 restart 子进程
//
// 重连采用指数退避：1s → 2s → 4s → 8s，封顶 30s
func (w *KubectlWatcher) Watch(ctx context.Context, onEvent func(WatchEvent)) error {
	w.lastEventNano.Store(time.Now().UnixNano())

	backoff := time.Second
	const backoffMax = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// 每次重连生成独立的 streamCtx，心跳守卫可以取消它来强制 restart
		streamCtx, cancelStream := context.WithCancel(ctx)

		// heartbeat guard
		go w.heartbeatGuard(streamCtx, cancelStream)

		err := w.streamEvents(streamCtx, onEvent)
		cancelStream()

		if ctx.Err() != nil {
			return nil // 上游取消，正常退出
		}

		// 发生错误（或 kubectl 自然退出）→ 重连
		if err != nil {
			slog.Info("watcher stream 结束，准备重连",
				"resource", w.Resource,
				"namespace", w.Namespace,
				"err", err,
				"backoff", backoff)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}

		// 指数退避
		backoff *= 2
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

// streamEvents 单次 watch session：启动 kubectl --watch，流式解析 JSON
func (w *KubectlWatcher) streamEvents(ctx context.Context, onEvent func(WatchEvent)) error {
	args := w.buildArgs()
	kubectlPath := executor.GetExecutor().KubectlPath()

	cmd := exec.CommandContext(ctx, kubectlPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe 失败: %w", err)
	}
	// stderr 合并进 stdout 太脏（会污染 JSON 流），单独吃掉
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 kubectl --watch 失败: %w", err)
	}

	slog.Debug("watcher 子进程已启动",
		"resource", w.Resource,
		"label", w.LabelSelector,
		"pid", cmd.Process.Pid)

	decoder := json.NewDecoder(stdout)

	// 流式解码：kubectl --output-watch-events=true 输出形如
	// {"type":"ADDED","object":{"kind":"Deployment","metadata":{...}}}
	for {
		var raw struct {
			Type   string                 `json:"type"`
			Object map[string]interface{} `json:"object"`
		}
		if err := decoder.Decode(&raw); err != nil {
			// io.EOF 是 kubectl 正常结束；其他错误也都走重连
			cmd.Wait() // 回收子进程
			if err == io.EOF {
				return fmt.Errorf("kubectl --watch 流已关闭")
			}
			return fmt.Errorf("JSON 解码失败: %w", err)
		}

		action := WatchAction(strings.ToUpper(raw.Type))
		if action == "" {
			continue
		}

		// 更新心跳时间戳
		w.lastEventNano.Store(time.Now().UnixNano())

		onEvent(WatchEvent{
			Action: action,
			Object: raw.Object,
		})
	}
}

// heartbeatGuard 心跳守卫：30s 无事件 → 主动 kubectl get 探活
// 探活失败 → cancelStream() 强制重连
func (w *KubectlWatcher) heartbeatGuard(ctx context.Context, cancelStream context.CancelFunc) {
	ticker := time.NewTicker(w.HeartbeatTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			last := time.Unix(0, w.lastEventNano.Load())
			if time.Since(last) < w.HeartbeatTimeout {
				continue // 最近有事件，心跳正常
			}

			// 长时间无事件 → 主动探活
			probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := executor.GetExecutor().Kubectl(probeCtx, w.Kubeconfig,
				"get", w.Resource, "--no-headers", "--output=name",
			)
			probeCancel()

			if err != nil {
				slog.Info("🫀 watcher 心跳探活失败，触发重连",
					"resource", w.Resource,
					"since_last_event", time.Since(last).Round(time.Second),
					"err", err)
				cancelStream()
				return
			}
			// 探活成功但长时间无事件是正常的（资源没变化），更新心跳避免反复探活
			w.lastEventNano.Store(time.Now().UnixNano())
		}
	}
}

// buildArgs 组装 kubectl args
// 形如：kubectl get <resource> [-A | -n ns] [-l label] --watch --output-watch-events=true -o json
func (w *KubectlWatcher) buildArgs() []string {
	args := []string{"get", w.Resource}

	if w.Namespace == "" {
		args = append(args, "-A")
	} else {
		args = append(args, "-n", w.Namespace)
	}

	if w.LabelSelector != "" {
		args = append(args, "-l", w.LabelSelector)
	}
	if w.FieldSelector != "" {
		args = append(args, "--field-selector", w.FieldSelector)
	}

	args = append(args,
		"--watch",
		"--output-watch-events=true",
		"-o", "json",
	)
	args = append(args, w.ExtraArgs...)

	// kubeconfig 由 executor 统一注入，这里不加
	return args
}
