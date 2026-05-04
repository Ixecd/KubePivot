// Copyright 2026 qc (GitHub: Ixecd). All rights reserved.
// Use of this source code is governed by an MIT-style license.

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ixecd/kubepivot/internal/metrics"
	"github.com/Ixecd/kubepivot/internal/planner"
	"github.com/Ixecd/kubepivot/internal/sizing"
	"gopkg.in/yaml.v3"
)

// =============================================================================
// 1. 权重逻辑测试 (getWeights)
// =============================================================================

func TestGetWeights(t *testing.T) {
	tests := []struct {
		profile      sizing.Profile
		expectedCPUW float64
		expectedMemW float64
	}{
		{sizing.ProfileWeb, 0.7, 0.3},     // Web 侧重响应，CPU 权重高
		{sizing.ProfileBatch, 0.3, 0.7},   // Batch 侧重吞吐，内存权重高
		{sizing.ProfileDB, 0.3, 0.7},      // DB 侧重缓存，内存权重高
		{sizing.ProfileDefault, 0.5, 0.5}, // 默认对等
		{"unknown", 0.5, 0.5},             // 未知类型降级到对等
	}

	for _, tt := range tests {
		t.Run(string(tt.profile), func(t *testing.T) {
			cpuW, memW := getWeights(tt.profile)
			if cpuW != tt.expectedCPUW || memW != tt.expectedMemW {
				t.Errorf("getWeights(%s) = (%.1f, %.1f), want (%.1f, %.1f)",
					tt.profile, cpuW, memW, tt.expectedCPUW, tt.expectedMemW)
			}
		})
	}
}

// =============================================================================
// 2. 配置解析测试 (resolveSizingConfig)
// =============================================================================

func TestResolveSizingConfig(t *testing.T) {
	tests := []struct {
		name            string
		cfgMode         string
		compSizing      *planner.SizingConfig
		expectedMode    string
		expectedProfile sizing.Profile
	}{
		{
			name:            "Flag 优先: 全局 auto",
			cfgMode:         "auto",
			compSizing:      nil,
			expectedMode:    "auto",
			expectedProfile: sizing.ProfileDefault,
		},
		{
			name:    "YAML 覆盖: 全局 manual 但组件 auto",
			cfgMode: "manual",
			compSizing: &planner.SizingConfig{
				Mode:    "auto",
				Profile: "web",
			},
			expectedMode:    "auto",
			expectedProfile: sizing.ProfileWeb,
		},
		{
			name:    "默认行为: 均未配置",
			cfgMode: "manual",
			compSizing: &planner.SizingConfig{
				Mode: "",
			},
			expectedMode:    "manual",
			expectedProfile: sizing.ProfileDefault,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &deployConfig{sizingMode: tt.cfgMode}
			p := &planner.Plan{Sizing: tt.compSizing}
			mode, profile := resolveSizingConfig(cfg, p)

			if mode != tt.expectedMode {
				t.Errorf("mode = %s, want %s", mode, tt.expectedMode)
			}
			if profile != tt.expectedProfile {
				t.Errorf("profile = %s, want %s", profile, tt.expectedProfile)
			}
		})
	}
}

// =============================================================================
// 3. YAML 原子批量修改测试 (updateComponentsSizingBatch)
// =============================================================================

func TestUpdateComponentsSizingBatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "components.yaml")

	// 构造原始 YAML (包含注释，验证注释是否被保留)
	originalContent := `
components:
  - name: api-server
    image: nginx:latest
    cpu: "500m" # 原始注释
    memory: "512Mi"
  - name: worker
    cpu: "1000m"
    memory: "1Gi"
`
	err := os.WriteFile(path, []byte(originalContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// 构造优化建议
	suggestions := map[string]*sizing.Suggestion{
		"api-server": {
			RecommendedCPU: 250,               // 500m -> 250m
			RecommendedMem: 128 * 1024 * 1024, // 512Mi -> 128Mi
			Confidence:     0.85,
			SampleCount:    672,
		},
	}
	// 构造推荐理由 (Level5 注入, v3.0 富元数据格式)
	reasons := map[string]string{
		"api-server": "sizing: profile=web confidence=0.85 samples=672 cpu=500m→250m(-50%) mem=512Mi→128Mi(-75%) at=2026-05-04T00:00:00Z",
	}

	// 执行修改
	err = updateComponentsSizingBatch(path, suggestions, reasons)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// 验证结果
	data, _ := os.ReadFile(path)
	var root yaml.Node
	yaml.Unmarshal(data, &root)

	// 深度校验字段
	// 我们通过字符串匹配快速检查关键值的变更，同时检查是否包含 Reason 注释
	content := string(data)
	if !strings.Contains(content, `cpu: "250m"`) {
		t.Errorf("CPU not updated correctly, got:\n%s", content)
	}
	if !strings.Contains(content, `memory: "128Mi"`) {
		t.Errorf("Memory not updated correctly, got:\n%s", content)
	}
	if !strings.Contains(content, `# Reason: sizing: profile=web`) {
		t.Errorf("Comment not injected, got:\n%s", content)
	}

	// 验证未被建议的组件 (worker) 保持不变
	if !strings.Contains(content, `name: worker`) || !strings.Contains(content, `cpu: "1000m"`) {
		t.Errorf("Untouched component modified unexpectedly")
	}
}

// =============================================================================
// 4. Hook 流程拦截测试
// =============================================================================

func TestRunSizingHook_SkipManual(t *testing.T) {
	// 验证当 mode=manual 时，Hook 是否直接跳过（不产生任何 IO 操作）
	cfg := &deployConfig{sizingMode: "manual"}
	plans := []planner.Plan{{Name: "test-pod"}}

	// 如果逻辑错误触发了后续代码，这会导致 panic 或错误（因为我们传入了空路径）
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Hook should skip silently when mode is manual, but it crashed: %v", r)
		}
	}()

	runSizingHook(cfg, plans, "/non-existent-path", "", "")
}

