// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Ixecd/kubepivot/internal/supplychain"
)

var osExitFunc = os.Exit

// runSupplyChain 是 kp supply-chain 的入口 (dispatch 子命令)
func runSupplyChain(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: kp supply-chain <subcommand>\nAvailable: verify\n")
		osExitFunc(1)
	}

	switch args[0] {
	case "verify":
		runSupplyChainVerify(args[1:])
	case "sbom":
		runSupplyChainSbom(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n", args[0])
		osExitFunc(1)
	}
}

// ensureCosignAvailable 检查 cosign 是否可用 (对齐 doctor.go 的 checkTrivy 模式)
// 返回:
//
//	(string, nil): cosign 二进制路径
//	("", error): 未找到或不可执行
func ensureCosignAvailable() (string, error) {
	// 1. 查找 PATH
	path, err := exec.LookPath("cosign")
	if err != nil {
		return "", fmt.Errorf("cosign not found in PATH")
	}
	// 2. 快速冒烟测试（对齐 checkTrivy 执行 --version）
	if _, err := exec.Command(path, "version").Output(); err != nil {
		return "", fmt.Errorf("cosign found but not executable")
	}
	return path, nil
}

// runSupplyChainVerify 实现 kp supply-chain verify <IMAGE> --key <PUBKEY> [--output json|text]
func runSupplyChainVerify(args []string) {
	// 1. 参数解析 (简单手动解析，保持轻量)
	var image, pubKey, outputFmt string
	outputFmt = "text" // 默认

	// 解析位置参数 <IMAGE>
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		image = args[0]
		args = args[1:]
	}

	// 解析 flag
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--key", "-k":
			if i+1 < len(args) {
				pubKey = args[i+1]
				i++
			}
		case "--output", "-o":
			if i+1 < len(args) {
				outputFmt = args[i+1]
				i++
			}
		case "--help", "-h":
			printSupplyChainVerifyHelp()
			return
		}
	}

	// 2. 参数校验
	if image == "" || pubKey == "" {
		fmt.Fprintf(os.Stderr, "Error: --image and --key are required\n")
		printSupplyChainVerifyHelp()
		osExitFunc(1)
	}

	// 3. 依赖检测：对齐 doctor.go 风格
	//    错误输出格式: "✗ cosign              <detail>" + 缩进的 fix 建议
	cosignPath, err := ensureCosignAvailable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ cosign %-16s %s\n", "", err)
		fmt.Fprintf(os.Stderr, "  %sinstall: https://github.com/sigstore/cosign/releases\n", strings.Repeat(" ", 18))
		fmt.Fprintf(os.Stderr, "  %sor run 'kp doctor' for full environment check\n", strings.Repeat(" ", 18))
		osExitFunc(1)
	}

	// 4. 执行验证
	verifier := supplychain.NewCosignVerifier(cosignPath)
	result, err := verifier.Verify(image, pubKey)

	// 5. 处理执行错误 (工具故障，非业务失败)
	//    退出码 2 = 工具错误，1 = 验证失败，0 = 成功 (对齐 Unix 惯例)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExitFunc(1)
	}

	// 6. 输出结果
	switch supplychain.OutputFormat(outputFmt) {
	case supplychain.FormatJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
	default: // text
		if result.Verified {
			fmt.Printf("✓ %s verified", result.Image)
			if result.SignatureDigest != "" {
				fmt.Printf(" (sha256:%s)", supplychain.ShortDigest(result.SignatureDigest))
			}
			fmt.Println()
		} else {
			fmt.Fprintf(os.Stderr, "✗ verification failed: %s\n", result.Error)
		}
	}

	// 7. 退出码：验证失败 → 1，方便 CI/CD 判断
	if !result.Verified {
		osExitFunc(1)
	}
}

func printSupplyChainVerifyHelp() {
	fmt.Println(`Usage: kp supply-chain verify <IMAGE> --key <PUBKEY> [options]

Verify container image signature using cosign (key-based mode).

Arguments:
  <IMAGE>          Container image reference (e.g., registry.io/repo:tag)

Flags:
  -k, --key PATH   Path to cosign public key (required)
  -o, --output FMT Output format: text (default) or json
  -h, --help       Show this help

Examples:
  kp supply-chain verify registry.io/wallet:v1.2.3 --key cosign.pub
  kp supply-chain verify registry.io/wallet:v1.2.3 -k cosign.pub -o json | jq .verified

Exit codes:
  0  Verification succeeded
  1  Verification failed (invalid signature)
  2  Tool error (cosign not found, network timeout, etc.)`)
}

// shortDigest 截断 sha256:xxx 为前 12 位，方便终端阅读
func shortDigest(digest string) string {
	if strings.HasPrefix(digest, "sha256:") && len(digest) > 19 {
		return digest[7:19]
	}
	return digest
}

