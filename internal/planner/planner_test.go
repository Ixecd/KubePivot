package planner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── LoadComponents 测试 ───────────────────────────────────────────────────────

func TestLoadComponents_ValidFile(t *testing.T) {
	path := writeYAML(t, `
components:
  - name: myapp
    image: myapp
    port: 8080
    replicas: 2
    cpu: 200m
    memory: 256Mi
    storage: 2Gi
`)
	components, err := LoadComponents(path)
	require.NoError(t, err)
	require.Len(t, components, 1)

	c := components[0]
	assert.Equal(t, "myapp", c.Name)
	assert.Equal(t, "myapp", c.Image)
	assert.Equal(t, 8080, c.Port)
	assert.Equal(t, 2, c.Replicas)
	assert.Equal(t, "200m", c.CPU)
	assert.Equal(t, "256Mi", c.Memory)
	assert.Equal(t, "2Gi", c.Storage)
}

func TestLoadComponents_MultipleComponents(t *testing.T) {
	path := writeYAML(t, `
components:
  - name: backend
    image: backend
    port: 8080
  - name: worker
    image: worker
    port: 9090
`)
	components, err := LoadComponents(path)
	require.NoError(t, err)
	assert.Len(t, components, 2)
	assert.Equal(t, "backend", components[0].Name)
	assert.Equal(t, "worker", components[1].Name)
}

func TestLoadComponents_EmptyImage(t *testing.T) {
	path := writeYAML(t, `
components:
  - name: cli-tool
    port: 0
`)
	components, err := LoadComponents(path)
	require.NoError(t, err)
	require.Len(t, components, 1)
	assert.Equal(t, "", components[0].Image)
}

func TestLoadComponents_MissingFields(t *testing.T) {
	path := writeYAML(t, `
components:
  - name: minimal
`)
	components, err := LoadComponents(path)
	require.NoError(t, err)
	require.Len(t, components, 1)
	assert.Equal(t, "minimal", components[0].Name)
	assert.Equal(t, "", components[0].Image)
	assert.Equal(t, 0, components[0].Port)
	assert.Equal(t, 0, components[0].Replicas)
}

func TestLoadComponents_EmptyComponents(t *testing.T) {
	path := writeYAML(t, `components: []`)
	components, err := LoadComponents(path)
	require.NoError(t, err)
	assert.Empty(t, components)
}

