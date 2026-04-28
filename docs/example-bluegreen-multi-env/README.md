# KubePivot v2.6.1 多环境流量传播 demo

> 在 `docs/example-bluegreen` 单 env 蓝绿基础上,演示 v2.6.1 多环境
> 流量传播链: staging 验证后的 traffic 配置传播到 prod, 不需要手动重复
> 配置.

---

## 这个 demo 演示什么

**v2.6.1 多环境流量传播的完整链路**:
- 在 staging env 部署蓝绿应用 + sandbox start 切换流量
- 等 staging 稳定 ≥5min, controller 自动写出 verified-traffic ConfigMap
- 在 prod env 用 `kp sandbox start --from-env staging` 部署
- prod 自动复用 staging 已验证的 traffic 配置, 不需要手动重写

**v2.6.1 的核心价值**: 多环境推广不再是"在每个 env 重复写 resources.yaml",
而是 "staging 跑通的配置自动传播到下游 env".

---

## 三方协作链 (设计核心)

```
┌────────────────────────────────────────────────────────────────┐
│ Staging env                                                    │
│                                                                │
│  Step 1: kp sandbox start                                      │
│    ↓ (LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING)         │
│  Step 2: runBlueGreenSwitch (蓝绿切换)                         │
│    ↓                                                           │
│  Step 3: RUNNING + 等 5min 稳态                                │
│    ↓                                                           │
│  Step 4: Controller verifiedTrafficWriter (周期 1min 扫描)     │
│    ↓ 把 K8s 实际 traffic 状态写入 ConfigMap                    │
│  ┌──────────────────────────────────────────────┐              │
│  │ kubepivot-verified-traffic ConfigMap         │              │
│  │   data:                                       │              │
│  │     traffic.yaml: <K8s 实际路由>              │              │
│  │     source.yaml:  <project/version/since>    │              │
│  └──────────────────────────────────────────────┘              │
└─────────────────────────┬──────────────────────────────────────┘
                          │
                          │ kp sandbox start --from-env staging
                          │ (跨集群 / 跨 namespace 读 ConfigMap)
                          ▼
┌────────────────────────────────────────────────────────────────┐
│ Prod env                                                       │
│                                                                │
│  Step 5: kp 进程 (搬运工)                                      │
│    - 加载 KPEnv staging 配置                                   │
│    - kubectl get configmap kubepivot-verified-traffic          │
│      (在 staging env 上)                                       │
│    - yaml.Unmarshal traffic.yaml                              │
│    ↓                                                           │
│  Step 6: 注入到 runBlueGreenSwitch override 参数               │
│    - prod resources.yaml 必须自身声明 traffic (Q12 fail-fast)  │
│    - override 覆盖 prod traffic.routes                         │
│    ↓                                                           │
│  Step 7: prod ApplyRoutes → 跟 staging 一致的流量切换          │
│  Step 8: prod RUNNING                                          │
└────────────────────────────────────────────────────────────────┘

设计要点:
- 三方都不知道彼此存在 (单一职责解耦)
- ConfigMap 是 K8s 原生状态广播 (不引入新基础设施)
- 任何 RUNNING + 5min 稳态的 env 都能作为传播源 (级联可任意延伸)
```

---

## 前置条件

**比单 env 蓝绿 demo 多 4 项**:

1. K8s 集群 + Ingress controller (跟 example-bluegreen 一致)
2. KubePivot v2.6.1+ (`kp version` 检查)
3. **KubePivot Controller 已部署到集群**:
   ```bash
   kubectl get pods -n kubepivot-system -l app=kubepivot-controller
   # 应该有 controller pod 在 Running
   ```
   未部署时按 `docs/controller-installation.md` 安装.
4. **两个 KPEnv 配置** (一集群双 namespace 演示, 多集群路径同理):
   ```bash
   kp context add --name staging \
     --namespace kp-demo-bluegreen-staging
   kp context add --name prod \
     --namespace kp-demo-bluegreen-prod
   kp context list
   ```

---

## 文件结构

```
docs/example-bluegreen-multi-env/
├── README.md                  本文件
├── Makefile                   双 env 命令
├── resources-staging.yaml     staging 蓝绿配置 (跟 example-bluegreen 一致)
├── resources-prod.yaml        prod 也声明 traffic (Q12 要求)
├── chart/                     共享 Helm chart (复制自 example-bluegreen)
│                              ↑ 首次使用前需手动 cp, 见下方 "Chart 准备"
└── scripts/
    ├── setup-staging.sh       staging: helm install + kp enroll + sandbox start
    ├── propagate-to-prod.sh   prod: helm install + kp sandbox start --from-env staging
    ├── verify-both.sh         检查两 env traffic 状态
    └── cleanup.sh             清理两 env
```

