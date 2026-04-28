// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Ixecd/kubepivot/internal/route"
	"github.com/Ixecd/kubepivot/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ════════════════════════════════════════════════════════════════════════════
// VerifiedTrafficWriter 测试 (v2.6.1 Step 2)
//
// 注入式 mock 替换 5 个函数变量，避开真实 K8s API 依赖：
//   - loadStateRecord
//   - readResourcesConfigMap
//   - readActualRoutes
//   - needsTrafficUpdate
//   - writeVerifiedTrafficCM
//
// 与 v2.7 readTokenFile / kubectl func 同模式。
// ════════════════════════════════════════════════════════════════════════════

// withMockedDeps 替换 5 个函数变量并在 t.Cleanup 时恢复
type mockDeps struct {
	loadState    func(ns string) (*state.DeployRecord, error)
	readResCM    func(ctx context.Context, kc, ns string) (*ResourcesConfig, error)
	readRoutes   func(ctx context.Context, kc, ns string, traffic *Traffic) ([]route.Route, error)
	needsUpdate  func(ctx context.Context, kc, ns string, actual []route.Route) bool
	writeCM      func(ctx context.Context, kc, ns string, traffic *Traffic, actualRoutes []route.Route, record *state.DeployRecord, runningSince time.Time) error

	// 副作用计数 / 记录
	writeCalls int
}

func withMockedDeps(t *testing.T, deps *mockDeps) {
	t.Helper()

	origLoadState := loadStateRecord
	origReadRes := readResourcesConfigMap
	origReadRoutes := readActualRoutes
	origNeeds := needsTrafficUpdate
	origWrite := writeVerifiedTrafficCM

	if deps.loadState != nil {
		loadStateRecord = deps.loadState
	}
	if deps.readResCM != nil {
		readResourcesConfigMap = deps.readResCM
	}
	if deps.readRoutes != nil {
		readActualRoutes = deps.readRoutes
	}
	if deps.needsUpdate != nil {
		needsTrafficUpdate = deps.needsUpdate
	}
	if deps.writeCM != nil {
		// 包一层计数
		userWrite := deps.writeCM
		writeVerifiedTrafficCM = func(ctx context.Context, kc, ns string,
			traffic *Traffic, actualRoutes []route.Route,
			record *state.DeployRecord, runningSince time.Time) error {
			deps.writeCalls++
			return userWrite(ctx, kc, ns, traffic, actualRoutes, record, runningSince)
		}
	}

	t.Cleanup(func() {
		loadStateRecord = origLoadState
		readResourcesConfigMap = origReadRes
		readActualRoutes = origReadRoutes
		needsTrafficUpdate = origNeeds
		writeVerifiedTrafficCM = origWrite
	})
}

// ── helper: 构造常用 fixture ────────────────────────────────────────────────

func makeRunningRecord(t *testing.T, runningAgo time.Duration) *state.DeployRecord {
	t.Helper()
	return &state.DeployRecord{
		Project:   "wallet",
		Namespace: "wallet",
		State:     state.StateRunning,
		Version:   "v1.0.0",
		History: []state.Transition{
			{From: state.StateValidating, To: state.StateRunning,
				Timestamp: time.Now().Add(-runningAgo)},
		},
	}
}

func makeTrafficCfg() *ResourcesConfig {
	return &ResourcesConfig{
		Resources: []Resource{},
		Traffic: &Traffic{
			Kind:     "Ingress",
			Strategy: "blue-green",
			Refs:     TrafficRefs{Name: "wallet-ingress"},
			Routes: []TrafficRoute{
				{Service: "wallet-service-blue", Weight: 100},
				{Service: "wallet-service-green", Weight: 0},
			},
		},
	}
}

// ════════════════════════════════════════════════════════════════════════════
// 主流程测试
// ════════════════════════════════════════════════════════════════════════════

func TestProcessNamespace_HappyPath(t *testing.T) {
	deps := &mockDeps{
		loadState: func(ns string) (*state.DeployRecord, error) {
			return makeRunningRecord(t, 6*time.Minute), nil
		},
		readResCM: func(ctx context.Context, kc, ns string) (*ResourcesConfig, error) {
			return makeTrafficCfg(), nil
		},
		readRoutes: func(ctx context.Context, kc, ns string, traffic *Traffic) ([]route.Route, error) {
			return []route.Route{
				{Service: "wallet-service-blue", Weight: 100},
				{Service: "wallet-service-green", Weight: 0},
			}, nil
		},
		needsUpdate: func(ctx context.Context, kc, ns string, actual []route.Route) bool {
			return true
		},
		writeCM: func(ctx context.Context, kc, ns string, traffic *Traffic,
			actualRoutes []route.Route, record *state.DeployRecord, runningSince time.Time) error {
			return nil
		},
	}
	withMockedDeps(t, deps)

	ok := processNamespaceForVerifiedTraffic(context.Background(), "wallet",
		nil /* shardMgr nil 跳过 shard 过滤 */, 0, "")
	assert.True(t, ok, "happy path 应该写入 ConfigMap")
	assert.Equal(t, 1, deps.writeCalls)
}