// runSupplyChainSbom 实现 kp supply-chain sbom <IMAGE> [--format cyclonedx|spdx|table] [--output PATH]
func runSupplyChainSbom(args []string) {
	// 1. 参数解析
	var image, formatStr, outputPath string
	formatStr = string(supplychain.CycloneDXJSON) // 默认

	// 位置参数 <IMAGE>
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		image = args[0]
		args = args[1:]
	}

	// Flags
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--format", "-f":
			if i+1 < len(args) {
				formatStr = args[i+1]
				i++
			}
		case "--output", "-o":
			if i+1 < len(args) {
				outputPath = args[i+1]
				i++
			}
		case "--help", "-h":
			printSupplyChainSbomHelp()
			return
		}
	}

	// 2. 参数校验
	if image == "" {
		fmt.Fprintf(os.Stderr, "Error: <IMAGE> is required\n")
		printSupplyChainSbomHelp()
		osExitFunc(1)
	}

	format := supplychain.SBOMFormat(formatStr)
	if !format.IsValid() {
		fmt.Fprintf(os.Stderr, "Error: unsupported format %q (supported: cyclonedx-json, spdx-json, table)\n", formatStr)
		osExitFunc(1)
	}

	// 3. 依赖检测
	_, err := ensureSyftAvailable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ syft %-16s %s\n", "", err)
		fmt.Fprintf(os.Stderr, "  %sinstall: https://github.com/anchore/syft/releases\n", strings.Repeat(" ", 18))
		fmt.Fprintf(os.Stderr, "  %sor run 'kp doctor' for full environment check\n", strings.Repeat(" ", 18))
		osExitFunc(1)
	}

	// 4. 执行扫描
	//    注意：supplychain.Scan 内部用 execCommandFunc，测试可 mock
	//    这里直接调用，生产环境用真实 syft
	ctx := context.Background()
	result, err := supplychain.Scan(ctx, image, format)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExitFunc(2) // 工具错误
	}

	// 5. 输出
	//    默认：stdout (可 pipe)
	//    --output 指定：写文件 (自动创建父目录)
	//    Table 格式：直接输出原始文本
	//    JSON 格式：可考虑美化输出（可选）

	output := result.Content
	if format == supplychain.Table {
		// Table 格式已经是人类可读，直接输出
	}

	if outputPath != "" {
		// 写文件：自动创建父目录
		// 复用 KubePivot 既有文件写入模式
		// TODO: 替换为实际项目中的 file utils
		if err := writeToFile(outputPath, output); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to write SBOM to %s: %v\n", outputPath, err)
			osExitFunc(2)
		}
		fmt.Printf("✓ SBOM written to %s\n", outputPath)
	} else {
		// stdout
		fmt.Print(output)
	}
}

// ensureSyftAvailable 检查 syft 是否可用 (对齐 ensureCosignAvailable)
func ensureSyftAvailable() (string, error) {
	path, err := exec.LookPath("syft")
	if err != nil {
		return "", fmt.Errorf("syft not found in PATH")
	}
	// 冒烟测试
	if _, err := exec.Command(path, "--version").Output(); err != nil {
		return "", fmt.Errorf("syft found but not executable")
	}
	return path, nil
}

// writeToFile 写内容到文件 (复用项目既有模式)
func writeToFile(path, content string) error {
	// TODO: 替换为 internal/scaffold 或 internal/logger 的文件写入工具
	// 这里用标准库最小实现
	// 注意：生产环境需考虑权限、原子写入等
	// Level2 先保持简单
	// 如果父目录不存在，尝试创建
	// ... (具体实现按项目现有 file utils 调整)

	// 简化版：
	// 1. 创建父目录
	// 2. 写入内容
	// 3. 返回错误
	// 实际请用项目既有的 file.WriteAtomic 或类似工具

	// 临时实现：
	// return os.WriteFile(path, []byte(content), 0644)

	// 更健壮实现（推荐）：
	// 参考 internal/scaffold/scaffold.go 的 writeIfChanged 模式
	return fmt.Errorf("TODO: implement writeToFile with project file utils")
}

func printSupplyChainSbomHelp() {
	fmt.Println(`Usage: kp supply-chain sbom <IMAGE> [options]

Generate Software Bill of Materials (SBOM) for a container image using syft.

Arguments:
  <IMAGE>          Container image reference (e.g., registry.io/repo:tag)

Flags:
  -f, --format FMT Output format: cyclonedx-json (default), spdx-json, table
  -o, --output PATH Write to file instead of stdout
  -h, --help       Show this help

Examples:
  kp supply-chain sbom registry.io/app:v1.2.3
  kp supply-chain sbom registry.io/app:v1.2.3 -f spdx-json
  kp supply-chain sbom registry.io/app:v1.2.3 -o sbom.json
  kp supply-chain sbom registry.io/app:v1.2.3 -f table | less

Output:
  - cyclonedx-json / spdx-json: Structured JSON for CI/CD integration
  - table: Human-readable summary for terminal preview

Exit codes:
  0  SBOM generated successfully
  1  Invalid arguments or unsupported format
  2  Tool error (syft not found, network timeout, etc.)`)
}
