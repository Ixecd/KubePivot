// internal/scheduler/plan_writer.go
package scheduler

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// componentsFile 表示 configs/components.yaml 的顶层结构
type componentsFile struct {
	Components []componentEntry `yaml:"components"`
}

type componentEntry struct {
	Name         string            `yaml:"name"`
	Namespace    string            `yaml:"namespace,omitempty"`
	NodeSelector map[string]string `yaml:"nodeSelector,omitempty"`
	// 保留其他字段：通过 yaml.Node 或 map 保持原样
	Rest map[string]interface{} `yaml:",inline,omitempty"`
}

// filePlanWriter 将调度结果写入 configs/components.yaml
type filePlanWriter struct {
	projectRoot string
}

// NewFilePlanWriter 创建基于文件的 PlanWriter
func NewFilePlanWriter(projectRoot string) PlanWriter {
	return &filePlanWriter{projectRoot: projectRoot}
}

func (w *filePlanWriter) WriteAssignments(path string, assignments map[string]string) error {
	if path == "" {
		// 默认使用项目根下的 configs/components.yaml
		path = filepath.Join(w.projectRoot, "configs", "components.yaml")
	}

	// 读取原文件
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read components.yaml: %w", err)
	}

	var cf componentsFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return fmt.Errorf("parse components.yaml: %w", err)
	}

	// 建立组件名到条目的映射
	compMap := make(map[string]*componentEntry)
	for i := range cf.Components {
		compMap[cf.Components[i].Name] = &cf.Components[i]
	}

	// 应用调度结果
	for key, node := range assignments {
		// key 格式：namespace/name 或仅 name
		ns, name := splitKey(key)
		entry, ok := compMap[name]
		if !ok {
			// 尝试用 key 直接匹配
			entry, ok = compMap[key]
			if !ok {
				continue
			}
		}
		// 如果 namespace 不匹配则跳过（安全保护）
		if ns != "" && entry.Namespace != "" && entry.Namespace != ns {
			continue
		}
		if entry.NodeSelector == nil {
			entry.NodeSelector = make(map[string]string)
		}
		entry.NodeSelector["kubernetes.io/hostname"] = node
	}

	// 序列化回 YAML
	out, err := yaml.Marshal(&cf)
	if err != nil {
		return fmt.Errorf("marshal components.yaml: %w", err)
	}

	// 原子写入：临时文件 + rename
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		return fmt.Errorf("write tmp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath) // 清理临时文件
		return fmt.Errorf("rename tmp file: %w", err)
	}

	return nil
}

// splitKey 将 "namespace/name" 拆分为 ns 和 name
func splitKey(key string) (ns, name string) {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '/' {
			return key[:i], key[i+1:]
		}
	}
	return "", key
}