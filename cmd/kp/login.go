// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Ixecd/kubepivot/internal/auth"
)

// runLogin kp login 命令 (v2.8 SSO).
//
// 用法:
//   kp login --provider google --client-id <id> --client-secret <secret>
//   kp login --provider github --client-id <id> --client-secret <secret>
//   kp login --provider dex --issuer https://dex.example.com --client-id <id> [--client-secret <secret>]
//
// 流程:
//   1. 解析 --provider, 构造对应 AuthProvider
//   2. 调 provider.Login() 跑完整 OAuth2 流程 (浏览器 → callback → token)
//   3. 保存 token + userinfo 到 ~/.kp/credentials/<provider>.yaml
//   4. 设置 default.yaml 指向当前 provider (Q-A3=C)
//
// 失败处理:
//   - 端口 18888 被占用 → fail-fast 提示
//   - 用户取消授权 / 浏览器关闭 → 5min 超时 + 错误
//   - token 端点错误 → 完整保留 OAuth provider 错误信息
func runLogin(args []string) {
	flags := flag.NewFlagSet("login", flag.ExitOnError)
	provider := flags.String("provider", "", "OAuth provider: google / github / dex (必填)")
	issuer := flags.String("issuer", "", "Dex issuer URL (仅 --provider=dex 必填, 如 https://dex.example.com)")
	clientID := flags.String("client-id", "", "OAuth client ID (必填)")
	clientSecret := flags.String("client-secret", "", "OAuth client secret (Google/GitHub 必填, Dex 可选)")
	flags.Parse(args)

	if *provider == "" {
		fmt.Fprintln(os.Stderr, "❌ --provider 必填: google / github / dex")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "示例:")
		fmt.Fprintln(os.Stderr, "  kp login --provider google --client-id xxx.apps.googleusercontent.com --client-secret yyy")
		fmt.Fprintln(os.Stderr, "  kp login --provider github --client-id Iv1.xxx --client-secret yyy")
		fmt.Fprintln(os.Stderr, "  kp login --provider dex --issuer https://dex.example.com --client-id kubepivot --client-secret yyy")
		os.Exit(1)
	}
	if *clientID == "" {
		fmt.Fprintln(os.Stderr, "❌ --client-id 必填")
		os.Exit(1)
	}

	// 构造 provider
	var p auth.AuthProvider
	var err error
	switch *provider {
	case "google":
		if *clientSecret == "" {
			fmt.Fprintln(os.Stderr, "❌ Google 必填 --client-secret")
			os.Exit(1)
		}
		p, err = auth.NewGoogleProvider(*clientID, *clientSecret)
	case "github":
		if *clientSecret == "" {
			fmt.Fprintln(os.Stderr, "❌ GitHub 必填 --client-secret")
			os.Exit(1)
		}
		p, err = auth.NewGitHubProvider(*clientID, *clientSecret)
	case "dex":
		if *issuer == "" {
			fmt.Fprintln(os.Stderr, "❌ Dex 必填 --issuer (如 https://dex.example.com)")
			os.Exit(1)
		}
		p, err = auth.NewDexProvider(*issuer, *clientID, *clientSecret)
	default:
		fmt.Fprintf(os.Stderr, "❌ 未知 provider: %q (支持 google/github/dex)\n", *provider)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 构造 provider 失败: %v", err)
		os.Exit(1)
	}

	// 跑 OAuth 流程
	P.Info("🔐", fmt.Sprintf("启动 OAuth 流程 (provider=%s)", p.Name()))
	P.Info("🌐", "浏览器即将打开授权页面 (回调端口 18888)")

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute) // 5min OAuth + buffer
	defer cancel()

	token, info, err := p.Login(ctx)
	if err != nil {
		P.Fail(fmt.Sprintf("OAuth 失败: %v", err))
		os.Exit(1)
	}

	// 保存 credentials
	if err := auth.SaveCredentials(p.Name(), token, info); err != nil {
		P.Fail(fmt.Sprintf("保存 credentials 失败: %v", err))
		os.Exit(1)
	}

	// 设置默认 provider (Q-A3=C)
	if err := auth.SetDefault(p.Name()); err != nil {
		P.Fail(fmt.Sprintf("设置默认 provider 失败: %v", err))
		os.Exit(1)
	}

	// 成功消息
	fmt.Println()
	P.Done(fmt.Sprintf("登录成功 (provider=%s)", p.Name()))
	fmt.Printf("  Email:      %s", info.Email)
	fmt.Printf("  Name:       %s", info.Name)
	if len(info.Groups) > 0 {
		fmt.Printf("  Groups:     %v", info.Groups)
	}
	fmt.Printf("  Expires at: %s", token.ExpiresAt.Local().Format("2006-01-02 15:04:05"))
	fmt.Println()
	fmt.Printf("  credentials 已保存: %s", auth.CredentialsPath(p.Name()))
	fmt.Printf("  当前默认 provider: %s", p.Name())
	fmt.Println()
	fmt.Println("  下一步: kp whoami 查看登录信息")
}
