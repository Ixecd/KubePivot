// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package main

import (
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
