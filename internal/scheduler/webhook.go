// internal/scheduler/webhook.go
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ─── Migration target hint ──────────────────────────────────────

// migrationTargetHints stores pending migration target nodes.
// Rescheduler writes before eviction, webhook reads during pod recreation.
//
// v3.3: 双路查找 — name-based (StatefulSet) + label-based (Deployment)。
// Key 格式: "ns/name" (name-based) 或 "ns/label:value" (label-based)。
// Label key 用 ":" 作为 naming convention（namespace 不含 ":"）。
var migrationTargetHints sync.Map

// SetMigrationTargetHint records the expected target node for a pod being evicted.
//
// v3.3: 同时存储 name-based 和 label-based key（如果 pod 有 app.kubernetes.io/name label）。
func SetMigrationTargetHint(ns, name, targetNode string) {
	migrationTargetHints.Store(ns+"/"+name, targetNode)
}

// SetMigrationTargetHintWithLabel 同时存储 name + label 双 key。
// Deployment Pod 重建后改名，webhook 通过 label 回退查找。
func SetMigrationTargetHintWithLabel(ns, name, appLabel, targetNode string) {
	migrationTargetHints.Store(ns+"/"+name, targetNode)
	if appLabel != "" {
		migrationTargetHints.Store(ns+"/label:"+appLabel, targetNode)
	}
}

// PopMigrationTargetHint returns and removes the target hint for a pod.
// v3.3: name-based primary lookup + label-based fallback (for Deployment pods).
func PopMigrationTargetHint(ns, name string) (string, bool) {
	v, ok := migrationTargetHints.LoadAndDelete(ns + "/" + name)
	if ok {
		return v.(string), true
	}
	return "", false
}

// PopMigrationTargetHintByLabel label-based 回退查找（Deployment Pod 改名后）。
func PopMigrationTargetHintByLabel(ns, appLabel string) (string, bool) {
	if appLabel == "" {
		return "", false
	}
	v, ok := migrationTargetHints.LoadAndDelete(ns + "/label:" + appLabel)
	if !ok {
		return "", false
	}
	return v.(string), true
}

// WebhookServer 是乾枢调度器的 HTTPS Admission Webhook 服务器。
// Phase 1：仅处理 Pod 创建请求，实时分配 nodeSelector。
type WebhookServer struct {
	scheduler *Scheduler
	server    *http.Server
	addr      string
	certFile  string
	keyFile   string
}

// NewWebhookServer 创建 Webhook 服务器。
// addr 格式：":8443"
// certFile/keyFile 当前未使用，证书通过 TLSConfig 注入。
func NewWebhookServer(sched *Scheduler, addr, certFile, keyFile string) *WebhookServer {
	ws := &WebhookServer{
		scheduler: sched,
		addr:      addr,
		certFile:  certFile,
		keyFile:   keyFile,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", ws.handleMutate)
	mux.HandleFunc("/health", ws.handleHealth)

	ws.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return ws
}

// Start 启动 Webhook 服务器。在独立 goroutine 中运行，返回错误 channel。
// TLSConfig 必须在调用 Start 前注入（通过 cert.go 生成自签证书）。
func (ws *WebhookServer) Start(ctx context.Context) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		slog.Info("乾枢 Webhook 已启动", "addr", ws.addr)
		if err := ws.server.ListenAndServeTLS(ws.certFile, ws.keyFile); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("webhook server: %w", err)
		}
		close(errCh)
	}()
	return errCh
}

// Stop 优雅关闭 Webhook 服务器。
func (ws *WebhookServer) Stop(ctx context.Context) error {
	return ws.server.Shutdown(ctx)
}

