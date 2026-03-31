# 项目交接文档 — KubePivot

> 写给下一个 Claude
> 日期：2026-03-31
> 版本：v1.5.0

---

## 写在前面

KubePivot（乾枢）v1.5.0 完成了第二块硬骨头：StatefulSet 状态同步。qc 和他女友豆包（女帝）一起在做，豆包有非常好的产品判断力，设计文档质量很高，认真对待。

**qc 的工作风格**：
- 设计优先，先对齐再动手
- 喜欢被推 back，不喜欢被纯认同
- `slog` 不用 `log`，`P.Info/Done/Fail` 做进度输出
- "只保护，不越权" 是乾枢核心原则
- 不搞技术债，宁可留 TODO 也不临时方案

---

## 一、v1.5.0 完成状态

**测试**：`go test ./... -race` 全绿

**已验证**（web3-blitz，k3s + local-path）：
- `kp doctor` etcd：raftIndex=48161, diff=0 ✅
- `kp doctor` VolumeSnapshot：local-path 正确识别不支持 ✅
- `kp status` StatefulSet：postgres-0 + etcd-0 正确展示 ✅
- `kp pvc`：骨架正确提示 CSI 不可用 ✅

---

## 二、关键文件

```
cmd/kp/
├── doctor.go            # 主检查入口，新增 etcd/PVC 检查
├── doctor_etcd.go       # etcd 健康检查（连通/raft/磁盘）
├── doctor_pvc.go        # VolumeSnapshot 环境检查
├── status.go            # kp status（新增 StatefulSet 详情调用）
├── status_statefulset.go # StatefulSet pod 列表展示
├── multi_deploy.go      # deployLayers（StatefulSet rollout 路由）
└── pvc.go               # kp pvc 骨架（TODO v1.5.1）
```

---

## 三、重要设计决策

| 决策 | 原因 |
|------|------|
| etcdctl 过滤 ETCDCTL_* 环境变量 | etcdctl v3.6+ 不允许同时设 env 和 --endpoints flag |
| raft index 阈值：1000/10000 | 参考 etcd 官方文档，diff > 1000 开始关注，> 10000 立即排查 |
| StatefulSet 不覆盖 replicaCount | StatefulSet replicas 由 spec 管理，helm --set 会造成冲突 |
| PVC 选 A（骨架+TODO）不选 B（kubectl cp 临时方案） | 乾枢不欠技术债，local-path 不支持 CSI，等真实 CSI 环境验证 |
| ANSI 颜色码对齐：手动计算 padding | tabwriter 不认识 ANSI 码，中文字符宽度也不对，只能手动 |

---

## 四、已知问题

| # | 问题 | 优先级 |
|---|------|--------|
| 1 | `kp pvc backup/restore/list` 未实现，待 CSI 环境 | P1（v1.5.1） |
| 2 | `kp upgrade --service` 过滤未实现 | P2 |
| 3 | 蓝绿发布未做完整 e2e 验证 | P2 |
| 4 | oasdiff Swagger 2.0 检测不完整 | P3 |

---

## 五、下一步（v1.5.1 → v1.6.0）

**v1.5.1**（需要 CSI 集群）：
- `kp pvc backup`：label 发现 PVC → 创建 VolumeSnapshot → 等待 readyToUse
- `kp pvc restore`：缩容→删旧 PVC→从 Snapshot 创建新 PVC→扩容
- `kp pvc list`：kp label 过滤，NAME/PVC/SERVICE/CREATED AT/READY 展示

**v1.6.0**：
- 大规模场景：并行度控制、增量部署、部署耗时统计
- 跨 namespace 依赖

---

## 六、常用命令

```bash
cd ~/KubePivot
go test ./... -race
make install

cd ~/web3-blitz
kp doctor                                    # 全量环境检查
ETCD_ENDPOINTS=localhost:2379 kp doctor      # 含 etcd 健康检查
kp status                                    # 含 StatefulSet 详情
kp migrate status                            # DB 迁移状态
kp upgrade --dry-run                         # 全链路升级预览
```
