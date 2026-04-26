## v2.8 — Enterprise Governance

### 1. 目标

```
KubePivot v2.x 阶段的"企业级"补完。
不是从零做企业版，是**在 v2.6 GitOps 框架基础上加企业必需的 4 个治理面**：

  A. SSO / OAuth 集成        身份认证
  B. RBAC 多团队隔离          权限边界
  G. 加密 at rest             数据保护
  H. 镜像签名 + 供应链        交付安全

设计哲学：
  - 不重做认证 / 加密 / 签名（成熟方案太多，复用而非自研）
  - KubePivot 做"集成层"——把这些工业级方案 GitOps 化
  - 用户只需 resources.yaml 声明意图，后端复杂度 KubePivot 吃下

排除：
  - C: SOC2 / ISO27001 真实表单（等真实客户出现再做）
  - D: CMDB / ITSM 集成（不是 KubePivot 用户的痛点）
  - E: 多租户隔离（推迟到 v3.x+，等商业方向明确）
  - F: 审计日志 immutable（A+G 实质覆盖一部分）
```

### 2. 关键设计

#### 2.1 A — SSO / OAuth 集成

```go
// internal/auth/sso.go (草图)

// AuthProvider 抽象 SSO 后端
type AuthProvider interface {
    Name() string  // "google" / "github" / "dex" / "keycloak"
    
    // BuildLoginURL 构造重定向 URL（带 state + PKCE）
    BuildLoginURL(state string) string
    
    // ExchangeCode OAuth 授权码换 token
    ExchangeCode(ctx context.Context, code string) (*Token, error)
    
    // VerifyToken 验证 token 有效性
    VerifyToken(ctx context.Context, token string) (*UserInfo, error)
}

type UserInfo struct {
    Subject string            // OAuth sub claim（唯一标识）
    Email   string
    Name    string
    Groups  []string          // 用于 RBAC 映射
}

// 已知实现：
//   internal/auth/oauth_google.go
//   internal/auth/oauth_github.go
//   internal/auth/dex.go            （企业内部 OIDC 兜底）
```

```bash
# 用户体验
$ kp login                    # 浏览器打开 SSO 授权页
$ kp login --provider github  # 显式指定 provider
$ kp whoami                   # 当前登录用户

# Token 存储位置
~/.kube/kubepivot/credentials.yaml
（与 ~/.kube/config 平级，不污染 kubectl 配置）
```

```
后端集成：
  - Token 通过 K8s ServiceAccount 映射进集群
  - kp 命令带 token 调 K8s API
  - controller 验证 token → 解析 UserInfo
  - 与 v2.5 sharding 联动（用户只能看到自己有权的 namespace）
```

#### 2.2 B — RBAC 多团队隔离

```yaml
# v2.8 新增：configs/teams.yaml
teams:
  - name: backend-team
    members:
      - alice@example.com
      - bob@example.com
    namespaces:
      - kp-backend-*
    permissions:
      - deploy
      - sandbox
      - rollback
    excluded:
      - controller-uninstall   # 危险操作明确禁止

  - name: frontend-team
    members:
      - charlie@example.com
    namespaces:
      - kp-frontend-*
    permissions:
      - deploy
      - status

  - name: ops-team
    members:
      - dave@example.com
    namespaces:
      - "*"                    # 全权限
    permissions:
      - "*"
```

```go
// internal/rbac/checker.go (草图)

type Permission string
const (
    PermDeploy             Permission = "deploy"
    PermSandbox            Permission = "sandbox"
    PermRollback           Permission = "rollback"
    PermStatus             Permission = "status"
    PermControllerInstall  Permission = "controller-install"
    PermControllerUninstall Permission = "controller-uninstall"
    PermAll                Permission = "*"
)

type Checker interface {
    // Check 验证用户是否有权限
    Check(ctx context.Context, user *UserInfo, ns string, perm Permission) error
}

// 默认实现：基于 teams.yaml + UserInfo.Groups
type FileBasedChecker struct {
    teamsFile string
    cache     atomic.Pointer[teamConfig]
}
```

```
集成路径：
  - 每个 kp 命令开始时调 Checker.Check()
  - controller 处理 reconcile 时调 Checker.Check()
  - 失败时返回明确错误：
    "user@example.com 无 deploy 权限于 kp-backend-prod"
  
  - teams.yaml 通过 ConfigMap 同步到集群
  - controller 监控 teams ConfigMap 变化（live reload）
  - 不需要重启 controller 即可生效
```

#### 2.3 G — 加密 at rest

```
KubePivot 数据 at rest 的现状：
  - etcd 存储项目状态（DeployRecord）—— 默认明文
  - K8s Secret 存储敏感数据 —— 默认 base64（不是加密）
  - 配置文件（resources.yaml / teams.yaml）—— 明文

v2.8 加密方案：
  
  1. etcd 数据加密：
     - 启用 etcd 加密（K8s 集群级别）
     - kp 工具检查集群是否启用，未启用时 warn
     - kp doctor 加 "etcd encryption" 检测
     - 不强制——尊重集群管理员决策
     
  2. Secret 加密（Sealed Secrets 集成）：
     - 引入 sealed-secrets 控制器（可选）
     - kp secret seal --from-literal=db_password=xxx
       生成 sealedsecrets.bitnami.com/v1alpha1 资源
     - 加密后的 Secret 可以放进 Git（GitOps 友好）
     - 集群解密只在运行时
     
  3. 配置文件加密（kp secret 扩展）：
     - resources.yaml / teams.yaml 中的敏感字段
     - 用 SOPS（Mozilla）模式加密
     - 与 KMS 集成（AWS KMS / GCP KMS / Vault）
```

```bash
# v2.8 用户体验
$ kp secret seal \
    --secret db-credentials \
    --from-literal=password=mypass

# 输出：
# apiVersion: bitnami.com/v1alpha1
# kind: SealedSecret
# spec:
#   encryptedData:
#     password: AgB8q2ZoX...
#
# 这个 YAML 可以提交到 Git，安全。

$ kp doctor --etcd-encryption
# Checking etcd encryption...
# ⚠ etcd 数据未启用 KMS 加密
#   建议：kubeadm 集群 → /etc/kubernetes/manifests/kube-apiserver.yaml
#         加 --encryption-provider-config=...
```

#### 2.4 H — 镜像签名 + 供应链安全

```
v2.5 已有 kp scan（trivy CVE 扫描）。
v2.8 扩展为完整"供应链安全"：

  1. cosign 签名验证：
     - 部署前验证镜像签名
     - 未签名镜像默认拒绝（可配置）
     
  2. SBOM 生成：
     - 部署时自动生成 SBOM（Software Bill of Materials）
     - Syft / cyclonedx 输出
     - SBOM 存到 OCI registry（与镜像同 repo）
     
  3. 供应链策略：
     - resources.yaml 加 supply-chain 字段
     - 声明镜像必须来自哪些 registry
     - 必须有有效 cosign 签名
     - 必须有 SBOM
     - 必须无 critical CVE（trivy）
```

