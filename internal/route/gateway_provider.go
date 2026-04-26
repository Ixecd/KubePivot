package route

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Ixecd/kubepivot/internal/code"
	"github.com/Ixecd/kubepivot/internal/executor"
)

// GatewayAPIProvider 基于 Gateway API HTTPRoute（gateway.networking.k8s.io/v1）
// 实现 Provider 接口。
//
// 与 IngressProvider 的差异：
//   - HTTPRoute 原生支持加权 backendRefs，蓝绿场景下两个 backend 都写入
//   - canary 场景（v2.6.1+）天然支持（只需调整权重，无需切换 backend.name）
//   - 需要集群安装 Gateway API CRD（gateway.networking.k8s.io/v1）
//
// 检测：调用 Validate() 确认集群是否可用。
type GatewayAPIProvider struct{}

// NewGatewayAPIProvider 构造一个 GatewayAPIProvider 实例。
func NewGatewayAPIProvider() *GatewayAPIProvider {
	return &GatewayAPIProvider{}
}

// Name 返回 provider 标识。
func (p *GatewayAPIProvider) Name() string {
	return "GatewayAPI"
}

// Validate 检查集群是否安装 Gateway API CRD。
//
// 实现：kubectl get crd 查找 httproutes.gateway.networking.k8s.io。
func (p *GatewayAPIProvider) Validate(ctx context.Context) error {
	e := executor.GetExecutor()
	out, err := e.Kubectl(ctx, "",
		"get", "crd", "httproutes.gateway.networking.k8s.io",
		"--ignore-not-found",
		"-o", "name",
	)
	if err != nil {
		return WrapError(code.ErrRouteProviderNotAvailable, err,
			"kubectl get crd 失败")
	}

	if len(out) == 0 || strings.TrimSpace(string(out)) == "" {
		return NewError(code.ErrRouteProviderNotAvailable,
			"集群未安装 Gateway API CRD（httproutes.gateway.networking.k8s.io）")
	}
	return nil
}

// GetCurrentRoutes 读取 HTTPRoute 当前路由规则。
//
// 实现：kubectl get httproute <name> -n <ns> -o json，
// 解析 spec.rules[].backendRefs。
func (p *GatewayAPIProvider) GetCurrentRoutes(ctx context.Context, ns, name string) ([]Route, error) {
	hr, err := p.fetchHTTPRouteJSON(ctx, ns, name)
	if err != nil {
		return nil, err
	}
	return parseHTTPRouteRoutes(hr), nil
}

// ApplyRoutes 原子地应用一组路由规则。
//
// HTTPRoute 比 Ingress 更直接——所有 routes 都写入 backendRefs，
// 包括 weight=0 的（这样切回时无需重建资源）。
func (p *GatewayAPIProvider) ApplyRoutes(ctx context.Context, ns, name string, routes []Route) error {
	if err := ValidateRoutes(routes); err != nil {
		return err
	}

	// 读取现有 HTTPRoute（保留 parentRefs / hostnames / matches 等用户字段）
	current, err := p.fetchHTTPRouteJSON(ctx, ns, name)
	if err != nil && !isNotFound(err) {
		return err
	}

	newHR := buildHTTPRouteFromRoutes(ns, name, routes, current)
	yamlOut, err := json.Marshal(newHR)
	if err != nil {
		return WrapError(code.ErrRouteApplyFailed, err,
			"序列化 HTTPRoute JSON 失败")
	}

	if err := applyViaStdin(ctx, yamlOut); err != nil {
		return WrapError(code.ErrRouteApplyFailed, err,
			"kubectl apply HTTPRoute %s/%s 失败", ns, name)
	}

	return nil
}

// SetWeight 调整指定 service 的权重。
//
// HTTPRoute 比 Ingress 灵活——任何 0-100 的 weight 都支持。
// 但 v2.6.0 蓝绿模式仍然限制为 0 或 100，canary 加权留 v2.6.1+。
func (p *GatewayAPIProvider) SetWeight(ctx context.Context, ns, name, service string, weight int32) error {
	if weight != 0 && weight != 100 {
		return NewError(code.ErrRouteInvalid,
			"v2.6.0 蓝绿模式下 weight 必须是 0 或 100（实际 %d）", weight)
	}

	current, err := p.GetCurrentRoutes(ctx, ns, name)
	if err != nil && !isNotFound(err) {
		return err
	}

	newRoutes := updateWeight(current, service, weight)
	return p.ApplyRoutes(ctx, ns, name, newRoutes)
}

// ── 私有辅助 ────────────────────────────────────────────────────────────────

