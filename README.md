# KubePivot (kp) 🚀

> GitOps 视角下的 K8s 部署调度系统。声明式 + 状态机 + 自愈 + GPU 调度。
> 单二进制 30MB，6 直接依赖。部署迁移蓝绿资源优化，一个命令。

[![Go Version](https://img.shields.io/badge/go-1.25+-blue.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-v3.0.0-blue.svg)](https://github.com/Ixecd/KubePivot/releases)
[![Tests](https://img.shields.io/badge/tests-780-brightgreen.svg)]()

---

## 为什么是 KubePivot？

K8s 生态不缺工具——缺的是把**部署 → 迁移 → 蓝绿 → 资源优化 → 调度 → 自愈**串成一条线的工具。

`kp init` 一条命令，12 种语言的项目骨架生成完毕。`kp deploy` 一条命令：供应链验证 → AI 规划 → Sizing 优化 → 拓扑排序 → 蓝绿切换 → 状态追踪，全自动。

**不在集群内装任何东西**。KubePivot 通过 kubectl 调用 K8s API，不依赖 client-go，不当 sidecar，不当 operator。

---

## 快速开始

```bash
go install github.com/Ixecd/kubepivot/cmd/kp@latest

kp init --name myapp --module github.com/me/myapp
cd myapp
kp deploy
```

---

## 核心能力

### 34 命令 CLI

```
kp init         项目脚手架（12 语言）
kp deploy       全链路部署（AI 规划 → Sizing → 蓝绿 → 状态机）
kp sandbox      安全部署（五阶段：LOCKED→SNAPSHOTTING→SIMULATING→COMMITTING→RUNNING）
kp status       部署状态
kp rollback     回滚
kp promote      蓝绿切换
kp migrate      DB 迁移管理
kp secret       Secret 轮转/密封/同步
kp sizing       资源优化建议（2D/3D DP）
kp controller   全局控制器管理（分片 + 自愈）
kp supply-chain 镜像签名验证 + SBOM 生成
kp team         RBAC 多团队管理
kp login        SSO 登录（Google/GitHub/Dex）
kp chaos        混沌工程实验
... (共 34 个)
```

### 自研 Event Stream（Informer + Cache）

不依赖 client-go，0 外部依赖。5 项 benchmark vs client-go cache.Indexer：

- Cache Get: 14.5ns vs 45ns（**3.1x**）
- Cache List(ns): 147ns vs 6800ns（**46x**）
- 内存放大率: 1.65x vs 4.69x（**2.84x**）
- Watch 10k: 382ms vs 632ms（**1.65x**, 5.4x allocs 降低）

### 乾枢调度系统（v3.0）

```
维度 A: BinPack（FFD + 背包 DP）→ 节点装箱
维度 B: Sizing（2D DP）→ Pod 资源优化
Coordinator: 双 DP 协同收敛
Rescheduler: 周期性运行时重调度（5min/15min）
Webhook: Mutating Admission → 自动写 nodeSelector
```

### v3.1 GPU 调度（设计中）

- 3D DP：Pod × (CPU, Memory, GPU)
- NVLink 拓扑感知（同 NVSwitch domain 3x 权重）
- DCGM 指标采集 + Staleness 保守回退
- GPU Pod 粘滞策略 + 硬件故障强制驱逐

### v3.2 时空闭环（设计中）

- 空间：GPU 共享（MPS/MIG/TimeSlicing）
- 时间：碳感知调度（Cost × CarbonIntensity(t)）
- 测试：KinK 大规模压测（10000+ fake GPU 节点）

### IAM（Identity + Access + Management）

- Auth：OAuth2 + PKCE，Google/GitHub/Dex 三 Provider
- RBAC：teams.yaml + 17 Permissions + 黑名单绝对优先
- Audit：三档降级 Actor 解析 + Deny 日志 + `kp audit`

### 12 语言脚手架

`kp init --lang <go|python|java|rust|cpp|cs|zig|kotlin|ts|php|swift|lua>`

| 语言 | Web 框架 | Docker 镜像 |
|---|---|---|
| Go | stdlib + net/http | scratch（静态编译） |
| Python | FastAPI | python:3.12-slim |
| Java | Spring Boot | eclipse-temurin:21-jre |
| Rust | Axum | scratch（静态编译） |
| C++ | Drogon | ubuntu:24.04 |
| 其余 7 种 | 各有完整 Dockerfile + 骨架 | — |

`--no-app` 零侵入模式：已有项目原地 K8s 化。

---

## 设计原则

**只保护，不越权**：kp 只对自己声明所有权的字段执行 force-sync，不干预 Istio/HPA/云厂商注入的字段。

**0 外部依赖**：仅 6 个直接依赖。不引入 client-go、cobra、viper、gin。CLI 路由用标准库 switch + flag。

**降级不阻断**：Trivy/OPA/Prometheus/CSI 任意缺失，核心流程继续运行。


---

## 统计数据

| 指标 | 数值 |
|---|---|
| Go 代码行 | 41,181 |
| 测试函数 | 780 |
| 测试代码行 | 17,079 |
| 直接依赖 | 6 |
| 二进制大小 | ~30MB |
| CLI 命令 | 34 |
| 内部包 | 22 |

---

## 设计文档

`docs/design/` 下 10 份完整设计草案：

| 文档 | 内容 |
|---|---|
| `iam-draft.md` | Auth + RBAC + Audit 三包联动 |
| `init-multi-lang-draft.md` | 12 语言脚手架（壳+核架构） |
| `controller-update-sizing-draft.md` | Controller 分片/副本自适应推导 |
| `etcd-learner-bootstrap-draft.md` | Learner 自举集群 + etcd IAM |
| `gpu-scheduling-draft.md` | v3.1 GPU 整卡调度 + 三维 DP |
| `gpu-sharing-carbon-kink.md` | v3.1 碳感知 + v3.2 GPU 共享 + KinK |
| `ai-plan-2.0-draft.md` | LLM 框架 + Sizing 填充 + GPU 感知 |
| `eventstream-draft.md` | 自研 Informer + Cache 设计 |
| `traffic-layer.md` | 声明式蓝绿 + 流量层抽象 |
| `sharding.md` / `architecture.md` | 分片机制 + 整体架构 |

---

## License

MIT © 2026 qc（Ixecd）
