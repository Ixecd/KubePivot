# kp deploy

部署服务到 K8s 集群。KubePivot 最核心的命令。

## 用法

```
kp deploy [flags]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--components` | `configs/components.yaml` | 组件配置文件路径 |
| `--namespace` | (project.env 的 `KUBE_NAMESPACE`) | K8s namespace |
| `--context` | (project.env 的 `KUBE_CONTEXT`) | kube context |
| `--kubeconfig` | (project.env 的 `KUBE_CONFIG`) | kubeconfig 文件路径 |
| `--env` | | 指定部署环境（`kp context add` 配置） |
| `--dry-run` | `false` | 只打印执行计划，不实际部署 |
| `--sign` | `false` | 部署后 cosign keyless 签名镜像 |
| `--force-migrate` | `false` | 忽略破坏性迁移警告强制部署 |
| `--preview` | `false` | 部署后生成 Header-based Preview 路由模板 |
| `--changed-only` | `false` | 只部署有 git 变更的服务 |
| `--parallelism` | `0`（不限制） | 同层最大并发数，大规模集群建议 4-8 |
| `--skip-supply-chain` | `false` | 跳过供应链策略验证（紧急回滚/调试） |
| `--sizing-mode` | `manual` | 资源优化模式：`auto` / `manual` |
| `--sizing-profile` | `default` | 业务模板：`web` / `batch` / `db` / `default` |
| `--sizing-force` | `false` | 自动应用 sizing 建议，不等人工确认 |
| `--prometheus-url` | | Prometheus API 地址（sizing auto 模式需要） |
| `--prometheus-window` | `7d` | Prometheus 查询窗口 |
| `--prometheus-step` | `15m` | Prometheus 查询步长 |
| `--sizing-threshold` | `0.7` | 自动应用置信度阈值 (0.0-1.0) |

## 执行流程

```
1. 解析参数 + 加载 project.env
2. RBAC 检查 (PermDeploy)
3. 确保 namespace 存在
4. 读取 components.yaml → BuildPlan → BuildLayers
5. 供应链策略预验证 (--skip-supply-chain 可跳过)
6. Sizing hook (--sizing-mode=auto 时)
7. 状态机: IDLE/RUNNING → INITIALIZING → DEPLOYING
8. 多服务模式: 逐层 deployLayers (helm upgrade 独立 release)
   单服务模式: make deploy.build → make deploy.push → make deploy.install → make deploy.run.all
9. DEPLOYING → VALIDATING → RUNNING
10. 如果已 enroll controller，自动同步 resources.yaml
```

## 状态机

```
IDLE → INITIALIZING → DEPLOYING → VALIDATING → RUNNING
  ↑                         ↓ (失败)
  └── ROLLING_BACK ←────────┘
```

## 副作用

- 写 K8s Secret（`<project>-secret`，自动创建 dev Secret）
- 写 etcd 状态机记录
- 写 ConfigMap `kubepivot-resources`（如果已 enroll controller）
- 修改 `components.yaml`（sizing auto 模式 + --sizing-force）
- `--sign` 时调用 cosign

## 相关命令

- `kp status` — 查看部署状态
- `kp sandbox start` — 带迁移原子性的安全部署
- `kp promote` — 蓝绿流量切换
- `kp rollback` — 回滚
- `kp down` — 下线服务
- `kp sizing recommend` — 单独运行 sizing 计算
