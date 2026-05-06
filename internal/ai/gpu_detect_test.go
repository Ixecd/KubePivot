// internal/ai/gpu_detect_test.go
package ai

import (
	"testing"
)

// ── DetectGPU ────────────────────────────────────────────────────

func TestDetectGPU_NoGPU(t *testing.T) {
	ctx := &RepoContext{GPULibs: nil}
	d := DetectGPU(ctx)
	if d.NeedsGPU {
		t.Error("empty context should not need GPU")
	}
}

func TestDetectGPU_VLLM(t *testing.T) {
	ctx := &RepoContext{GPULibs: []string{"vllm", "torch.cuda"}}
	d := DetectGPU(ctx)
	if !d.NeedsGPU {
		t.Error("vllm detected - should need GPU")
	}
	if d.DetectionLayer != "code" {
		t.Errorf("detection layer = %s, want code", d.DetectionLayer)
	}
	if d.RecommendedModel == "" {
		t.Error("should recommend a GPU model")
	}
	if d.MinVRAM == 0 {
		t.Error("MinVRAM should be > 0")
	}
}

func TestDetectGPU_Dockerfile(t *testing.T) {
	ctx := &RepoContext{
		GPULibs:          nil,
		DockerBaseImages: []string{"nvidia/cuda:12.4.1-runtime-ubuntu22.04"},
	}
	d := DetectGPU(ctx)
	if !d.NeedsGPU {
		t.Error("nvidia cuda base image should trigger GPU detection")
	}
	if d.DetectionLayer != "dockerfile" {
		t.Errorf("detection layer = %s, want dockerfile", d.DetectionLayer)
	}
}

// ── inferGPUModel ────────────────────────────────────────────────

func TestInferGPUModel_VLLM_Llama(t *testing.T) {
	model, vram, conf := inferGPUModel([]string{"vllm", "transformers", "Llama-70B"})
	if model != "A100-80GB" {
		t.Errorf("vllm+Llama-70B → want A100-80GB, got %s", model)
	}
	if vram != 80*GB {
		t.Errorf("vllm+Llama-70B → vram=%d", vram)
	}
	if conf < 0.9 {
		t.Errorf("vllm confidence should be >= 0.9, got %.2f", conf)
	}
}

func TestInferGPUModel_Bert(t *testing.T) {
	model, _, _ := inferGPUModel([]string{"transformers", "bert"})
	if model != "T4" {
		t.Errorf("transformers+bert → want T4, got %s", model)
	}
}

func TestInferGPUModel_StableDiffusion(t *testing.T) {
	model, _, _ := inferGPUModel([]string{"diffusers", "stable-diffusion"})
	if model != "A10" {
		t.Errorf("diffusers+stable-diffusion → want A10, got %s", model)
	}
}

func TestInferGPUModel_TorchOnly(t *testing.T) {
	model, _, conf := inferGPUModel([]string{"torch.cuda"})
	if model != "T4" {
		t.Errorf("torch only → want T4, got %s", model)
	}
	if conf < 0.15 {
		t.Errorf("torch confidence should be >= 0.15, got %.2f", conf)
	}
}

func TestInferGPUModel_Unknown(t *testing.T) {
	model, _, conf := inferGPUModel([]string{"some-obscure-lib"})
	if model != "T4" {
		t.Errorf("unknown lib → default T4, got %s", model)
	}
	if conf > 0.3 {
		t.Errorf("unknown lib → low confidence, got %.2f", conf)
	}
}

// ── GPUConfidenceByLib ───────────────────────────────────────────

func TestGPUConfidenceByLib(t *testing.T) {
	if c := GPUConfidenceByLib("vllm"); c != 1.0 {
		t.Errorf("vllm confidence = %.2f, want 1.0", c)
	}
	if c := GPUConfidenceByLib("torch.cuda"); c != 0.6 {
		t.Errorf("torch.cuda confidence = %.2f, want 0.6", c)
	}
	if c := GPUConfidenceByLib("triton"); c != 0.3 {
		t.Errorf("triton confidence = %.2f, want 0.3", c)
	}
}

// ── MaxGPUConfidence ─────────────────────────────────────────────

func TestMaxGPUConfidence(t *testing.T) {
	c := MaxGPUConfidence([]string{"torch.cuda", "vllm"})
	if c != 1.0 {
		t.Errorf("max(vllm=1.0, torch=0.6) = %.2f, want 1.0", c)
	}
}

// ── RecommendMIG ─────────────────────────────────────────────────

func TestRecommendMIG_Available(t *testing.T) {
	yes, profile := RecommendMIG(5*GB, true)
	if !yes || profile != "1g.10gb" {
		t.Errorf("5GB+MIG available → want 1g.10gb, got (%v, %s)", yes, profile)
	}
}

func TestRecommendMIG_TooMuchVRAM(t *testing.T) {
	yes, _ := RecommendMIG(40*GB, true)
	if yes {
		t.Error("40GB should not recommend MIG")
	}
}

func TestRecommendMIG_NotAvailable(t *testing.T) {
	yes, _ := RecommendMIG(5*GB, false)
	if yes {
		t.Error("MIG not available should return false")
	}
}
