package controller

import "testing"

// TestFindReleaseForResource_ExplicitOverride
// resources.yaml 里显式声明了 helm-release 字段时（蓝绿场景），
// 必须优先使用，覆盖默认推断逻辑。
func TestFindReleaseForResource_ExplicitOverride(t *testing.T) {
	t.Setenv("PROJECT_NAME", "web3-blitz") // 即便 PROJECT_NAME 设了也无效

	res := Resource{
		Kind:        "Deployment",
		Name:        "wallet-service",
		HelmRelease: "web3-blitz-blue",
	}
	got := findReleaseForResource("web3-blitz", res)
	if got != "web3-blitz-blue" {
		t.Errorf("显式 HelmRelease 应优先，got=%q want=web3-blitz-blue", got)
	}
}

// TestFindReleaseForResource_DefaultInference
// 没有显式声明时，按 PROJECT_NAME-Name 推断
func TestFindReleaseForResource_DefaultInference(t *testing.T) {
	t.Setenv("PROJECT_NAME", "myapp")

	res := Resource{
		Kind: "Deployment",
		Name: "api",
	}
	got := findReleaseForResource("myapp-ns", res)
	if got != "myapp-api" {
		t.Errorf("默认推断应为 PROJECT_NAME-Name，got=%q want=myapp-api", got)
	}
}

// TestFindReleaseForResource_FallbackToNamespace
// PROJECT_NAME 为空时回退到 namespace（v2.3.0 的 fallback 逻辑保留）
func TestFindReleaseForResource_FallbackToNamespace(t *testing.T) {
	t.Setenv("PROJECT_NAME", "")

	res := Resource{
		Kind: "Deployment",
		Name: "worker",
	}
	got := findReleaseForResource("dev-ns", res)
	if got != "dev-ns-worker" {
		t.Errorf("PROJECT_NAME 空时应回退到 namespace，got=%q want=dev-ns-worker", got)
	}
}

// TestFindReleaseForResource_ExplicitTrumpsAll
// 显式 > 推断 是绝对优先级。即便 PROJECT_NAME 空 + namespace 不一致，
// 显式声明的值也必须被尊重。
func TestFindReleaseForResource_ExplicitTrumpsAll(t *testing.T) {
	t.Setenv("PROJECT_NAME", "")

	res := Resource{
		Kind:        "Deployment",
		Name:        "wallet",
		HelmRelease: "custom-release-name-anywhere",
	}
	got := findReleaseForResource("any-ns", res)
	if got != "custom-release-name-anywhere" {
		t.Errorf("显式声明应碾压一切，got=%q", got)
	}
}