func TestProcessNamespace_NotInRunning(t *testing.T) {
	deps := &mockDeps{
		loadState: func(ns string) (*state.DeployRecord, error) {
			r := makeRunningRecord(t, 6*time.Minute)
			r.State = state.StateDeploying // 不在 RUNNING
			return r, nil
		},
	}
	withMockedDeps(t, deps)

	ok := processNamespaceForVerifiedTraffic(context.Background(), "wallet", nil, 0, "")
	assert.False(t, ok, "非 RUNNING 应该跳过")
	assert.Equal(t, 0, deps.writeCalls)
}

func TestProcessNamespace_LessThan5MinStable(t *testing.T) {
	deps := &mockDeps{
		loadState: func(ns string) (*state.DeployRecord, error) {
			return makeRunningRecord(t, 2*time.Minute), nil // 不到 5min
		},
	}
	withMockedDeps(t, deps)

	ok := processNamespaceForVerifiedTraffic(context.Background(), "wallet", nil, 0, "")
	assert.False(t, ok, "不到 5min 稳态应该跳过")
	assert.Equal(t, 0, deps.writeCalls)
}

func TestProcessNamespace_StateLoadFailure(t *testing.T) {
	deps := &mockDeps{
		loadState: func(ns string) (*state.DeployRecord, error) {
			return nil, errors.New("state record not found")
		},
	}
	withMockedDeps(t, deps)

	ok := processNamespaceForVerifiedTraffic(context.Background(), "wallet", nil, 0, "")
	assert.False(t, ok, "state 读取失败应静默跳过(首次部署 IDLE 阶段)")
	assert.Equal(t, 0, deps.writeCalls)
}

func TestProcessNamespace_NoBlueGreen(t *testing.T) {
	deps := &mockDeps{
		loadState: func(ns string) (*state.DeployRecord, error) {
			return makeRunningRecord(t, 6*time.Minute), nil
		},
		readResCM: func(ctx context.Context, kc, ns string) (*ResourcesConfig, error) {
			cfg := makeTrafficCfg()
			cfg.Traffic = nil // 没启用蓝绿
			return cfg, nil
		},
	}
	withMockedDeps(t, deps)

	ok := processNamespaceForVerifiedTraffic(context.Background(), "wallet", nil, 0, "")
	assert.False(t, ok, "没启用蓝绿的项目应跳过")
	assert.Equal(t, 0, deps.writeCalls)
}

func TestProcessNamespace_StrategyNotBlueGreen(t *testing.T) {
	deps := &mockDeps{
		loadState: func(ns string) (*state.DeployRecord, error) {
			return makeRunningRecord(t, 6*time.Minute), nil
		},
		readResCM: func(ctx context.Context, kc, ns string) (*ResourcesConfig, error) {
			cfg := makeTrafficCfg()
			cfg.Traffic.Strategy = "canary" // 非 blue-green strategy
			return cfg, nil
		},
	}
	withMockedDeps(t, deps)

	ok := processNamespaceForVerifiedTraffic(context.Background(), "wallet", nil, 0, "")
	assert.False(t, ok, "非 blue-green strategy 应跳过 (HasBlueGreen gate)")
	assert.Equal(t, 0, deps.writeCalls)
}

func TestProcessNamespace_ResourcesCMReadFailure(t *testing.T) {
	deps := &mockDeps{
		loadState: func(ns string) (*state.DeployRecord, error) {
			return makeRunningRecord(t, 6*time.Minute), nil
		},
		readResCM: func(ctx context.Context, kc, ns string) (*ResourcesConfig, error) {
			return nil, errors.New("ConfigMap not found")
		},
	}
	withMockedDeps(t, deps)

	ok := processNamespaceForVerifiedTraffic(context.Background(), "wallet", nil, 0, "")
	assert.False(t, ok, "读 resources ConfigMap 失败 warn-only 跳过")
	assert.Equal(t, 0, deps.writeCalls)
}

