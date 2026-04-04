package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ── 主入口 ────────────────────────────────────────────────────────────────────

func runSecret(args []string) {
	if len(args) == 0 {
		printSecretUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "rotate":
		runSecretRotate(args[1:])
	case "cleanup":
		runSecretCleanup(args[1:])
	case "sync":
		runSecretSync(args[1:])
	case "audit":
		runSecretAudit(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		printSecretUsage()
		os.Exit(1)
	}
}

func printSecretUsage() {
	fmt.Println("用法: kp secret <子命令>")
	fmt.Println("  kp secret rotate  --secret <name> [--strategy graceful|immediate] [--namespace <ns>]")
	fmt.Println("  kp secret cleanup --secret <name> [--namespace <ns>]")
	fmt.Println("  kp secret audit   [--namespace <ns>]")
}

// ── secretKeyRef 引用记录 ─────────────────────────────────────────────────────

type SecretRef struct {
	Service   string // 服务名
	MountType string // "env" | "volume"
	Kind      string // Deployment | StatefulSet
}

// ── kp secret rotate ──────────────────────────────────────────────────────────

func runSecretRotate(args []string) {
	flags := flag.NewFlagSet("secret rotate", flag.ExitOnError)
	secretName := flags.String("secret", "", "Secret 名称（必填）")
	strategy := flags.String("strategy", "immediate", "轮转策略：immediate | graceful")
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}
	if *secretName == "" {
		fmt.Fprintln(os.Stderr, "必须指定 --secret")
		os.Exit(1)
	}

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}
	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	cfg := resolvePVCConfig(*namespace, *context, *kubeconfig)
	if cfg.namespace == "" {
		cfg.namespace = envOrDefault(env, "KUBE_NAMESPACE", "default")
	}

	P.Info("🔐", fmt.Sprintf("Secret 轮转：%s（策略: %s）", *secretName, *strategy))
	fmt.Println()

	// 1. 扫描引用
	refs := scanSecretRefs(root, *secretName)
	if len(refs) == 0 {
		P.Info("⏭ ", fmt.Sprintf("未找到引用 %s 的服务", *secretName))
		return
	}
	fmt.Printf("  发现 %d 个服务引用该 Secret：\n", len(refs))
	for _, r := range refs {
		fmt.Printf("    %s %s（%s，挂载方式: %s）\n",
			colorize(colorCyan, "→"), r.Service, r.Kind, r.MountType)
	}
	fmt.Println()

	switch *strategy {
	case "graceful":
		runGracefulRotate(cfg, *secretName, refs, root, env)
	default:
		runImmediateRotate(cfg, *secretName, refs)
	}
}

// runImmediateRotate 立即轮转：直接 rollout restart
func runImmediateRotate(cfg pvcConfig, secretName string, refs []SecretRef) {
	P.Info("⚡", "立即轮转模式（适用于非 DB 类 Secret）")
	fmt.Println()

	for _, ref := range refs {
		if ref.MountType == "volume" {
			P.Info("⚠️ ", fmt.Sprintf("%s 通过 Volume 挂载 Secret，应用需支持热加载，强制触发 rollout restart", ref.Service))
		}
		if err := rolloutRestart(cfg, ref); err != nil {
			P.Fail(fmt.Sprintf("%s rollout restart 失败: %v", ref.Service, err))
		} else {
			P.Done(fmt.Sprintf("%s rollout restart 完成", ref.Service))
		}
	}

	fmt.Println()
	P.Info("✅", "轮转完成，请验证服务连接是否正常")
	writeAuditLog(secretName, "rotate/immediate", refs)
}

