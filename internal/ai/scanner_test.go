// internal/ai/scanner_test.go
package ai

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanRepo_MultiLangDeps(t *testing.T) {
	dir := t.TempDir()

	// Create a Python project with GPU libs
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("torch>=2.0\nvllm==0.4.2\ntransformers\n"), 0644)
	os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[tool.poetry.dependencies]\npython = \"^3.11\"\n"), 0644)

	ctx, err := ScanRepo(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	// Check multi-lang deps detected
	if len(ctx.MultiLangDeps) < 2 {
		t.Errorf("expected 2 lang deps, got %d", len(ctx.MultiLangDeps))
	}

	// Check GPU libs detected from deps (torch in dep files = GPU signal)
	foundVLLM := false
	foundTorch := false
	for _, lib := range ctx.GPULibs {
		if lib == "vllm" {
			foundVLLM = true
		}
		if lib == "torch" {
			foundTorch = true
		}
	}
	if !foundVLLM {
		t.Error("vllm should be detected from requirements.txt")
	}
	if !foundTorch {
		t.Error("torch should be detected from requirements.txt (broad pattern)")
	}
}

func TestScanRepo_DockerBaseImages(t *testing.T) {
	dir := t.TempDir()

	// Create Dockerfile with GPU base image
	dockerDir := filepath.Join(dir, "build", "docker")
	os.MkdirAll(dockerDir, 0755)
	os.WriteFile(filepath.Join(dockerDir, "Dockerfile"), []byte("FROM nvidia/cuda:12.4.1-runtime-ubuntu22.04\nRUN echo hello\n"), 0644)

	ctx, err := ScanRepo(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(ctx.DockerBaseImages) == 0 {
		t.Error("should detect nvidia/cuda base image")
	}
}

func TestScanRepo_NoDeps(t *testing.T) {
	dir := t.TempDir()

	ctx, err := ScanRepo(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(ctx.MultiLangDeps) != 0 {
		t.Error("empty dir should have no lang deps")
	}
	if len(ctx.GPULibs) != 0 {
		t.Error("empty dir should have no GPU libs")
	}
	if len(ctx.DockerBaseImages) != 0 {
		t.Error("empty dir should have no docker images")
	}
}
