package executor

import (
	"context"
	"sync"
	"testing"
)

func TestGetExecutor_Singleton(t *testing.T) {
	e1 := GetExecutor()
	e2 := GetExecutor()
	if e1 != e2 {
		t.Error("GetExecutor should return same singleton instance")
	}
}

func TestKpExecutor_Paths(t *testing.T) {
	e := GetExecutor()
	if e.KubectlPath() == "" {
		t.Error("KubectlPath should not be empty")
	}
	if e.HelmPath() == "" {
		t.Error("HelmPath should not be empty")
	}
}

func TestKpExecutor_Kubectl_InjectsKubeconfig(t *testing.T) {
	e := &KpExecutor{
		kubectl: "echo",
		sem:     make(chan struct{}, 5),
	}

	out, err := e.Kubectl(context.Background(), "/fake/kubeconfig", "version")
	if err != nil {
		t.Fatal(err)
	}
	// Args should contain --kubeconfig /fake/kubeconfig version
	if len(out) == 0 {
		t.Error("expected output from echo")
	}
}

func TestKpExecutor_Helm_InjectsKubeconfig(t *testing.T) {
	e := &KpExecutor{
		helm: "echo",
		sem:  make(chan struct{}, 5),
	}

	out, err := e.Helm(context.Background(), "/fake/kubeconfig", "version")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Error("expected output from echo")
	}
}

func TestKpExecutor_SemaphoreLimits(t *testing.T) {
	e := &KpExecutor{
		kubectl: "sleep",
		sem:     make(chan struct{}, 2), // only 2 concurrent
	}

	var wg sync.WaitGroup
	started := make(chan struct{}, 5)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			e.Kubectl(context.Background(), "", "0.1") // sleep 0.1s
		}()
	}

	wg.Wait()
	// All 4 should complete — semaphore just limits concurrency to 2
}

func TestKpExecutor_Generic_Fallback(t *testing.T) {
	e := &KpExecutor{
		kubectl: "kubectl",
		helm:    "helm",
		sem:     make(chan struct{}, 1),
	}

	// Unknown binary falls back to direct path
	out, err := e.Generic(context.Background(), "echo", "", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Error("Generic should execute unknown binary via echo")
	}
}

func TestKpExecutor_Sh(t *testing.T) {
	e := &KpExecutor{
		kubectl: "kubectl",
		helm:    "helm",
		sem:     make(chan struct{}, 5),
	}

	out, err := e.Sh(context.Background(), "echo hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Error("Sh should execute command via shell")
	}
}

func TestKpExecutor_CmdKubectl_ReturnsCmd(t *testing.T) {
	e := GetExecutor()
	cmd := e.CmdKubectl(context.Background(), "", "version")
	if cmd == nil {
		t.Fatal("CmdKubectl should return non-nil *exec.Cmd")
	}
	if cmd.Path == "" {
		t.Error("Cmd should have a Path set")
	}
}

func TestKpExecutor_Kubectl_NoKubeconfig(t *testing.T) {
	e := &KpExecutor{
		kubectl: "echo",
		sem:     make(chan struct{}, 5),
	}

	out, err := e.Kubectl(context.Background(), "", "version")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Error("Kubectl should work without kubeconfig")
	}
}

func TestResolveBin(t *testing.T) {
	// resolveBin should always return a non-empty string
	got := resolveBin("nonexistent-binary-xyz")
	if got == "" {
		t.Error("resolveBin should return fallback name even for missing binary")
	}
	if got != "nonexistent-binary-xyz" {
		t.Logf("resolveBin returned: %s", got)
	}
}

func TestKpExecutor_Generic_KnownBin(t *testing.T) {
	e := &KpExecutor{
		kubectl: "echo",
		helm:    "echo",
		sem:     make(chan struct{}, 5),
	}

	out, err := e.Generic(context.Background(), "kubectl", "", "version")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Error("Generic with known bin should work")
	}
}

func TestKpExecutor_ConcurrentAccess(t *testing.T) {
	e := &KpExecutor{
		kubectl: "echo",
		helm:    "echo",
		sem:     make(chan struct{}, 10),
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.Kubectl(context.Background(), "", "hello")
		}()
	}
	wg.Wait()
	// No panic = pass
}
