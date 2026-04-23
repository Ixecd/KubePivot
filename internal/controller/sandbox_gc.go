package controller

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
)

const sandboxSessionTTL = time.Hour
const sandboxGCInterval = 5 * time.Minute

type sandboxSessionFile struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Namespace string    `json:"namespace"`
	StartedAt time.Time `json:"started_at"`
	TTL       int       `json:"ttl"`
}

// StartSandboxGCLoop 定时扫描并清理超期 Sandbox Session
func (r *Reconciler) StartSandboxGCLoop(ctx context.Context) {
	ticker := time.NewTicker(sandboxGCInterval)
	defer ticker.Stop()

	slog.Info("🧹 Sandbox GC Loop 已启动", "interval", sandboxGCInterval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.gcExpiredSandboxSessions()
		}
	}
}

// gcExpiredSandboxSessions 清理超过 TTL 的 Sandbox Session
func (r *Reconciler) gcExpiredSandboxSessions() {
	// Session 文件存在 <project-root>/.kp/sandbox/
	// Controller 运行在集群内，从环境变量获取项目根目录
	projectRoot := getenv("PROJECT_ROOT", "/app")
	sandboxDir := filepath.Join(projectRoot, ".kp", "sandbox")

	entries, err := os.ReadDir(sandboxDir)
	if err != nil {
		// 目录不存在时静默跳过
		return
	}

	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(sandboxDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var session sandboxSessionFile
		if err := json.Unmarshal(data, &session); err != nil {
			slog.Warn("无法解析 sandbox session 文件", "path", path)
			continue
		}

		ttl := time.Duration(session.TTL) * time.Second
		if ttl <= 0 {
			ttl = sandboxSessionTTL
		}

		if now.Sub(session.StartedAt) > ttl {
			slog.Warn("⏰ Sandbox Session 超期，触发清理",
				"id", session.ID,
				"project", session.Project,
				"age", now.Sub(session.StartedAt).Round(time.Second),
			)
			r.cleanExpiredSandbox(session, path)
		}
	}
}

// cleanExpiredSandbox 清理超期 Sandbox 的残余资源
func (r *Reconciler) cleanExpiredSandbox(session sandboxSessionFile, sessionPath string) {
	ns := session.Namespace
	if ns == "" {
		ns = getenv("KUBE_NAMESPACE", session.Project)
	}

	slog.Info("清理 Sandbox 残余资源",
		"sandbox_id", session.ID,
		"namespace", ns)

	// 清理带 sandbox-id label 的资源
	kinds := []string{"job", "pod", "configmap"}
	for _, kind := range kinds {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out, err := executor.GetExecutor().Kubectl(ctx, r.kubeconfig,
			"delete", kind,
			"--namespace", ns,
			"-l", "kubepivot.io/sandbox-id="+session.ID,
			"--ignore-not-found",
		)
		cancel()
		if err != nil {
			slog.Warn("清理 Sandbox 资源失败",
				"kind", kind, "err", string(out))
		} else {
			slog.Info("已清理 Sandbox 资源",
				"kind", kind, "sandbox_id", session.ID)
		}
	}

	// 强制解锁状态机（如果还在 Sandbox 状态）
	r.forceUnlockIfSandboxState(session.ID)

	// 删除 session 文件
	os.Remove(sessionPath)
	slog.Info("✅ Sandbox Session 已清理", "id", session.ID)
}

// forceUnlockIfSandboxState 检查状态机，如果还在 Sandbox 状态则强制回 IDLE
func (r *Reconciler) forceUnlockIfSandboxState(sandboxID string) {
	if r.sm == nil {
		return
	}

	cur := r.sm.State()
	sandboxStates := map[string]bool{
		"LOCKED": true, "SNAPSHOTTING": true,
		"SIMULATING": true, "COMMITTING": true, "RESTORING": true,
	}
	if !sandboxStates[string(cur)] {
		return
	}

	slog.Warn("状态机仍在 Sandbox 状态，强制回 IDLE",
		"state", cur, "sandbox_id", sandboxID)

	if err := r.sm.ForceState("IDLE",
		"controller: sandbox GC 超期强制解锁 id="+sandboxID); err != nil {
		slog.Error("强制解锁失败", "err", err)
	}
}