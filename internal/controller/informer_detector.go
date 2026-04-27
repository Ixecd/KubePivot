package controller

import (
	"strings"
)

// ─── Informer-backed Detector（v2.7 Step 2b-1）─────────────────────
//
// InformerDetector 实现 Detector 接口，优先走 informer cache 查询。
//
// 双保险设计（C 路径）：
//
//   informer 起来 + cache 命中  → 直接返回 true（避免 kubectl fork）
//   informer 起来 + cache 未命中 → fallback 到 kubectl 二次验证
//   informer 未起来 / kind 不支持 → fallback 到 kubectl
//
// "找到信任，没找到 fallback" — 一致性高 + 性能好：
//   - 不会有"informer 滞后导致 kubectl 漏查"的假阴性
//   - 大部分查询走 cache（< 50ns）替代 kubectl fork（~100ms）
//   - 既有 v2.5/v2.6 行为完全保留（fallback 兜底）
//
// 当前支持的 kind 映射（v2.7 试点）：
//   "Deployment" → "deployments"
//   其他 kind → 直接 fallback（不走 informer 路径）
//
// v2.7.x / v2.8 计划：
//   - 扩展 kind 映射（Service / Pod 等，需对应 pool.Start 启动 informer）
//   - 与 Step 2b-2 (loadResourceLabels 改造) 协同

// InformerDetector 实现 Detector 接口。
//
// 不持有自己的资源，仅引用 pool 和 fallback。
// 多个 reconciler 可共享同一 InformerDetector 实例。
//
// 并发安全：依赖 pool / fallback 的并发安全性（都已保证）。
type InformerDetector struct {
	pool     *InformerPool
	fallback Detector
}

// NewInformerDetector 创建一个 informer-backed detector。
//
// pool 不可为 nil（即使 pool 内无 informer，也走 fallback 路径）。
// fallback 不可为 nil（informer 不可用时必需的兜底）。
//
// 典型用法（global.go）：
//
//	kubectlDetector := NewKubectlDetector(kubeconfig)
//	informerDetector := NewInformerDetector(informerPool, kubectlDetector)
//	// reconciler 用 informerDetector
func NewInformerDetector(pool *InformerPool, fallback Detector) *InformerDetector {
	return &InformerDetector{
		pool:     pool,
		fallback: fallback,
	}
}

// ResourceExists 实现 Detector 接口。
//
// 算法：
//  1. kind 转换为 informer resource name（仅支持已知映射）
//  2. 不支持的 kind → fallback
//  3. informer 未启动 → fallback
//  4. informer.Get 找到 → return true (信任 cache)
//  5. informer.Get 未找到 → fallback (cache 可能滞后，二次验证)
//
// 错误透传：fallback 的 error 直接返回。
func (d *InformerDetector) ResourceExists(kind, name, namespace string) (bool, error) {
	// kind → resource 映射（仅试点 deployment）
	resource, ok := kindToResource(kind)
	if !ok {
		// 不支持的 kind → fallback
		return d.fallback.ResourceExists(kind, name, namespace)
	}

	// 防御：pool 为 nil（不应发生，但保护）
	if d.pool == nil {
		return d.fallback.ResourceExists(kind, name, namespace)
	}

	informer := d.pool.Get(resource)
	if informer == nil {
		// informer 未启动（fail soft 触发） → fallback
		return d.fallback.ResourceExists(kind, name, namespace)
	}

	// cache hit → 信任，直接返回 true
	if _, found := informer.Get(namespace, name); found {
		return true, nil
	}

	// cache miss → fallback 二次验证
	// 理由：informer cache 可能滞后（list 未完成 / watch 延迟）
	//       fallback 到 kubectl 保证不漏查
	return d.fallback.ResourceExists(kind, name, namespace)
}

// ─── LabelGetter 接口（v2.7 Step 2b-2）─────────────────────────────
//
// LabelGetter 暴露"按资源 ID 拉取 labels"的能力，独立于 Detector。
//
// 为什么独立成接口（而非扩展 Detector）：
//   - Detector 接口已被 KubectlDetector / mockDetector 实现
//   - 扩展 Detector 会强制所有实现加新方法（破坏既有测试）
//   - LabelGetter 是 InformerDetector 特有能力（cache 中已有 labels）
//   - 调用方用类型断言判断（heal.go loadResourceLabels）
//
// 实现者：
//   InformerDetector  - 从 informer cache 读 Resource.Labels
//   KubectlDetector   - 不实现（loadResourceLabels 走原 kubectl 路径）
//   mockDetector      - 不实现（既有测试不受影响）
//
// 返回 bool 而非 error：
//   - cache 查询不会出 error
//   - bool 表示"cache 是否找到该资源"
//   - 调用方 fallback 到 kubectl 路径，跟 ResourceExists 一致
type LabelGetter interface {
	GetResourceLabels(kind, name, namespace string) (map[string]string, bool)
}

// GetResourceLabels 实现 LabelGetter 接口。
//
// 算法（与 ResourceExists 同模式）:
//   1. kind → resource 映射（仅试点 Deployment）
//   2. 不支持 / pool nil / informer 未启动 / cache miss → 返回 (nil, false)
//   3. cache 命中 → 返回 (Resource.Labels, true)
//
// 注意：返回的 labels 可能为 nil（K8s 对象本身无 labels）
// 调用方应理解 (nil, true) = "cache 找到资源但该资源无 labels"
// 这与原 kubectl 路径行为一致（obj.Metadata.Labels 也可能 nil）。
func (d *InformerDetector) GetResourceLabels(kind, name, namespace string) (map[string]string, bool) {
	resource, ok := kindToResource(kind)
	if !ok {
		return nil, false
	}
	if d.pool == nil {
		return nil, false
	}
	informer := d.pool.Get(resource)
	if informer == nil {
		return nil, false
	}
	r, found := informer.Get(namespace, name)
	if !found || r == nil {
		return nil, false
	}
	return r.Labels, true
}

// kindToResource 把 K8s Kind (大驼峰单数) 映射到 API resource name (小写复数)。
//
// v2.7.0 仅支持 Deployment（informer 试点资源）。
// 不支持的 kind 返回 (_, false)，调用方应 fallback。
//
// 未来扩展：Service / Pod / ConfigMap / Ingress 等，
// 需要在 informer pool 启动对应资源的 informer。
func kindToResource(kind string) (string, bool) {
	switch strings.ToLower(kind) {
	case "deployment":
		return "deployments", true
	default:
		return "", false
	}
}
