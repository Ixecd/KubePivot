// internal/ai/gpu_detect.go — v2.0 Phase 4: GPU 智能感知
package ai

import (
	"strings"
)

// ─── 常量 ────────────────────────────────────────────────────────

// 显存单位常量，避免魔法数字。
const (
	GB = 1024 * 1024 * 1024
)

// ─── GPU 检测结果类型 ────────────────────────────────────────────

// GPUDetection GPU 检测的完整输出。
type GPUDetection struct {
	NeedsGPU         bool     // 是否需要 GPU
	Confidence       float64  // 检测置信度 0-1
	Libraries        []string // 检测到的 GPU 库
	RecommendedModel string   // 推荐 GPU 型号
	MinVRAM          int64    // 建议最小显存 (bytes)
	DetectionLayer   string   // 最高置信度检测来源: "code" | "deps" | "dockerfile"
}

// GPUHeuristic GPU 型号推断启发式规则。
type GPUHeuristic struct {
	Keywords []string // 代码/依赖中的关键词
	Model    string   // 推荐 GPU 型号
	MinVRAM  int64    // 最小显存 (bytes)
	Priority int      // 优先级 (高→低)
}

// gpuHeuristics 型号推断规则表（按优先级降序）。
var gpuHeuristics = []GPUHeuristic{
	{[]string{"vllm", "Llama-70B"}, "A100-80GB", 80 * GB, 100},
	{[]string{"vllm", "Llama-13B"}, "A100-80GB", 80 * GB, 90},
	{[]string{"vllm"}, "A100-40GB", 40 * GB, 80},
	{[]string{"transformers", "llama"}, "A100-40GB", 40 * GB, 70},
	{[]string{"transformers", "bert"}, "T4", 16 * GB, 50},
	{[]string{"diffusers", "stable-diffusion"}, "A10", 24 * GB, 60},
	{[]string{"diffusers"}, "T4", 16 * GB, 40},
	{[]string{"torch.cuda", "CNN"}, "A10", 24 * GB, 30},
	{[]string{"torch.cuda"}, "T4", 16 * GB, 20},
	{[]string{"tensorflow"}, "T4", 16 * GB, 15},
	{[]string{"jax"}, "T4", 16 * GB, 15},
}

// ─── 三层检测 ────────────────────────────────────────────────────

// DetectGPU 执行三层 GPU 检测：代码 imports → 依赖文件 → Dockerfile 镜像。
// 返回 GPUDetection 含推荐型号 + 置信度。
func DetectGPU(ctx *RepoContext) *GPUDetection {
	d := &GPUDetection{}

	// 第一层：GPU 库直接匹配（确定性最高）
	d.Libraries = ctx.GPULibs
	if len(d.Libraries) == 0 {
		// 第二层：Dockerfile FROM 镜像检测
		d.Libraries = detectFromDockerImages(ctx)
		if len(d.Libraries) > 0 {
			d.DetectionLayer = "dockerfile"
		}
	} else {
		d.DetectionLayer = "code"
	}

	if len(d.Libraries) == 0 {
		return d // 无 GPU，置信度 0
	}

	d.NeedsGPU = true

	// 第三层：型号推断
	d.RecommendedModel, d.MinVRAM, d.Confidence = inferGPUModel(d.Libraries)
	return d
}

// detectFromDockerImages 从 Dockerfile FROM 镜像检测 GPU 需求。
func detectFromDockerImages(ctx *RepoContext) []string {
	for _, img := range ctx.DockerBaseImages {
		lower := strings.ToLower(img)
		if strings.Contains(lower, "nvidia/cuda") ||
			strings.Contains(lower, "nvcr.io") ||
			strings.Contains(lower, "nvidia") {
			return []string{"nvidia-docker"}
		}
	}
	return nil
}

// inferGPUModel 根据检测到的 GPU 库推断推荐型号。
// 返回 (型号, 最小显存 bytes, 置信度 0-1)。
func inferGPUModel(libs []string) (string, int64, float64) {
	libsLower := make([]string, len(libs))
	for i, l := range libs {
		libsLower[i] = strings.ToLower(l)
	}

	best := GPUHeuristic{Priority: -1}
	for _, h := range gpuHeuristics {
		if matchAll(libsLower, h.Keywords) {
			if h.Priority > best.Priority {
				best = h
			}
		}
	}

	if best.Priority < 0 {
		// 低级匹配：有 GPU 库但无法推断型号 → 默认 T4
		return "T4", 16 * GB, 0.2
	}

	confidence := float64(best.Priority) / 100.0
	return best.Model, best.MinVRAM, confidence
}

func matchAll(haystack []string, needles []string) bool {
	for _, n := range needles {
		found := false
		nLower := strings.ToLower(n)
		for _, h := range haystack {
			if strings.Contains(h, nLower) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ─── GPU 信任分层 ────────────────────────────────────────────────

// GPUConfidenceByLib 返回指定库的信任度 0-1。
// vllm = 100%, torch.cuda = 60%, triton = 30%。
func GPUConfidenceByLib(lib string) float64 {
	switch strings.ToLower(lib) {
	case "vllm", "nvidia.nccl":
		return 1.0
	case "torch.cuda", "tensorflow", "cupy":
		return 0.6
	case "transformers", "diffusers", "jax":
		return 0.5
	case "triton", "onnxruntime-gpu":
		return 0.3
	default:
		return 0.1
	}
}

// MaxGPUConfidence 返回检测到的 GPU 库中的最高信任度。
func MaxGPUConfidence(libs []string) float64 {
	max := 0.0
	for _, lib := range libs {
		if c := GPUConfidenceByLib(lib); c > max {
			max = c
		}
	}
	return max
}

// ─── MIG 感知 ────────────────────────────────────────────────────

// RecommendMIG 判断是否应推荐 MIG 分区而非整卡。
// 条件：显存需求 ≤ 10GB + MIG 可用。
func RecommendMIG(minVRAM int64, migAvailable bool) (bool, string) {
	if !migAvailable {
		return false, ""
	}
	// 如果显存需求 ≤ 10GB，优先推荐 1g.10gb MIG 分区
	if minVRAM <= 10*1024*1024*1024 {
		return true, "1g.10gb"
	}
	return false, ""
}
