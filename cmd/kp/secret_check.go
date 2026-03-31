package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var secretKeyRefPattern = regexp.MustCompile(`secretKeyRef:\s*\n\s*name:\s*(\S+)`)

// checkRequiredSecrets 扫描 deployments/ 下所有 yaml 里的 secretKeyRef，
// 检查对应 secret 是否在 K8s 里存在，缺失时打印警告。
// 不阻断部署，只警告。
func checkRequiredSecrets(cfg *deployConfig, root string) {
	deploymentsDir := filepath.Join(root, "deployments")
	secrets := collectSecretNames(deploymentsDir)
	if len(secrets) == 0 {
		return
	}

	var missing []string
	for name := range secrets {
		if !secretExists(cfg, name) {
			missing = append(missing, name)
		}
	}

	if len(missing) == 0 {
		return
	}

	fmt.Println()
	P.Info("⚠️ ", "以下 Secret 在 K8s 中不存在，服务可能无法启动：")
	for _, name := range missing {
		fmt.Printf("    - %s\n", name)
	}
	fmt.Println("  请先运行: ./scripts/create-secret.sh")
	fmt.Println()
}

// collectSecretNames 扫描目录下所有 yaml 文件，提取 secretKeyRef.name 字段
func collectSecretNames(dir string) map[string]struct{} {
	secrets := make(map[string]struct{})
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if strings.Contains(line, "secretKeyRef:") && i+1 < len(lines) {
				next := strings.TrimSpace(lines[i+1])
				if strings.HasPrefix(next, "name:") {
					name := strings.TrimSpace(strings.TrimPrefix(next, "name:"))
					secrets[name] = struct{}{}
				}
			}
		}
		return nil
	})
	return secrets
}

// secretExists 检查 secret 是否在 K8s namespace 里存在
func secretExists(cfg *deployConfig, name string) bool {
	args := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	args = append(args, "get", "secret", name)
	_, err := runOutput(args...)
	return err == nil
}
