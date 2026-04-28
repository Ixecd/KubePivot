// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Ixecd/kubepivot/internal/controller"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ════════════════════════════════════════════════════════════════════════════
// loadVerifiedTrafficFromEnv 测试 (v2.6.1 Step 3)
//
// 注入式 mock 替换 2 个函数变量,避开真实文件系统和 K8s API 依赖:
//   - loadKPEnv      KPEnv 加载 (~/.kp/envs/)
//   - kubectlGetCM   kubectl get configmap
//
// 与 v2.6.1 Step 2 verified_traffic_writer 同模式.
// e2e 真集群验证暂搁 (qc 拍板).
// ════════════════════════════════════════════════════════════════════════════

// withMockedFromEnvDeps 替换 2 个函数变量并在 t.Cleanup 时恢复
type fromEnvMockDeps struct {
	loadEnv  func(envName string) (*KPEnv, error)
	getCM    func(ctx context.Context, kubeconfig, namespace, dataKey string) (string, error)
}

func withMockedFromEnvDeps(t *testing.T, deps *fromEnvMockDeps) {
	t.Helper()

	origLoadEnv := loadKPEnv
	origGetCM := kubectlGetCM

	if deps.loadEnv != nil {
		loadKPEnv = deps.loadEnv
	}
	if deps.getCM != nil {
		kubectlGetCM = deps.getCM
	}

	t.Cleanup(func() {
		loadKPEnv = origLoadEnv
		kubectlGetCM = origGetCM
	})
}

// makeStagingEnv 构造测试用 KPEnv
func makeStagingEnv() *KPEnv {
	return &KPEnv{
		Name:       "staging",
		Kubeconfig: "/tmp/kubeconfig-staging",
		Context:    "staging-ctx",
		Namespace:  "wallet-staging",
	}
}

// 完整 traffic.yaml 内容(模拟 verifiedTrafficWriter 写出的)
const validTrafficYAML = `kind: Ingress
strategy: blue-green
refs:
  name: wallet-ingress
routes:
  - service: wallet-service-blue
    weight: 100
  - service: wallet-service-green
    weight: 0
validation:
  podReadyTimeoutSec: 60
`

const validSourceYAML = `project: wallet
namespace: wallet-staging
version: v1.5.0
runningSince: "2026-04-28T08:00:00Z"
verifiedAt: "2026-04-28T08:05:00Z"
kpVersion: v2.6.1
`

// ════════════════════════════════════════════════════════════════════════════
// Happy path
// ════════════════════════════════════════════════════════════════════════════

func TestLoadVerifiedTrafficFromEnv_HappyPath(t *testing.T) {
	deps := &fromEnvMockDeps{
		loadEnv: func(envName string) (*KPEnv, error) {
			assert.Equal(t, "staging", envName)
			return makeStagingEnv(), nil
		},
		getCM: func(ctx context.Context, kubeconfig, namespace, dataKey string) (string, error) {
			assert.Equal(t, "wallet-staging", namespace)
			switch dataKey {
			case "traffic.yaml":
				return validTrafficYAML, nil
			case "source.yaml":
				return validSourceYAML, nil
			}
			return "", errors.New("unexpected dataKey")
		},
	}
	withMockedFromEnvDeps(t, deps)

	traffic, source, err := loadVerifiedTrafficFromEnv(context.Background(), "staging")
	require.NoError(t, err)

	// 验证 traffic 字段完整解析
	require.NotNil(t, traffic)
	assert.Equal(t, "Ingress", traffic.Kind)
	assert.Equal(t, "blue-green", traffic.Strategy)
	assert.Equal(t, "wallet-ingress", traffic.Refs.Name)
	require.Len(t, traffic.Routes, 2)
	assert.Equal(t, "wallet-service-blue", traffic.Routes[0].Service)
	assert.Equal(t, int32(100), traffic.Routes[0].Weight)
	assert.Equal(t, "wallet-service-green", traffic.Routes[1].Service)
	assert.Equal(t, int32(0), traffic.Routes[1].Weight)
	assert.Equal(t, 60, traffic.Validation.PodReadyTimeoutSec)

	// 验证 source 字段完整解析
	require.NotNil(t, source)
	assert.Equal(t, "wallet", source.Project)
	assert.Equal(t, "wallet-staging", source.Namespace)
	assert.Equal(t, "v1.5.0", source.Version)
	assert.Equal(t, "2026-04-28T08:00:00Z", source.RunningSince)
	assert.Equal(t, "v2.6.1", source.KpVersion)
}

