// internal/scheduler/plan_writer_test.go
package scheduler

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFilePlanWriter_WriteAssignments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "components.yaml")

	// 准备初始 YAML
	initial := `
components:
  - name: web
    namespace: default
    cpu: "100m"
  - name: worker
    namespace: default
    cpu: "200m"
`
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	writer := &filePlanWriter{Root: dir}
	assignments := map[string]string{
		"default/web":    "node1",
		"default/worker": "node2",
	}
	if err := writer.WriteAssignments(path, assignments); err != nil {
		t.Fatal(err)
	}

	// 读取并验证
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cf componentsFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		t.Fatal(err)
	}
	for _, c := range cf.Components {
		switch c.Name {
		case "web":
			if c.NodeSelector["kubernetes.io/hostname"] != "node1" {
				t.Errorf("web nodeSelector = %v, want node1", c.NodeSelector)
			}
		case "worker":
			if c.NodeSelector["kubernetes.io/hostname"] != "node2" {
				t.Errorf("worker nodeSelector = %v, want node2", c.NodeSelector)
			}
		}
	}
}
