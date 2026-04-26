package route

import (
	"context"

	"github.com/Ixecd/kubepivot/internal/code"
)

// AutoDetect 自动探测当前集群可用的流量层 provider。
//
// 优先级：Gateway API > Ingress
//   - Gateway API 是 K8s 流量层的未来标准（v1 GA 2023-10）
//   - Ingress 是普及最广的兜底
//
// 检测流程：
//  1. 尝试 GatewayAPIProvider.Validate()，成功返回
//  2. 尝试 IngressProvider.Validate()，成功返回
//  3. 都失败返回 ErrRouteAutoDetectFailed
//
// resources.yaml 里 traffic.kind 未指定时调用此函数。
func AutoDetect(ctx context.Context) (Provider, error) {
	// 优先 Gateway API
	gw := NewGatewayAPIProvider()
	if err := gw.Validate(ctx); err == nil {
		return gw, nil
	}

	// fallback Ingress
	ing := NewIngressProvider()
	if err := ing.Validate(ctx); err == nil {
		return ing, nil
	}

	return nil, NewError(code.ErrRouteAutoDetectFailed,
		"集群既未安装 Gateway API CRD 也不支持 networking.k8s.io/v1 Ingress")
}

// NewProvider 根据 kind 显式构造 provider。
//
// resources.yaml 里 traffic.kind 字段使用此函数：
//
//	traffic:
//	  kind: Gateway      → NewProvider("Gateway")  → GatewayAPIProvider
//	  kind: Ingress      → NewProvider("Ingress")  → IngressProvider
//	  （未设置）          → AutoDetect()
//
// kind 取值：
//   - "Ingress"        K8s Ingress
//   - "Gateway"        Gateway API HTTPRoute
//   - 其他             返回 ErrRouteProviderNotAvailable
//
// 注意：本函数不调用 Validate——构造时不连集群。
// 调用方应先 NewProvider() 再 Provider.Validate(ctx) 确认可用。
func NewProvider(kind string) (Provider, error) {
	switch kind {
	case "Ingress":
		return NewIngressProvider(), nil
	case "Gateway", "GatewayAPI":
		return NewGatewayAPIProvider(), nil
	default:
		return nil, NewError(code.ErrRouteProviderNotAvailable,
			"未知的 traffic.kind: %q（支持 Ingress / Gateway）", kind)
	}
}

// ProviderForKind 是 NewProvider 的语法糖，专门用于"未指定 kind 时自动检测"场景。
//
// kind 为空字符串 → AutoDetect
// kind 非空       → NewProvider(kind)
//
// 调用方在 resources.yaml 解析后调用：
//
//	traffic := config.Traffic
//	provider, err := route.ProviderForKind(ctx, traffic.Kind)
//	if err != nil { return err }
//	provider.ApplyRoutes(...)
func ProviderForKind(ctx context.Context, kind string) (Provider, error) {
	if kind == "" {
		return AutoDetect(ctx)
	}
	p, err := NewProvider(kind)
	if err != nil {
		return nil, err
	}
	if err := p.Validate(ctx); err != nil {
		return nil, err
	}
	return p, nil
}
