// Package controller_installer 负责全局 KubePivot Controller 的安装/卸载/状态检查
//
// 设计原则：
//   - 走 exec kubectl apply，不引入 client-go
//   - 模板走 embed.FS，镜像 tag 从 kpVersion 自动注入，--image 可覆盖
//   - 幂等：重复 install 等于 upgrade，保留状态
package controller_installer

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
)

//go:embed templates/*.yaml
var templates embed.FS

// Config 安装配置
type Config struct {
	Namespace  string // 部署 namespace，默认 kubepivot-system
	Image      string // 镜像 tag，默认 qingchun22/kubepivot-controller:<kpVersion>
	Kubeconfig string // 可选 kubeconfig 路径
	Context    string // 可选 kube context
}

// Installer 安装器
type Installer struct {
	cfg Config
}

// New 构造 installer，应用默认值
func New(cfg Config) *Installer {
	if cfg.Namespace == "" {
		cfg.Namespace = "kubepivot-system"
	}
	return &Installer{cfg: cfg}
}

// Install 部署全局 controller
func (i *Installer) Install(ctx context.Context) error {
	// 1. 读取所有 embedded 模板
	manifests, err := i.loadManifests()
	if err != nil {
		return fmt.Errorf("加载模板失败: %w", err)
	}

	// 2. 变量替换（__IMAGE__）
	rendered := i.render(manifests)

	// 3. kubectl apply（合并成一个 stream，一次 apply）
	return i.apply(ctx, rendered)
}

// Uninstall 卸载全局 controller
// 注意：这个操作会删除 kubepivot-system namespace + 所有 ClusterRole/Binding
// 不删除被管理项目的 namespace label（用户需手工 unenroll）
func (i *Installer) Uninstall(ctx context.Context) error {
	// 倒序删除：先 namespace（带走 deployment/sa），再 cluster-scoped
	exec := executor.GetExecutor()

	// 1. 删除 cluster-scoped 资源
	if _, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"delete", "clusterrolebinding", "kubepivot-controller",
		"--ignore-not-found",
	); err != nil {
		return fmt.Errorf("删除 ClusterRoleBinding 失败: %w", err)
	}
	if _, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"delete", "clusterrole", "kubepivot-controller",
		"--ignore-not-found",
	); err != nil {
		return fmt.Errorf("删除 ClusterRole 失败: %w", err)
	}

	// 2. 删除 namespace（级联带走 deployment/sa/configmap）
	if _, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"delete", "namespace", i.cfg.Namespace,
		"--ignore-not-found", "--timeout=60s",
	); err != nil {
		return fmt.Errorf("删除 namespace %s 失败: %w", i.cfg.Namespace, err)
	}

	return nil
}

// Status 查询当前 controller 状态
type Status struct {
	Installed         bool
	Namespace         string
	DeploymentReady   string // "3/3"
	LeaderPod         string // pod name
	ManagedNamespaces int    // label=managed 的 ns 数量
}

func (i *Installer) Status(ctx context.Context) (*Status, error) {
	exec := executor.GetExecutor()

	st := &Status{Namespace: i.cfg.Namespace}

	// 1. 检查 namespace 是否存在
	_, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"get", "namespace", i.cfg.Namespace, "--ignore-not-found",
	)
	if err != nil {
		return st, nil // namespace 不存在
	}

	// 2. 检查 Deployment ready
	out, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"get", "deployment", "kubepivot-controller",
		"-n", i.cfg.Namespace,
		"-o", "jsonpath={.status.readyReplicas}/{.spec.replicas}",
		"--ignore-not-found",
	)
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		st.Installed = true
		st.DeploymentReady = strings.TrimSpace(string(out))
	}

	// 3. 统计被管理的 namespace 数量
	nsOut, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"get", "namespace",
		"-l", "kubepivot.io/managed=true",
		"-o", "jsonpath={.items[*].metadata.name}",
	)
	if err == nil {
		names := strings.Fields(strings.TrimSpace(string(nsOut)))
		st.ManagedNamespaces = len(names)
	}

	return st, nil
}

// ── 内部辅助 ──────────────────────────────────────────────────────────────────

// loadManifests 读取 embedded templates，返回有序的文件列表
// 顺序：namespace 先于 rbac 先于 deployment（apply 顺序）
func (i *Installer) loadManifests() ([]string, error) {
	entries, err := fs.ReadDir(templates, "templates")
	if err != nil {
		return nil, err
	}

	// 排序：namespace.yaml → rbac.yaml → deployment.yaml
	order := map[string]int{
		"namespace.yaml":  1,
		"rbac.yaml":       2,
		"deployment.yaml": 3,
	}
	sort.Slice(entries, func(a, b int) bool {
		return order[entries[a].Name()] < order[entries[b].Name()]
	})

	var result []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(templates, "templates/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("读取 %s 失败: %w", e.Name(), err)
		}
		result = append(result, string(data))
	}
	return result, nil
}

// render 变量替换 + 合并成一个 multi-doc YAML
func (i *Installer) render(manifests []string) string {
	joined := strings.Join(manifests, "\n---\n")

	// 替换 __IMAGE__
	image := i.cfg.Image
	if image == "" {
		image = "qingchun22/kubepivot-controller:latest"
	}
	joined = strings.ReplaceAll(joined, "__IMAGE__", image)

	// 替换 namespace（如果用户指定了非默认值）
	if i.cfg.Namespace != "kubepivot-system" {
		joined = strings.ReplaceAll(joined,
			"namespace: kubepivot-system", "namespace: "+i.cfg.Namespace)
		joined = strings.ReplaceAll(joined,
			"name: kubepivot-system", "name: "+i.cfg.Namespace)
	}

	return joined
}

// apply 把渲染后的 YAML 通过 kubectl apply -f - 推送
func (i *Installer) apply(ctx context.Context, yaml string) error {
	exec := executor.GetExecutor()
	cmd := exec.CmdKubectl(ctx, i.cfg.Kubeconfig, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply 失败: %w\n%s", err, string(out))
	}
	return nil
}

// WaitReady 等待 Deployment ready（供 install 命令可选调用）
func (i *Installer) WaitReady(ctx context.Context, timeout time.Duration) error {
	exec := executor.GetExecutor()
	out, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"rollout", "status", "deployment/kubepivot-controller",
		"-n", i.cfg.Namespace,
		fmt.Sprintf("--timeout=%ds", int(timeout.Seconds())),
	)
	if err != nil {
		return fmt.Errorf("等待 controller ready 失败: %w\n%s", err, string(out))
	}
	return nil
}
