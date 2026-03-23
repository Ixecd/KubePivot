package ai

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
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

// yamlComponents 对应 components.yaml 的结构
type yamlComponents struct {
	Components []struct {
		Name     string `yaml:"name"`
		Image    string `yaml:"image"`
		Port     int    `yaml:"port"`
		Replicas int    `yaml:"replicas"`
		CPU      string `yaml:"cpu"`
		Memory   string `yaml:"memory"`
		Storage  string `yaml:"storage"`
	} `yaml:"components"`
}

func LoadComponents(path string) ([]Component, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open components config: %w", err)
	}

	var raw yamlComponents
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse components config: %w", err)
	}

	components := make([]Component, 0, len(raw.Components))
	for _, c := range raw.Components {
		components = append(components, Component{
			Name:     c.Name,
			Image:    c.Image,
			Port:     c.Port,
			Replicas: c.Replicas,
			CPU:      c.CPU,
			Memory:   c.Memory,
			Storage:  c.Storage,
		})
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