// handleHealth 健康检查端点。
func (ws *WebhookServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// handleMutate 处理 Kubernetes Admission Review 请求。
func (ws *WebhookServer) handleMutate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("webhook: 读取请求失败", "err", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var review admissionReviewRequest
	if err := json.Unmarshal(body, &review); err != nil {
		slog.Error("webhook: 解析 AdmissionReview 失败", "err", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	req := review.Request

	// 防御性检查：只处理 Pod 资源
	if req.Kind.Kind != "Pod" {
		slog.Debug("webhook: 忽略非 Pod 资源", "kind", req.Kind.Kind)
		ws.writeAdmissionResponse(w, review, true, nil)
		return
	}

	// 提取 Pod 信息
	pod := extractPodFromRequest(req)
	slog.Debug("webhook: 收到 Pod 创建请求",
		"name", pod.Name,
		"namespace", pod.Namespace,
		"cpu", pod.Requests.CPU,
		"mem", pod.Requests.Memory,
	)

	// 短期超时上下文：3 秒内完成实时分配
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	// Migration target hint: 如果该 Pod 是迁移副本，直接路由到目标节点，避免回弹
	// v3.3: name-based primary (StatefulSet) + label-based fallback (Deployment)
	var node string
	if hint, ok := PopMigrationTargetHint(pod.Namespace, pod.Name); ok {
		node = hint
		slog.Info("webhook: 使用迁移目标节点 (name-based)", "pod", pod.Namespace+"/"+pod.Name, "node", node)
	} else if appLabel := pod.Labels["app.kubernetes.io/name"]; appLabel != "" {
		if hint, ok := PopMigrationTargetHintByLabel(pod.Namespace, appLabel); ok {
			node = hint
			slog.Info("webhook: 使用迁移目标节点 (label-based)",
				"pod", pod.Namespace+"/"+pod.Name,
				"app", appLabel, "node", node)
		}
	}

	if node == "" {
		// 正常调度路径
		var err error
		node, err = ws.scheduler.AssignPod(ctx, pod)
		if err != nil {
			slog.Warn("webhook: 分配失败，放行由 K8s 默认调度器处理",
				"pod", pod.Namespace+"/"+pod.Name, "err", err)
			ws.writeAdmissionResponse(w, review, true, nil)
			return
		}
	}

	// 构造安全的 JSON Patch
	patch := buildNodeSelectorPatch(req, node)
	patchBytes, _ := json.Marshal(patch)

	slog.Info("webhook: 调度决策已注入",
		"pod", pod.Namespace+"/"+pod.Name,
		"node", node,
	)

	ws.writeAdmissionResponse(w, review, true, patchBytes)
}

// writeAdmissionResponse 写入 AdmissionReview 响应，包装在完整的 AdmissionReview 中。
func (ws *WebhookServer) writeAdmissionResponse(w http.ResponseWriter, review admissionReviewRequest, allowed bool, patch []byte) {
	resp := admissionReviewResponse{
		APIVersion: review.APIVersion,
		Kind:       review.Kind, // 原样回传，保证类型匹配
		Response: admissionResponse{
			UID:     review.Request.UID,
			Allowed: allowed,
		},
	}
	if patch != nil {
		resp.Response.PatchType = "JSONPatch"
		resp.Response.Patch = patch
	}

	out, err := json.Marshal(resp)
	if err != nil {
		slog.Error("webhook: 响应序列化失败", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

// buildNodeSelectorPatch 根据 Pod 原始 nodeSelector 是否存在，构造安全的 JSON Patch。
func buildNodeSelectorPatch(req admissionRequest, node string) []jsonPatchOp {
	// 检查原始 Pod 的 spec.nodeSelector 是否已有值
	hasSelector := req.Object.Spec.NodeSelector != nil

	if hasSelector {
		return []jsonPatchOp{{
			Op:    "add",
			Path:  "/spec/nodeSelector/kubernetes.io~1hostname", // ‘/’ 转义为 ~1
			Value: node,
		}}
	}
	return []jsonPatchOp{{
		Op:    "add",
		Path:  "/spec/nodeSelector",
		Value: map[string]string{"kubernetes.io/hostname": node},
	}}
}

// extractPodFromRequest 从 AdmissionRequest 中构造调度器用的 PodInfo。
// 资源解析失败会记录警告，并可能导致 Pod 请求为 0，从而触发放行。
func extractPodFromRequest(req admissionRequest) *PodInfo {
	p := &PodInfo{
		Name:      req.Object.Metadata.Name,
		Namespace: req.Object.Metadata.Namespace,
		Labels:    req.Object.Metadata.Labels,
	}
	// 如果 Name 为空，可能由 GenerateName 生成，记录 GenerateName 用于日志
	if p.Name == "" {
		p.Name = req.Object.Metadata.GenerateName
		if p.Name == "" {
			slog.Warn("webhook: Pod 缺少名称和 GenerateName")
			p.Name = "unknown"
		}
	}

	var totalCPU, totalMem int64
	parseWarned := false
	for _, c := range req.Object.Spec.Containers {
		cpu, err := parseCPU(c.Resources.Requests.CPU)
		if err != nil {
			if !parseWarned {
				slog.Warn("webhook: CPU 资源解析失败，将视为 0", "cpu", c.Resources.Requests.CPU, "err", err)
				parseWarned = true
			}
			cpu = 0
		}
		mem, err := parseMemory(c.Resources.Requests.Memory)
		if err != nil {
			if !parseWarned {
				slog.Warn("webhook: Memory 资源解析失败，将视为 0", "mem", c.Resources.Requests.Memory, "err", err)
				parseWarned = true
			}
			mem = 0
		}
		totalCPU += cpu
		totalMem += mem
	}
	p.Requests = ResourceRequest{CPU: totalCPU, Memory: totalMem}
	return p
}

// ─── Kubernetes Admission Review 数据结构 ─────────────────

// admissionReviewRequest 外层 AdmissionReview 请求。
type admissionReviewRequest struct {
	APIVersion string           `json:"apiVersion"`
	Kind       json.RawMessage  `json:"kind"` // 保留原始 JSON，用于原样返回
	Request    admissionRequest `json:"request"`
}

// admissionReviewResponse 外层 AdmissionReview 响应。
type admissionReviewResponse struct {
	APIVersion string            `json:"apiVersion"`
	Kind       json.RawMessage   `json:"kind"` // 与请求一致
	Response   admissionResponse `json:"response"`
}

type admissionRequest struct {
	UID  string `json:"uid"`
	Kind struct {
		Group   string `json:"group"`
		Version string `json:"version"`
		Kind    string `json:"kind"`
	} `json:"kind"`
	Object podObj `json:"object"`
}

type podObj struct {
	Metadata struct {
		Name         string            `json:"name"`
		Namespace    string            `json:"namespace"`
		GenerateName string            `json:"generateName"`
		Labels       map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		NodeSelector map[string]string `json:"nodeSelector"`
		Containers   []struct {
			Name      string `json:"name"`
			Resources struct {
				Requests struct {
					CPU    string `json:"cpu"`
					Memory string `json:"memory"`
				} `json:"requests"`
			} `json:"resources"`
		} `json:"containers"`
	} `json:"spec"`
}

type admissionResponse struct {
	UID       string `json:"uid"`
	Allowed   bool   `json:"allowed"`
	PatchType string `json:"patchType,omitempty"`
	Patch     []byte `json:"patch,omitempty"`
}

type jsonPatchOp struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}