```yaml
# v2.8 resources.yaml 扩展
resources:
  - kind: Deployment
    name: wallet-service
    image: registry.example.com/wallet:v1.2.3

# v2.8 新增：供应链策略
supply-chain:
  registries:
    - registry.example.com  # 白名单
  
  signing:
    enforce: true
    cosign-key: /path/to/cosign.pub
    # 或用 keyless（OIDC 签名）：
    # keyless-issuer: https://accounts.google.com
    # keyless-subject: ci@example.com
  
  sbom:
    require: true
    format: cyclonedx-json
  
  cve:
    max-severity: high       # critical 一律拒绝
    exceptions:
      - CVE-2024-12345       # 已知不影响
```

```go
// internal/supplychain/verifier.go (草图)

type Verifier interface {
    // Verify 验证镜像满足供应链策略
    Verify(ctx context.Context, image string, policy *Policy) (*Report, error)
}

type Report struct {
    Image      string
    Passed     bool
    Reasons    []string
    SBOM       *SBOM
    Signatures []Signature
    CVEs       []CVE
}

// 实现：
// - cosign-cli wrapper（不引入 cosign Go SDK，与 v2.5 哲学一致）
// - syft-cli wrapper（SBOM 生成）
// - trivy-cli wrapper（CVE 扫描，已有）
```

#### 2.5 与既有功能的集成

```
v2.8 不破坏 v2.0-v2.7 任何功能。
新增字段都是可选的：
  - 没配 SSO → kp 工具仍按"本地 kubeconfig"模式工作
  - 没配 teams.yaml → 全权限（保持兼容）
  - 没配 supply-chain → 无策略（保持兼容）

新增配置默认行为：
  kp init --enterprise  显式生成企业模板（含 teams.yaml / supply-chain 字段）
  kp init               只生成 v2.6 基础模板
```

### 3. 任务清单

```
A. SSO / OAuth 集成（~600 行 + 测试 ~300 行）
  [ ] internal/auth/sso.go             AuthProvider 接口
  [ ] internal/auth/oauth_google.go    Google OAuth 实现
  [ ] internal/auth/oauth_github.go    GitHub OAuth 实现
  [ ] internal/auth/dex.go             Dex / OIDC 兜底实现
  [ ] internal/auth/credentials.go     ~/.kube/kubepivot/credentials.yaml 管理
  [ ] cmd/kp/login.go                  kp login 命令
  [ ] cmd/kp/whoami.go                 kp whoami 命令

B. RBAC 多团队隔离（~500 行 + 测试 ~300 行）
  [ ] internal/rbac/checker.go         Permission / Checker 定义
  [ ] internal/rbac/file_based.go      FileBasedChecker
  [ ] internal/rbac/teams.go           teams.yaml 解析
  [ ] internal/controller/rbac_hook.go controller reconcile 接入
  [ ] 改造 cmd/kp 各命令调 Checker.Check()
  [ ] kp team 命令组（add/remove/list/check）

G. 加密 at rest（~400 行 + 测试 ~200 行）
  [ ] internal/sealed/wrapper.go       Sealed Secrets CLI wrapper
  [ ] cmd/kp/secret_seal.go            kp secret seal 命令
  [ ] internal/sops/wrapper.go         SOPS 加密 wrapper
  [ ] cmd/kp/doctor_encryption.go      etcd 加密检测

H. 镜像签名 + 供应链（~700 行 + 测试 ~400 行）
  [ ] internal/supplychain/verifier.go Verifier 接口
  [ ] internal/supplychain/cosign.go   cosign-cli wrapper
  [ ] internal/supplychain/syft.go     SBOM 生成 wrapper
  [ ] 扩展 internal/controller/policy.go OPA + supply-chain 联动
  [ ] 改造 cmd/kp/deploy.go 加供应链验证 hook

文档（~400 行）
  [ ] docs/design/enterprise-governance.md
  [ ] docs/example-enterprise/ 多团队 demo 工程

合计：
  代码：~2200 行 + 测试 ~1200 行 = ~3400 行
  文档：~400 行
  
工作量预估：3 周
```

### 4. 风险与对策

```
风险 1：SSO 集成的多 provider 维护成本
  概率：中等
  影响：每个 provider 都有 quirks，长期维护负担
  
  对策：
  - 优先支持 Dex（OIDC 标准）作为兜底
  - Google / GitHub 是补充，参考 https://oauth2-proxy.github.io
  - 引入 SSO 测试矩阵（每个 provider 至少 3 个 case）

风险 2：RBAC 配置错误导致用户被锁
  概率：中等
  影响：管理员误配置后无法登录修复
  
  对策：
  - 引入 break-glass 机制：
    KubePivot 集群启动参数 --rbac-override-secret=<secret-name>
    持有这个 Secret 的人可以绕过 RBAC（应急）
  - 配置变更时 dry-run 验证（影响哪些用户预览）
  - 最近 5 次配置历史保留（一键回滚）

风险 3：加密方案的密钥管理复杂度
  概率：高
  影响：用户配置 KMS / Vault 失败 → 部署阻塞
  
  对策：
  - 默认不启用加密（保持兼容）
  - 启用时分层：
    Level 1: Sealed Secrets（最简单，仅集群密钥）
    Level 2: SOPS + KMS（需要云厂商密钥）
    Level 3: Vault 集成（需要部署 Vault）
  - kp doctor --encryption 全方位检测

风险 4：供应链验证拖慢部署
  概率：高
  影响：cosign / trivy 验证 ~10-30s，部署体验差
  
  对策：
  - 验证缓存（同 image digest 已验证 → 跳过）
  - 验证可异步（reconcile 时验证，不阻塞 kp deploy）
  - resources.yaml supply-chain 字段独立，不强制
  - 大集群可在 controller 端预扫描镜像
```

### 5. 验收

```
功能验收：
  ✓ kp login 完整流程：浏览器 → SSO → token 存储
  ✓ kp whoami 显示当前用户和团队
  ✓ teams.yaml 配置后，无权用户被正确拒绝
  ✓ Sealed Secret 加密 / 解密往返正确
  ✓ 镜像签名验证：未签名 → 拒绝；已签名 → 放行
  ✓ SBOM 生成 + 上传 OCI registry

集成验收：
  ✓ docs/example-enterprise/ 多团队 demo 工程跑通
  ✓ 模拟 3 个团队 / 5 个用户的真实场景
  ✓ make dev 全绿
  ✓ 现有 v2.6 / v2.5 / v2.4 测试无 regression
```

### 6. 工作量预估

```
3 周专注 = 21 天
分阶段：
  Week 1: A (SSO) + B (RBAC) 主体
  Week 2: G (加密) + H (供应链) 主体
  Week 3: 集成 + 测试 + demo + 文档

关键里程碑：
  Day 7:  A + B 单测通过
  Day 14: G + H 主体完成
  Day 21: v2.8.0 release
```

