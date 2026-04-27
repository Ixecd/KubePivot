package eventstream

import (
	"strings"
	"time"
)

// 全量 resync 周期默认值（Q8=C "按资源类型差异化"）。
//
// 设计依据：变更频率
//   - 高频变更资源 → 短周期，避免漏事件
//   - 低频变更资源 → 长周期，节省 K8s API server 压力
//
// 周期单位是"全量 list 的间隔"，而非"watch 长连接的健康检查"。
// watch 本身一直在跑（除非断线），resync 是兜底。
//
// 调优依据：
//   - 越短：watch 漏事件的窗口越小，但 API server 压力越大
//   - 越长：API server 压力越小，但漏事件后的恢复延迟更长
const (
	// ResyncPeriodHigh 高频变更资源的 resync 周期（10min）。
	// 适用：pods / events
	ResyncPeriodHigh = 10 * time.Minute

	// ResyncPeriodMedium 中频变更资源（30min）。
	// 适用：deployments / statefulsets / daemonsets / replicasets
	ResyncPeriodMedium = 30 * time.Minute

	// ResyncPeriodLow 低频变更资源（60min）。
	// 适用：services / configmaps / secrets / ingresses / hpa
	ResyncPeriodLow = 60 * time.Minute

	// ResyncPeriodVeryLow 极低频资源（120min）。
	// 适用：namespaces / nodes / storageclasses / persistentvolumes
	ResyncPeriodVeryLow = 120 * time.Minute
)

// resourcePeriodMap 资源类型 → resync 周期的映射表。
//
// key 是 K8s API 的 plural lowercase（"deployments"、"pods"）。
// 单数或大小写都通过 normalizeResource 标准化后查询。
var resourcePeriodMap = map[string]time.Duration{
	// 高频（10min）
	"pods":   ResyncPeriodHigh,
	"events": ResyncPeriodHigh,

	// 中频（30min）
	"deployments":  ResyncPeriodMedium,
	"statefulsets": ResyncPeriodMedium,
	"daemonsets":   ResyncPeriodMedium,
	"replicasets":  ResyncPeriodMedium,
	"jobs":         ResyncPeriodMedium,
	"cronjobs":     ResyncPeriodMedium,

	// 低频（60min）
	"services":                 ResyncPeriodLow,
	"configmaps":               ResyncPeriodLow,
	"secrets":                  ResyncPeriodLow,
	"ingresses":                ResyncPeriodLow,
	"horizontalpodautoscalers": ResyncPeriodLow,
	"persistentvolumeclaims":   ResyncPeriodLow,
	"endpoints":                ResyncPeriodLow,
	"endpointslices":           ResyncPeriodLow,

	// 极低频（120min）
	"namespaces":         ResyncPeriodVeryLow,
	"nodes":              ResyncPeriodVeryLow,
	"storageclasses":     ResyncPeriodVeryLow,
	"persistentvolumes":  ResyncPeriodVeryLow,
	"customresourcedefinitions": ResyncPeriodVeryLow,
}

// DefaultResyncPeriod 返回指定资源类型的推荐 resync 周期。
//
// 输入：
//   - resource: K8s API 资源名（如 "deployments" / "Pod" / "pod"）
//
// 容错：
//   - 大小写不敏感
//   - 单数转复数（"pod" → "pods"）
//   - 未知资源类型返回 ResyncPeriodMedium（30min，安全兜底）
//
// 用户可在 InformerOptions.ResyncPeriod 显式覆盖此默认值。
func DefaultResyncPeriod(resource string) time.Duration {
	normalized := normalizeResource(resource)
	if period, ok := resourcePeriodMap[normalized]; ok {
		return period
	}
	// 兜底：未知资源用中频
	return ResyncPeriodMedium
}

// normalizeResource 标准化资源名。
//
//   - 转小写
//   - 单数转复数（简单 +s 规则，覆盖常用资源）
//
// 不依赖 K8s 的 RESTMapper（v2.7 不引入 client-go）。
// 只覆盖最常见的资源类型，未知类型保留原样。
func normalizeResource(resource string) string {
	r := strings.ToLower(strings.TrimSpace(resource))
	if r == "" {
		return r
	}

	// 已是复数 → 直接返回
	if _, ok := resourcePeriodMap[r]; ok {
		return r
	}

	// 简单单复数规则
	// 注意：K8s 资源命名比英语规则简单，多数 +s 即可
	plural := r + "s"
	if _, ok := resourcePeriodMap[plural]; ok {
		return plural
	}

	// 处理 ies / ches 等特殊变换
	switch {
	case strings.HasSuffix(r, "y"):
		// policy → policies, ingress 不是这种
		alt := r[:len(r)-1] + "ies"
		if _, ok := resourcePeriodMap[alt]; ok {
			return alt
		}
	case strings.HasSuffix(r, "s") || strings.HasSuffix(r, "x") || strings.HasSuffix(r, "ch"):
		// ingress → ingresses
		alt := r + "es"
		if _, ok := resourcePeriodMap[alt]; ok {
			return alt
		}
	}

	return r
}