func TestLoadComponents_FileNotFound(t *testing.T) {
	_, err := LoadComponents("/nonexistent/path/components.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "open components config")
}

func TestLoadComponents_InvalidYAML(t *testing.T) {
	path := writeYAML(t, `invalid: yaml: [unclosed`)
	_, err := LoadComponents(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse components config")
}

func TestLoadComponents_QuotedValues(t *testing.T) {
	path := writeYAML(t, `
components:
  - name: "myapp"
    image: "myapp"
    cpu: "100m"
    memory: "128Mi"
`)
	components, err := LoadComponents(path)
	require.NoError(t, err)
	assert.Equal(t, "myapp", components[0].Name)
	assert.Equal(t, "100m", components[0].CPU)
}

// ── EstimateReplicas 测试 ─────────────────────────────────────────────────────

func TestEstimateReplicas_Explicit(t *testing.T) {
	c := Component{Replicas: 3}
	assert.Equal(t, 3, EstimateReplicas(c))
}

func TestEstimateReplicas_DefaultToOne(t *testing.T) {
	c := Component{Replicas: 0}
	assert.Equal(t, 1, EstimateReplicas(c))
}

func TestEstimateReplicas_NegativeDefaultsToOne(t *testing.T) {
	// 负数不合法，但只检查 > 0，所以 fallback 到 1
	c := Component{Replicas: -1}
	assert.Equal(t, 1, EstimateReplicas(c))
}

// ── EstimateResources 测试 ────────────────────────────────────────────────────

func TestEstimateResources_AllExplicit(t *testing.T) {
	c := Component{CPU: "500m", Memory: "512Mi", Storage: "10Gi"}
	cpu, memory, storage := EstimateResources(c)
	assert.Equal(t, "500m", cpu)
	assert.Equal(t, "512Mi", memory)
	assert.Equal(t, "10Gi", storage)
}

func TestEstimateResources_AllDefaults(t *testing.T) {
	c := Component{}
	cpu, memory, storage := EstimateResources(c)
	assert.Equal(t, "100m", cpu)
	assert.Equal(t, "128Mi", memory)
	assert.Equal(t, "1Gi", storage)
}

func TestEstimateResources_PartialDefaults(t *testing.T) {
	c := Component{CPU: "200m"}
	cpu, memory, storage := EstimateResources(c)
	assert.Equal(t, "200m", cpu)
	assert.Equal(t, "128Mi", memory)
	assert.Equal(t, "1Gi", storage)
}

// ── BuildPlan 测试 ────────────────────────────────────────────────────────────

func TestBuildPlan_HappyPath(t *testing.T) {
	path := writeYAML(t, `
components:
  - name: myapp
    image: myapp
    port: 8080
    replicas: 2
    cpu: 200m
    memory: 256Mi
    storage: 2Gi
`)
	plans, err := BuildPlan(path)
	require.NoError(t, err)
	require.Len(t, plans, 1)

	p := plans[0]
	assert.Equal(t, "myapp", p.Name)
	assert.Equal(t, "myapp", p.Image)
	assert.Equal(t, 8080, p.Port)
	assert.Equal(t, 2, p.Replicas)
	assert.Equal(t, "200m", p.CPU)
	assert.Equal(t, "256Mi", p.Memory)
	assert.Equal(t, "2Gi", p.Storage)
}

func TestBuildPlan_DefaultResources(t *testing.T) {
	path := writeYAML(t, `
components:
  - name: myapp
    image: myapp
    port: 8080
`)
	plans, err := BuildPlan(path)
	require.NoError(t, err)
	require.Len(t, plans, 1)

	p := plans[0]
	assert.Equal(t, 1, p.Replicas)
	assert.Equal(t, "100m", p.CPU)
	assert.Equal(t, "128Mi", p.Memory)
	assert.Equal(t, "1Gi", p.Storage)
}

func TestBuildPlan_EmptyImagePreserved(t *testing.T) {
	// image 为空的组件依然生成 plan，由上层决定是否跳过
	path := writeYAML(t, `
components:
  - name: cli-tool
    port: 0
`)
	plans, err := BuildPlan(path)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	assert.Equal(t, "", plans[0].Image)
}

func TestBuildPlan_MultipleComponents(t *testing.T) {
	path := writeYAML(t, `
components:
  - name: backend
    image: backend
    port: 8080
  - name: worker
    image: worker
    port: 9090
    replicas: 3
`)
	plans, err := BuildPlan(path)
	require.NoError(t, err)
	assert.Len(t, plans, 2)
	assert.Equal(t, "backend", plans[0].Name)
	assert.Equal(t, 1, plans[0].Replicas)
	assert.Equal(t, "worker", plans[1].Name)
	assert.Equal(t, 3, plans[1].Replicas)
}

func TestBuildPlan_FileNotFound(t *testing.T) {
	_, err := BuildPlan("/nonexistent/components.yaml")
	assert.Error(t, err)
}

func TestBuildPlan_OrderPreserved(t *testing.T) {
	path := writeYAML(t, `
components:
  - name: aaa
    image: aaa
  - name: bbb
    image: bbb
  - name: ccc
    image: ccc
`)
	plans, err := BuildPlan(path)
	require.NoError(t, err)
	require.Len(t, plans, 3)
	assert.Equal(t, "aaa", plans[0].Name)
	assert.Equal(t, "bbb", plans[1].Name)
	assert.Equal(t, "ccc", plans[2].Name)
}

// ── 工具函数 ──────────────────────────────────────────────────────────────────

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "components.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

// ── LoadComponents: type / depends_on ────────────────────────────────────────

func TestLoadComponents_TypeAndDependsOn(t *testing.T) {
	dir := t.TempDir()
	content := `
components:
  - name: postgres
    type: statefulset
    port: 5432
    image: ""
  - name: wallet-service
    type: deployment
    port: 2113
    image: wallet-service
    depends_on:
      - postgres
`
	path := filepath.Join(dir, "components.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	components, err := LoadComponents(path)
	require.NoError(t, err)
	require.Len(t, components, 2)

	assert.Equal(t, "statefulset", components[0].Type)
	assert.Empty(t, components[0].DependsOn)

	assert.Equal(t, "deployment", components[1].Type)
	assert.Equal(t, []string{"postgres"}, components[1].DependsOn)
}

func TestLoadComponents_DefaultType(t *testing.T) {
	dir := t.TempDir()
	content := `
components:
  - name: myapp
    port: 8080
    image: myapp
`
	path := filepath.Join(dir, "components.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	components, err := LoadComponents(path)
	require.NoError(t, err)
	assert.Equal(t, "deployment", components[0].Type)
}

// ── BuildLayers: 拓扑排序 ─────────────────────────────────────────────────────

func TestBuildLayers_NoDependencies(t *testing.T) {
	dir := t.TempDir()
	content := `
components:
  - name: postgres
    port: 5432
    image: ""
  - name: etcd
    port: 2379
    image: ""
  - name: myapp
    port: 8080
    image: myapp
`
	path := filepath.Join(dir, "components.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	layers, err := BuildLayers(path)
	require.NoError(t, err)
	// 没有依赖，全部在同一层
	assert.Len(t, layers, 1)
	assert.Len(t, layers[0], 3)
}

func TestBuildLayers_LinearDependency(t *testing.T) {
	dir := t.TempDir()
	content := `
components:
  - name: postgres
    port: 5432
    image: ""
  - name: wallet-service
    port: 2113
    image: wallet-service
    depends_on:
      - postgres
  - name: admin-service
    port: 8080
    image: admin-service
    depends_on:
      - wallet-service
`
	path := filepath.Join(dir, "components.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	layers, err := BuildLayers(path)
	require.NoError(t, err)
	require.Len(t, layers, 3)

	assert.Equal(t, "postgres", layers[0][0].Name)
	assert.Equal(t, "wallet-service", layers[1][0].Name)
	assert.Equal(t, "admin-service", layers[2][0].Name)
}

func TestBuildLayers_DiamondDependency(t *testing.T) {
	dir := t.TempDir()
	// postgres ─┐
	//           ├──→ admin-service
	// etcd    ──┘
	content := `
components:
  - name: postgres
    port: 5432
    image: ""
  - name: etcd
    port: 2379
    image: ""
  - name: admin-service
    port: 8080
    image: admin-service
    depends_on:
      - postgres
      - etcd
`
	path := filepath.Join(dir, "components.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	layers, err := BuildLayers(path)
	require.NoError(t, err)
	require.Len(t, layers, 2)

	// 层级 0：postgres 和 etcd（顺序不保证，但都在这层）
	names0 := []string{layers[0][0].Name, layers[0][1].Name}
	assert.Contains(t, names0, "postgres")
	assert.Contains(t, names0, "etcd")

	// 层级 1：admin-service
	assert.Equal(t, "admin-service", layers[1][0].Name)
}

func TestBuildLayers_CircularDependency(t *testing.T) {
	dir := t.TempDir()
	content := `
components:
  - name: service-a
    port: 8080
    image: service-a
    depends_on:
      - service-b
  - name: service-b
    port: 8081
    image: service-b
    depends_on:
      - service-a
`
	path := filepath.Join(dir, "components.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	_, err := BuildLayers(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "循环依赖")
}

func TestBuildLayers_UndefinedDependency(t *testing.T) {
	dir := t.TempDir()
	content := `
components:
  - name: myapp
    port: 8080
    image: myapp
    depends_on:
      - nonexistent
`
	path := filepath.Join(dir, "components.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	_, err := BuildLayers(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestBuildLayers_BuildPlanCompatible(t *testing.T) {
	// 验证 BuildPlan 和 BuildLayers 返回的内容一致（顺序按拓扑）
	dir := t.TempDir()
	content := `
components:
  - name: postgres
    port: 5432
    image: ""
  - name: myapp
    port: 8080
    image: myapp
    depends_on:
      - postgres
`
	path := filepath.Join(dir, "components.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	plans, err := BuildPlan(path)
	require.NoError(t, err)
	require.Len(t, plans, 2)

	assert.Equal(t, "postgres", plans[0].Name)
	assert.Equal(t, "myapp", plans[1].Name)
}

// ── Downstream ────────────────────────────────────────────────────────────────

func TestDownstream_SingleService(t *testing.T) {
	// postgres → wallet → admin
	layers := []Layer{
		{{Name: "postgres"}},
		{{Name: "wallet-service", DependsOn: []string{"postgres"}}},
		{{Name: "admin-service", DependsOn: []string{"wallet-service"}}},
	}

	result := Downstream(layers, "wallet-service")
	// 逆序：admin-service 先，wallet-service 后
	assert.Equal(t, []string{"admin-service", "wallet-service"}, result)
}

func TestDownstream_RootService(t *testing.T) {
	// postgres → wallet → admin
	layers := []Layer{
		{{Name: "postgres"}},
		{{Name: "wallet-service", DependsOn: []string{"postgres"}}},
		{{Name: "admin-service", DependsOn: []string{"wallet-service"}}},
	}

	result := Downstream(layers, "postgres")
	// postgres 的下游是 wallet 和 admin，逆序
	assert.Contains(t, result, "postgres")
	assert.Contains(t, result, "wallet-service")
	assert.Contains(t, result, "admin-service")
	// postgres 应该在最后（最上游最后 rollback）
	assert.Equal(t, "postgres", result[len(result)-1])
}

func TestDownstream_LeafService(t *testing.T) {
	layers := []Layer{
		{{Name: "postgres"}},
		{{Name: "wallet-service", DependsOn: []string{"postgres"}}},
		{{Name: "admin-service", DependsOn: []string{"wallet-service"}}},
	}

	result := Downstream(layers, "admin-service")
	// 叶节点没有下游，只有自身
	assert.Equal(t, []string{"admin-service"}, result)
}

func TestDownstream_NoDependencies(t *testing.T) {
	layers := []Layer{
		{
			{Name: "postgres"},
			{Name: "etcd"},
			{Name: "myapp"},
		},
	}

	result := Downstream(layers, "postgres")
	assert.Equal(t, []string{"postgres"}, result)
}
