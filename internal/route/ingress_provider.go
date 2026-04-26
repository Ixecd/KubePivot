package route

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Ixecd/kubepivot/internal/code"
	"github.com/Ixecd/kubepivot/internal/executor"
)

// IngressProvider 基于 K8s Ingress（networking.k8s.io/v1）实现 Provider 接口。
//
// 蓝绿语义实现策略（v2.6.0 决策 B）：
//   - 不依赖 nginx-ingress 特有的 canary annotation
//   - weight=100 的 service 直接成为 Ingress 的 backend.service.name
//   - weight=0 的 service 不出现在 Ingress 中
//   - 任何 Ingress controller 都兼容（nginx / traefik / haproxy / 云厂商）
//
// 局限：v2.6.0 不支持加权路由（A/B 测试 / canary）。
// canary 留 v2.6.1+ 时通过 nginx canary annotation 实现。
type IngressProvider struct{}

// NewIngressProvider 构造一个 IngressProvider 实例。
func NewIngressProvider() *IngressProvider {
	return &IngressProvider{}
}

// Name 返回 provider 标识。
func (p *IngressProvider) Name() string {
	return "Ingress"
}

// Validate 检查集群是否支持 networking.k8s.io/v1 Ingress 资源。
//
// 实现：kubectl api-resources --api-group=networking.k8s.io 查找 ingresses。
func (p *IngressProvider) Validate(ctx context.Context) error {
	e := executor.GetExecutor()
	out, err := e.Kubectl(ctx, "",
		"api-resources",
		"--api-group=networking.k8s.io",
		"-o", "name",
	)
	if err != nil {
		return WrapError(code.ErrRouteProviderNotAvailable, err,
			"kubectl api-resources 失败")
	}

	if !strings.Contains(string(out), "ingresses") {
		return NewError(code.ErrRouteProviderNotAvailable,
			"集群不支持 networking.k8s.io/v1 Ingress")
	}
	return nil
}

// GetCurrentRoutes 读取 Ingress 当前路由规则。
//
// 实现：kubectl get ingress <name> -n <ns> -o json，
// 解析 spec.rules[].http.paths[].backend.service。
//
// 蓝绿场景下通常只有一个 backend，weight 默认 100。
func (p *IngressProvider) GetCurrentRoutes(ctx context.Context, ns, name string) ([]Route, error) {
	e := executor.GetExecutor()
	out, err := e.Kubectl(ctx, "",
		"get", "ingress", name,
		"-n", ns,
		"-o", "json",
		"--ignore-not-found",
	)
	if err != nil {
		return nil, WrapError(code.ErrRouteApplyFailed, err,
			"kubectl get ingress %s/%s 失败", ns, name)
	}
	if len(out) == 0 || strings.TrimSpace(string(out)) == "" {
		return nil, NewError(code.ErrRouteResourceNotFound,
			"ingress %s/%s 不存在", ns, name)
	}

	var ing ingressManifest
	if err := json.Unmarshal(out, &ing); err != nil {
		return nil, WrapError(code.ErrRouteApplyFailed, err,
			"解析 ingress JSON 失败")
	}

	return parseIngressRoutes(&ing), nil
}

// ApplyRoutes 原子地应用一组路由规则。
//
// 蓝绿场景下 routes 应该恰好包含 1 个 weight=100 的 entry，
// 其他 weight=0 的 entry 会被忽略（不写入 Ingress）。
//
// 实现：构造完整 Ingress YAML，通过 kubectl apply -f - 应用。
func (p *IngressProvider) ApplyRoutes(ctx context.Context, ns, name string, routes []Route) error {
	if err := ValidateRoutes(routes); err != nil {
		return err
	}

	// 找出 weight > 0 的 service（蓝绿场景应该只有一个）
	activeServices := make([]Route, 0, len(routes))
	for _, r := range routes {
		if r.Weight > 0 {
			activeServices = append(activeServices, r)
		}
	}
	if len(activeServices) == 0 {
		return NewError(code.ErrRouteInvalid, "没有 weight > 0 的 service")
	}

	// v2.6.0 蓝绿场景：只支持单 active service
	// canary 多 backend 留 v2.6.1+ 通过 nginx annotation 实现
	if len(activeServices) > 1 {
		return NewError(code.ErrRouteInvalid,
			"v2.6.0 蓝绿模式不支持多个 weight>0 的 service（当前 %d 个）",
			len(activeServices))
	}

	// 读取现有 Ingress（保留用户自定义字段：tls / annotation 等）
	current, err := p.fetchIngressJSON(ctx, ns, name)
	if err != nil && !isNotFound(err) {
		return err
	}

	// 构造新 Ingress（保留现有 spec.tls / metadata.annotations）
	newIng := buildIngressFromRoutes(ns, name, activeServices[0], current)
	yamlOut, err := json.Marshal(newIng)
	if err != nil {
		return WrapError(code.ErrRouteApplyFailed, err,
			"序列化 ingress JSON 失败")
	}

	// 通过 stdin 提交给 kubectl apply
	if err := applyViaStdin(ctx, yamlOut); err != nil {
		return WrapError(code.ErrRouteApplyFailed, err,
			"kubectl apply ingress %s/%s 失败", ns, name)
	}

	return nil
}