---

## v2.8.1 — v2.x 收尾（持续改进，不打 tag）

### 1. 目标

```
v2.x 阶段的"答应了但没做"清单收口。
不打 tag，作为 v2.8.0 → v2.9.0 之间的过渡 commits。

主要任务：
  1. web3-blitz 升级到 v2.6 蓝绿
  2. 数据保护机制（resources.yaml protect: true）
  3. 多环境流量配置传播（kp deploy --from-env）
  4. tools/codegen 多 const block 改造
  5. kp release 自动 push（含 push --tags）
```

### 2. 任务详情

#### 2.1 web3-blitz 升级到 v2.6 蓝绿

```
当前 web3-blitz：
  - 用 v2.0 命令式蓝绿（kp deploy --bluegreen + kp promote）
  - 工作正常但不是 GitOps 友好
  
升级路径：
  1. 写 web3-blitz/configs/resources.yaml 的 traffic 字段
  2. 改 deploy 流程：kp deploy → kp sandbox commit
  3. 测试蓝绿切换 + 失败回滚
  4. 文档化作为"真实生产案例"

价值：
  - 验证 KubePivot v2.6 在真实业务里的可用性
  - example-blue-green 是教学，web3-blitz 是真实
  - 完成后写一篇博客《用 KubePivot v2.6 重做 web3-blitz 蓝绿》
```

#### 2.2 数据保护机制

```
原计划：v2.8.0 主版本含
新计划：放到 v2.8.1（v2.8 主版本聚焦企业治理 4 项）

设计：
  resources.yaml 加 protect: true
  
  示例：
  resources:
    - kind: PersistentVolumeClaim
      name: postgres-data
      protect: true   # ← v2.8.1 新增
  
  reconcile 检测到 protect=true 资源"应该删除/重建"时：
    1. 阻断操作
    2. 返回明确错误
    3. 提示用户：kp confirm <ns>/<resource> 显式确认
  
  kp release 阻断：
    如果即将打 tag 的 commit 包含 protected 资源删除
    → 阻断 release
    → 要求 --force-release-with-data-loss 显式标记
```

#### 2.3 多环境流量配置传播

```
原计划：v2.6.1 候选
新计划：放到 v2.8.1 一并实现

kp deploy --env prod --from-env staging
  从 staging 环境读"已验证的 traffic 配置"
  应用到 prod 环境
  不做"分批切流"，只做"配置传播"

实现：
  - cmd/kp/deploy.go 加 --from-env 参数
  - controller 读 staging configmap 的 resources.yaml
  - 提取 traffic 字段，应用到 prod 部署
  - 工作量：~200 行 + 测试
```

#### 2.4 tools/codegen 多 const block 改造

```
原计划：v2.6.1 候选
v2.8.1 一并实施

修法：
  tools/codegen/codegen.go 的 genDecl 函数
  ~30 行 ast.Inspect 改造
  递归收集所有匹配 typeName 的 const block
  
verify：
  - 重新跑 go run tools/codegen -type=ErrorCode internal/code/
  - 期望生成完整 case 分支（不再只识别第一个 const block）
```

#### 2.5 kp release 自动 push

```
当前问题：
  v2.6.0 release 时遇到 SSL_ERROR_SYSCALL
  需要手工 git push --tags

改进：
  cmd/kp/release.go 加：
    --push (default true)     是否自动 push
    --push-retry N (default 3) push 失败重试次数
    --tags-only               仅 push tag（如果 commit 已 push）
  
  实现：
    git push 失败时不立即失败
    重试 N 次（每次间隔 2s 指数退避）
    最终失败提示用户手工 git push
```

### 3. 工作量预估

```
v2.8.1 不是大版本，是"清单收口"
预计工作量：1 周（~5 个任务，每个 1-2 天）

无独立 release，作为 v2.8.0 → v2.9.0 之间的 commits 流入主线
```

---

## v2.9 — Resource Sizing Engine（维度 B）

### 1. 目标

```
KubePivot 第一次进入"决策系统"领域。

v2.9 之前：用户写 deployment.spec.containers[].resources.requests.cpu = "200m"
v2.9 起：  用户不写，KubePivot 算出最优值

具体范围：
  - 维度：Pod × (CPU, Memory) 二维 DP
  - 不管节点拓扑（v3.0 才管）
  - 部署前算一次最优 sizing
  - 不做运行时重 sizing（v3.0 才做）
  - 目标：单 Pod 资源利用率 70%+

设计原则：
  - "降低门槛"是核心（每个 Go 开发者部署到云端时都不用想资源配额）
  - 业务零感知（用户不关心 sizing 算法）
  - 安全兜底（算错时自动降级到保守值）
  - 不替代 K8s VPA，与之共存（VPA 做运行时调整，v2.9 做部署时算法）
```

### 2. 关键设计

#### 2.1 问题形式化

```
输入：
  - 一个 Pod 的历史资源使用数据（来自 v2.7 metrics 接入）
    形式：[(timestamp, cpu_usage, memory_usage), ...]
    时间窗口：最近 7 天 / 30 天（可配置）
  - 业务约束：
    - QoS 等级（Guaranteed / Burstable / BestEffort）
    - SLA 等级（critical / normal / best-effort）
    - 用户声明的"业务保留头空间"（如 "20% headroom"）

输出：
  - resources.requests.cpu       int (millicores)
  - resources.requests.memory    int (bytes)
  - resources.limits.cpu         int (millicores)
  - resources.limits.memory      int (bytes)

目标函数：
  最大化 utilization = actual_usage / requested
  subject to:
    P95(usage) ≤ requested * (1 - headroom)
    P99(usage) ≤ limit * 0.95
    OOMKill 概率 < 0.1%
```

#### 2.2 二维 DP 状态定义

```
为什么需要 DP？
  
  朴素方案：取历史 P95 当 request，P99 当 limit
  问题：
    1. CPU 和 Memory 不独立（高 CPU 时 GC 加剧 → 内存上升）
    2. 不同业务模式有不同最优配比（CPU-bound vs Memory-bound）
    3. headroom 不是常量（峰值时段 headroom 应该更大）
  
  DP 方案：
  
  状态：dp[c][m]
    c = CPU request (millicores)
    m = Memory request (bytes)
    dp[c][m] = 在该配置下的"利用率得分"
  
  状态空间：
    c ∈ [50m, 4000m] 离散化为 80 个等级（步长 50m）
    m ∈ [64MiB, 8GiB] 离散化为 128 个等级（步长 64MiB）
    总状态数：80 × 128 = 10240 个候选配置
    
  目标：
    arg max dp[c][m]
    subject to: 
      P95(cpu_usage) ≤ c * (1 - headroom)
      P95(mem_usage) ≤ m * (1 - headroom)
      OOMKill 概率 < 0.1%（基于历史数据估算）

得分函数：
  dp[c][m] = w1 * cpu_util(c) + w2 * mem_util(m) - w3 * waste_penalty(c, m)
  
  其中：
    cpu_util(c) = avg(cpu_usage) / c    （越高越好，但不能超 1.0）
    mem_util(m) = avg(mem_usage) / m
    waste_penalty(c, m) = max(0, c - p95_cpu) + max(0, m - p95_mem) * λ
    
  λ 是 CPU 和 Memory 浪费的相对权重
  默认 λ = 0.5（Memory 浪费稍微容忍一些，因 CPU 更弹性）
```