### Chart 准备 (首次)

chart 目录共享自 `docs/example-bluegreen/example-bluegreen/chart/`. 首次跑 demo
之前手动复制一份:

```bash
cd docs/example-bluegreen-multi-env
cp -r ../example-bluegreen/example-bluegreen/chart .
```

不同于符号链接, 复制一份保持 demo 工程独立性 — 修改 multi-env demo 的 chart
不影响既有 example-bluegreen demo.

---

## 快速开始

```bash
# 0. 准备 KPEnv (前置条件已完成则跳过)
kp context add --name staging --namespace kp-demo-bluegreen-staging
kp context add --name prod    --namespace kp-demo-bluegreen-prod

# 1. 进入 demo 目录
cd docs/example-bluegreen-multi-env

# 2. 复制 chart (首次)
cp -r ../example-bluegreen/example-bluegreen/chart .

# 3. staging: 部署 + 切流量
make setup-staging

# 4. 等 5 分钟 (controller 5min 稳态判定)
sleep 300

# 5. 验证 staging 已写出 verified-traffic
make verify-staging-cm

# 6. prod: 用 staging 配置部署
make propagate-to-prod

# 7. 验证两 env traffic 一致
make verify-both

# 8. 清理
make cleanup
```

整个流程在干净集群上 **~7 分钟** 完成 (含 5min 稳态等待).

---

## 关键文件解析

### resources-staging.yaml

跟 example-bluegreen 的 resources.yaml 一致 (单 env 蓝绿). KubePivot
Controller 监听后写出 verified-traffic ConfigMap.

### resources-prod.yaml

**关键**: prod 自身**也必须声明** `traffic` 字段 (Q12 fail-fast).
`kp sandbox start --from-env staging` 不会"自动启用蓝绿",
prod 必须明确说"我支持蓝绿", 然后 staging 的 traffic 配置才能注入覆盖.

跟 staging 的 traffic 字段几乎一样, 关键差别:
- 用同样的 chart, 但 helm release 名前缀不同
- routes 字段最初是占位 (会被 --from-env 覆盖)

---

## 内部时序 (multi-env 视角)

```
T+0   make setup-staging
       └─ helm install whoami (blue+green)
       └─ kp controller enroll
       └─ kp sandbox start (LOCKED → ... → COMMITTING):
            └─ runSandboxCommit (helm upgrade)
            └─ runBlueGreenSwitch (route.ApplyRoutes blue→green)
       └─ RUNNING

T+5min Controller verifiedTrafficWriter 周期扫描 (本地 1min 一轮):
       └─ Gate 1: shard 过滤 (本 pod 持有 staging ns ✓)
       └─ Gate 2: state == RUNNING ✓
       └─ Gate 3: RunningSinceFromHistory ≥ 5min ✓
       └─ Gate 4: HasBlueGreen() ✓
       └─ readActualRoutes (Provider.GetCurrentRoutes)
       └─ Gate 5: needsUpdate (vs 现有 ConfigMap) ✓ (首次写入)
       └─ writeVerifiedTrafficCM
            ConfigMap kubepivot-verified-traffic 创建

T+5min make propagate-to-prod
       └─ helm install whoami (prod ns, blue+green)
       └─ kp controller enroll (prod resources.yaml)
       └─ kp sandbox start --from-env staging:
            └─ loadVerifiedTrafficFromEnv:
                 └─ loadKPEnv("staging") 读 ~/.kp/envs/staging.yaml
                 └─ kubectlGetCM staging 集群 / staging ns / traffic.yaml
                 └─ yaml.Unmarshal → controller.Traffic
            └─ LOCKED → SNAPSHOTTING → SIMULATING → COMMITTING:
                 └─ runSandboxCommit (helm)
                 └─ runBlueGreenSwitch(cfg, root, fromEnvTraffic):
                      └─ HasBlueGreen() check (prod 自身要声明 ✓ Q12)
                      └─ rc.Traffic = override (注入 staging 配置)
                      └─ ApplyRoutes (prod ingress 切到 green=100%)
            └─ RUNNING

T+10min Controller 也给 prod 写 verified-traffic ConfigMap
        (prod 也成为新的传播源, 可被下游 env 用 --from-env 读取)

设计意义:
  传播链可级联 (staging → prod → DR-cluster → ...)
  任何 RUNNING + 5min 稳态的 env 都能作为传播源
  这是单一职责解耦的直接推论
```

---

## 失败场景演示

### 场景 1: prod 自身没声明 traffic 字段

去掉 `resources-prod.yaml` 的 `traffic:` block 跑 propagate-to-prod:

```
❌ --from-env staging 加载失败:
   项目自身 resources.yaml 没声明蓝绿 traffic, 不能用 --from-env
   (hint: 先在 resources.yaml 声明 traffic 字段, 详见 docs/design/traffic-layer.md)
```

