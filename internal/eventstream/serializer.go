package eventstream

import (
	"encoding/json"
	"fmt"
	"time"
)

// ParseSkeleton 从 K8s 对象的 RawJSON 解出 Skeleton 字段。
//
// 增量序列化的核心：
//   - 不反序列化整个 K8s 对象（如 *appsv1.Deployment）
//   - 仅解出 Skeleton 字段（业务关心的子集）
//   - RawJSON 保留在 Resource 中，需要时再 Unmarshal
//
// 性能（基于 Day 1 benchmark）：
//   - ParseSkeleton: ~35μs / 4KB / 88 allocs
//   - 全量反序列化:   ~60μs / 35KB / 516 allocs
//   - 提升: 1.7x 时间, 8.7x 内存, 6x allocs
//
// 错误处理：
//   - JSON 格式错误返回 error
//   - 必要字段缺失（如 Kind）返回 error
//   - 非必要字段缺失忽略（如 Replicas 对 Service 不存在）
func ParseSkeleton(rawJSON []byte) (*Resource, error) {
	if len(rawJSON) == 0 {
		return nil, fmt.Errorf("eventstream: ParseSkeleton: empty rawJSON")
	}

	var partial struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Namespace         string            `json:"namespace"`
			Name              string            `json:"name"`
			UID               string            `json:"uid"`
			Generation        int64             `json:"generation"`
			ResourceVersion   string            `json:"resourceVersion"`
			Labels            map[string]string `json:"labels"`
			Annotations       map[string]string `json:"annotations"`
			CreationTimestamp string            `json:"creationTimestamp"`
			DeletionTimestamp *string           `json:"deletionTimestamp"`
		} `json:"metadata"`
		Spec struct {
			Replicas *int32 `json:"replicas"`
		} `json:"spec"`
		Status struct {
			Phase         string `json:"phase"`
			ReadyReplicas *int32 `json:"readyReplicas"`
		} `json:"status"`
	}

	if err := json.Unmarshal(rawJSON, &partial); err != nil {
		return nil, fmt.Errorf("eventstream: ParseSkeleton: json unmarshal: %w", err)
	}

	// 必要字段验证
	if partial.Kind == "" {
		return nil, fmt.Errorf("eventstream: ParseSkeleton: missing kind")
	}
	if partial.Metadata.Name == "" {
		return nil, fmt.Errorf("eventstream: ParseSkeleton: missing metadata.name")
	}

	// 时间戳解析（容错处理）
	creationTime := parseTime(partial.Metadata.CreationTimestamp)
	deletionTime := parseTimePtr(partial.Metadata.DeletionTimestamp)

	return &Resource{
		APIVersion:        partial.APIVersion,
		Kind:              partial.Kind,
		Namespace:         partial.Metadata.Namespace,
		Name:              partial.Metadata.Name,
		UID:               partial.Metadata.UID,
		Generation:        partial.Metadata.Generation,
		ResourceVersion:   partial.Metadata.ResourceVersion,
		Labels:            partial.Metadata.Labels,
		Annotations:       partial.Metadata.Annotations,
		Replicas:          partial.Spec.Replicas,
		Phase:             partial.Status.Phase,
		ReadyReplicas:     partial.Status.ReadyReplicas,
		CreationTimestamp: creationTime,
		DeletionTimestamp: deletionTime,
		RawJSON:           rawJSON,
	}, nil
}

// parseTime 解析 K8s 风格的 RFC3339 时间戳。
//
// 失败时返回零值 time.Time，不返回 error（容错）。
// 因为时间戳缺失或格式异常不应阻断 Cache 加载。
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseTimePtr 解析时间戳指针（K8s API 中 deletionTimestamp 可能为 null）。
func parseTimePtr(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil
	}
	return &t
}