#### 2.3 转移方程 + 启发式

```
朴素 DP：
  for c in CPU_levels:
    for m in Memory_levels:
      dp[c][m] = score(c, m, history_data)
  
  时间复杂度：O(80 × 128 × T) = O(10240 × T)
  T 是历史数据点数（7 天 1min 粒度 = 10080）
  实测：~10s 单 Pod 求解
  
对于 100 Pod 的集群：~17min 求解全集群 → 太慢

启发式优化（v2.9 重点）：
  
  H1: 二分查找式裁剪
      CPU request 至少 = P50(usage)
      CPU request 至多 = P99(usage) * 1.5
      → 状态空间从 80 个降到 ~20 个
  
  H2: Memory 单调性
      若 m1 < m2 且 m1 满足约束，则 m2 也满足
      → DP 单调，找到最小满足 m 即可停止扩大
  
  H3: 业务模板预设
      根据业务类型（web / batch / streaming）预设搜索起点
      web:        CPU=100m, Mem=128MiB 起步
      batch:      CPU=500m, Mem=512MiB 起步
      streaming:  CPU=200m, Mem=256MiB 起步
      → 减少搜索范围
  
  H4: 时间分桶
      历史数据按天分桶
      只用最近 7 天精细数据 + 之前 23 天粗粒度数据
      → 数据点从 43200 降到 ~14000
  
经过启发式：
  时间复杂度：O(20 × 30 × 14000) = O(8.4M)
  实测：~500ms 单 Pod 求解
  100 Pod 集群：~50s（可接受）
```

#### 2.4 抖动处理

```
v2.9 的"抖动"指：

  抖动类型 1: 业务流量周期性
    例：白天 CPU 高，晚上 CPU 低
    问题：取 P95 会过度配置（晚上 80% 资源闲置）
  
  抖动类型 2: 突发 burst
    例：每天 9 点定时任务 → CPU 1min 内冲到 5 倍
    问题：取 P99 仍可能漏（短时 burst 数据点少）
  
  抖动类型 3: 业务转型
    例：服务从 v1 升级到 v2 → 资源模式根本变化
    问题：历史数据不再代表未来

对策：

  Type 1: 周期感知 sizing
    在历史数据里检测周期性
    给出"白天配置"和"晚上配置"两套
    标记给 v3.0 的运行时调度器消费
    （v2.9 只输出"白天最大值"作为部署时配置）
  
  Type 2: burst 缓冲
    requests = P95(usage) * (1 + headroom)
    limits = P99(usage) * 1.5
    （limits 给 50% 缓冲应对短时 burst）
  
  Type 3: 数据降权
    历史数据按时间衰减权重：
    weight = 0.5 ** (days_ago / 7)
    最近 7 天权重 1，14 天前权重 0.5，21 天前 0.25
    业务转型后，旧数据自然失效

兜底：
  数据不足（< 24h 历史）→ 用业务模板预设值
  算法异常（结果太离谱）→ 降级到"用户原配置"
```

#### 2.5 与 K8s VPA 的关系

```
K8s VPA（Vertical Pod Autoscaler）功能上重叠：
  - VPA 也是基于历史数据建议 resources.requests/limits
  - VPA 也支持 "Auto" 模式（自动应用建议）

差异：
  
  VPA：
    - K8s 集群级别的 controller
    - 持续运行，每 N 分钟更新一次
    - 应用 update 需要 Pod 重启（破坏性）
    - 建议偏保守（业务敏感）
    - 算法相对简单（基于 EWMA + 安全 margin）
  
  KubePivot v2.9：
    - 部署时（kp deploy）一次性算出最优值
    - 写入 GitOps 真相（resources.yaml）
    - 不需要 Pod 重启（部署时已是最优）
    - 算法可激进（DP + 启发式）
    - GitOps 友好（配置可审计可回滚）

共存模式：
  resources.yaml 加 sizing 字段：
  
  resources:
    - kind: Deployment
      name: wallet-service
      sizing:
        mode: auto              # auto / manual / vpa
        history-window: 7d
        headroom: 0.20
  
  mode = auto:
    KubePivot v2.9 算法
    部署时写入最优 requests/limits
  
  mode = manual:
    用户在 Helm chart 里写 requests/limits
    KubePivot 不干预（v2.9 之前的行为）
  
  mode = vpa:
    KubePivot 不算，配置 VPA controller 接管
    适用于"持续优化"场景

默认行为：
  v2.9 起，sizing 字段不写时默认 mode=manual（保持兼容）
  v3.0 起，可能切换默认值（视实测效果）
```

#### 2.6 具体实现包

```go
// internal/sizing/engine.go (草图)

type Engine interface {
    // Compute 单个 Pod 的最优 sizing
    Compute(ctx context.Context, req *Request) (*Recommendation, error)
}

type Request struct {
    Namespace      string
    PodName        string
    HistoryWindow  time.Duration
    Headroom       float64
    QoSClass       v1.PodQOSClass
    BusinessTemplate string  // "web" / "batch" / "streaming" / ""
}

type Recommendation struct {
    CPURequest    int64  // millicores
    CPULimit      int64
    MemoryRequest int64  // bytes
    MemoryLimit   int64
    
    Confidence    float64  // 0.0 - 1.0
    DataPoints    int
    HistoryRange  time.Duration
    
    // 调试信息
    Algorithm     string  // "dp" / "fallback-template" / "fallback-manual"
    SearchSpace   int     // 搜索的状态数
    SolveDuration time.Duration
}

// 实现：
//   internal/sizing/dp_solver.go        DP 求解器
//   internal/sizing/heuristics.go       4 个启发式
//   internal/sizing/templates.go        业务模板预设
//   internal/sizing/history.go          v2.7 metrics 集成
//   internal/sizing/jitter_detect.go    抖动检测
```

### 3. 任务清单