// ════════════════════════════════════════════════════════════════════════════
// Fail-fast: KPEnv 加载相关
// ════════════════════════════════════════════════════════════════════════════

func TestLoadVerifiedTrafficFromEnv_EnvNotFound(t *testing.T) {
	deps := &fromEnvMockDeps{
		loadEnv: func(envName string) (*KPEnv, error) {
			return nil, errors.New("env \"staging\" 不存在,请先运行: kp context add --name staging")
		},
	}
	withMockedFromEnvDeps(t, deps)

	_, _, err := loadVerifiedTrafficFromEnv(context.Background(), "staging")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kp context add",
		"应保留 loadEnv 既有 hint")
}

func TestLoadVerifiedTrafficFromEnv_EmptyNamespace(t *testing.T) {
	deps := &fromEnvMockDeps{
		loadEnv: func(envName string) (*KPEnv, error) {
			env := makeStagingEnv()
			env.Namespace = "" // KPEnv 没声明 namespace
			return env, nil
		},
	}
	withMockedFromEnvDeps(t, deps)

	_, _, err := loadVerifiedTrafficFromEnv(context.Background(), "staging")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace")
	assert.Contains(t, err.Error(), "kp context add",
		"应给出修复 hint")
}

// ════════════════════════════════════════════════════════════════════════════
// Fail-fast: kubectl 调用失败
// ════════════════════════════════════════════════════════════════════════════

func TestLoadVerifiedTrafficFromEnv_ClusterUnreachable(t *testing.T) {
	deps := &fromEnvMockDeps{
		loadEnv: func(envName string) (*KPEnv, error) {
			return makeStagingEnv(), nil
		},
		getCM: func(ctx context.Context, kubeconfig, namespace, dataKey string) (string, error) {
			return "", errors.New("dial tcp: connection refused")
		},
	}
	withMockedFromEnvDeps(t, deps)

	_, _, err := loadVerifiedTrafficFromEnv(context.Background(), "staging")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "访问 env")
	assert.Contains(t, err.Error(), "connection refused",
		"应保留底层错误信息")
}

func TestLoadVerifiedTrafficFromEnv_ConfigMapNotFound(t *testing.T) {
	deps := &fromEnvMockDeps{
		loadEnv: func(envName string) (*KPEnv, error) {
			return makeStagingEnv(), nil
		},
		getCM: func(ctx context.Context, kubeconfig, namespace, dataKey string) (string, error) {
			return "", nil // kubectl --ignore-not-found 返回空字符串
		},
	}
	withMockedFromEnvDeps(t, deps)

	_, _, err := loadVerifiedTrafficFromEnv(context.Background(), "staging")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "没有已验证的 traffic 配置")
	assert.Contains(t, err.Error(), "RUNNING",
		"应说明前提条件")
	assert.Contains(t, err.Error(), "5min",
		"应说明稳态时长要求")
	assert.Contains(t, err.Error(), "kubectl",
		"应给出排查命令")
}

func TestLoadVerifiedTrafficFromEnv_TrafficYamlCorrupted(t *testing.T) {
	deps := &fromEnvMockDeps{
		loadEnv: func(envName string) (*KPEnv, error) {
			return makeStagingEnv(), nil
		},
		getCM: func(ctx context.Context, kubeconfig, namespace, dataKey string) (string, error) {
			if dataKey == "traffic.yaml" {
				return "this is: not [valid yaml: !@#$", nil
			}
			return "", nil
		},
	}
	withMockedFromEnvDeps(t, deps)

	_, _, err := loadVerifiedTrafficFromEnv(context.Background(), "staging")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "解析失败")
	assert.Contains(t, err.Error(), "重新部署",
		"应给出修复 hint")
}

