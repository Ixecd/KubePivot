package ai

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Component struct {
	Name     string
	Image    string
	Port     int
	Replicas int
	CPU      string
	Memory   string
	Storage  string
}

type Plan struct {
	Name     string
	Replicas int
	CPU      string
	Memory   string
	Storage  string
	Image    string
	Port     int
}

func LoadComponents(path string) ([]Component, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open components config: %w", err)
	}
	defer file.Close()

	var components []Component
	var current *Component

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "- name:") {
			if current != nil {
				components = append(components, *current)
			}
			name := strings.TrimSpace(strings.TrimPrefix(line, "- name:"))
			current = &Component{Name: name}
			continue
		}
		if current == nil {
			continue
		}
		if strings.HasPrefix(line, "image:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "image:"))
			val = strings.Trim(val, `"`)  // ← 去掉引号
			current.Image = val
			continue
		}
		if strings.HasPrefix(line, "port:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "port:"))
			if port, err := strconv.Atoi(value); err == nil {
				current.Port = port
			}
			continue
		}
		if strings.HasPrefix(line, "replicas:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "replicas:"))
			if replicas, err := strconv.Atoi(value); err == nil {
				current.Replicas = replicas
			}
			continue
		}
		if strings.HasPrefix(line, "cpu:") {
			current.CPU = strings.TrimSpace(strings.TrimPrefix(line, "cpu:"))
			continue
		}
		if strings.HasPrefix(line, "memory:") {
			current.Memory = strings.TrimSpace(strings.TrimPrefix(line, "memory:"))
			continue
		}
		if strings.HasPrefix(line, "storage:") {
			current.Storage = strings.TrimSpace(strings.TrimPrefix(line, "storage:"))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan components config: %w", err)
	}
	if current != nil {
		components = append(components, *current)
	}
	return components, nil
}

func EstimateReplicas(component Component) int {
	if component.Replicas > 0 {
		return component.Replicas
	}
	return 1
}

func EstimateResources(component Component) (cpu string, memory string, storage string) {
	if component.CPU != "" {
		cpu = component.CPU
	}
	if component.Memory != "" {
		memory = component.Memory
	}
	if component.Storage != "" {
		storage = component.Storage
	}
	if cpu == "" {
		cpu = "100m"
	}
	if memory == "" {
		memory = "128Mi"
	}
	if storage == "" {
		storage = "1Gi"
	}
	return cpu, memory, storage
}

func BuildPlan(path string) ([]Plan, error) {
	components, err := LoadComponents(path)
	if err != nil {
		return nil, err
	}
	plans := make([]Plan, 0, len(components))
	for _, component := range components {
		cpu, memory, storage := EstimateResources(component)
		plans = append(plans, Plan{
			Name:     component.Name,
			Replicas: EstimateReplicas(component),
			CPU:      cpu,
			Memory:   memory,
			Storage:  storage,
			Image:    component.Image,
			Port:     component.Port,
		})
	}
	return plans, nil
}
