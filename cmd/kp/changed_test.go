package main

import (
	"testing"

	"github.com/Ixecd/kubepivot/internal/planner"
)

var testPlans = []planner.Plan{
	{Name: "wallet-service", Image: "wallet-service"},
	{Name: "chain-miner", Image: "chain-miner"},
	{Name: "web3-blitz-postgres", Image: ""},
}

func TestDetectChangedServices_CmdPath(t *testing.T) {
	// cmd/wallet-service/* → 只部署 wallet-service
	files := []string{"cmd/wallet-service/main.go"}
	changed := classifyChangedFiles(files, testPlans)
	if changed == nil {
		t.Fatal("不应触发全量部署")
	}
	if !changed["wallet-service"] {
		t.Error("cmd/wallet-service 变更应标记 wallet-service")
	}
	if changed["chain-miner"] {
		t.Error("chain-miner 不应被标记")
	}
}

func TestDetectChangedServices_InternalPath(t *testing.T) {
	// internal/* → 所有有 image 的服务
	files := []string{"internal/db/migrate.go"}
	changed := classifyChangedFiles(files, testPlans)
	if changed == nil {
		t.Fatal("不应触发全量部署")
	}
	if !changed["wallet-service"] || !changed["chain-miner"] {
		t.Error("internal 变更应标记所有有 image 的服务")
	}
	if changed["web3-blitz-postgres"] {
		t.Error("image 为空的服务不应被标记")
	}
}

func TestDetectChangedServices_GlobalPath(t *testing.T) {
	// go.mod → 全量部署（返回 nil）
	files := []string{"go.mod"}
	changed := classifyChangedFiles(files, testPlans)
	if changed != nil {
		t.Error("go.mod 变更应触发全量部署（返回 nil）")
	}
}

func TestDetectChangedServices_ConfigsPath(t *testing.T) {
	// configs/* → 全量部署
	files := []string{"configs/components.yaml"}
	changed := classifyChangedFiles(files, testPlans)
	if changed != nil {
		t.Error("configs 变更应触发全量部署（返回 nil）")
	}
}

func TestDetectChangedServices_DeploymentsPath(t *testing.T) {
	// deployments/<proj>/<svc>/* → 对应服务
	files := []string{"deployments/web3-blitz/wallet-service/values.yaml"}
	changed := classifyChangedFiles(files, testPlans)
	if changed == nil {
		t.Fatal("不应触发全量部署")
	}
	if !changed["wallet-service"] {
		t.Error("deployments/wallet-service 变更应标记 wallet-service")
	}
}

func TestFilterChangedPlans_FiltersCorrectly(t *testing.T) {
	changed := map[string]bool{"wallet-service": true}
	filtered := filterChangedPlans(testPlans, changed)
	if len(filtered) != 1 {
		t.Errorf("应过滤出 1 个服务，got %d", len(filtered))
	}
	if filtered[0].Name != "wallet-service" {
		t.Errorf("过滤结果应为 wallet-service，got %s", filtered[0].Name)
	}
}

func TestFilterChangedPlans_NilReturnsAll(t *testing.T) {
	filtered := filterChangedPlans(testPlans, nil)
	if len(filtered) != len(testPlans) {
		t.Errorf("nil changed 应返回全部 %d 个，got %d", len(testPlans), len(filtered))
	}
}