func TestProcessNamespace_ReadRoutesFailure(t *testing.T) {
	deps := &mockDeps{
		loadState: func(ns string) (*state.DeployRecord, error) {
			return makeRunningRecord(t, 6*time.Minute), nil
		},
		readResCM: func(ctx context.Context, kc, ns string) (*ResourcesConfig, error) {
			return makeTrafficCfg(), nil
		},
		readRoutes: func(ctx context.Context, kc, ns string, traffic *Traffic) ([]route.Route, error) {
			return nil, errors.New("Ingress not found")
		},
	}
	withMockedDeps(t, deps)

	ok := processNamespaceForVerifiedTraffic(context.Background(), "wallet", nil, 0, "")
	assert.False(t, ok, "读 K8s 实际路由失败 warn-only 跳过")
	assert.Equal(t, 0, deps.writeCalls)
}

func TestProcessNamespace_NoUpdateNeeded(t *testing.T) {
	deps := &mockDeps{
		loadState: func(ns string) (*state.DeployRecord, error) {
			return makeRunningRecord(t, 6*time.Minute), nil
		},
		readResCM: func(ctx context.Context, kc, ns string) (*ResourcesConfig, error) {
			return makeTrafficCfg(), nil
		},
		readRoutes: func(ctx context.Context, kc, ns string, traffic *Traffic) ([]route.Route, error) {
			return []route.Route{
				{Service: "wallet-service-blue", Weight: 100},
			}, nil
		},
		needsUpdate: func(ctx context.Context, kc, ns string, actual []route.Route) bool {
			return false // ConfigMap 已是最新
		},
		writeCM: func(ctx context.Context, kc, ns string, traffic *Traffic,
			actualRoutes []route.Route, record *state.DeployRecord, runningSince time.Time) error {
			return nil
		},
	}
	withMockedDeps(t, deps)

	ok := processNamespaceForVerifiedTraffic(context.Background(), "wallet", nil, 0, "")
	assert.False(t, ok, "needsUpdate=false 应跳过写入")
	assert.Equal(t, 0, deps.writeCalls, "writeCM 不应被调用")
}

func TestProcessNamespace_WriteFailure(t *testing.T) {
	deps := &mockDeps{
		loadState: func(ns string) (*state.DeployRecord, error) {
			return makeRunningRecord(t, 6*time.Minute), nil
		},
		readResCM: func(ctx context.Context, kc, ns string) (*ResourcesConfig, error) {
			return makeTrafficCfg(), nil
		},
		readRoutes: func(ctx context.Context, kc, ns string, traffic *Traffic) ([]route.Route, error) {
			return []route.Route{{Service: "wallet-service-blue", Weight: 100}}, nil
		},
		needsUpdate: func(ctx context.Context, kc, ns string, actual []route.Route) bool {
			return true
		},
		writeCM: func(ctx context.Context, kc, ns string, traffic *Traffic,
			actualRoutes []route.Route, record *state.DeployRecord, runningSince time.Time) error {
			return errors.New("kubectl apply failed")
		},
	}
	withMockedDeps(t, deps)

	ok := processNamespaceForVerifiedTraffic(context.Background(), "wallet", nil, 0, "")
	assert.False(t, ok, "写失败 warn-only 返回 false")
	assert.Equal(t, 1, deps.writeCalls, "writeCM 应被调用一次(即使失败)")
}

// ════════════════════════════════════════════════════════════════════════════
// 辅助函数测试
// ════════════════════════════════════════════════════════════════════════════

func TestBuildSourceMetadata_Fields(t *testing.T) {
	record := &state.DeployRecord{
		Project:   "wallet",
		Namespace: "wallet",
		Version:   "v1.5.0",
	}
	runningSince := time.Date(2026, 4, 28, 8, 0, 0, 0, time.UTC)

	src := buildSourceMetadata("wallet-staging", record, runningSince)

	assert.Equal(t, "wallet", src.Project)
	assert.Equal(t, "wallet-staging", src.Namespace)
	assert.Equal(t, "v1.5.0", src.Version)
	assert.Equal(t, "2026-04-28T08:00:00Z", src.RunningSince)
	assert.NotEmpty(t, src.VerifiedAt)
	assert.Equal(t, "v2.6.1", src.KpVersion)
}

func TestIndentYAML_BasicIndent(t *testing.T) {
	input := "kind: Ingress\nrefs:\n  name: wallet"
	got := indentYAML(input, "    ")
	expected := "    kind: Ingress\n    refs:\n      name: wallet"
	assert.Equal(t, expected, got)
}

func TestIndentYAML_TrailingNewlineHandling(t *testing.T) {
	// 输入末尾有换行,indent 应该 trim 末尾再处理
	input := "a: 1\nb: 2\n\n"
	got := indentYAML(input, "  ")
	expected := "  a: 1\n  b: 2"
	assert.Equal(t, expected, got)
}

