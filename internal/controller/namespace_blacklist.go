package controller

import "strings"

// protectedNamespaces 全局 controller 的 namespace 黑名单
//
// 这些 namespace 即使被手工打上 kubepivot.io/managed=true 标签，
// 也会被 controller 在逻辑层拒绝处理。护栏作用：
//   - 防止用户误操作导致 controller 接管关键系统组件
//   - 即使 RBAC 授权，代码层再拒一次，双保险
//
// 可通过 KUBEPIVOT_EXTRA_PROTECTED_NS 环境变量追加黑名单
// 格式：逗号分隔，例如 "istio-system,monitoring"
var protectedNamespaces = map[string]bool{
	"kube-system":       true,
	"kube-public":       true,
	"kube-node-lease":   true,
	"kubepivot-system":  true, // controller 自己的 ns，永不自管理
	"default":           true, // default ns 通常混杂，保守拒绝
}

// IsProtectedNamespace 判断 namespace 是否在黑名单
// 用于 Reconcile 执行前的最后护栏检查
func IsProtectedNamespace(ns string) bool {
	if protectedNamespaces[ns] {
		return true
	}
	// 运行时追加的黑名单（KUBEPIVOT_EXTRA_PROTECTED_NS 环境变量）
	for _, extra := range extraProtectedNamespaces() {
		if extra == ns {
			return true
		}
	}
	return false
}

// extraProtectedNamespaces 读取环境变量追加的黑名单
func extraProtectedNamespaces() []string {
	extra := getenv("KUBEPIVOT_EXTRA_PROTECTED_NS", "")
	if extra == "" {
		return nil
	}
	var result []string
	for _, s := range strings.Split(extra, ",") {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