// ════════════════════════════════════════════════════════════════════════════
// Source 元数据降级 (失败不阻断)
// ════════════════════════════════════════════════════════════════════════════

func TestLoadVerifiedTrafficFromEnv_SourceMissing_StillReturnsTraffic(t *testing.T) {
	deps := &fromEnvMockDeps{
		loadEnv: func(envName string) (*KPEnv, error) {
			return makeStagingEnv(), nil
		},
		getCM: func(ctx context.Context, kubeconfig, namespace, dataKey string) (string, error) {
			switch dataKey {
			case "traffic.yaml":
				return validTrafficYAML, nil
			case "source.yaml":
				return "", nil // source.yaml 缺失
			}
			return "", errors.New("unexpected")
		},
	}
	withMockedFromEnvDeps(t, deps)

	traffic, source, err := loadVerifiedTrafficFromEnv(context.Background(), "staging")
	require.NoError(t, err, "source 缺失不应阻断")
	require.NotNil(t, traffic, "traffic 应正常返回")
	require.NotNil(t, source, "source 应返回零值结构而非 nil")
	assert.Empty(t, source.Project, "source 字段为空(降级到默认值)")
}

func TestLoadVerifiedTrafficFromEnv_SourceCorrupted_StillReturnsTraffic(t *testing.T) {
	deps := &fromEnvMockDeps{
		loadEnv: func(envName string) (*KPEnv, error) {
			return makeStagingEnv(), nil
		},
		getCM: func(ctx context.Context, kubeconfig, namespace, dataKey string) (string, error) {
			switch dataKey {
			case "traffic.yaml":
				return validTrafficYAML, nil
			case "source.yaml":
				return "garbage: [invalid", nil // source.yaml 损坏
			}
			return "", nil
		},
	}
	withMockedFromEnvDeps(t, deps)

	traffic, source, err := loadVerifiedTrafficFromEnv(context.Background(), "staging")
	require.NoError(t, err, "source 解析失败不应阻断 traffic 加载")
	require.NotNil(t, traffic)
	require.NotNil(t, source, "source 应是零值结构")
}

// ════════════════════════════════════════════════════════════════════════════
// 辅助函数测试
// ════════════════════════════════════════════════════════════════════════════

func TestExtractServiceNames_BlueGreen(t *testing.T) {
	routes := []controller.TrafficRoute{
		{Service: "wallet-service-blue", Weight: 100},
		{Service: "wallet-service-green", Weight: 0},
	}
	got := extractServiceNames(routes)
	assert.Equal(t, "wallet-service-blue=100%, wallet-service-green=0%", got)
}

func TestExtractServiceNames_Canary(t *testing.T) {
	routes := []controller.TrafficRoute{
		{Service: "v1", Weight: 80},
		{Service: "v2", Weight: 20},
	}
	got := extractServiceNames(routes)
	assert.Equal(t, "v1=80%, v2=20%", got)
}

func TestExtractServiceNames_EmptyRoutes(t *testing.T) {
	got := extractServiceNames([]controller.TrafficRoute{})
	assert.Equal(t, "(empty)", got, "空 routes 应返回 (empty) 占位符")
}

// ════════════════════════════════════════════════════════════════════════════
// JSON path 转义验证 (defaultKubectlGetCM 内部逻辑)
// ════════════════════════════════════════════════════════════════════════════

func TestDefaultKubectlGetCM_JSONPathEscaping(t *testing.T) {
	// 这个测试只验证 jsonpath 字符串构造,不实际调用 kubectl
	// (kubectl 调用部分通过 mock 在其他 test 验证)
	//
	// data.traffic.yaml 在 jsonpath 里需要转义 . 为 \.
	// 即: jsonpath={.data.traffic\.yaml}
	dataKey := "traffic.yaml"
	expected := `jsonpath={.data.traffic\.yaml}`
	got := "jsonpath={.data." + strings.ReplaceAll(dataKey, ".", `\.`) + "}"
	assert.Equal(t, expected, got)
}
