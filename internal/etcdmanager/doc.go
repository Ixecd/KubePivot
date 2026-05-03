// Package etcdmanager 管理 KubePivot Controller 内嵌 etcd Learner 集群。
//
// 三层架构：
//   - Bootstrap: Pod-0 冷启动自举 + Pod-N Learner 加入
//   - Manager:   运行时 compact/defrag/优雅退出
//   - Status:    Shadow CRD KubePivotStatus 状态投影
//
// 设计原则：
//   - Sidecar 模式：etcd 作为独立容器跑在 Pod 内，kp 通过 localhost:2379 调它
//   - 零外部依赖：不依赖 etcd-operator/CRD controller/Webhook
//   - 自举确定性：StatefulSet OrderedReady + K8s API label 动态 Peer Discovery
//   - Learner 零风险：新节点追平数据后才 promote，不拖慢 Quorum
//
// 详见 docs/design/etcd-learner-bootstrap-draft.md
package etcdmanager
