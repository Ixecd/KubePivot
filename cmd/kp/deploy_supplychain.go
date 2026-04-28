// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Ixecd/kubepivot/internal/planner"
	"github.com/Ixecd/kubepivot/internal/supplychain"
)

// hasSupplyChainPolicy 判断是否启用供应链策略
// 优先级: 命令行 > env > project.env > 默认(false)
func hasSupplyChainPolicy(cfg *deployConfig, env map[string]string) bool {
	// 1. 命令行显式启用/禁用 (预留未来 flag)
	// if cfg.enforceSupplyChain != nil { return *cfg.enforceSupplyChain }

	// 2. 环境变量覆盖 (CI/CD 注入)
	if v := os.Getenv("KP_SUPPLY_CHAIN_ENFORCE"); v != "" {
		return v == "true" || v == "1"
	}

	// 3. project.env 配置
	if v := env["SUPPLY_CHAIN_ENFORCE"]; v != "" {
		return v == "true" || v == "1"
	}

	// 4. 默认: 不启用 (安全: 显式开启才拦截)
	return false
}

// verifySupplyChainPolicy 执行部署前的供应链策略验证
func verifySupplyChainPolicy(cfg *deployConfig, plan []planner.Plan, root string, env map[string]string) error {
	// 1. 逃生阀: 显式跳过
	//    注意: runDeploy 已处理 --skip-supply-chain, 这里是二次防护
	// if cfg.skipSupplyChain {
	//     P.Warn("⚠", "Supply chain policy check skipped (--skip-supply-chain)")
	//     return nil
	// }

	// 2. 收集待验证镜像 (去重)
	images := make(map[string]bool)
	for _, p := range plan {
		if p.Image != "" {
			images[p.Image] = true
		}
	}
	if len(images) == 0 {
		return nil // 无镜像 = 无需验证
	}

	// 3. 加载全局策略 (project.env 默认 + 可扩展 resources.yaml)
	//    Level3 简化: 仅支持项目级默认策略
	//    Level4 扩展: 合并 resource.SupplyChain 覆盖
	globalPolicy := &supplychain.SupplyChainConfig{}

	// 从 env 解析 Registries.Allow/Deny
	if v := env["REGISTRY_ALLOW"]; v != "" {
		globalPolicy.Registries.Allow = strings.Split(v, ",")
	}
	if v := env["REGISTRY_DENY"]; v != "" {
		globalPolicy.Registries.Deny = strings.Split(v, ",")
	}
	// 签名策略
	if v := env["SUPPLY_CHAIN_SIGN_ENFORCE"]; v == "true" {
		globalPolicy.Signing.Enforce = true
		globalPolicy.Signing.CosignKey = env["SUPPLY_CHAIN_COSIGN_KEY"] // 相对路径
	}
	// SBOM 策略
	if v := env["SUPPLY_CHAIN_SBOM_REQUIRE"]; v == "true" {
		globalPolicy.SBOM.Require = true
		// 🔧 内联 envOrDefault 逻辑，避免依赖外部函数
		format := env["SUPPLY_CHAIN_SBOM_FORMAT"]
		if format == "" {
			format = "cyclonedx-json"
		}
		globalPolicy.SBOM.Format = format
	}

	// 配置校验
	if err := globalPolicy.IsValid(); err != nil {
		return fmt.Errorf("invalid supply-chain config: %w", err)
	}
	// 无策略 = 放行
	if !globalPolicy.Signing.Enforce && !globalPolicy.SBOM.Require &&
		len(globalPolicy.Registries.Allow) == 0 && len(globalPolicy.Registries.Deny) == 0 {
		return nil
	}

	// 4. 逐个验证镜像 (串行, 简化; Level4 可升级并发)
	ctx := context.Background()
	for image := range images {
		// Level4: 合并 per-resource 策略
		// resourcePolicy := loadResourceSupplyChain(root, image)
		// effectivePolicy := globalPolicy.Clone(); effectivePolicy.Merge(resourcePolicy)

		if err := supplychain.ValidatePolicy(ctx, image, globalPolicy); err != nil {
			return fmt.Errorf("image %q: %w", image, err)
		}
	}

	// 5. 全部通过 (可选日志)
	// P.Info("✓", fmt.Sprintf("Supply chain policy passed for %d images", len(images)))
	return nil
}
