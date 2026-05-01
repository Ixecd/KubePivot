package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Ixecd/kubepivot/internal/audit"
	"github.com/Ixecd/kubepivot/internal/bluegreen"
	"github.com/Ixecd/kubepivot/internal/planner"
	"github.com/Ixecd/kubepivot/internal/rbac"
)

func runPromote(args []string) {
	flags := flag.NewFlagSet("promote", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 文件路径")
	service := flags.String("service", "", "指定单个服务，留空则 promote 所有蓝绿服务")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}

	root, err := Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	cfg := &deployConfig{
		namespace:  *namespace,
		context:    *context,
		kubeconfig: *kubeconfig,
	}
	resolveDeployConfig(cfg, env, root)

	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))

	// RBAC 检查
	mustCheck(audit.ResolveActor(), cfg.namespace, rbac.PermPromote)

	etcdEndpoints := env["ETCD_ENDPOINTS"]
	store := bluegreen.NewAutoStore(etcdEndpoints)

	components, err := planner.LoadComponents(filepath.Join(root, "configs", "components.yaml"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取 components.yaml 失败:", err)
		os.Exit(1)
	}

	promoted := 0
	for _, c := range components {
		if c.Strategy != "blue-green" {
			continue
		}
		if *service != "" && c.Name != *service {
			continue
		}

		bgState, err := store.Load(projectName, cfg.namespace, c.Name)
		if err != nil || bgState == nil {
			P.Info("⏭ ", fmt.Sprintf("跳过 %s（无蓝绿状态）", c.Name))
			continue
		}

		targetSlot := bgState.InactiveSlot()
		P.Info("🔄", fmt.Sprintf("%s：切换流量 %s → %s", c.Name, bgState.Active, targetSlot))

		if err := switchServiceSelector(cfg, c.Name, targetSlot); err != nil {
			P.Fail(fmt.Sprintf("%s Service selector 切换失败: %v", c.Name, err))
			continue
		}

		// 更新活跃 slot
		bgState.Active = targetSlot
		if err := store.Save(projectName, cfg.namespace, c.Name, bgState); err != nil {
			P.Info("⚠️ ", fmt.Sprintf("状态保存失败: %v", err))
		}

		P.Done(fmt.Sprintf("%s 已切换到 %s slot ✅", c.Name, targetSlot))
		promoted++
	}

	if promoted == 0 {
		P.Info("ℹ️ ", "没有需要 promote 的蓝绿服务")
		return
	}
	P.Info("✅", fmt.Sprintf("完成，%d 个服务已 promote", promoted))
}
