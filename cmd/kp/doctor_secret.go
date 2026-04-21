package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// checkTLSSecretExpiry 检查 namespace 下所有 TLS Secret 的过期时间
func checkTLSSecretExpiry(namespace string) []checkResult {
	if namespace == "" {
		return nil
	}

	out, err := exec.Command("/usr/local/bin/kubectl", "get", "secret",
		"--namespace", namespace,
		"--field-selector", "type=kubernetes.io/tls",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{'\\n'}{end}",
	).Output()
	if err != nil {
		return []checkResult{{
			name:    "TLS Secret 检查",
			ok:      false,
			isError: false,
			detail:  "查询失败（集群不可达或无 TLS Secret）",
		}}
	}

	names := strings.Fields(strings.TrimSpace(string(out)))
	if len(names) == 0 {
		return []checkResult{{
			name:   "TLS Secret",
			ok:     true,
			detail: "无 TLS 类型 Secret",
		}}
	}

	var results []checkResult
	for _, name := range names {
		result := checkSingleTLSSecret(namespace, name)
		results = append(results, result)
	}
	return results
}

func checkSingleTLSSecret(namespace, name string) checkResult {
	out, err := exec.Command("/usr/local/bin/kubectl", "get", "secret", name,
		"--namespace", namespace,
		"-o", "jsonpath={.data.tls\\.crt}",
	).Output()
	if err != nil || len(out) == 0 {
		return checkResult{
			name:   fmt.Sprintf("TLS %s", name),
			ok:     true,
			detail: "无法读取证书内容，跳过",
		}
	}

	// base64 decode
	decoded, err := exec.Command("base64", "-d").Output()
	if err != nil {
		// 直接尝试解析
	}

	// 用 openssl 解析过期时间
	cmd := exec.Command("openssl", "x509", "-noout", "-enddate")
	cmd.Stdin = strings.NewReader(string(out))
	endOut, err := cmd.Output()
	_ = decoded
	if err != nil {
		// openssl 不可用时用 Go 原生解析
		expiry, parseErr := parseCertExpiry(out)
		if parseErr != nil {
			return checkResult{
				name:   fmt.Sprintf("TLS %s", name),
				ok:     true,
				detail: "证书解析失败，跳过",
			}
		}
		return buildCertCheckResult(name, expiry)
	}

	// 解析 notAfter=Apr  3 00:00:00 2027 GMT 格式
	line := strings.TrimSpace(string(endOut))
	line = strings.TrimPrefix(line, "notAfter=")
	expiry, err := time.Parse("Jan  2 15:04:05 2006 MST", line)
	if err != nil {
		expiry, err = time.Parse("Jan _2 15:04:05 2006 MST", line)
		if err != nil {
			return checkResult{
				name:   fmt.Sprintf("TLS %s", name),
				ok:     true,
				detail: fmt.Sprintf("日期解析失败: %s", line),
			}
		}
	}
	return buildCertCheckResult(name, expiry)
}

func buildCertCheckResult(name string, expiry time.Time) checkResult {
	daysLeft := int(time.Until(expiry).Hours() / 24)
	detail := fmt.Sprintf("过期时间: %s（%d 天后）",
		expiry.Format("2006-01-02"), daysLeft)

	if daysLeft <= 0 {
		return checkResult{
			name:    fmt.Sprintf("TLS %s", name),
			ok:      false,
			isError: true,
			detail:  detail,
			fix:     "证书已过期，立即更新：kp secret rotate --secret " + name + " --type=tls",
		}
	}
	if daysLeft <= 7 {
		return checkResult{
			name:    fmt.Sprintf("TLS %s", name),
			ok:      false,
			isError: true,
			detail:  detail,
			fix:     "证书即将过期，立即更新：kp secret rotate --secret " + name,
		}
	}
	if daysLeft <= 30 {
		return checkResult{
			name:    fmt.Sprintf("TLS %s", name),
			ok:      false,
			isError: false,
			detail:  detail,
			fix:     "证书 30 天内过期，建议提前更新：kp secret rotate --secret " + name,
		}
	}
	return checkResult{
		name:   fmt.Sprintf("TLS %s", name),
		ok:     true,
		detail: detail,
	}
}