func TestIndentYAML_EmptyInput(t *testing.T) {
	got := indentYAML("", "  ")
	assert.Equal(t, "  ", got, "空字符串应返回单个 prefix(边界情况)")
}

func TestSha256HexBytes_Determinism(t *testing.T) {
	data := []byte("hello world")
	h1 := sha256HexBytes(data)
	h2 := sha256HexBytes(data)
	assert.Equal(t, h1, h2, "sha256 必须确定性")
	assert.Len(t, h1, 64, "sha256 hex 长度 64 字符")
}

func TestSha256HexBytes_Different(t *testing.T) {
	h1 := sha256HexBytes([]byte("a"))
	h2 := sha256HexBytes([]byte("b"))
	assert.NotEqual(t, h1, h2)
}

// ════════════════════════════════════════════════════════════════════════════
// scanOnce 集成测试(多 ns 场景)
// ════════════════════════════════════════════════════════════════════════════

func TestScanOnce_MultipleNamespaces_PartialSuccess(t *testing.T) {
	gs := NewGlobalState()
	// 注册 3 个 ns 进 GlobalState
	gs.UpsertProject("ns-running-stable", "resources: []")
	gs.UpsertProject("ns-running-unstable", "resources: []")
	gs.UpsertProject("ns-not-running", "resources: []")

	deps := &mockDeps{
		loadState: func(ns string) (*state.DeployRecord, error) {
			switch ns {
			case "ns-running-stable":
				return makeRunningRecord(t, 6*time.Minute), nil
			case "ns-running-unstable":
				return makeRunningRecord(t, 1*time.Minute), nil // 不到 5min
			case "ns-not-running":
				r := makeRunningRecord(t, 6*time.Minute)
				r.State = state.StateDeploying
				return r, nil
			}
			return nil, fmt.Errorf("unexpected ns: %s", ns)
		},
		readResCM: func(ctx context.Context, kc, ns string) (*ResourcesConfig, error) {
			return makeTrafficCfg(), nil
		},
		readRoutes: func(ctx context.Context, kc, ns string, traffic *Traffic) ([]route.Route, error) {
			return []route.Route{{Service: "wallet-service-blue", Weight: 100}}, nil
		},
		needsUpdate: func(ctx context.Context, kc, ns string, actual []route.Route) bool {
			return true
		},
		writeCM: func(ctx context.Context, kc, ns string, traffic *Traffic,
			actualRoutes []route.Route, record *state.DeployRecord, runningSince time.Time) error {
			return nil
		},
	}
	withMockedDeps(t, deps)

	scanOnceForVerifiedTraffic(context.Background(), gs, nil, 0, "")

	assert.Equal(t, 1, deps.writeCalls,
		"3 个 ns 中只有 ns-running-stable 通过所有 gate,应写入 1 次")
}

// ════════════════════════════════════════════════════════════════════════════
// state 集成 (RunningSinceFromHistory 集成)
// ════════════════════════════════════════════════════════════════════════════

func TestProcessNamespace_RunningSinceCalculation(t *testing.T) {
	// 验证 5min 阈值的边界判定(4:59 跳过 / 5:00 通过)
	cases := []struct {
		name       string
		runningAgo time.Duration
		shouldPass bool
	}{
		{"刚进 RUNNING (1s)", 1 * time.Second, false},
		{"接近 5min (4:59)", 4*time.Minute + 59*time.Second, false},
		{"刚到 5min (5:00)", 5*time.Minute, true},
		{"远超 5min (1h)", 1 * time.Hour, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := &mockDeps{
				loadState: func(ns string) (*state.DeployRecord, error) {
					return makeRunningRecord(t, tc.runningAgo), nil
				},
				readResCM: func(ctx context.Context, kc, ns string) (*ResourcesConfig, error) {
					return makeTrafficCfg(), nil
				},
				readRoutes: func(ctx context.Context, kc, ns string, traffic *Traffic) ([]route.Route, error) {
					return []route.Route{{Service: "wallet-service-blue", Weight: 100}}, nil
				},
				needsUpdate: func(ctx context.Context, kc, ns string, actual []route.Route) bool {
					return true
				},
				writeCM: func(ctx context.Context, kc, ns string, traffic *Traffic,
					actualRoutes []route.Route, record *state.DeployRecord, runningSince time.Time) error {
					return nil
				},
			}
			withMockedDeps(t, deps)

			ok := processNamespaceForVerifiedTraffic(context.Background(),
				"wallet", nil, 0, "")
			require.Equal(t, tc.shouldPass, ok)
		})
	}
}
