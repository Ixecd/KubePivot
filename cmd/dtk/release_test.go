package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSemverPattern(t *testing.T) {
	valid := []string{"v0.1.0", "v1.0.0", "v1.2.3", "v10.20.30"}
	for _, v := range valid {
		if !semverPattern.MatchString(v) {
			t.Errorf("%s 应该是合法版本号", v)
		}
	}

	invalid := []string{"1.0.0", "v1.0", "v1", "v1.0.0.0", "v1.0.a", "", "latest"}
	for _, v := range invalid {
		if semverPattern.MatchString(v) {
			t.Errorf("%s 不应该是合法版本号", v)
		}
	}
}

func TestUpdateVersion_ExistingKey(t *testing.T) {
	f, err := os.CreateTemp("", "project*.env")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	f.WriteString("PROJECT_NAME=myapp\nVERSION=v0.1.0\nARCH=arm64\n")
	f.Close()

	if err := updateVersion(f.Name(), "v1.2.3"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(f.Name())
	content := string(data)

	if !contains(content, "VERSION=v1.2.3") {
		t.Errorf("VERSION 应已更新为 v1.2.3，got:\n%s", content)
	}
	if contains(content, "VERSION=v0.1.0") {
		t.Error("旧版本号 v0.1.0 应该被替换掉")
	}
	if !contains(content, "PROJECT_NAME=myapp") {
		t.Error("其他字段不应受影响")
	}
	if !contains(content, "ARCH=arm64") {
		t.Error("其他字段不应受影响")
	}
}

func TestUpdateVersion_NoExistingKey(t *testing.T) {
	f, err := os.CreateTemp("", "project*.env")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	f.WriteString("PROJECT_NAME=myapp\nARCH=arm64\n")
	f.Close()

	if err := updateVersion(f.Name(), "v1.0.0"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(f.Name())
	if !contains(string(data), "VERSION=v1.0.0") {
		t.Errorf("没有 VERSION 行时应追加，got:\n%s", string(data))
	}
}

func TestUpdateVersion_PreservesComments(t *testing.T) {
	f, err := os.CreateTemp("", "project*.env")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	f.WriteString("# Auto-generated\nPROJECT_NAME=myapp\nVERSION=v0.1.0\n")
	f.Close()

	updateVersion(f.Name(), "v2.0.0")

	data, _ := os.ReadFile(f.Name())
	if !contains(string(data), "# Auto-generated") {
		t.Error("注释行应该被保留")
	}
}

func TestTagExists_NotFound(t *testing.T) {
	dir := t.TempDir()
	runCmd(dir, nil, "git", "init")
	runCmd(dir, nil, "git", "commit", "--allow-empty", "-m", "init")

	if tagExists(dir, "v9.9.9") {
		t.Error("不存在的 tag 应返回 false")
	}
}

func TestTagExists_Found(t *testing.T) {
	dir := t.TempDir()
	runCmd(dir, nil, "git", "init")
	runCmd(dir, nil, "git", "commit", "--allow-empty", "-m", "init")
	runCmd(dir, nil, "git", "tag", "-a", "v1.0.0", "-m", "test")

	if !tagExists(dir, "v1.0.0") {
		t.Error("已存在的 tag 应返回 true")
	}
}

func TestCheckCleanWorkspace_Clean(t *testing.T) {
	dir := t.TempDir()
	runCmd(dir, nil, "git", "init")
	runCmd(dir, nil, "git", "commit", "--allow-empty", "-m", "init")

	if err := checkCleanWorkspace(dir); err != nil {
		t.Errorf("干净工作区应该通过，got: %v", err)
	}
}

func TestCheckCleanWorkspace_Dirty(t *testing.T) {
	dir := t.TempDir()
	runCmd(dir, nil, "git", "init")
	runCmd(dir, nil, "git", "commit", "--allow-empty", "-m", "init")

	// 创建未提交的文件
	os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("hello"), 0o644)

	if err := checkCleanWorkspace(dir); err == nil {
		t.Error("有未提交改动时应该返回错误")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