这是 Q12 fail-fast: 配置不一致显式报错, 不静默跳过.

### 场景 2: staging 还在 5min 内, 没写出 verified-traffic

```
❌ --from-env staging 加载失败:
   env "staging" 没有已验证的 traffic 配置 (期望 ConfigMap kubepivot-verified-traffic
   在 namespace "kp-demo-bluegreen-staging"). 前提条件: 项目在该 env 处于 RUNNING
   状态且稳定 ≥5min, controller 才会写出此 ConfigMap.
   排查命令: kubectl --kubeconfig=... get configmap kubepivot-verified-traffic
   -n kp-demo-bluegreen-staging
```

这是 Q7 fail-fast: 上游不就绪显式报错, 给出排查命令.

### 场景 3: KPEnv staging 不存在

```
❌ --from-env staging 加载失败:
   env "staging" 不存在,请先运行: kp context add --name staging
```

这是 Q3 fail-fast: 复用既有 KPEnv 错误信息.

---

## e2e 真集群验证 (本 demo 范围外)

本 demo 的脚本写的是**真实命令**, 但**没有 CI 验证**.

完整 e2e 验证需要:
1. 一个真集群 (orbstack / minikube / kind 都行)
2. KubePivot Controller 部署到集群
3. 两个不同 namespace + 两个对应 KPEnv

预期实施在 v2.6.1 release 阶段 (commit ~444+) 跑通完整闭环 + 录制截图,
本 demo 工程作为"能跑通的脚本骨架"已经就绪.

如果你正好有真集群想试一下, 跑通后欢迎提 PR 改进本 README.

---

## 与 example-bluegreen 的关系

| 维度 | example-bluegreen | example-bluegreen-multi-env |
|---|---|---|
| Env 数量 | 1 (单 namespace) | 2 (双 namespace 或多集群) |
| Controller 依赖 | 不强制 | **必需** (verifiedTrafficWriter) |
| 演示重点 | 蓝绿切换基本能力 | 多 env 传播链 |
| 适用场景 | v2.6.0 单项目蓝绿入门 | v2.6.1 多 env 推广姿势 |

如果 v2.6 蓝绿基本概念还不熟, 先跑 example-bluegreen.

---

## 进一步阅读

- [v2.6.1 多环境流量传播实施草案](../design/traffic-multi-env-impl-draft.md) — 完整设计 (16 个 Q + 5 处实施期校准)
- [v2.6 流量层设计](../design/traffic-layer.md) — 单 env 蓝绿基础
- [Sandbox 状态机](../design/state-machine.md) — COMMITTING 内 runBlueGreenSwitch 调用
- [verifiedTrafficWriter 源码](../../internal/controller/verified_traffic_writer.go) — 生产者实现
- [--from-env 实现源码](../../cmd/kp/sandbox_from_env.go) — 搬运工实现

---

## 故障排查

跟 `example-bluegreen/README.md` 的故障排查一致 (Provider 不可用 / Pod ready 超时
/ Ingress 缓存等). 多 env 特有的:

### "env staging 没有已验证的 traffic 配置"

按 fail-fast 错误 hint 跑排查命令:

```bash
kubectl --kubeconfig=<staging-kubeconfig> \
  get configmap kubepivot-verified-traffic -n <staging-ns>
```

可能原因:
- staging RUNNING 不到 5 min (`kp status` 看 staging 状态)
- KubePivot Controller 没部署 (`kubectl get pods -n kubepivot-system`)
- Controller 没接管该 ns (`kubectl get ns -l kubepivot.io/managed=true`)

### Controller 知道 staging 但没写 ConfigMap

看 controller 日志:

```bash
kubectl logs -n kubepivot-system deployment/kubepivot-controller \
  | grep VerifiedTrafficWriter
```

应该有形如 `🔄 VerifiedTrafficWriter 已写入 ns=kp-demo-bluegreen-staging
running_since=... elapsed=Xm0s` 的日志.

如果只有"扫描"没有"写入", 检查 5 个 gate 哪个没通过:
- shard 过滤
- state == RUNNING
- RunningSinceFromHistory ≥ 5min
- HasBlueGreen()
- needsTrafficUpdate

### prod 拿到了 staging 配置但 ingress 没切流量

`runBlueGreenSwitch` override 注入路径检查:

```bash
# 看 prod sandbox start 日志, 应该有 "使用 --from-env 注入的 traffic" 这行
kp sandbox start --from-env staging  # 重跑看输出
```

如果输出 "使用 --from-env 注入的 traffic (services: ...)" 但 ingress 没变, 检查
provider.ApplyRoutes 是否真跑了 (kubectl describe ingress 看 backend.service.name).