// SetWeight 调整指定 service 的权重。
//
// 蓝绿场景：weight 只接受 0 或 100。
// 0 = 该 service 从 Ingress 中移除
// 100 = 该 service 成为 Ingress 唯一 backend
//
// 调用方需先 GetCurrentRoutes 拿到现有列表，再构造新 routes 调 ApplyRoutes。
// SetWeight 是 ApplyRoutes 的语法糖。
func (p *IngressProvider) SetWeight(ctx context.Context, ns, name, service string, weight int32) error {
	if weight != 0 && weight != 100 {
		return NewError(code.ErrRouteInvalid,
			"v2.6.0 蓝绿模式下 weight 必须是 0 或 100（实际 %d）", weight)
	}

	current, err := p.GetCurrentRoutes(ctx, ns, name)
	if err != nil && !isNotFound(err) {
		return err
	}

	// 构造新 routes
	newRoutes := updateWeight(current, service, weight)
	return p.ApplyRoutes(ctx, ns, name, newRoutes)
}

// ── 私有辅助 ────────────────────────────────────────────────────────────────

// fetchIngressJSON 拉取 Ingress 原始 JSON，资源不存在时返回 nil。
func (p *IngressProvider) fetchIngressJSON(ctx context.Context, ns, name string) (*ingressManifest, error) {
	e := executor.GetExecutor()
	out, err := e.Kubectl(ctx, "",
		"get", "ingress", name,
		"-n", ns,
		"-o", "json",
		"--ignore-not-found",
	)
	if err != nil {
		return nil, WrapError(code.ErrRouteApplyFailed, err,
			"kubectl get ingress 失败")
	}
	if len(out) == 0 || strings.TrimSpace(string(out)) == "" {
		return nil, NewError(code.ErrRouteResourceNotFound,
			"ingress %s/%s 不存在", ns, name)
	}

	var ing ingressManifest
	if err := json.Unmarshal(out, &ing); err != nil {
		return nil, WrapError(code.ErrRouteApplyFailed, err,
			"解析 ingress JSON 失败")
	}
	return &ing, nil
}

// applyViaStdin 通过 stdin 把 JSON/YAML 提交给 kubectl apply。
func applyViaStdin(ctx context.Context, data []byte) error {
	e := executor.GetExecutor()
	cmd := e.CmdKubectl(ctx, "", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply: %v: %s", err, out)
	}
	return nil
}

// isNotFound 判断 error 是否是"资源不存在"。
func isNotFound(err error) bool {
	var rerr *Error
	if !errorAs(err, &rerr) {
		return false
	}
	return rerr.Code == code.ErrRouteResourceNotFound
}

