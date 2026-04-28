// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/auth"
)

// runWhoami kp whoami 命令 (v2.8 SSO).
//
// Q-A4=A 拍板: 不联网, 只本地读 credentials + 显示 token 状态.
// 不调 VerifyToken (避免每次 whoami 都触发 HTTP 请求).
//
// 用法:
//   kp whoami                    显示当前默认 provider 的登录信息
//   kp whoami --provider github  显示指定 provider 的登录信息
//   kp whoami --all              列出所有已登录 provider
func runWhoami(args []string) {
	flags := flag.NewFlagSet("whoami", flag.ExitOnError)
	provider := flags.String("provider", "", "指定 provider (默认读 default.yaml)")
	all := flags.Bool("all", false, "列出所有已登录 provider")
	flags.Parse(args)

	if *all {
		runWhoamiAll()
		return
	}

	// 决定 provider
	var providerName string
	if *provider != "" {
		providerName = *provider
	} else {
		def, err := auth.GetDefault()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		providerName = def
	}

	// 读 credentials
	token, info, err := auth.LoadCredentials(providerName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 显示
	displayUserInfo(providerName, token, info)
}

// runWhoamiAll 列出所有已登录 provider.
func runWhoamiAll() {
	providers, err := auth.ListCredentials()
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取 credentials 目录失败:", err)
		os.Exit(1)
	}

	if len(providers) == 0 {
		fmt.Println("当前没有任何已登录的 provider")
		fmt.Println("运行 kp login --provider <google|github|dex> ... 登录")
		return
	}

	defaultProvider, _ := auth.GetDefault()

	fmt.Printf("已登录 provider (%d 个):", len(providers))
	for _, name := range providers {
		token, info, err := auth.LoadCredentials(name)
		if err != nil {
			fmt.Printf("  ⚠ %s (读取失败: %v)", name, err)
			continue
		}

		marker := "  "
		if name == defaultProvider {
			marker = "* " // 当前默认
		}
		statusIcon := "✓"
		if token.Expired() {
			statusIcon = "⚠"
		}
		fmt.Printf("%s%s %-10s %s   (%s)",
			marker, statusIcon, name, info.Email, tokenStatus(token))
	}
	fmt.Println()
	fmt.Println("(* = 当前默认 provider, ⚠ = token 已过期)")
}

// displayUserInfo 单 provider 详细显示.
func displayUserInfo(provider string, token *auth.Token, info *auth.UserInfo) {
	fmt.Printf("  Provider:   %s", provider)
	fmt.Printf("  Email:      %s", info.Email)
	fmt.Printf("  Name:       %s", info.Name)
	fmt.Printf("  Subject:    %s", info.Subject)
	if len(info.Groups) > 0 {
		fmt.Printf("  Groups:     %s", strings.Join(info.Groups, ", "))
	}
	fmt.Printf("  Token:      %s", tokenStatus(token))
	fmt.Println()

	if token.Expired() {
		fmt.Printf("⚠️  Token 已过期, 请重新登录: kp login --provider %s ...", provider)
	}
}

// tokenStatus 描述 token 状态字符串.
//
// "Valid (expires in 23h)" / "Expired (1h ago)"
func tokenStatus(token *auth.Token) string {
	now := time.Now()
	if token.Expired() {
		ago := now.Sub(token.ExpiresAt).Round(time.Minute)
		return fmt.Sprintf("Expired (%s ago)", ago)
	}
	in := token.ExpiresAt.Sub(now).Round(time.Minute)
	return fmt.Sprintf("Valid (expires in %s)", in)
}
