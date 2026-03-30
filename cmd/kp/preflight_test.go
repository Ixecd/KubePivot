package main

import (
	"os"
	"path/filepath"
	"testing"
)

// withPath 在测试中临时替换 PATH，测试结束后恢复。
// 通过控制 PATH 来模拟工具存在 / 不存在，不依赖系统环境。
func withPath(t *testing.T, path string) {
	t.Helper()
	orig := os.Getenv("PATH")
	t.Setenv("PATH", path)
	t.Cleanup(func() { os.Setenv("PATH", orig) })
}

// makeFakeBin 在临时目录创建一个可执行的空文件，模拟工具已安装。
func makeFakeBin(t *testing.T, dir, name string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("创建 fake bin %s 失败: %v", name, err)
	}
}

func TestCheckDeps_AllPresent(t *testing.T) {
	tmp := t.TempDir()
	for _, d := range deployDeps {
		makeFakeBin(t, tmp, d.bin)
	}
	withPath(t, tmp)

	if err := checkDeps(deployDeps); err != nil {
		t.Errorf("所有工具都存在时应返回 nil，got: %v", err)
	}
}

func TestCheckDeps_AllMissing(t *testing.T) {
	withPath(t, "") // 空 PATH，找不到任何工具

	err := checkDeps(deployDeps)
	if err == nil {
		t.Fatal("所有工具都缺失时应返回 error")
	}
}

func TestCheckDeps_PartialMissing(t *testing.T) {
	tmp := t.TempDir()
	// 只安装 docker，kubectl 和 helm 缺失
	makeFakeBin(t, tmp, "docker")
	withPath(t, tmp)

	err := checkDeps(deployDeps)
	if err == nil {
		t.Fatal("部分工具缺失时应返回 error")
	}
}

func TestCheckDeps_EmptyDeps(t *testing.T) {
	if err := checkDeps(nil); err != nil {
		t.Errorf("空依赖列表应返回 nil，got: %v", err)
	}
}
