package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var sensitiveKeyPattern = regexp.MustCompile(
	`(?i)(password|secret|token|key|seed|credential|passwd|api_key)\s*:\s*(".+"|[^"'\s#][^\s#]+)`,
)

// checkPlaintextSecrets 扫描 deployments/ 下所有 values.yaml 是否含明文敏感字段
func checkPlaintextSecrets(root string) checkResult {
	deploymentsDir := filepath.Join(root, "deployments")
	var hits []string

	_ = filepath.Walk(deploymentsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Name() != "values.yaml" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") {
				continue
			}
			if sensitiveKeyPattern.MatchString(line) {
				rel, _ := filepath.Rel(root, path)
				hits = append(hits, rel+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})

	if len(hits) > 0 {
		return checkResult{
			name:    "明文密码",
			ok:      false,
			isError: true,
			detail:  "发现敏感字段明文存储（" + strings.Join(hits[:min(2, len(hits))], "; ") + "...）",
			fix:     "将敏感变量迁移到 K8s Secret，使用 secretKeyRef 引用",
		}
	}
	return checkResult{name: "明文密码", ok: true, detail: "未发现明文敏感字段"}
}

// checkPodSecurityContext 检查 deployment/statefulset templates 是否含 securityContext
func checkPodSecurityContext(root string) checkResult {
	deploymentsDir := filepath.Join(root, "deployments")
	var missing []string

	_ = filepath.Walk(deploymentsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".yaml") {
			return nil
		}
		if !strings.Contains(name, "deployment") && !strings.Contains(name, "statefulset") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		if strings.Contains(content, "kind: Deployment") || strings.Contains(content, "kind: StatefulSet") {
			if !strings.Contains(content, "securityContext") {
				rel, _ := filepath.Rel(root, path)
				missing = append(missing, rel)
			}
		}
		return nil
	})

	if len(missing) > 0 {
		return checkResult{
			name:    "Pod 安全上下文",
			ok:      false,
			isError: false,
			detail:  "以下 template 缺少 securityContext: " + strings.Join(missing[:min(2, len(missing))], ", "),
			fix:     "添加 securityContext（runAsNonRoot/readOnlyRootFilesystem/allowPrivilegeEscalation）",
		}
	}
	return checkResult{name: "Pod 安全上下文", ok: true, detail: "所有 Pod 已配置 securityContext"}
}

// checkRBACWildcard 检查 RBAC Role 是否含 * 通配符
func checkRBACWildcard(root string) checkResult {
	deploymentsDir := filepath.Join(root, "deployments")
	var hits []string

	wildcardPattern := regexp.MustCompile(`resources:\s*\[?\s*"\*"`)

	_ = filepath.Walk(deploymentsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.Contains(info.Name(), "rbac") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if wildcardPattern.Match(data) || strings.Contains(string(data), `resources: ["*"]`) {
			rel, _ := filepath.Rel(root, path)
			hits = append(hits, rel)
		}
		return nil
	})

	if len(hits) > 0 {
		return checkResult{
			name:    "RBAC 权限",
			ok:      false,
			isError: false,
			detail:  "发现 * 通配符权限: " + strings.Join(hits, ", "),
			fix:     "收紧 RBAC Role，按最小权限原则只授予必要资源的必要动词",
		}
	}
	return checkResult{name: "RBAC 权限", ok: true, detail: "未发现过度权限配置"}
}

// checkNetworkPolicy 检查是否存在 NetworkPolicy 模板
func checkNetworkPolicy(root string) checkResult {
	deploymentsDir := filepath.Join(root, "deployments")
	found := false

	_ = filepath.Walk(deploymentsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(data), "kind: NetworkPolicy") {
			found = true
		}
		return nil
	})

	if !found {
		return checkResult{
			name:    "Network Policy",
			ok:      false,
			isError: false,
			detail:  "未发现 NetworkPolicy，Pod 间默认全通",
			fix:     "在 chart templates/ 添加 networkpolicy.yaml，限制入站流量",
		}
	}
	return checkResult{name: "Network Policy", ok: true, detail: "已配置 NetworkPolicy"}
}