// runGracefulRotate 优雅轮转：双密码过渡期
func runGracefulRotate(cfg pvcConfig, secretName string, refs []SecretRef, root string, env map[string]string) {
	P.Info("🌿", "优雅轮转模式（双密码过渡期，适用于 DB 类 Secret）")
	fmt.Println()

	// Step 1: 在 Secret 里新增 *_OLD 备份字段
	P.Start("🔑", "备份当前 Secret 值（写入 *_OLD 字段）")
	if err := backupSecretFields(cfg, secretName); err != nil {
		P.Fail(fmt.Sprintf("备份失败: %v", err))
		os.Exit(1)
	}
	P.Done("备份完成，旧值已保留为 *_OLD 字段")

	// Step 2: 滚动重启所有引用服务
	fmt.Println()
	P.Info("🔄", "滚动重启所有引用服务")
	for _, ref := range refs {
		if ref.MountType == "volume" {
			P.Info("⚠️ ", fmt.Sprintf("%s 通过 Volume 挂载，强制 rollout restart", ref.Service))
		}
		if err := rolloutRestart(cfg, ref); err != nil {
			P.Fail(fmt.Sprintf("%s rollout restart 失败: %v", ref.Service, err))
			continue
		}
		P.Done(fmt.Sprintf("%s 重启完成", ref.Service))
	}

	// Step 3: 健康检查
	fmt.Println()
	P.Info("🔍", "健康检查（超时 60s）")
	for _, ref := range refs {
		if err := waitRolloutReady(cfg, ref, 60*time.Second); err != nil {
			P.Info("⚠️ ", fmt.Sprintf("%s 健康检查超时，请手动验证（Secret 已更新）", ref.Service))
		} else {
			P.Done(fmt.Sprintf("%s 健康", ref.Service))
		}
	}

	// Step 4: 提示用户在 DB 端禁用旧密码
	fmt.Println()
	fmt.Printf("%s 所有服务已使用新 Secret，请在数据库端禁用旧密码\n", colorize(colorYellow, "💡"))
	fmt.Printf("  确认完成后运行：%s\n", colorize(colorCyan, fmt.Sprintf("kp secret cleanup --secret %s", secretName)))

	writeAuditLog(secretName, "rotate/graceful", refs)
}

// ── kp secret cleanup ─────────────────────────────────────────────────────────

func runSecretCleanup(args []string) {
	flags := flag.NewFlagSet("secret cleanup", flag.ExitOnError)
	secretName := flags.String("secret", "", "Secret 名称（必填）")
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}
	if *secretName == "" {
		fmt.Fprintln(os.Stderr, "必须指定 --secret")
		os.Exit(1)
	}

	cfg := resolvePVCConfig(*namespace, *context, *kubeconfig)
	P.Info("🧹", fmt.Sprintf("清理 %s 的 *_OLD 过渡字段", *secretName))

	if err := cleanupOldSecretFields(cfg, *secretName); err != nil {
		P.Fail(fmt.Sprintf("清理失败: %v", err))
		os.Exit(1)
	}
	P.Done("清理完成，旧密码字段已删除")
}

// ── kp secret audit ───────────────────────────────────────────────────────────

