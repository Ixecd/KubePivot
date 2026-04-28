// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/sealed"
)

// runSecretSeal 实现 kp secret seal 命令.
//
// 用法 (Q-G.3=A 跟 kubectl create secret 风格一致):
//
//	kp secret seal <name> \
//	    --from-literal=KEY=VALUE \
//	    [--from-literal=K2=V2 ...] \
//	    [--from-file=KEY=PATH ...] \
//	    [--namespace <ns>] \
//	    [--scope <strict|namespace-wide|cluster-wide>] \
//	    [--cert <path>] \
//	    [--output <path>]
//
// 示例:
//
//	kp secret seal db-creds \
//	    --from-literal=password=mypass \
//	    --from-literal=user=admin \
//	    --namespace kp-prod \
//	    --output configs/sealed/db-creds.yaml
//
// 输出 (Q-G.4=C 默认 stdout, --output 覆盖):
//
//	没指定 --output → 写到 stdout (Unix 哲学, 可 pipe)
//	指定 --output    → 写到文件 (跟 git commit 集成)
//
// 14/18 次打脸预防:
//
//	位置参数 + flag 混用模式, 必须先提取 name 再 flags.Parse(args[1:])
//	不能用 flags.Parse(args) (会在第一个非 flag "name" 处停止)
func runSecretSeal(args []string) {
	flags := flag.NewFlagSet("secret seal", flag.ExitOnError)

	var literalsFlag stringSliceFlag
	var filesFlag stringSliceFlag
	flags.Var(&literalsFlag, "from-literal", "key=value (可指定多次)")
	flags.Var(&filesFlag, "from-file", "key=path (可指定多次)")

	namespace := flags.String("namespace", "default", "K8s namespace")
	scopeFlag := flags.String("scope", string(sealed.DefaultScope),
		"解密 scope: strict | namespace-wide | cluster-wide")
	certPath := flags.String("cert", "",
		"公钥文件路径 (空时 kubeseal 实时从集群 fetch)")
	outputPath := flags.String("output", "",
		"输出文件路径 (空时输出到 stdout)")

	// Q-G.3=A 位置参数 + flag 混用 (跟 kp team 一致)
	// 必须先提取 name, 再 flags.Parse(args[1:])
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "用法: kp secret seal <name> [flags]")
		fmt.Fprintln(os.Stderr, "  --from-literal=KEY=VALUE   (必须 1 个或多个)")
		fmt.Fprintln(os.Stderr, "  --from-file=KEY=PATH       (可选)")
		fmt.Fprintln(os.Stderr, "  --namespace <ns>           (默认 default)")
		fmt.Fprintln(os.Stderr, "  --scope <s>                (默认 namespace-wide)")
		fmt.Fprintln(os.Stderr, "  --cert <path>              (默认实时获取)")
		fmt.Fprintln(os.Stderr, "  --output <path>            (默认 stdout)")
		os.Exit(1)
	}
	secretName := args[0]
	flags.Parse(args[1:])

	// Step 1: 检测 kubeseal 是否可用 (Q-G.2=A doctor 模式)
	available, err := sealed.DetectKubeseal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌ 检测 kubeseal 失败:", err)
		os.Exit(1)
	}
	if !available {
		fmt.Fprintln(os.Stderr, "❌ "+sealed.InstallHint())
		os.Exit(1)
	}

	// Step 2: 解析 --from-literal / --from-file 到 map
	literals, err := parseKVPairs(literalsFlag, "from-literal")
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌", err)
		os.Exit(1)
	}
	files, err := parseKVPairs(filesFlag, "from-file")
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌", err)
		os.Exit(1)
	}

	if len(literals) == 0 && len(files) == 0 {
		fmt.Fprintln(os.Stderr,
			"❌ 必须至少 1 个 --from-literal 或 --from-file")
		os.Exit(1)
	}

	// Step 3: 调 sealed.SealSecret
	opts := sealed.SealOptions{
		SecretName:   secretName,
		Namespace:    *namespace,
		Scope:        sealed.Scope(*scopeFlag),
		FromLiterals: literals,
		FromFiles:    files,
		CertPath:     *certPath,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sealedYAML, err := sealed.SealSecret(ctx, opts, sealed.NewRunner())
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌ Seal 失败:", err)
		os.Exit(1)
	}

	// Step 4: 输出 (Q-G.4=C 默认 stdout, --output 覆盖)
	if *outputPath == "" {
		os.Stdout.Write(sealedYAML)
		return
	}

	if err := os.WriteFile(*outputPath, sealedYAML, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "❌ 写入 --output 失败:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "✅ SealedSecret 已写入 %s\n", *outputPath)
	fmt.Fprintf(os.Stderr, "   可以 git commit (即使泄露也无法解密)\n")
}

// stringSliceFlag 实现 flag.Value, 支持多次出现的 flag (--from-literal=a=1 --from-literal=b=2).
type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// parseKVPairs 把 ["k1=v1", "k2=v2"] 解析成 map[k1:v1, k2:v2].
//
// 错误情况:
//   - 没 = 号: "key1" → 错误
//   - = 号在开头: "=value" → 错误 (key 为空)
//   - 重复 key: 后者覆盖前者 (跟 kubectl 行为一致)
func parseKVPairs(pairs []string, flagName string) (map[string]string, error) {
	if len(pairs) == 0 {
		return map[string]string{}, nil
	}

	result := make(map[string]string, len(pairs))
	for _, p := range pairs {
		idx := strings.Index(p, "=")
		if idx < 0 {
			return nil, fmt.Errorf("--%s=%s: 缺少 = 号 (期望格式 KEY=VALUE)",
				flagName, p)
		}
		key := strings.TrimSpace(p[:idx])
		value := p[idx+1:] // value 不 trim, 保留前后空格

		if key == "" {
			return nil, fmt.Errorf("--%s=%s: key 不能为空",
				flagName, p)
		}
		result[key] = value
	}
	return result, nil
}
