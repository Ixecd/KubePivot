package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Ixecd/kubepivot/internal/planner"
)

// checkCrossNsDeps 跨 namespace 依赖嗅探（只读，不自愈）
func checkCrossNsDeps(cfg *deployConfig, root string) []checkResult {
	components, err := planner.LoadComponents(
		filepath.Join(root, "configs", "components.yaml"),
	)
	if err != nil {
		return nil
	}

	// 收集所有跨 namespace 依赖
	type crossDep struct {
		from      string
		namespace string
		service   string
	}
	var deps []crossDep
	for _, c := range components {
		for _, d := range c.CrossNsDeps {
			parts := strings.SplitN(d, "/", 2)
			if len(parts) != 2 {
				continue
			}
			deps = append(deps, crossDep{
				from:      c.Name,
				namespace: parts[0],
				service:   parts[1],
			})
		}
	}

	if len(deps) == 0 {
		return nil
	}

	var results []checkResult
	for _, dep := range deps {
		name := fmt.Sprintf("跨域依赖 %s→%s/%s", dep.from, dep.namespace, dep.service)

		// 只读嗅探：尝试 get deployment/statefulset
		found := false
		for _, kind := range []string{"deployment", "statefulset"} {
			args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, dep.namespace)
			args = append(args, "get", kind, dep.service,
				"--ignore-not-found", "-o", "name")
			out, err := runOutput(args...)
			if err == nil && strings.TrimSpace(string(out)) != "" {
				found = true
				break
			}
		}

		if found {
			results = append(results, checkResult{
				name:   name,
				ok:     true,
				detail: fmt.Sprintf("namespace/%s 中 %s 存在", dep.namespace, dep.service),
			})
		} else {
			results = append(results, checkResult{
				name:    name,
				ok:      false,
				isError: false,
				detail:  fmt.Sprintf("namespace/%s 中 %s 不存在或无权限", dep.namespace, dep.service),
				fix:     fmt.Sprintf("检查 %s namespace 是否已部署，或确认 RBAC 只读权限", dep.namespace),
			})
		}
	}
	return results
}
