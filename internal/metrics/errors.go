package metrics

import "errors"

// ErrNotFound 资源不存在（Pod / Node 在 K8s 中查不到）。
//
// 调用方应区分此错误与"网络/解析错误"做不同处理：
//   - ErrNotFound: 业务逻辑分支（资源已删除等）
//   - 其他 error: 重试或告警
//
// 用法：
//
//	_, err := client.GetPodMetrics(ctx, ns, name)
//	if errors.Is(err, metrics.ErrNotFound) {
//	    // 处理资源不存在
//	}
var ErrNotFound = errors.New("metrics: resource not found")
