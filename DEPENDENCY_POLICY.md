## KubePivot 依赖管理政策

### 一、哲学根基

**KubePivot 的依赖管理不是关于“零依赖”，而是关于“依赖不拥有我们”。**

每条外部代码进入项目，必须能回答三个问题：
1. **它侵入核心逻辑吗？** ——如果它决定了 KubePivot 如何思考调度、如何管理状态、如何操作资源，它就不只是“依赖”，而是“共同决策者”
2. **它被限定在边界吗？** ——如果它只在输入/输出层、工具链、或可观测性侧支，且失败时不影响主路径，它就可以被隔离管理
3. **我们控制它的退出成本吗？** ——如果有一天需要替换它，代价是一条接口适配器还是整个架构重构？

这三个问题的答案，决定了依赖进入哪一级。

---

### 二、三级分级标准

#### **Level 0 — 禁止：逻辑侵入性依赖**

即使有接口抽象也无法隔离其对 KubePivot 核心决策的影响。

**判定标准**：
- 定义了 KubePivot 会直接调用的核心数据结构或接口，且替换成本涉及架构级重构
- 接管了本应由 KubePivot 自己实现的执行路径（如 K8s 资源监听、调度决策）

**当前判定**：
- `k8s.io/client-go` — 禁止。原因：它会接管 Informer/Watch/资源操作，使 KubePivot 的核心 reconcile 路径依赖外部实现。v2.7 自研 eventstream 包证明了这个决定是正确且可实现的。

**未来候选**（需单独论证）：
- 任何 K8s scheduling framework SDK（如果 v3.0 模式 2 被启用，需要先通过 Level 1 判定）
- OPA/Casbin 等策略引擎（如果 v2.8 RBAC 转向外部引擎，需要论证为何 FileBasedChecker 不够）

---

#### **Level 1 — 受限：标准协议库**

允许引入，但必须满足两个条件：(a) 仅用于特定边界层（可观测性、标准协议交互），(b) 失败时零影响核心执行路径。

**判定标准**：
- 实现的是行业通用协议或标准，而非 KubePivot 特有逻辑
- 替换成本中等（需要重写适配层，但不涉及核心架构改动）

**当前判定**：
- `github.com/prometheus/client_golang` v1.20.5 — 允许。用于 Prometheus metrics exposition（可观测性侧支）。注册到 `prometheus.Registerer` 接口，测试可 mock。如果未来需要替换，只需重写 `internal/eventstream/metrics.go` 的 collector 实现，不影响 informer/watch/cache 核心路径。已在 HANDOFF.md 3.1 节记录了例外理由。
- `gopkg.in/yaml.v3` — 允许。用于配置文件解析（输入层）。解析失败只影响配置加载，不影响运行时已运行的 controller。Go 标准库无 YAML 支持，且 yaml.v3 是 Go 生态的事实标准。

**v3.0 候选**（需在实施前判定）：
- Mutating Webhook 的 TLS 证书管理：如果引入 `k8s.io/apiserver` 或 `cert-manager` 的 Go SDK，需先通过此级判定。v2.8 的 sealed-secrets 用 CLI wrapper 避开了 SDK 引入——v3.0 Webhook 优先考虑同样策略。

---

#### **Level 2 — 允许：零侵入 CLI wrapper**

通过 `exec.CommandContext` 调用外部二进制，KubePivot 不链接任何外部 SDK。

**判定标准**：
- 外部工具独立运行，通过结构化输出（JSON/YAML）与 KubePivot 交互
- 失败时 KubePivot 优雅降级（fallback 路径或明确错误提示）
- 二进制不存在时给出安装引导而非 panic