```
[ ] internal/sizing/engine.go (~400 行 + 测试 ~250 行)
    Engine 接口 + Request / Recommendation 类型

[ ] internal/sizing/dp_solver.go (~600 行 + 测试 ~400 行)
    DP 状态空间定义
    转移方程实现
    得分函数 + 权重调优

[ ] internal/sizing/heuristics.go (~400 行 + 测试 ~250 行)
    H1 二分查找式裁剪
    H2 Memory 单调性
    H3 业务模板预设
    H4 时间分桶

[ ] internal/sizing/jitter_detect.go (~300 行 + 测试 ~200 行)
    周期性检测（FFT / autocorrelation）
    burst 检测
    业务转型检测（数据漂移）

[ ] internal/sizing/templates.go (~150 行)
    web / batch / streaming / 自定义业务模板

[ ] internal/sizing/history.go (~250 行 + 测试 ~150 行)
    与 v2.7 MetricsClient 集成
    数据获取 + 聚合 + 衰减权重

[ ] cmd/kp/sizing.go (~200 行)
    kp sizing recommend --pod=xxx 命令
    kp sizing apply --resources-yaml=xxx
    kp sizing diff（当前 vs 推荐）

[ ] resources.yaml schema 扩展
    sizing 字段定义

[ ] 改造 cmd/kp/deploy.go
    部署前调 sizing.Engine.Compute()
    若 mode=auto，写入算出的 requests/limits

[ ] benchmark：在 example 工程上验证 70%+ 利用率

[ ] 文档（~600 行）
    docs/design/resource-sizing.md
    DP 算法推导
    启发式说明
    与 VPA 对比

合计：
  代码：~2300 行 + 测试 ~1250 行 = ~3550 行
  文档：~600 行
  benchmark + 验证：~1 周

工作量预估：4 周
```

### 4. 风险与对策

```
风险 1：DP 算法过于复杂导致 bug
  概率：高
  影响：算出离谱配置 → 部署失败 / OOM
  
  对策：
  - 完善的单测覆盖（>200 个 case）
  - 算法输出 + Confidence 字段
  - Confidence < 0.7 时降级到模板预设
  - 集成测试在真实集群跑 7 天观察
  - kp sizing diff 让用户审计推荐值

风险 2：历史数据不足时算法无效
  概率：高（新部署服务都没历史）
  影响：default 配置不准
  
  对策：
  - 数据 < 24h → 用业务模板
  - 数据 < 7d → DP 但 Confidence 低
  - kp init 让用户声明业务类型（web / batch / streaming）
    没数据时也能给出合理起点

风险 3：与 K8s VPA 冲突
  概率：中等
  影响：双方互相改 resources，进入无限循环
  
  对策：
  - resources.yaml sizing.mode 字段明确语义
  - mode=auto 时给 Deployment 加 annotation:
    kubepivot.io/sizing-managed: "true"
  - VPA 检测到此 annotation → 跳过
  - 文档明确：如果用 VPA，请 mode=vpa

风险 4：节点真实资源够不够装
  概率：高（本来就是 v3.0 解决的问题）
  影响：v2.9 算出"理想 sizing"，但节点装不下
  
  对策：
  - v2.9 阶段不管节点拓扑（明确范围）
  - 部署时如 K8s 调度器无法分配 → 失败提示用户
  - 提示用户："建议 v3.0 智能调度"或"扩容节点"
  - v3.0 才真正解决（维度 A bin packing）

风险 5：抖动处理不到位
  概率：中等
  影响：白天 OOM 或晚上资源浪费
  
  对策：
  - jitter_detect 作为 v2.9 重点
  - 实测 demo 验证周期性业务的处理
  - 留一个调试开关：kp sizing recommend --debug
    输出完整搜索空间 + 决策路径
```

### 5. 验收

```
功能验收：
  ✓ kp sizing recommend 输出合理建议
  ✓ Confidence 计算正确（数据少 → 低分）
  ✓ DP 求解时间 < 1s 单 Pod
  ✓ 业务模板覆盖 web / batch / streaming
  ✓ 数据漂移检测（业务转型）

性能验收：
  ✓ 在 demo 工程上验证：单 Pod CPU 利用率 70%+
  ✓ 在 demo 工程上验证：单 Pod Memory 利用率 70%+
  ✓ 7 天观察期内无 OOMKill
  ✓ 100 Pod 集群完整 sizing 在 1min 内完成

集成验收：
  ✓ 与 K8s VPA 共存测试（双 controller 不冲突）
  ✓ resources.yaml sizing 字段反序列化正确
  ✓ kp deploy --dry-run 显示推荐 sizing
  ✓ make dev 全绿
```

### 6. 工作量预估

```
4 周专注 = 28 天
分阶段：
  Week 1: DP 算法核心（dp_solver + 单测）
  Week 2: 启发式 + 抖动检测
  Week 3: 集成（resources.yaml / cmd/kp / controller）
  Week 4: benchmark + 验证 + 文档

关键里程碑：
  Day 7:  DP 单测通过（小规模 case）
  Day 14: 启发式优化达到 < 1s 求解
  Day 21: 真实集群 7 天观察期开始
  Day 28: v2.9.0 release（含真实数据）

DP 这块工作量最不确定，可能延期 1-2 周。
延期不打 tag，作为 v2.9.x 持续改进。
```

---

## v3.0 — Intelligent Scheduling System

### 1. 目标

```
KubePivot v2.x 章节的"成人礼"。
v3.0 不是 v2.9 + 一点，是**完整叙事的版本**。

核心论断：
  把 K8s 默认调度器 + VPA 的能力统一起来
  通过 GitOps 框架对外提供"智能调度"语义
  
具体范围：
  - 维度 A: 节点 × Pod × 资源类型（bin packing）
  - 维度 B: Pod × CPU × Memory（继承 v2.9）
  - 双 DP 协同求解
  - 周期性运行时重调度（5min / 15min）
  - 抖动检测 + 降级
  
目标：
  - 节点 CPU 利用率 85%+
  - 节点 Memory 利用率 70%+
  - 务实声明：不冲 98%
  - 业务零感知

不冲 98% 的理由：
  1. 98% 是单一公司方案在特定业务前提下的数据
  2. KubePivot 是通用工具，需要安全 margin
  3. 85% CPU + 70% Memory 已是行业领先（K8s 默认 ~50%）
  4. 留 15% 应对突发 burst + 节点维护
  5. 实事求是优于虚高指标
```

### 2. 关键设计

#### 2.1 双 DP 架构

```
v2.9 (维度 B) 已实现：
  Pod × (CPU, Memory) 求最优 sizing
  输出每 Pod 的 requests/limits

v3.0 加入维度 A：
  节点 × Pod × 资源类型
  Bin packing 问题：把 N 个 Pod 装进 M 个节点
  目标：最小化空闲资源 / 最大化节点利用率

形式化：
  输入：
    P = {p1, p2, ..., pn}      Pod 列表（含 v2.9 算出的 requests）
    N = {n1, n2, ..., nm}      节点列表（含 capacity）
    
    p_i.requests = (cpu_i, mem_i)
    n_j.capacity = (Cj, Mj)
    
  约束：
    affinity 约束（Pod 必须 / 不能在某节点）
    anti-affinity（同 Pod 副本分散）
    taints / tolerations
    topology spread constraints
    
  目标：
    最大化 sum_j (used_j / capacity_j)
    其中 used_j = sum_{i in node_j} p_i.requests

复杂度：
  K8s 调度本身是 NP-hard
  实际通过启发式 + DP 近似求解
```