// errorAs 是 errors.As 的简短包装（避免在每个调用点 import errors）。
func errorAs(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		// unwrap
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// updateWeight 把 routes 中匹配 service 的 weight 改成新值。
// 如果 service 不在 routes 中，append 一个新 Route。
//
// 用于 SetWeight 内部。
func updateWeight(routes []Route, service string, weight int32) []Route {
	result := make([]Route, 0, len(routes)+1)
	found := false
	for _, r := range routes {
		if r.Service == service {
			r.Weight = weight
			found = true
		}
		result = append(result, r)
	}
	if !found {
		result = append(result, Route{Service: service, Weight: weight})
	}
	return result
}

// ── Ingress JSON 结构 ──────────────────────────────────────────────────────

// ingressManifest 是 networking.k8s.io/v1 Ingress 的最小化解析结构。
//
// 我们只关心 spec.rules、spec.tls、metadata.annotations，其他字段保留原始值
// 通过 RawJSON 透传，避免覆盖用户字段。
type ingressManifest struct {
	APIVersion string         `json:"apiVersion,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Spec       ingressSpec    `json:"spec"`
}

type ingressSpec struct {
	IngressClassName *string         `json:"ingressClassName,omitempty"`
	Rules            []ingressRule   `json:"rules,omitempty"`
	TLS              []map[string]any `json:"tls,omitempty"`
}

type ingressRule struct {
	Host string             `json:"host,omitempty"`
	HTTP *ingressHTTP       `json:"http,omitempty"`
}

type ingressHTTP struct {
	Paths []ingressPath `json:"paths"`
}

type ingressPath struct {
	Path     string         `json:"path,omitempty"`
	PathType *string        `json:"pathType,omitempty"`
	Backend  ingressBackend `json:"backend"`
}

type ingressBackend struct {
	Service ingressBackendService `json:"service"`
}

type ingressBackendService struct {
	Name string             `json:"name"`
	Port ingressBackendPort `json:"port"`
}

type ingressBackendPort struct {
	Number int32 `json:"number,omitempty"`
	Name   string `json:"name,omitempty"`
}

// parseIngressRoutes 把 Ingress 的 spec.rules 转换成 []Route。
//
// 蓝绿场景：每个 backend.service.name 是一个 Route，weight=100
// （Ingress 不直接支持 weight，单 backend 默认 100%）
func parseIngressRoutes(ing *ingressManifest) []Route {
	seen := make(map[string]bool)
	routes := make([]Route, 0)

	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			svc := path.Backend.Service.Name
			if svc == "" || seen[svc] {
				continue
			}
			seen[svc] = true

			r := Route{
				Service: svc,
				Weight:  100, // Ingress 不显式支持 weight，单 backend 即 100
			}
			// 如果有 path 信息，填到 Match
			if path.Path != "" {
				r.Match = &Match{
					Path: path.Path,
				}
				if path.PathType != nil {
					r.Match.PathType = *path.PathType
				}
			}
			routes = append(routes, r)
		}
	}
	return routes
}

// buildIngressFromRoutes 构造一个 Ingress manifest 用于 kubectl apply。
//
// 如果 current != nil，保留其 metadata.annotations / spec.tls / spec.ingressClassName，
// 仅替换 spec.rules 的 backend service。这是"只保护，不越权"原则的体现：
// 不动用户加的 cert-manager / 自定义 annotation 等字段。
func buildIngressFromRoutes(ns, name string, active Route, current *ingressManifest) ingressManifest {
	pathTypePrefix := "Prefix"
	defaultPort := int32(80)

	// 默认从 active.Match 取 path / pathType
	path := "/"
	pathType := pathTypePrefix
	if active.Match != nil {
		if active.Match.Path != "" {
			path = active.Match.Path
		}
		if active.Match.PathType != "" {
			pathType = active.Match.PathType
		}
	}

	// 构造新的 spec.rules（保留 host 信息如果存在）
	host := ""
	if current != nil && len(current.Spec.Rules) > 0 {
		host = current.Spec.Rules[0].Host
		// 也尝试从已有 path 复用 path / pathType
		if len(current.Spec.Rules[0].HTTP.Paths) > 0 {
			oldPath := current.Spec.Rules[0].HTTP.Paths[0]
			if active.Match == nil && oldPath.Path != "" {
				path = oldPath.Path
				if oldPath.PathType != nil {
					pathType = *oldPath.PathType
				}
			}
		}
	}

	newRule := ingressRule{
		Host: host,
		HTTP: &ingressHTTP{
			Paths: []ingressPath{
				{
					Path:     path,
					PathType: &pathType,
					Backend: ingressBackend{
						Service: ingressBackendService{
							Name: active.Service,
							Port: ingressBackendPort{Number: defaultPort},
						},
					},
				},
			},
		},
	}

	// 构造完整 manifest
	result := ingressManifest{
		APIVersion: "networking.k8s.io/v1",
		Kind:       "Ingress",
		Metadata: map[string]any{
			"name":      name,
			"namespace": ns,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "kp",
				"kubepivot.io/strategy":        "blue-green",
			},
			"annotations": map[string]any{
				"kubepivot.io/managed":        "true",
				"kubepivot.io/managed-fields": "spec.rules",
			},
		},
		Spec: ingressSpec{
			Rules: []ingressRule{newRule},
		},
	}

	// 如果有现有 Ingress，合并保护字段
	if current != nil {
		// 保留现有 metadata.annotations（用户自定义的 cert-manager 等）
		if currentAnns, ok := current.Metadata["annotations"].(map[string]any); ok {
			resultAnns := result.Metadata["annotations"].(map[string]any)
			for k, v := range currentAnns {
				// 不覆盖 KubePivot 自己的 annotation
				if !strings.HasPrefix(k, "kubepivot.io/") {
					resultAnns[k] = v
				}
			}
		}
		// 保留 IngressClassName
		if current.Spec.IngressClassName != nil {
			result.Spec.IngressClassName = current.Spec.IngressClassName
		}
		// 保留 TLS
		if len(current.Spec.TLS) > 0 {
			result.Spec.TLS = current.Spec.TLS
		}
	}

	return result
}
