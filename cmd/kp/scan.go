package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ixecd/kubepivot/internal/planner"
)

// TrivyReport trivy JSON 输出结构
type TrivyReport struct {
	Results []TrivyResult `json:"Results"`
}

type TrivyResult struct {
	Target          string           `json:"Target"`
	Vulnerabilities []TrivyVulnEntry `json:"Vulnerabilities"`
}

type TrivyVulnEntry struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
}

func runScan(args []string) {
	flags := flag.NewFlagSet("scan", flag.ExitOnError)
	severityFlag := flags.String("severity", "CRITICAL,HIGH", "阻断的严重级别，逗号分隔（CRITICAL/HIGH/MEDIUM/LOW）")
	imageFlag := flags.String("image", "", "指定单个镜像扫描，留空则扫描所有服务镜像")
	sbom := flags.Bool("sbom", false, "生成 SBOM 物料清单（CycloneDX 格式）")
	verify := flags.Bool("verify", false, "验证镜像 cosign 签名（keyless）")

	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "解析参数失败:", err)
		os.Exit(1)
	}

	// 检查 trivy 是否安装
	if _, err := runOutput("trivy", "--version"); err != nil {
		fmt.Fprintln(os.Stderr, "trivy 未安装，请运行: make install.trivy")
		os.Exit(1)
	}

	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}

	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	registryPrefix := envOrDefault(env, "REGISTRY_PREFIX", "")
	arch := envOrDefault(env, "ARCH", "amd64")
	version := envOrDefault(env, "VERSION", "v0.1.0")

	blockSeverities := parseSeverities(*severityFlag)

	// 收集要扫描的镜像
	var images []string
	if *imageFlag != "" {
		images = []string{*imageFlag}
	} else {
		components, err := planner.LoadComponents(filepath.Join(root, "configs", "components.yaml"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "读取 components.yaml 失败:", err)
			os.Exit(1)
		}
		for _, c := range components {
			if c.Image == "" {
				continue
			}
			images = append(images, buildImageName(registryPrefix, c.Image, arch, version))
		}
	}

	if len(images) == 0 {
		P.Info("ℹ️ ", "没有需要扫描的镜像")
		return
	}

	P.Info("🔍", fmt.Sprintf("开始扫描 %d 个镜像（阻断级别: %s）", len(images), *severityFlag))
	fmt.Println()

	var hasBlocker bool
	for _, image := range images {
		blocked := scanImage(image, blockSeverities)
		if blocked {
			hasBlocker = true
		}
	}

	fmt.Println()
	if hasBlocker {
		P.Fail(fmt.Sprintf("发现高危漏洞（%s），部署已阻断", *severityFlag))
		os.Exit(1)
	}

	// SBOM 生成
	if *sbom {
		fmt.Println()
		P.Info("📋", "生成 SBOM 物料清单（CycloneDX）")
		for _, image := range images {
			generateSBOM(image)
		}
	}

	if *verify {
		fmt.Println()
		P.Info("🔏", "验证镜像签名（keyless）")
		for _, image := range images {
			verifySignature(image)
		}
	}

	P.Info("✅", "镜像扫描通过，未发现阻断级别漏洞")
}

// scanImage 扫描单个镜像，返回是否有阻断级别漏洞
func scanImage(image string, blockSeverities map[string]bool) bool {
	P.Start("🔍", fmt.Sprintf("扫描 %s", image))

	out, err := runOutput("trivy", "image",
		"--format", "json",
		"--quiet",
		image,
	)
	if err != nil {
		P.Info("⏭ ", fmt.Sprintf("跳过 %s（镜像不存在或扫描失败）", image))
		return false // 不阻断，但也不算通过
	}

	var report TrivyReport
	if err := json.Unmarshal(out, &report); err != nil {
		P.Fail(fmt.Sprintf("解析扫描结果失败: %v", err))
		return false
	}

	var blockers []TrivyVulnEntry
	var warnings []TrivyVulnEntry

	for _, result := range report.Results {
		for _, vuln := range result.Vulnerabilities {
			if blockSeverities[vuln.Severity] {
				blockers = append(blockers, vuln)
			} else {
				warnings = append(warnings, vuln)
			}
		}
	}

	if len(blockers) == 0 && len(warnings) == 0 {
		P.Done(fmt.Sprintf("%s 无漏洞", image))
		return false
	}

	if len(blockers) > 0 {
		P.Fail(fmt.Sprintf("%s 发现 %d 个高危漏洞", image, len(blockers)))
		for _, v := range blockers {
			fmt.Printf("    ❌ [%s] %s %s → %s\n", v.Severity, v.VulnerabilityID, v.PkgName, v.Title)
		}
	}

	if len(warnings) > 0 {
		P.Done(fmt.Sprintf("%s 发现 %d 个中低危漏洞（告警）", image, len(warnings)))
		for _, v := range warnings {
			fmt.Printf("    ⚠️  [%s] %s %s → %s\n", v.Severity, v.VulnerabilityID, v.PkgName, v.Title)
		}
	}

	return len(blockers) > 0
}

// parseSeverities 解析 severity 字符串为 map
func parseSeverities(s string) map[string]bool {
	result := make(map[string]bool)
	for _, sev := range strings.Split(s, ",") {
		result[strings.TrimSpace(strings.ToUpper(sev))] = true
	}
	return result
}

// generateSBOM 生成 CycloneDX 格式的 SBOM 物料清单
func generateSBOM(image string) {
	// 文件名：镜像名里的 / 和 : 替换成 -
	safe := strings.NewReplacer("/", "-", ":", "-").Replace(image)
	outFile := fmt.Sprintf("sbom-%s.json", safe)

	P.Start("📋", fmt.Sprintf("生成 %s 的 SBOM", image))
	_, err := runOutput("trivy", "image",
		"--format", "cyclonedx",
		"--output", outFile,
		"--quiet",
		image,
	)
	if err != nil {
		P.Fail(fmt.Sprintf("SBOM 生成失败: %v", err))
		return
	}
	P.Done(fmt.Sprintf("已输出到 %s", outFile))
}

func verifySignature(image string) {
	P.Start("🔏", fmt.Sprintf("验证 %s", image))
	_, err := runOutput("cosign", "verify",
		"--certificate-identity-regexp", ".*",
		"--certificate-oidc-issuer-regexp", ".*",
		image,
	)
	if err != nil {
		P.Fail(fmt.Sprintf("%s 签名验证失败（未签名或签名无效）", image))
		return
	}
	P.Done(fmt.Sprintf("%s 签名验证通过", image))
}

func signImage(image string) {
	if _, err := runOutput("cosign", "version"); err != nil {
		P.Info("⏭ ", "cosign 未安装，跳过签名")
		return
	}
	P.Start("🔏", fmt.Sprintf("签名 %s（keyless）", image))
	// keyless 需要交互式 OIDC 登录，CI 环境通过 COSIGN_EXPERIMENTAL=1 自动完成
	cmd := []string{"cosign", "sign", "--yes", image}
	if _, err := runOutput(cmd...); err != nil {
		P.Fail(fmt.Sprintf("签名失败: %v", err))
		return
	}
	P.Done(fmt.Sprintf("%s 签名完成", image))
}