#### 2.2 维度 A 的 DP 状态

```
状态定义：
  dp[j][s] = 在节点 j 上占用资源 s 时的最大装载得分
  s = (cpu_used, mem_used) 二元组（离散化）
  
  状态空间：
    j ∈ [1, M] 节点数
    cpu_used ∈ [0, max_cpu]，离散化 100 级
    mem_used ∈ [0, max_mem]，离散化 100 级
    总状态数：M × 100 × 100 = M × 10000

转移方程：
  dp[j][cpu_used][mem_used] = max(
    放不下任何新 Pod 的得分,                    // 不动
    {dp[j][cpu_used - p_i.cpu][mem_used - p_i.mem] + score(p_i, j)} 对每个未分配的 p_i
    {dp[j-1][...]} 切换到下一个节点
  )

得分函数：
  score(p_i, j) = 1 + topology_bonus(p_i, j) - constraint_penalty(p_i, j)
  
  topology_bonus: affinity 偏好满足度
  constraint_penalty: 违反硬约束（不能调度）
```

#### 2.3 双 DP 协同求解

```
朴素方案：
  Step 1: v2.9 单 Pod sizing（独立算每 Pod）
  Step 2: 维度 A bin packing（用 sizing 后的 requests）
  
问题：
  独立算 Pod sizing → 可能算出"理想但装不下的"
  需要双向反馈

协同方案：

  Outer Loop: 维度 A bin packing
    For each iteration:
      调用维度 B 的 sizing 引擎，可能调整 Pod requests
      尝试 pack 到节点
      若失败 → 标记需要降低 Pod requests → 进入下次迭代
  
  Inner Loop: 维度 B sizing
    Engine.Compute() 时接收"目标 requests 上限"参数
    在该上限下求解最优 utilization
  
  收敛条件：
    1. 所有 Pod 都能装进节点
    2. 总利用率不再上升 (delta < 0.5%)
    3. 迭代次数 ≤ 5（防死循环）

伪代码：
```

```go
func Schedule(pods []Pod, nodes []Node) (*Plan, error) {
    plan := &Plan{}
    iteration := 0
    
    for iteration < 5 {
        // Inner: 维度 B sizing（可能受 pod_request_caps 约束）
        for _, p := range pods {
            cap := plan.RequestCap[p.Name]  // 第一次为空
            r, err := sizing.Compute(p, cap)
            if err != nil {
                return nil, err
            }
            plan.Sizing[p.Name] = r
        }
        
        // Outer: 维度 A bin packing
        assignment, err := binPack(pods, nodes, plan.Sizing)
        if err == ErrCapacityExceeded {
            // 找最 "fit-failure" 的 Pod，降低 cap
            culprit := findCulprit(pods, plan.Sizing, nodes)
            plan.RequestCap[culprit] = plan.Sizing[culprit].CPURequest * 0.9
            iteration++
            continue
        }
        plan.Assignment = assignment
        return plan, nil
    }
    
    return nil, ErrSchedulerNoConverge
}
```

#### 2.4 周期性运行时重调度

```
v2.9 是部署时一次性算
v3.0 是部署时 + 运行时持续优化

为什么需要运行时？
  - 业务流量随时间变化
  - 节点添加 / 移除（弹性扩缩）
  - 新 Pod 加入 / 老 Pod 退出
  - 历史数据持续累积，模型变准

调度时机：
  T1: 部署时（kp deploy / sandbox commit）
  T2: 周期性（5min / 15min，可配置）
  T3: 事件驱动（节点 add/remove，Pod OOMKill）

调度算法：
  T1 / T3: 完整双 DP（可能耗时）
  T2: 增量调度（只重算被影响的 Pod）

增量调度：
  探测出"利用率不平衡"的节点
    例：某节点 CPU 30%，相邻节点 95%
  把高利用率节点的部分 Pod 迁移到低利用率节点
  代价：Pod 重启 → 业务感知
  
  约束：
    - 同时迁移的 Pod 数 ≤ 总 Pod 5%
    - 蓝绿 Pod 不迁移（v2.6 蓝绿语义已锁定）
    - StatefulSet 不主动迁移（数据敏感）
```

#### 2.5 抖动处理 + 降级

```
"高利用率"的本质风险：
  85% 利用率离 100% 只有 15% 缓冲
  突发 burst 容易撑爆
  必须有"快速降级"机制

抖动检测：
  
  detect_jitter(节点)：
    采样窗口：最近 5min
    指标：CPU usage 变化率 + 内存使用率
    判定：
      - 5min 内有 ≥3 次 spike (> 95% CPU)
      - 或 OOMKill 出现
      → 标记"jitter"
  
  if jitter:
    降级模式开启
    停止重调度
    把目标利用率从 85% 下调到 70%
    给所有 Pod requests * 1.2
    持续 30min
    然后重新评估

降级策略层级：
  Level 0: 正常运行（85% / 70%）
  Level 1: 抖动观察（70% / 60%）  ← 降低目标但不大改
  Level 2: 紧急降级（60% / 50%）  ← 保守模式
  Level 3: 用户介入（停止自动调度，等用户决策）

升级到 Level 3 的条件：
  - 连续 3 次自动降级仍出问题
  - 节点 OOMKill 频率 > 1/小时
  - 业务报警（外部信号）
```

#### 2.6 与 K8s 默认调度器的关系

```
K8s 默认调度器 (kube-scheduler) 已经做了很多事：
  - PriorityClass / Affinity / Taints
  - TopologySpreadConstraints
  - 抢占调度

KubePivot v3.0 的三种模式：

  模式 1: Mutating Admission Webhook
    Pod 创建时拦截，修改 spec.nodeSelector
    强制 Pod 到 KubePivot 算出的节点
    优点：不替换 K8s 调度器
    缺点：与 K8s 调度器决策不完全协调
    工作量：低

  模式 2: 替换调度器（custom scheduler）
    Pod 用 spec.schedulerName: kubepivot-scheduler
    KubePivot 自己实现 scheduling extender
    优点：完全控制
    缺点：复杂，需要实现完整 K8s scheduling framework
    工作量：高
    
  模式 3: GitOps 间接调度
    KubePivot 不实时干预调度
    部署时通过 resources.yaml 写死 nodeSelector / nodeAffinity
    K8s 默认调度器执行
    优点：与现有 K8s 完全兼容
    缺点：无法做运行时重调度（只能改 resources.yaml 重部署）
    工作量：中等

v3.0 选择：模式 1 + 模式 3 混合

  原因：
  - 模式 2 工程量太大，不在 v3.0 范围
  - 模式 1 处理"运行时调度优化"
  - 模式 3 处理"部署时 GitOps 真相"
  
  分工：
  - 部署时（kp deploy）→ 模式 3
  - 周期性优化（5min/15min）→ 模式 1
  - 事件驱动（OOM 等）→ 模式 1

模式 1 实现：
  internal/scheduler/webhook.go
  注册为 K8s MutatingWebhookConfiguration
  webhook 收到 Pod 创建请求 → 调 KubePivot scheduler → 写 nodeSelector
```

