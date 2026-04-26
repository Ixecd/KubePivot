package route

import (
	"context"

	"github.com/Ixecd/kubepivot/internal/code"
)

// Provider 抽象不同流量层后端的统一接口。
//
// 调用风格遵循 Go 标准库（io.Reader / io.Writer），
// 包名已表明域，类型名不重复前缀。
//
// 实现类必须线程安全（Sandbox 状态机可能从多个 goroutine 调用）。
//
// 当前已知实现：
//   - IngressProvider     基于 K8s Ingress
//   - GatewayAPIProvider  基于 Gateway API HTTPRoute
type Provider interface {
	// Name 返回 provider 的标识名（用于日志和 status）
	// 例："Ingress" / "GatewayAPI"
	Name() string

	// Validate 检查 provider 在当前集群可用。
	// 例：GatewayAPIProvider 检查 GatewayClass CRD 是否存在。
	// 不可用时返回 *Error{Code: ErrRouteProviderNotAvailable}。
	Validate(ctx context.Context) error

	// GetCurrentRoutes 获取流量层资源（Ingress/HTTPRoute）当前的路由规则。
	//
	// 用于：
	//   - Drift 检测（比较期望路由 vs 实际路由）
	//   - 切换前的状态记录（用于 RESTORING）
	//
	// 资源不存在时返回 *Error{Code: ErrRouteResourceNotFound}。
	GetCurrentRoutes(ctx context.Context, ns, name string) ([]Route, error)

	// ApplyRoutes 原子地应用一组路由规则。
	//
	// "原子" 指：底层 K8s API 的 update 是单次操作，
	// 要么全部成功要么全部失败。多步 ApplyRoutes 之间不保证原子，
	// 由调用方（Sandbox 状态机）通过 RESTORING 状态机制保证。
	//
	// 用于 COMMITTING 阶段切流。
	// 失败时返回 *Error{Code: ErrRouteApplyFailed}。
	ApplyRoutes(ctx context.Context, ns, name string, routes []Route) error

	// SetWeight 调整指定 service 的流量权重。
	//
	// 蓝绿场景：weight 只取 0 或 100。
	// canary 场景（v2.6.1+）：weight 渐进 0 → 100。
	//
	// service 不在当前路由中时返回 *Error{Code: ErrRouteInvalid}。
	SetWeight(ctx context.Context, ns, name, service string, weight int32) error
}

// Route 流量层无关的路由规则抽象。
//
// 各 Provider 内部把它转换成具体的后端格式：
//   - Ingress: spec.rules[].http.paths[].backend.service
//   - Gateway API: HTTPRoute.spec.rules[].backendRefs
type Route struct {
	// Service 目标 K8s Service 名（如 "wallet-service-blue"）
	// 必须在同一个 namespace 内
	Service string

	// Weight 流量权重，范围 [0, 100]
	// 蓝绿：0 或 100
	// canary：渐进比例（v2.6.1+）
	Weight int32

	// Match 路径/header 匹配条件，可选
	// 蓝绿场景通常不用（整个 ingress 切流）
	// 可选地用于"按 path 切流"或"按 header 切流"
	Match *Match
}

// Match 路径或 header 匹配条件。
//
// Path / PathType 与 K8s Ingress 同义：
//   - PathType="Exact"  精确匹配
//   - PathType="Prefix" 前缀匹配
type Match struct {
	Path     string
	PathType string
	Headers  map[string]string
}

// Validate 检查 Route 字段合法性。
//
// 调用方在 ApplyRoutes 前应调用此方法预校验。
func (r *Route) Validate() error {
	if r.Service == "" {
		return NewError(code.ErrRouteInvalid, "service 名为空")
	}
	if r.Weight < 0 || r.Weight > 100 {
		return NewError(code.ErrRouteInvalid, "weight=%d 不在 [0,100]", r.Weight)
	}
	if r.Match != nil {
		switch r.Match.PathType {
		case "", "Exact", "Prefix":
			// OK
		default:
			return NewError(code.ErrRouteInvalid,
				"PathType=%q 非法（需要 Exact/Prefix）", r.Match.PathType)
		}
	}
	return nil
}

// ValidateRoutes 批量校验。
//
// 额外检查：
//   - 至少有一个 weight > 0 的 route（否则等于全切走，没有流量入口）
//   - weight 总和不超过 100（蓝绿是 0+100=100，canary 渐进过程中也应满足）
func ValidateRoutes(routes []Route) error {
	if len(routes) == 0 {
		return NewError(code.ErrRouteInvalid, "routes 为空")
	}

	totalWeight := int32(0)
	hasNonZero := false
	for i := range routes {
		if err := routes[i].Validate(); err != nil {
			// 这里要 wrap 一层带 index 信息
			return WrapError(code.ErrRouteInvalid, err, "route[%d] 校验失败", i)
		}
		totalWeight += routes[i].Weight
		if routes[i].Weight > 0 {
			hasNonZero = true
		}
	}

	if !hasNonZero {
		return NewError(code.ErrRouteInvalid, "所有 route 的 weight 都为 0")
	}
	if totalWeight > 100 {
		return NewError(code.ErrRouteInvalid, "weight 总和 %d 超过 100", totalWeight)
	}
	return nil
}