**当前判定**：
- **cosign**（v2.8 H-Level1）：`kp supply-chain verify` 用 `cosign verify --key`，纯 CLI wrapper，输出 JSON 解析
- **syft**（v2.8 H-Level2）：`kp supply-chain sbom` 用 `syft -o cyclonedx-json`，输出 JSON
- **oras**（v2.8 H-Level4）：SBOM OCI 上传用 `oras push`，纯 CLI
- **kubeseal**（v2.8 G-Level1）：`kp secret seal` 用 `kubeseal --fetch-cert`，零 Go SDK
- **kubectl**（v1.0+ 至今）：KubePivot 的 K8s 操作基础，通过 `internal/executor` 封装
- **helm**（v1.0+ 至今）：Chart 操作通过 CLI wrapper，不引入 Helm Go SDK
- **trivy**（v2.5+）：`kp scan` 用 `trivy image`，输出 JSON

**工程约定**：
- 所有 CLI wrapper 需实现 `execCommandFunc` 函数变量注入模式，让单测可以 mock（与 v2.7 `readTokenFile`/`newInformerFunc`、v2.8 `cosign.go`/`syft.go`、v2.9 Prometheus `httpDo` 一致）
- 外部 CLI 检测统一用 `Detect*` + `InstallHint` 模式（与 v2.6 `DetectKubeseal`、v2.8 `ensureCosignAvailable`/`ensureSyftAvailable`/`ensureOrasAvailable` 一致）
- 超时硬编码 30s（与 v2.8 H-Level1 Q-H.5、H-Level3 Q-H.13 一致），Level2 不做 `--timeout` flag（留后续扩展）

---

### 三、决策流程

新依赖进入 KubePivot 时的判定路径：

```
1. 它能通过 CLI wrapper 模式使用吗？
   → 是：Level 2，零侵入允许。实现 execCommandFunc mock
   → 否：进入问题 2

2. 它是实现行业通用协议/标准的工具库吗？
   → 是，且仅用于边界层（可观测性/输入输出/标准协议交互）：
     进入 Level 1 判定。论证"失败不影响核心路径"的具体机制。
   → 否，或虽然通用但侵入核心逻辑：进入问题 3

3. 它会接管 KubePivot 的核心执行路径吗？
   → 是：Level 0 禁止。寻找替代方案（自研、CLI wrapper、或拒绝该功能）
   → 否：回到 Level 1 判定
```

---

### 四、当前依赖清单

| 依赖 | 级别 | 用途 | 引入版本 | 替换成本 |
|------|------|------|----------|----------|
| `k8s.io/client-go` | Level 0 | ✗ 禁止。v2.7 自研 eventstream 替代 | — | — |
| `github.com/prometheus/client_golang` | Level 1 | 可观测性 metrics exposition | v2.7.0 | 中等（重写 `metrics.go`） |
| `gopkg.in/yaml.v3` | Level 1 | 配置文件解析 | v1.0.0 | 中等（适配层） |
| cosign (CLI) | Level 2 | 镜像签名验证 | v2.8.0 | 低（去除 wrapper 文件） |
| syft (CLI) | Level 2 | SBOM 生成 | v2.8.0 | 低 |
| oras (CLI) | Level 2 | OCI artifact 上传 | v2.8.0 | 低 |
| kubeseal (CLI) | Level 2 | Secret 加密 | v2.8.0 | 低 |
| kubectl (CLI) | Level 2 | K8s 资源操作 | v1.0.0 | 中等（需重写 executor） |
| helm (CLI) | Level 2 | Helm chart 操作 | v1.0.0 | 中等 |
| trivy (CLI) | Level 2 | CVE 扫描 | v2.5.0 | 低 |

**当前违反此政策的历史代码**：无。v2.7 之前的依赖（kubectl/helm/yaml.v3）在政策制定时已被 retroactively 符合。

---

### 五、重新评估

此政策在以下时机重新评估：
- v3.0 实施前：判定 Webhook TLS 证书管理的依赖级别
- v3.0 release 时：如果调度器需要引入新的数据源 SDK
- 任何时候引入了当前清单中不存在的 Go 第三方库