#### 2.7 观测性 + 告警

```
v3.0 必须可观测：

Prometheus metrics：
  kubepivot_scheduler_iterations_total
  kubepivot_scheduler_solve_duration_seconds
  kubepivot_node_utilization_cpu_ratio
  kubepivot_node_utilization_memory_ratio
  kubepivot_pod_oomkill_total
  kubepivot_scheduler_fallback_level
  kubepivot_pod_migration_total

Grafana dashboard:
  - 集群整体利用率（CPU / Memory）
  - 节点利用率分布
  - 调度器决策时间
  - 抖动级别历史
  - Pod 迁移频率

告警规则：
  - 节点 CPU > 95% 持续 5min   → warning
  - 节点 Memory > 90% 持续 5min → warning
  - OOMKill > 1/小时           → critical
  - 调度器 fallback level ≥ 2  → critical
```

### 3. 任务清单

```
[ ] internal/scheduler/ 包基础（~600 行 + 测试 ~400 行）
    - scheduler.go            Schedule 接口
    - plan.go                 Plan / Assignment 类型
    - errors.go               调度器错误

[ ] 维度 A bin packing 实现（~800 行 + 测试 ~600 行）
    - bin_pack.go             DP 求解
    - constraints.go          affinity / taints / topology
    - heuristics.go           first-fit-decreasing 变种

[ ] 双 DP 协同（~500 行 + 测试 ~400 行）
    - coordinator.go          Outer/Inner loop
    - convergence.go          收敛判定
    - culprit_finder.go       找 fit-failure 的 Pod

[ ] 运行时重调度（~600 行 + 测试 ~400 行）
    - rescheduler.go          周期性触发
    - incremental.go          增量调度
    - migration.go            Pod 迁移执行

[ ] 抖动处理 + 降级（~400 行 + 测试 ~250 行）
    - jitter_monitor.go       抖动检测
    - degradation.go          多级降级
    - emergency.go            紧急降级 → Level 3

[ ] Mutating Webhook（~500 行 + 测试 ~300 行）
    - webhook.go              admission webhook
    - server.go               TLS server
    - cert.go                 证书管理

[ ] 观测性（~400 行）
    - metrics.go              Prometheus 暴露
    - dashboard.json          Grafana dashboard 模板
    - alerts.yaml             Prometheus alert 规则

[ ] cmd/kp 命令扩展
    - kp scheduler status     当前调度状态
    - kp scheduler explain    解释 Pod 调度决策
    - kp node utilization     节点利用率视图

[ ] resources.yaml schema 扩展
    - scheduler:
        mode: auto | manual | hybrid
        target-cpu-utilization: 0.85
        target-mem-utilization: 0.70

[ ] 文档（~1000 行）
    - docs/design/scheduler.md
    - docs/example-scheduler/  完整 demo 工程

[ ] benchmark
    - 压测 100 / 500 / 1000 节点规模
    - 与 K8s 默认调度器对比

合计：
  代码：~3800 行 + 测试 ~2350 行 = ~6150 行
  文档：~1000 行
  benchmark + 真实集群验证：~2 周

工作量预估：6 周
```

### 4. 风险与对策

```
风险 1：双 DP 不收敛
  概率：中等
  影响：调度无法完成，部署阻塞
  
  对策：
  - 迭代上限 5 次
  - 收敛失败 → 降级到 v2.9 单 Pod sizing + K8s 默认调度
  - 完整测试覆盖（包括极端数据）

风险 2：运行时调度引发雪崩
  概率：高（这是高风险特性）
  影响：Pod 迁移引发连锁反应，集群不稳定
  
  对策：
  - 同时迁移上限严格执行（≤ 5%）
  - StatefulSet / 蓝绿 Pod 不迁移
  - 迁移前 health check（业务有警告则取消迁移）
  - 紧急 kill switch：kp scheduler pause

风险 3：实测利用率达不到 85%
  概率：高（v2.9 都还没验证 70% 单 Pod）
  影响：v3.0 价值打折
  
  对策：
  - 务实接受
  - 75% 也是行业领先
  - 文档明确"目标 75-85%，不冲 98%"
  - benchmark 数据公开，让用户自己判断

风险 4：与 K8s 默认调度器冲突
  概率：中等
  影响：决策不一致，行为奇怪
  
  对策：
  - 模式 1 webhook 优先级低于 K8s 默认
  - 与默认决策冲突时让步
  - 通过 PriorityClass 协调
  - 大量集成测试

风险 5：调度算法本身的 bug
  概率：高（这是大型工程）
  影响：算错节点 → Pod 启动失败
  
  对策：
  - 完整单测覆盖（>500 case）
  - 模拟集群仿真测试
  - 渐进 rollout（5% → 20% → 50% → 100%）
  - 一键回退（kp scheduler disable）

风险 6：性能不达标
  概率：中等
  影响：1000 节点规模调度太慢
  
  对策：
  - 调度阶段化（部署时 vs 运行时）
  - 启发式优化持续迭代
  - 大规模场景文档说明限制
  - v3.1 持续优化
```

### 5. 验收

```
功能验收：
  ✓ 100 节点 + 500 Pod 调度完成时间 < 30s
  ✓ 1000 节点规模 < 5min（启发式 + 增量）
  ✓ 双 DP 收敛率 > 95%
  ✓ Webhook 部署后调度延迟 < 1s（单 Pod）
  ✓ 运行时重调度 5min 周期跑稳定

性能验收：
  ✓ 节点 CPU 利用率 75%+ 在 demo 工程
  ✓ 节点 CPU 利用率 85%+ 在大规模场景（500+ Pod）
  ✓ 节点 Memory 利用率 65%+ 持续
  ✓ 调度器自身 CPU < 10% 单 controller pod

集成验收：
  ✓ 与 v2.9 sizing 引擎深度集成
  ✓ 与 v2.7 informer / metrics 集成
  ✓ 与 v2.5 sharding 协同
  ✓ 与 K8s 默认调度器无冲突
  ✓ 真实集群 30 天观察期
```

### 6. 工作量预估

```
6 周专注 = 42 天
分阶段：
  Week 1-2: 维度 A bin packing 算法
  Week 3:   双 DP 协同
  Week 4:   运行时重调度 + 抖动处理
  Week 5:   Mutating Webhook + 观测性
  Week 6:   集成 + benchmark + 文档

关键里程碑：
  Day 14: 维度 A 单测通过
  Day 21: 双 DP 协同跑通
  Day 28: webhook 部署测试
  Day 35: 100 节点压测
  Day 42: v3.0.0 release

v3.0 是大版本，如延期不打 tag，作为 v3.0-rc 持续迭代。
```

