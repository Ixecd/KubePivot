// Package eventstreamench 提供 v2.7 Event Stream 的基准测试。
//
// 关键约定：
//   - 必须设置 GOGC=200, GOMEMLIMIT=4GiB, GOMAXPROCS=4
//   - 仅使用 encoding/json 标准库（公平基准）
//   - 每个 bench 用 b.StopTimer()/b.StartTimer() 控制冷启动
package eventstreamench

import (
	"encoding/json"
	"fmt"
)

// generateComplexDeployment 生成"接近真实生产"的 Deployment。
//
// 包含：
//   - 4 个容器（带 envFrom / resources / probes / volumeMounts）
//   - 20 个 labels / 15 个 annotations
//   - 3 个 volumes
//   - 完整 status（含 conditions）
//
// 单个对象 RawJSON 大小：~5-8KB
// 1w 个总大小：~50-80MB
func generateComplexDeployment(idx int) []byte {
	deploy := map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":              fmt.Sprintf("test-deploy-%d", idx),
			"namespace":         fmt.Sprintf("ns-%d", idx%50), // 50 个 ns 分散
			"uid":               fmt.Sprintf("uid-%d-abcdef", idx),
			"generation":        int64(idx % 10),
			"resourceVersion":   fmt.Sprintf("%d", idx*1000+12345),
			"creationTimestamp": "2026-04-27T09:00:00Z",
			"labels":            generateLabels(20),
			"annotations":       generateAnnotations(15),
			"ownerReferences":   generateOwnerRefs(2),
		},
		"spec": map[string]interface{}{
			"replicas": int32(3),
			"selector": map[string]interface{}{
				"matchLabels": map[string]string{
					"app":     fmt.Sprintf("app-%d", idx),
					"version": "v1",
				},
			},
			"strategy": map[string]interface{}{
				"type": "RollingUpdate",
				"rollingUpdate": map[string]interface{}{
					"maxSurge":       "25%",
					"maxUnavailable": "25%",
				},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": generateLabels(10),
				},
				"spec": map[string]interface{}{
					"containers":       generateContainers(4),
					"volumes":          generateVolumes(3),
					"serviceAccountName": fmt.Sprintf("sa-%d", idx%20),
					"restartPolicy":    "Always",
					"dnsPolicy":        "ClusterFirst",
					"affinity":         generateAffinity(),
				},
			},
		},
		"status": map[string]interface{}{
			"replicas":            int32(3),
			"readyReplicas":       int32(3),
			"availableReplicas":   int32(3),
			"updatedReplicas":     int32(3),
			"observedGeneration":  int64(idx % 10),
			"phase":               "Running",
			"conditions":          generateConditions(5),
		},
	}
	raw, _ := json.Marshal(deploy)
	return raw
}

// ─── 辅助函数 ─────────────────────────────────────────────────

func generateLabels(n int) map[string]string {
	labels := make(map[string]string, n)
	for i := 0; i < n; i++ {
		labels[fmt.Sprintf("label-key-%d", i)] = fmt.Sprintf("label-value-%d", i)
	}
	return labels
}

func generateAnnotations(n int) map[string]string {
	ann := make(map[string]string, n)
	for i := 0; i < n; i++ {
		ann[fmt.Sprintf("annotation-key-%d", i)] = fmt.Sprintf("annotation-value-with-some-content-%d", i)
	}
	return ann
}

func generateContainers(n int) []map[string]interface{} {
	containers := make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		containers[i] = map[string]interface{}{
			"name":            fmt.Sprintf("container-%d", i),
			"image":           fmt.Sprintf("registry.example.com/app:v1.%d.0", i),
			"imagePullPolicy": "IfNotPresent",
			"ports": []map[string]interface{}{
				{"name": "http", "containerPort": 8080 + i, "protocol": "TCP"},
				{"name": "metrics", "containerPort": 9090 + i, "protocol": "TCP"},
			},
			"env": []map[string]interface{}{
				{"name": "ENV_VAR_1", "value": "value-1"},
				{"name": "ENV_VAR_2", "value": "value-2"},
				{"name": "ENV_VAR_3", "value": "value-3"},
			},
			"envFrom": []map[string]interface{}{
				{"configMapRef": map[string]interface{}{"name": "config-cm"}},
				{"secretRef": map[string]interface{}{"name": "config-secret"}},
			},
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"cpu":    "100m",
					"memory": "128Mi",
				},
				"limits": map[string]interface{}{
					"cpu":    "500m",
					"memory": "512Mi",
				},
			},
			"volumeMounts": []map[string]interface{}{
				{"name": "config-volume", "mountPath": "/etc/config"},
				{"name": "data-volume", "mountPath": "/var/data"},
			},
			"livenessProbe": map[string]interface{}{
				"httpGet":             map[string]interface{}{"path": "/healthz", "port": 8080 + i},
				"initialDelaySeconds": 30,
				"periodSeconds":       10,
			},
			"readinessProbe": map[string]interface{}{
				"httpGet":             map[string]interface{}{"path": "/ready", "port": 8080 + i},
				"initialDelaySeconds": 5,
				"periodSeconds":       5,
			},
		}
	}
	return containers
}

func generateVolumes(n int) []map[string]interface{} {
	volumes := make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		volumes[i] = map[string]interface{}{
			"name": fmt.Sprintf("volume-%d", i),
			"configMap": map[string]interface{}{
				"name": fmt.Sprintf("cm-%d", i),
			},
		}
	}
	return volumes
}

func generateOwnerRefs(n int) []map[string]interface{} {
	refs := make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		refs[i] = map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "ReplicaSet",
			"name":       fmt.Sprintf("rs-%d", i),
			"uid":        fmt.Sprintf("rs-uid-%d", i),
			"controller": true,
		}
	}
	return refs
}

func generateAffinity() map[string]interface{} {
	return map[string]interface{}{
		"podAntiAffinity": map[string]interface{}{
			"preferredDuringSchedulingIgnoredDuringExecution": []map[string]interface{}{
				{
					"weight": 100,
					"podAffinityTerm": map[string]interface{}{
						"labelSelector": map[string]interface{}{
							"matchExpressions": []map[string]interface{}{
								{"key": "app", "operator": "In", "values": []string{"my-app"}},
							},
						},
						"topologyKey": "kubernetes.io/hostname",
					},
				},
			},
		},
	}
}

func generateConditions(n int) []map[string]interface{} {
	conditions := make([]map[string]interface{}, n)
	conditionTypes := []string{"Available", "Progressing", "ReplicaFailure", "Initialized", "Ready"}
	for i := 0; i < n; i++ {
		conditions[i] = map[string]interface{}{
			"type":               conditionTypes[i%len(conditionTypes)],
			"status":             "True",
			"lastUpdateTime":     "2026-04-27T09:00:00Z",
			"lastTransitionTime": "2026-04-27T09:00:00Z",
			"reason":             "MinimumReplicasAvailable",
			"message":            "Deployment has minimum availability.",
		}
	}
	return conditions
}