func runSecretAudit(args []string) {
	flags := flag.NewFlagSet("secret audit", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	cfg := resolvePVCConfig(*namespace, *context, *kubeconfig)

	P.Info("🔍", "检查 Secret 过期时间（TLS 证书类）")
	fmt.Println()

	// 列出所有 Secret
	kargs := kubectlPVCArgs(cfg)
	kargs = append(kargs, "get", "secret",
		"--namespace", cfg.namespace,
		"-o", "json",
	)
	out, err := runOutput(kargs...)
	if err != nil {
		P.Fail("查询 Secret 失败")
		os.Exit(1)
	}

	var secretList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Type string            `json:"type"`
			Data map[string][]byte `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &secretList); err != nil {
		P.Fail("解析 Secret 失败")
		os.Exit(1)
	}

	found := false
	for _, s := range secretList.Items {
		if s.Type != "kubernetes.io/tls" {
			continue
		}
		found = true
		certData := s.Data["tls.crt"]
		if certData == nil {
			continue
		}
		expiry, err := parseCertExpiry(certData)
		if err != nil {
			fmt.Printf("  ⚠️  %-40s 无法解析证书\n", s.Metadata.Name)
			continue
		}
		daysLeft := int(time.Until(expiry).Hours() / 24)
		status := colorize(colorGreen, fmt.Sprintf("✓ %d 天后过期", daysLeft))
		if daysLeft <= 7 {
			status = colorize(colorRed, fmt.Sprintf("❌ %d 天后过期（紧急！）", daysLeft))
		} else if daysLeft <= 30 {
			status = colorize(colorYellow, fmt.Sprintf("⚠️  %d 天后过期", daysLeft))
		}
		fmt.Printf("  %-40s %s\n", s.Metadata.Name, status)
	}

	if !found {
		P.Info("ℹ️ ", "未找到 TLS 类型的 Secret")
	}
}

// ── 辅助函数 ──────────────────────────────────────────────────────────────────

// scanSecretRefs 扫描 deployments/ 下所有引用指定 Secret 的服务
func scanSecretRefs(root, secretName string) []SecretRef {
	var refs []SecretRef
	seen := make(map[string]bool)

	deploymentsDir := filepath.Join(root, "deployments")
	filepath.Walk(deploymentsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)

		// 检测 secretKeyRef
		if strings.Contains(content, fmt.Sprintf("name: %s", secretName)) {
			service, kind := extractServiceFromPath(path)
			key := service + kind
			if !seen[key] {
				mountType := "env"
				// 检测 volume 挂载
				if strings.Contains(content, "volumes:") &&
					strings.Contains(content, fmt.Sprintf("secretName: %s", secretName)) {
					mountType = "volume"
				}
				refs = append(refs, SecretRef{
					Service:   service,
					Kind:      kind,
					MountType: mountType,
				})
				seen[key] = true
			}
		}
		return nil
	})
	return refs
}

// extractServiceFromPath 从 yaml 路径提取服务名和 Kind
func extractServiceFromPath(path string) (string, string) {
	// deployments/<project>/<service>/templates/deployment.yaml
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, p := range parts {
		if p == "templates" && i >= 1 {
			service := parts[i-1]
			// 从文件名推断 Kind
			fileName := filepath.Base(path)
			kind := "Deployment"
			if strings.Contains(fileName, "statefulset") {
				kind = "StatefulSet"
			}
			return service, kind
		}
	}
	return filepath.Base(filepath.Dir(filepath.Dir(path))), "Deployment"
}

// rolloutRestart 重启服务
func rolloutRestart(cfg pvcConfig, ref SecretRef) error {
	args := kubectlPVCArgs(cfg)
	resource := strings.ToLower(ref.Kind) + "/" + ref.Service
	args = append(args, "rollout", "restart", resource,
		"--namespace", cfg.namespace,
	)
	_, err := runOutput(args...)
	return err
}

// waitRolloutReady 等待 rollout 完成
func waitRolloutReady(cfg pvcConfig, ref SecretRef, timeout time.Duration) error {
	args := kubectlPVCArgs(cfg)
	resource := strings.ToLower(ref.Kind) + "/" + ref.Service
	args = append(args, "rollout", "status", resource,
		"--namespace", cfg.namespace,
		fmt.Sprintf("--timeout=%ds", int(timeout.Seconds())),
	)
	_, err := runOutput(args...)
	return err
}

// backupSecretFields 把当前 Secret 的值备份为 *_OLD 字段
func backupSecretFields(cfg pvcConfig, secretName string) error {
	// 读取当前 Secret
	args := kubectlPVCArgs(cfg)
	args = append(args, "get", "secret", secretName,
		"--namespace", cfg.namespace,
		"-o", "json",
	)
	out, err := runOutput(args...)
	if err != nil {
		return err
	}

	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(out, &secret); err != nil {
		return err
	}

	// 构建 patch：把每个 key 的值复制到 key_OLD
	patch := map[string]interface{}{"data": map[string]string{}}
	data := patch["data"].(map[string]string)
	for k, v := range secret.Data {
		if !strings.HasSuffix(k, "_OLD") {
			data[k+"_OLD"] = v
		}
	}

	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return err
	}

	patchArgs := kubectlPVCArgs(cfg)
	patchArgs = append(patchArgs, "patch", "secret", secretName,
		"--namespace", cfg.namespace,
		"--type=merge",
		fmt.Sprintf("--patch=%s", string(patchJSON)),
	)
	_, err = runOutput(patchArgs...)
	return err
}

// cleanupOldSecretFields 删除 *_OLD 字段
func cleanupOldSecretFields(cfg pvcConfig, secretName string) error {
	args := kubectlPVCArgs(cfg)
	args = append(args, "get", "secret", secretName,
		"--namespace", cfg.namespace,
		"-o", "json",
	)
	out, err := runOutput(args...)
	if err != nil {
		return err
	}

	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(out, &secret); err != nil {
		return err
	}

	// patch 把 *_OLD 字段设为 null（删除）
	patch := map[string]interface{}{"data": map[string]interface{}{}}
	data := patch["data"].(map[string]interface{})
	for k := range secret.Data {
		if strings.HasSuffix(k, "_OLD") {
			data[k] = nil
		}
	}

	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return err
	}

	patchArgs := kubectlPVCArgs(cfg)
	patchArgs = append(patchArgs, "patch", "secret", secretName,
		"--namespace", cfg.namespace,
		"--type=merge",
		fmt.Sprintf("--patch=%s", string(patchJSON)),
	)
	_, err = runOutput(patchArgs...)
	return err
}

// parseCertExpiry 解析 TLS 证书过期时间
func parseCertExpiry(certPEM []byte) (time.Time, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return time.Time{}, fmt.Errorf("无法解析 PEM 数据")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return cert.NotAfter, nil
}

// writeAuditLog 写审计日志（etcd 优先，降级到本地文件）
func writeAuditLog(secretName, action string, refs []SecretRef) {
	services := make([]string, len(refs))
	for i, r := range refs {
		services[i] = r.Service
	}

	event := map[string]interface{}{
		"ts":       time.Now().Format(time.RFC3339),
		"action":   action,
		"resource": secretName,
		"services": services,
	}

	// 降级到本地文件
	home, _ := os.UserHomeDir()
	auditDir := filepath.Join(home, ".kp", "audit")
	os.MkdirAll(auditDir, 0o755)
	auditFile := filepath.Join(auditDir, "secret.jsonl")

	data, _ := json.Marshal(event)
	f, err := os.OpenFile(auditFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		defer f.Close()
		f.Write(append(data, '\n'))
	}
}

// runSecretSync 从 Vault 同步 Secret 到 K8s
func runSecretSync(args []string) {
	flags := flag.NewFlagSet("secret sync", flag.ExitOnError)
	from := flags.String("from", "", "来源（目前支持: vault）")
	vaultAddr := flags.String("vault-addr", "", "Vault 地址（默认读 VAULT_ADDR 环境变量）")
	vaultToken := flags.String("vault-token", "", "Vault Token（默认读 VAULT_TOKEN 环境变量）")
	vaultPath := flags.String("vault-path", "", "Vault KV 路径（如 secret/data/web3-blitz）")
	secretName := flags.String("secret", "", "目标 K8s Secret 名称（必填）")
	namespace := flags.String("namespace", "", "kubernetes namespace")
	context := flags.String("context", "", "kubernetes context")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig 路径")
	dryRun := flags.Bool("dry-run", false, "只打印将要同步的 key，不实际写入")
	flags.Parse(args)

	if *from != "vault" {
		fmt.Fprintln(os.Stderr, "❌ 目前只支持 --from vault")
		os.Exit(1)
	}
	if *secretName == "" {
		fmt.Fprintln(os.Stderr, "❌ --secret 必填")
		os.Exit(1)
	}
	if *vaultPath == "" {
		fmt.Fprintln(os.Stderr, "❌ --vault-path 必填（如 secret/data/myapp）")
		os.Exit(1)
	}

	// 读取 Vault 配置（环境变量优先）
	addr := *vaultAddr
	if addr == "" {
		addr = os.Getenv("VAULT_ADDR")
	}
	if addr == "" {
		addr = "http://127.0.0.1:8200"
	}

	token := *vaultToken
	if token == "" {
		token = os.Getenv("VAULT_TOKEN")
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "❌ 未找到 Vault Token，请设置 VAULT_TOKEN 或使用 --vault-token")
		os.Exit(1)
	}

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}
	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	cfg := &deployConfig{
		namespace:  *namespace,
		context:    *context,
		kubeconfig: expandHome(*kubeconfig),
	}
	resolveDeployConfig(cfg, env, root)

	// 从 Vault 读取 KV
	P.Start("🔐", fmt.Sprintf("从 Vault 读取 %s", *vaultPath))
	kvData, err := fetchVaultKV(addr, token, *vaultPath)
	if err != nil {
		P.Fail(fmt.Sprintf("读取 Vault 失败: %v", err))
		os.Exit(1)
	}
	P.Done(fmt.Sprintf("读取成功（%d 个 key）", len(kvData)))

	if *dryRun {
		fmt.Printf("\n%s dry-run 模式，将同步以下 key 到 Secret %q：\n\n",
			colorize(colorYellow, "📋"), *secretName)
		for k := range kvData {
			fmt.Printf("  %s %s\n", colorize(colorCyan, "→"), k)
		}
		fmt.Printf("\n实际执行：kp secret sync --from vault --secret %s --vault-path %s\n\n",
			*secretName, *vaultPath)
		return
	}

	// 构建 kubectl create secret 命令
	P.Start("📝", fmt.Sprintf("同步到 K8s Secret %q", *secretName))
	args2 := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	args2 = append(args2, "create", "secret", "generic", *secretName)
	for k, v := range kvData {
		args2 = append(args2, fmt.Sprintf("--from-literal=%s=%s", k, v))
	}
	args2 = append(args2, "--save-config", "--dry-run=client", "-o", "yaml")

	// 先生成 YAML，再 apply（幂等）
	out, err := runOutput(args2...)
	if err != nil {
		P.Fail(fmt.Sprintf("生成 Secret 失败: %v", err))
		os.Exit(1)
	}

	applyArgs := kubectlBaseArgs(cfg.kubeconfig, cfg.context, cfg.namespace)
	applyArgs = append(applyArgs, "apply", "-f", "-")

	// 通过 stdin pipe YAML 到 kubectl apply
	applyCmd := exec.Command(applyArgs[0], applyArgs[1:]...)
	applyCmd.Stdin = strings.NewReader(string(out))
	if applyOut, err := applyCmd.CombinedOutput(); err != nil {
		P.Fail(fmt.Sprintf("apply Secret 失败: %s", string(applyOut)))
		os.Exit(1)
	}

	P.Done(fmt.Sprintf("Secret %q 已同步（%d 个 key）", *secretName, len(kvData)))

	// 写审计日志
	home2, _ := os.UserHomeDir()
	af, _ := os.OpenFile(
		filepath.Join(home2, ".kp", "audit", "secret.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if af != nil {
		auditData, _ := json.Marshal(map[string]string{
			"action": "sync/vault", "resource": *secretName,
			"namespace": cfg.namespace, "ts": time.Now().Format(time.RFC3339),
		})
		af.Write(append(auditData, '\n'))
		af.Close()
	}
}

// fetchVaultKV 从 Vault KV v2 读取数据（net/http 实现，无外部依赖）
func fetchVaultKV(addr, token, path string) (map[string]string, error) {
	url := fmt.Sprintf("%s/v1/%s", strings.TrimRight(addr, "/"), path)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("构建请求失败: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("Vault Token 无权限（403）")
	}
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("Vault 路径 %q 不存在（404）", path)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Vault 返回 %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
		Errors []string `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("Vault 返回错误: %v", result.Errors)
	}
	if result.Data.Data == nil {
		return nil, fmt.Errorf("Vault 路径 %q 无数据", path)
	}
	return result.Data.Data, nil
}