---

## v3.x+ — 多租户 + 商业化探索（远期备忘）

### 触发条件

```
v3.x+ 不是"等 v3.0 后立刻做"，是"等以下任一条件发生"：

  1. 有真实多租户需求的客户出现
  2. KubePivot 在 GitHub 收获 1000+ stars
  3. 商业方向明确（KubePivot Cloud / 商业版 / 咨询）
  4. qc 决定将 KubePivot 作为主业（不再仅作开源项目）

任一条件不满足 → 不主动做 v3.x+
v3.0 后可能进入"维护期"或"v4.0 探索期"
```

### 设计草图（备忘）

```
v3.1: 多租户基础
  organization 数据模型（etcd 新 namespace）
  跨 org 隔离（namespace 前缀 + RBAC）
  资源配额（CPU / Memory / 项目数）
  
v3.2: 计费 + 监控
  按 org 聚合资源使用
  使用量导出（Prometheus / Cloud Billing）
  per-org dashboard
  
v3.3: KubePivot Cloud（如方向确认）
  SaaS 化部署
  Web UI（虽然之前说不做）
  托管模式
  
v4.0: 全面重构（如需要）
  Web UI 不再可选
  云原生架构（Operator pattern 完整化）
  可能引入 client-go（如数据驱动决定）
```

### 商业方向探讨（待定）

```
可能的商业模式：
  
  A. 开源 + 企业版双轨
     企业版加：SLA / 7x24 支持 / 高级功能
     社区版完整可用
     例：GitLab / Sentry 模式
  
  B. SaaS 化
     KubePivot Cloud（kubepivot.io）
     按 org / Pod 计费
     例：Vercel / Railway 模式
  
  C. 咨询 + 定制
     不做 SaaS，提供定制化部署 + 咨询
     企业一次性付费
     例：早期 Docker / Confluent 模式
  
  D. 不商业化
     纯开源 + 个人维护
     qc 通过其他工作维持生计
     KubePivot 作为长期工程作品

所有选项都不强迫提前决定。
v3.0 完成时再回头看 KubePivot 的真实定位 + 用户群体，
依据数据决定。
```

---

## 时间线与依赖

### 总时间线

```
2026-04-26  v2.6.0 release（起点）
                ↓
2026-05      v2.7 Event Stream Infrastructure (~3 周)
                ↓
2026-05/06   v2.8 Enterprise Governance (~3 周)
                ↓
2026-06      v2.8.1 v2.x 收尾 (~1 周)
                ↓
2026-06/07   v2.9 Resource Sizing Engine (~4 周)
                ↓
2026-07/09   v3.0 Intelligent Scheduling System (~6 周)
                ↓
2026-09      v3.0 release（预计）

合计：~17 周 ≈ 4 个月
现实 buffer：+25%
                ≈ 5 个月

v3.0 实际 release：2026-09 至 10 月
```

### 依赖关系图

```
v2.6.0 ──┐
         ├──> v2.7 (Event Stream)  ────┐
         │                              │
         │                              ↓
         ├──> v2.8 (Enterprise)        │ 
         │           │                  │
         │           └──> v2.8.1        │
         │                              │
         │                              ↓
         └─────────────> v2.9 (Sizing) ─┐
                                        │
                                        ↓
                                       v3.0 (Scheduler)
                                        │
                                        ↓
                                       v3.x+ (待定)
```

### 关键路径

```
v2.7 → v2.9 → v3.0 是关键路径
  v2.7 metrics + informer 是 v2.9 / v3.0 的基础
  v2.9 sizing 是 v3.0 维度 B 的基础
  
v2.8 是并行任务
  与 v2.7 / v2.9 关键路径无强依赖
  可以与 v2.7 平行开发（如团队规模允许）
  qc 一个人则按顺序

v2.8.1 是收尾任务
  应在 v2.8.0 → v2.9.0 之间穿插完成
```

---

## 风险全景

```
技术风险（按严重程度排序）：

  1. v3.0 双 DP 协同 + 运行时重调度
     概率：高
     影响：v3.0 核心功能
     缓解：渐进 rollout + 紧急 kill switch
     
  2. v2.9 DP 算法的算错风险
     概率：高
     影响：单 Pod 部署失败 / OOM
     缓解：完善单测 + Confidence 字段 + fallback
     
  3. v2.7 自研 Cache 性能不如 client-go
     概率：中等
     影响：v2.7 价值打折
     缓解：benchmark 优先 + 诚实承认
     
  4. v3.0 与 K8s 默认调度器冲突
     概率：中等
     影响：调度行为奇怪
     缓解：模式 1+3 混合 + 集成测试
     
  5. v2.8 SSO / 加密的多 provider 维护
     概率：中等
     影响：长期维护负担
     缓解：聚焦 Dex 兜底 + 测试矩阵
     
  6. v2.9 / v3.0 大规模性能不达标
     概率：中等
     影响：1000+ 节点用户用不上
     缓解：持续迭代 + 文档明确限制

时间风险：

  1. v2.9 / v3.0 工作量低估
     概率：高
     影响：v3.0 release 推迟到 2026 年底
     缓解：buffer 25% + 关键功能优先

  2. 单人开发的疲劳累积
     概率：中等
     影响：质量下降 / 健康问题
     缓解：每周休整 + 不连续高密度

商业风险（远期）：

  1. v3.0 后没有用户反馈 / 没人用
     概率：中等
     影响：v3.x+ 方向不清
     缓解：v2.8 web3-blitz 升级形成案例

  2. 类似项目竞争（k8sgpt / Karpenter / Goldilocks）
     概率：高（这些已经存在）
     缓解：KubePivot 差异化在 GitOps 视角下的统一调度
     
  3. 商业化时机难判断
     概率：中等
     缓解：v3.0 完成后回头看 + 不强迫提前决定
```

---

## 编辑记录

```
2026-04-26  ROADMAP.md 创建（v2.6.0 release 当日下午）
            分 4 批编写：
              批 1: 摘要 + v2.7 (~430 行)
              批 2: v2.8 + v2.8.1 (~440 行)
              批 3: v2.9 (~400 行)
              批 4: v3.0 + v3.x+ + 时间线 + 风险全景 (~430 行)
            合计 ~1700 行
            
            写作约定：
              Q1=A 高度技术（含 Go 接口草图 + DP 状态定义）
              Q2=灵感源段落删除（"好肉麻"）
              Q3=B 简短商业方向（v3.x+ 章节）
              
            灵感源备注：
              v2.7 Cache 优化 + v2.9-v3.0 三维 DP
              受到组里大 10 岁老哥方案的影响
              不是抄，是消化
```