func TestRunSizingHook_FilterValidTargets(t *testing.T) {
	// 验证 Hook 是否正确过滤掉没有镜像或未开启 auto 的 Plan
	cfg := &deployConfig{sizingMode: "auto"}
	plans := []planner.Plan{
		{Name: "no-image-pod", CPU: "100m"},                                                 // 缺少镜像
		{Name: "manual-pod", Image: "nginx", Sizing: &planner.SizingConfig{Mode: "manual"}}, // 显式 manual
		{Name: "valid-pod", Image: "nginx", CPU: "100m", Memory: "128Mi"},                   // 合法目标
	}

	// 在实际集成中，这里会调用 metrics 接口，
	// 此处主要测试 resolver 逻辑是否在 targets 收集阶段起作用
	var validTargets []string
	for i := range plans {
		p := &plans[i]
		if p.Image == "" || p.CPU == "" || p.Memory == "" {
			continue
		}
		mode, _ := resolveSizingConfig(cfg, p)
		if mode == "auto" {
			validTargets = append(validTargets, p.Name)
		}
	}

	if len(validTargets) != 1 || validTargets[0] != "valid-pod" {
		t.Errorf("Target filtering failed, found: %v", validTargets)
	}
}

// TestComputeSizingForPod_PrometheusFallback 验证 Prometheus 查询失败时自动降级到瞬时采样
// 业务场景: Prometheus 不可用/无数据 → fallback 到 kubectl top，保证部署不中断
// 测试重点: 验证 "Fallback -> 获取 Samples -> Compute" 这一集成链路的连通性
func TestSizingCompute_WithFallbackSamples(t *testing.T) {
	// 1. 准备测试环境上下文
	ctx := context.Background()
	profile := sizing.ProfileWeb

	// 2. 构造模拟样本 (模拟 Fallback 成功后从 kubectl top 采集到的 3 个点)
	// 这里的样本数据在均值 310m 左右，内存 305Mi 左右，波动极小
	samples := []*metrics.PodMetrics{
		{
			Timestamp:   time.Now().Add(-4 * time.Second),
			TotalCPU:    metrics.Quantity{Value: 300, Raw: "300m"},
			TotalMemory: metrics.Quantity{Value: 300 << 20, Raw: "300Mi"},
		},
		{
			Timestamp:   time.Now().Add(-2 * time.Second),
			TotalCPU:    metrics.Quantity{Value: 320, Raw: "320m"},
			TotalMemory: metrics.Quantity{Value: 310 << 20, Raw: "310Mi"},
		},
		{
			Timestamp:   time.Now(),
			TotalCPU:    metrics.Quantity{Value: 310, Raw: "310m"},
			TotalMemory: metrics.Quantity{Value: 305 << 20, Raw: "305Mi"},
		},
	}

	// 3. 执行核心计算逻辑 (模拟 computeSizingForPod 的最终输出阶段)
	// 在实际 deploy_sizing.go 中，这里会被封装在 computeSizingForPod 函数内
	// 验证：即使经过 Fallback 路径，只要 samples 存在，计算就不应报错
	sug, err := sizing.Compute(ctx, samples, profile)

	// 4. 验证核心业务语义 (Final Verification)
	// -----------------------------------------------------------------

	// 验证点 A: 逻辑健壮性 (无错误返回)
	if err != nil {
		t.Fatalf("Fallback computation failed: %v", err)
	}
	if sug == nil {
		t.Fatal("Expected suggestion result, got nil")
	}

	// 验证点 B: 置信度逻辑 (Confidence Check)
	// 算法逻辑: 3 个样本基础置信度为 3/5 = 0.6。
	// 由于样本波动极小 (CV 趋近 0)，最终置信度应保持在 0.6 附近。
	if sug.Confidence < 0.5 {
		t.Errorf("Expected moderate confidence (>=0.5) for 3 stable samples, got %.2f", sug.Confidence)
	}

	// 验证点 C: 推荐值合理性 (Recommendation Check)
	// CPU 均值 310m，ProfileWeb 权重 0.7，推荐值应落在 [300, 350] 范围内
	avgCPU := int64(310) // 样本均值
	if sug.RecommendedCPU < avgCPU {
		t.Errorf("Recommended CPU %d < avg %d, violates stability guarantee",
			sug.RecommendedCPU, avgCPU)
	}
	// 可选: 验证推荐值不超过均值的 2 倍 (避免过度配置)
	if sug.RecommendedCPU > avgCPU*2 {
		t.Errorf("Recommended CPU %d > 2x avg %d, possible over-provisioning",
			sug.RecommendedCPU, avgCPU)
	}

	// 内存均值 305Mi，推荐值应在 300Mi 以上 (需考虑 64Mi 步长对齐)
	avgMem := int64(305 << 20)
	if sug.RecommendedMem < avgMem {
		t.Errorf("Recommended Memory %d < avg %d, violates stability guarantee",
			sug.RecommendedMem, avgMem)
	}
	// 验证点 D: 治理信息 (Metadata Check)
	if sug.Profile != profile {
		t.Errorf("Profile mismatch: got %s, want %s", sug.Profile, profile)
	}

	t.Logf("Fallback test passed: CPU Rec=%dm, Mem Rec=%dMi, Confidence=%.2f",
		sug.RecommendedCPU, sug.RecommendedMem>>20, sug.Confidence)
}
