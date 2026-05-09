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

	"github.com/Ixecd/kubepivot/internal/etcdmanager"
	"github.com/Ixecd/kubepivot/internal/executor"
	"github.com/Ixecd/kubepivot/internal/scheduler"
)

//go:embed templates/*.yaml
var templates embed.FS

// Config 安装配置
type Config struct {
	Namespace  string // 部署 namespace，默认 kubepivot-system
	Image      string // 镜像，默认 qingchun22/kubepivot-controller:latest
	Kubeconfig string // 可选 kubeconfig 路径
	Context    string // 可选 kube context
}
// 无 Docker 账户时：
//   1. 自建镜像: make image.build IMAGES=controller
//   2. kind:    kind load docker-image qingchun22/kubepivot-controller:latest
//   3. minikube: minikube image load qingchun22/kubepivot-controller:latest
//   4. ghcr.io: docker tag <img> ghcr.io/<user>/kubepivot-controller:latest && docker push
//      kp controller install --image ghcr.io/<user>/kubepivot-controller:latest

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
	if err := i.apply(ctx, rendered); err != nil {
		return err
	}

	// 4. 生成 etcd TLS CA 证书 → 写入 Secret（方案 B — Controller 负责证书分发）
	if err := i.ensureCertsSecret(ctx); err != nil {
		return fmt.Errorf("生成 etcd CA 证书失败: %w", err)
	}

	// 5. 生成 Webhook TLS 自签证书 → 写入 Secret
	if err := i.ensureWebhookTLSSecret(ctx); err != nil {
		return fmt.Errorf("生成 Webhook TLS 证书失败: %w", err)
	}

	return nil
}

// ensureCertsSecret Controller 安装时预生成 etcd CA 证书并写入 Secret。
// 所有 etcd Pod 启动时只读此 Secret，不需要写权限。
func (i *Installer) ensureCertsSecret(ctx context.Context) error {
	return etcdmanager.CreateCertsSecret(ctx, etcdmanager.DefaultCertConfig(), i.cfg.Namespace, false)
}

// ensureWebhookTLSSecret Controller 安装时预生成 Webhook TLS 自签证书并写入 Secret。
// v3.3: 修复 P0#3 — Webhook 在 :443 运行但缺 TLS 证书，修复前静默降级（failurePolicy=Ignore）。
func (i *Installer) ensureWebhookTLSSecret(ctx context.Context) error {
	certPEM, keyPEM, _, err := scheduler.GenerateSelfSignedCert(i.cfg.Namespace)
	if err != nil {
		return fmt.Errorf("generate self-signed cert: %w", err)
	}

	yaml, err := scheduler.GenerateCertSecretYAML(certPEM, keyPEM, i.cfg.Namespace)
	if err != nil {
		return fmt.Errorf("generate secret yaml: %w", err)
	}

	// kubectl apply with stdin pipe
	exec := executor.GetExecutor()
	cmd := exec.CmdKubectl(ctx, i.cfg.Kubeconfig, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply webhook tls secret: %w\n%s", err, string(out))
	}
	return nil
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

	// 提醒：如果 etcd 用了 hostPath，需手动清理 /data/etcd
	fmt.Printf("💡 提示：如果 etcd 数据持久化在 hostPath，请手动清理节点上的 /data/etcd 目录\n")
	fmt.Printf("   否则下次 Install 时旧数据会导致集群以 existing 状态启动\n")
	return nil
}

// Status 查询当前 controller 状态
type Status struct {
	Installed         bool
	Namespace         string
	ControllerReady   string // "3/3"
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

	// 2. 检查 StatefulSet ready
	out, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"get", "statefulset", "kubepivot-controller",
		"-n", i.cfg.Namespace,
		"-o", "jsonpath={.status.readyReplicas}/{.spec.replicas}",
		"--ignore-not-found",
	)
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		st.Installed = true
		st.ControllerReady = strings.TrimSpace(string(out))
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

// WaitReady 等待 StatefulSet ready（供 install 命令可选调用）
func (i *Installer) WaitReady(ctx context.Context, timeout time.Duration) error {
	exec := executor.GetExecutor()
	out, err := exec.Kubectl(ctx, i.cfg.Kubeconfig,
		"rollout", "status", "statefulset/kubepivot-controller",
		"-n", i.cfg.Namespace,
		fmt.Sprintf("--timeout=%ds", int(timeout.Seconds())),
	)
	if err != nil {
		return fmt.Errorf("等待 controller ready 失败: %w\n%s", err, string(out))
	}
	return nil
}
