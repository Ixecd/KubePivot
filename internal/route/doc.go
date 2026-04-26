// Package route 提供 KubePivot v2.6 的流量层抽象。
//
// 核心抽象：Provider 接口，统一封装不同流量后端的操作：
//   - IngressProvider     标准 K8s Ingress (networking.k8s.io/v1)
//   - GatewayAPIProvider  Gateway API (gateway.networking.k8s.io/v1)
//   - 未来可扩展：Linkerd / Istio / 自定义
//
// 设计原则：
//   1. 不引入 client-go——所有 K8s 操作通过 kubectl exec
//   2. Provider 自包含——不依赖 controller / state 包
//   3. 路由抽象与具体后端解耦——Route/Match 是中间表示
//   4. 自动检测 + 显式覆盖——AutoDetect 探测集群可用 provider
//
// 主要场景：v2.6 蓝绿部署的 COMMITTING 阶段调用
//
//	provider, _ := route.AutoDetect(ctx)
//	provider.ApplyRoutes(ctx, ns, name, []route.Route{
//	    {Service: "wallet-service-green", Weight: 100},
//	})
//
// 错误码：在 internal/code 包中定义（ErrRoute* 系列，110000-110099 区段）。
//
// 详见 docs/design/traffic-layer-draft.md
package route
