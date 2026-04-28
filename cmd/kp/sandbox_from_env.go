// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/Ixecd/kubepivot/internal/controller"
	"github.com/Ixecd/kubepivot/internal/executor"
	"gopkg.in/yaml.v3"
)

// ════════════════════════════════════════════════════════════════════════════
// kp sandbox start --from-env <name>  v2.6.1 多环境流量传播链「搬运工」
//
// 从指定 env (跨集群) 读取 verified-traffic ConfigMap, 注入到当前 sandbox
// 的 runBlueGreenSwitch 流程, 实现"用 staging 已验证的 traffic 配置部署 prod".
//
// 三方协作链 (设计核心):
//   Staging Controller (生产者)    verifiedTrafficWriter 周期写 ConfigMap
//   kp 进程 (本文件, 搬运工)        --from-env 主动读 ConfigMap + 注入
//   Prod Controller (消费者)        sandbox commit 走完整状态机
//
// 详细设计: docs/design/traffic-multi-env-impl-draft.md §7
// ════════════════════════════════════════════════════════════════════════════

// 函数变量注入式 mock (与 verified_traffic_writer 同模式)
//
// 测试时可替换以隔离 KPEnv 加载和 kubectl 调用
var (
	loadKPEnv         = defaultLoadKPEnv
	kubectlGetCM      = defaultKubectlGetCM
)

// VerifiedTrafficSource 来自 verified-traffic ConfigMap source.yaml 的元数据.
// 用于 audit log 和操作可追溯性.
type VerifiedTrafficSource struct {
	Project      string `yaml:"project"`
	Namespace    string `yaml:"namespace"`
	Version      string `yaml:"version"`
	RunningSince string `yaml:"runningSince"`
	VerifiedAt   string `yaml:"verifiedAt"`
	KpVersion    string `yaml:"kpVersion"`
}

// loadVerifiedTrafficFromEnv 从指定 env 集群读取 verified-traffic ConfigMap.
//
// fail-fast 原则 (Q7/Q8/Q11/Q12 联合):
//   - Q3=A: env 不存在 → loadEnv 既有错误 ("kp context add" hint)
//   - Q8=A: cluster 不可达 → kubectl 失败 fail-fast
//   - Q7=A: ConfigMap 不存在 → 显式错误说明前提条件
//   - Q11=C: 错误 hint 不探测 image tag, 只给出排查命令
//
// 返回 traffic + source (audit log 用) + error.
// source 即使解析失败也不阻断 traffic 加载 (audit metadata 是 nice-to-have).
func loadVerifiedTrafficFromEnv(ctx context.Context, envName string) (
	*controller.Traffic, *VerifiedTrafficSource, error,
) {
	// 1. 加载 KPEnv (Q3=A 复用既有 ~/.kp/envs/ 机制)
	env, err := loadKPEnv(envName)
	if err != nil {
		return nil, nil, err // loadEnv 既有 error 已含 "kp context add" hint
	}
	if env.Namespace == "" {
		return nil, nil, fmt.Errorf("env %q 没有声明 namespace, 无法定位 verified-traffic ConfigMap (hint: kp context add --name %s --namespace <ns>)",
			envName, envName)
	}

	kubeconfig := expandHome(env.Kubeconfig)

	// 2. 读 traffic.yaml
	trafficOut, err := kubectlGetCM(ctx, kubeconfig, env.Namespace, "traffic.yaml")
	if err != nil {
		// Q8=A: cluster 不可达 fail-fast
		return nil, nil, fmt.Errorf("访问 env %q 集群失败 (kubeconfig=%s): %w",
			envName, kubeconfig, err)
	}
	if len(strings.TrimSpace(trafficOut)) == 0 {
		// Q7=A: ConfigMap 不存在 fail-fast (Q11=C: 不探测 image tag)
		return nil, nil, fmt.Errorf(
			"env %q 没有已验证的 traffic 配置 (期望 ConfigMap kubepivot-verified-traffic 在 namespace %q). "+
				"前提条件: 项目在该 env 处于 RUNNING 状态且稳定 ≥5min, controller 才会写出此 ConfigMap. "+
				"排查命令: kubectl --kubeconfig=%s get configmap kubepivot-verified-traffic -n %s",
			envName, env.Namespace, kubeconfig, env.Namespace)
	}

	var traffic controller.Traffic
	if err := yaml.Unmarshal([]byte(trafficOut), &traffic); err != nil {
		return nil, nil, fmt.Errorf("env %q verified-traffic.yaml 解析失败: %w (hint: ConfigMap 可能损坏, 重新部署 staging 触发 controller 覆写)",
			envName, err)
	}

	// 3. 读 source.yaml (audit log 用, 解析失败不阻断)
	var source VerifiedTrafficSource
	sourceOut, err := kubectlGetCM(ctx, kubeconfig, env.Namespace, "source.yaml")
	if err == nil && len(strings.TrimSpace(sourceOut)) > 0 {
		// 解析失败保留 source 默认空值
		_ = yaml.Unmarshal([]byte(sourceOut), &source)
	}

	return &traffic, &source, nil
}

// ════════════════════════════════════════════════════════════════════════════
// 默认实现 (生产环境用, 测试时通过函数变量替换)
// ════════════════════════════════════════════════════════════════════════════

// defaultLoadKPEnv 包装既有 loadEnv (env.go) 为函数变量
func defaultLoadKPEnv(envName string) (*KPEnv, error) {
	return loadEnv(envName)
}

// defaultKubectlGetCM 用 kubectl 读 verified-traffic ConfigMap 的指定 data 字段.
//
// dataKey: "traffic.yaml" 或 "source.yaml"
// 返回字段内容. ConfigMap 不存在时返回 "" (nil error), 由调用方判定.
func defaultKubectlGetCM(ctx context.Context, kubeconfig, namespace, dataKey string) (string, error) {
	jsonpath := fmt.Sprintf("jsonpath={.data.%s}",
		strings.ReplaceAll(dataKey, ".", `\.`))
	out, err := executor.GetExecutor().Kubectl(ctx, kubeconfig,
		"get", "configmap", "kubepivot-verified-traffic",
		"-n", namespace,
		"-o", jsonpath,
		"--ignore-not-found",
	)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ════════════════════════════════════════════════════════════════════════════
// 辅助函数
// ════════════════════════════════════════════════════════════════════════════

// extractServiceNames 提取 routes 列表的 service+weight 摘要 (日志展示用)
//
// 输出: "wallet-service-blue=100%, wallet-service-green=0%"
func extractServiceNames(routes []controller.TrafficRoute) string {
	if len(routes) == 0 {
		return "(empty)"
	}
	parts := make([]string, len(routes))
	for i, r := range routes {
		parts[i] = fmt.Sprintf("%s=%d%%", r.Service, r.Weight)
	}
	return strings.Join(parts, ", ")
}