func (p *GatewayAPIProvider) fetchHTTPRouteJSON(ctx context.Context, ns, name string) (*httpRouteManifest, error) {
	e := executor.GetExecutor()
	out, err := e.Kubectl(ctx, "",
		"get", "httproute.gateway.networking.k8s.io", name,
		"-n", ns,
		"-o", "json",
		"--ignore-not-found",
	)
	if err != nil {
		return nil, WrapError(code.ErrRouteApplyFailed, err,
			"kubectl get httproute 失败")
	}
	if len(out) == 0 || strings.TrimSpace(string(out)) == "" {
		return nil, NewError(code.ErrRouteResourceNotFound,
			"httproute %s/%s 不存在", ns, name)
	}

	var hr httpRouteManifest
	if err := json.Unmarshal(out, &hr); err != nil {
		return nil, WrapError(code.ErrRouteApplyFailed, err,
			"解析 httproute JSON 失败")
	}
	return &hr, nil
}

// ── HTTPRoute JSON 结构 ──────────────────────────────────────────────────────

// httpRouteManifest 是 gateway.networking.k8s.io/v1 HTTPRoute 的最小化解析结构。
type httpRouteManifest struct {
	APIVersion string         `json:"apiVersion,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Spec       httpRouteSpec  `json:"spec"`
}

type httpRouteSpec struct {
	ParentRefs []map[string]any `json:"parentRefs,omitempty"`
	Hostnames  []string         `json:"hostnames,omitempty"`
	Rules      []httpRouteRule  `json:"rules,omitempty"`
}

type httpRouteRule struct {
	Matches     []map[string]any   `json:"matches,omitempty"`
	BackendRefs []httpRouteBackend `json:"backendRefs"`
}

type httpRouteBackend struct {
	Name   string `json:"name"`
	Port   int32  `json:"port,omitempty"`
	Weight *int32 `json:"weight,omitempty"`
	// 其他字段（group / kind / namespace）保留默认（同命名空间 Service）
}

// parseHTTPRouteRoutes 把 HTTPRoute 的 spec.rules 转换成 []Route。
//
// HTTPRoute 原生支持 weight，所以直接读取 weight 字段。
// 如果 weight 字段缺失，默认 100（Gateway API 规范默认值）。
func parseHTTPRouteRoutes(hr *httpRouteManifest) []Route {
	seen := make(map[string]bool)
	routes := make([]Route, 0)

	for _, rule := range hr.Spec.Rules {
		for _, backend := range rule.BackendRefs {
			svc := backend.Name
			if svc == "" || seen[svc] {
				continue
			}
			seen[svc] = true

			weight := int32(100)
			if backend.Weight != nil {
				weight = *backend.Weight
			}

			routes = append(routes, Route{
				Service: svc,
				Weight:  weight,
			})
		}
	}
	return routes
}

// buildHTTPRouteFromRoutes 构造一个 HTTPRoute manifest 用于 kubectl apply。
//
// 保留用户字段：
//   - metadata.annotations（除 kubepivot.io/* 外）
//   - spec.parentRefs（gateway 关联）
//   - spec.hostnames
//   - spec.rules[*].matches（路径/header 匹配）
//
// 只覆盖：spec.rules[*].backendRefs（weight 切换）
func buildHTTPRouteFromRoutes(ns, name string, routes []Route, current *httpRouteManifest) httpRouteManifest {
	defaultPort := int32(80)

	// 构造 backendRefs
	backendRefs := make([]httpRouteBackend, 0, len(routes))
	for _, r := range routes {
		w := r.Weight
		backendRefs = append(backendRefs, httpRouteBackend{
			Name:   r.Service,
			Port:   defaultPort,
			Weight: &w,
		})
	}

	// 构造 rule（保留用户的 matches 字段如果存在）
	rule := httpRouteRule{
		BackendRefs: backendRefs,
	}
	if current != nil && len(current.Spec.Rules) > 0 {
		rule.Matches = current.Spec.Rules[0].Matches
	}

	result := httpRouteManifest{
		APIVersion: "gateway.networking.k8s.io/v1",
		Kind:       "HTTPRoute",
		Metadata: map[string]any{
			"name":      name,
			"namespace": ns,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "kp",
				"kubepivot.io/strategy":        "blue-green",
			},
			"annotations": map[string]any{
				"kubepivot.io/managed":        "true",
				"kubepivot.io/managed-fields": "spec.rules[*].backendRefs",
			},
		},
		Spec: httpRouteSpec{
			Rules: []httpRouteRule{rule},
		},
	}

	// 合并保护字段
	if current != nil {
		// 保留用户 annotations（除 kubepivot.io/* 外）
		if currentAnns, ok := current.Metadata["annotations"].(map[string]any); ok {
			resultAnns := result.Metadata["annotations"].(map[string]any)
			for k, v := range currentAnns {
				if !strings.HasPrefix(k, "kubepivot.io/") {
					resultAnns[k] = v
				}
			}
		}
		// 保留 parentRefs（gateway 关联）
		if len(current.Spec.ParentRefs) > 0 {
			result.Spec.ParentRefs = current.Spec.ParentRefs
		}
		// 保留 hostnames
		if len(current.Spec.Hostnames) > 0 {
			result.Spec.Hostnames = current.Spec.Hostnames
		}
	}

	return result
}
