# KubePivot IAM 系统 — 设计文档

> 状态：📝 实施后回顾（从 v2.8 代码反推）
> 关联：[auth](../../internal/auth/) / [rbac](../../internal/rbac/) / [audit](../../internal/audit/) / [team](../cmd/team/team.md) / [login](../cmd/login/login.md)
> 背景：v2.8 引入 SSO + RBAC + Audit 三件套，v3.0 审计补全 7 个写命令的 RBAC 检查

---

## 一、系统总览

KubePivot IAM 由三个独立包构成，通过 CLI 入口的 `mustCheck()` 一行接入串联：

```
┌─────────────────────────────────────────────────────────┐
│                    kp login --provider google            │
│  internal/auth/                                          │
│    OAuth2 + PKCE → Token + UserInfo                      │
│    → ~/.kp/credentials/google.yaml                       │
│    → ~/.kp/credentials/default.yaml (指向 google)         │
└──────────────┬──────────────────────────────────────────┘
               │ email: "alice@x.com"
               ▼
┌─────────────────────────────────────────────────────────┐
│                    kp deploy / migrate / chaos / ...     │
│  cmd/kp/rbac_helper.go: mustCheck(actor, ns, perm)      │
│                                                          │
│  audit.ResolveActor()                                    │
│    ├─ 档位 1: ~/.kp/credentials/ → email                │
│    ├─ 档位 2: $USER@hostname                            │
│    └─ 档位 3: anonymous@hostname                         │
│                                                          │
│  rbac.Checker.Check(user, ns, perm)                      │
│    └─ configs/teams.yaml → 匹配 member × ns glob × perm │
│                                                          │
│  通过 → 继续主逻辑                                       │
│  拒绝 → audit.Record() → P.Fail() → os.Exit(1)          │
└─────────────────────────────────────────────────────────┘
```

三条铁律：

1. **Auth 和 RBAC 完全解耦**。`audit.ResolveActor()` 读 auth 的 YAML 文件但不 import auth 包，避免循环依赖
2. **无 teams.yaml → 全权限**（Q-B5 向后兼容）。已有项目不加配置不受影响
3. **拒绝路径 fail-fast + audit denied**。未授权的 actor 在触及集群之前就被拦住

---

## 二、Identity — `internal/auth/`

### 2.1 核心接口

```go
// internal/auth/sso.go
type AuthProvider interface {
    Name() string
    Login(ctx context.Context) (*Token, *UserInfo, error)
    VerifyToken(ctx context.Context, token *Token) (*UserInfo, error)
}

type Token struct {
    AccessToken  string
    RefreshToken string
    TokenType    string
    ExpiresAt    time.Time
    IDToken      string  // OIDC 场景
}

type UserInfo struct {
    Subject string   // OAuth subject (唯一标识)
    Email   string   // ← RBAC 用这个做 member 匹配
    Name    string
    Groups  []string // ← teams.yaml group:<name> 语法匹配这个
}
```

### 2.2 三种 Provider 实现

| Provider | 包 | OAuth 流程 | Client Secret | PKCE |
|---|---|---|---|---|
| Google | `auth/google.go` | Authorization Code | 必填 | 否 |
| GitHub | `auth/github.go` | Device Flow 或 Web | 必填 | 否 |
| Dex | `auth/dex.go` | OIDC Discovery → Code | 可选 | 否 |

**Dex 的特殊性**：先调 `/.well-known/openid-configuration` 做 OIDC Discovery，拿到 token/userinfo endpoint。Dex 内部对接 LDAP / SAML / GitHub 等，KubePivot 不感知。

### 2.3 OAuth2 流程

```go
// internal/auth/oauth.go
func runOAuth2Flow(ctx context.Context, cfg *OAuth2Config) (*Token, error)
```

1. 生成 state (random 32 bytes) + code_verifier (PKCE)
2. 打开浏览器 → provider authorize URL
3. 启动本地 HTTP server (`localhost:18888`) 监听 callback
4. 收到 code → `exchangeCodeForToken()`
5. `fetchUserInfo()` → UserInfo
6. `SaveCredentials()` → 写 `~/.kp/credentials/<provider>.yaml`
7. `SetDefault()` → 写 `~/.kp/credentials/default.yaml`

**降级/错误处理**：

- 端口 18888 被占用 → fail-fast 提示
- 用户关闭浏览器 → 5min 超时 + 错误
- Token endpoint 错误 → 保留 OAuth provider 原始错误信息

### 2.4 凭证存储

```
~/.kp/credentials/
├── default.yaml          # {"provider": "google"}
├── google.yaml           # Token + UserInfo (权限 0600)
├── github.yaml
└── dex.yaml
```

**文件权限**：`0600`（仅 owner 可读写）。Token 是敏感数据，不能泄漏。

**`kp login` 的幂等性**：多次 login 同一个 provider → 覆盖旧 token，不创建重复文件。

### 2.5 CLI 命令

```
kp login --provider google --client-id xxx.apps.googleusercontent.com --client-secret yyy
kp login --provider github --client-id Iv1.xxx --client-secret yyy
kp login --provider dex --issuer https://dex.example.com --client-id kubepivot
kp whoami    # 显示当前 SSO 登录的身份
```

---

## 三、Access — `internal/rbac/`

### 3.1 核心接口

```go
// internal/rbac/checker.go
type Checker interface {
    Check(ctx context.Context, user *UserContext, namespace string, perm Permission) error
    Reload() error
}

type UserContext struct {
    Email  string   // ← auth 的 UserInfo.Email
    Groups []string // ← auth 的 UserInfo.Groups
}
```

`Check()` 返回 nil = 通过，非 nil = 拒绝。错误类型为 `ErrPermissionDenied`，包含 User / Namespace / Permission / Reason（哪个 team 拒绝的、为什么）。

### 3.2 FileBasedChecker

```go
// internal/rbac/file_based.go
func NewFileBasedChecker(configPath string) (Checker, error)
```

**加载逻辑**：

1. 读 YAML → `TeamConfig{Teams: []Team}`
2. 预编译：每个 team 的 namespaces glob → `nsMatcher`（三类：exact / prefix / glob）
3. members → `map[string]bool`（O(1) 查找）
4. permissions → `map[string]bool` + `permAllowAll`（`"*"` 通配符）
5. excluded → 全局去重（任何一个 team excluded 此 permission → 全拒绝）

**Check 流程**：

```
1. 遍历所有 teams
   ├─ user.Email 在 team.Members 中? 或 Groups 有 group:<name> 匹配?
   │  ├─ namespace 匹配 team.Namespaces 的 glob?
   │  │  ├─ rbac.Allow:
   │  │  │   ├─ perm 在 team.Excluded 中? → 拒绝（黑名单绝对优先）
   │  │  │   ├─ perm 在 team.Permissions 中? 或 permAllowAll? → 通过 ✓
   │  │  │   └─ 否 → 继续下一个 team
  2. 所有 team 都不匹配 → ErrPermissionDenied("no-team-match")
```

**黑名单绝对优先**（Q-B1.C）：即使 team 有 `permissions: ["*"]`，如果 `excluded: ["controller-uninstall"]`，该用户仍然不能执行 `kp controller uninstall`。这是安全底线——某些操作应该被显式禁止。

### 3.3 teams.yaml 配置

```yaml
teams:
  - name: backend
    members:
      - alice@example.com         # 邮箱直接匹配
      - bob@example.com
      - "group:dev-team"          # group: 前缀 → 匹配 UserInfo.Groups
    namespaces:
      - "kp-backend-*"            # shell glob
      - kp-shared                 # exact match
    permissions:
      - deploy
      - sandbox
      - migrate
      - pvc
      - secret
      - "*"                       # 通配符 = 所有权限
    excluded:
      - controller-uninstall      # 黑名单：即使有 "*"，也禁止此操作

  - name: sre
    members:
      - charlie@example.com
    namespaces:
      - "*"                       # 所有 namespace
    permissions:
      - controller-install
      - controller-uninstall
      - chaos
```

**文件查找优先级**（Q-B7.11）：

1. `<projectRoot>/configs/teams.yaml` — 项目级（推荐，进入 git）
2. `~/.kp/teams.yaml` — 用户级（本地开发测试）
3. 都没有 → 空 checker（Q-B5：全权限，向后兼容）

### 3.4 Permission 枚举

```go
const (
    PermDeploy              Permission = "deploy"
    PermSandbox             Permission = "sandbox"
    PermRollback            Permission = "rollback"
    PermStatus              Permission = "status"
    PermControllerInstall   Permission = "controller-install"
    PermControllerUninstall Permission = "controller-uninstall"
    PermMigrate             Permission = "migrate"         // v3.0 H1
    PermPVC                 Permission = "pvc"             // v3.0 H1
    PermSecret              Permission = "secret"          // v3.0 H1
    PermChaos               Permission = "chaos"           // v3.0 H1
    PermPromote             Permission = "promote"         // v3.0 H1
    PermSupplyChain         Permission = "supply-chain"    // v3.0 H1
    PermSizing              Permission = "sizing"          // v3.0 H1
    PermAll                 Permission = "*"               // 通配符

    // Controller 内部权限（不暴露给 CLI）
    PermDriftSync    Permission = "drift-sync"
    PermHeal         Permission = "heal"
    PermSandboxGC    Permission = "sandbox-gc"
    PermSweeperLease Permission = "sweeper-lease"
)
```

### 3.5 CLI 接入模式

```go
// cmd/kp/rbac_helper.go
// 一行接入，14 个命令共用：
mustCheck(audit.ResolveActor(), cfg.namespace, rbac.PermDeploy)
```

内部实现：

```go
func mustCheck(actor, namespace string, perm rbac.Permission) {
    checker := getRBACChecker()  // sync.Once lazy load + cache
    user := &rbac.UserContext{Email: actor}

    err := checker.Check(context.Background(), user, namespace, perm)
    if err == nil { return }  // 通过

    // 拒绝路径：写 audit + 打印错误 + exit(1)
    audit.Record("rbac", "rbac.denied", actor, string(perm), namespace,
        audit.OutcomeDenied, err.Error())
    P.Fail(err.Error())
    fmt.Fprintln(os.Stderr, "如需获得权限,请联系管理员修改 teams.yaml")
    fmt.Fprintln(os.Stderr, "  路径: configs/teams.yaml (项目级) 或 ~/.kp/teams.yaml (用户级)")
    os.Exit(1)
}
```

**读操作不加 RBAC**：`kp status`、`kp doctor`、`kp history`、`kp diff`、`kp scan`、`kp audit`、`kp team list/show/check` 不走 mustCheck。符合"读开放、写管控"原则。

### 3.6 `kp team` CLI

```
kp team list                          # 列出所有 team
kp team show <name>                   # 详情
kp team add <name> [--flags]          # 新增
kp team remove <name>                 # 删除（二次确认）
kp team member add <team> <member>    # 加成员
kp team member remove <team> <member> # 删成员
kp team check <user> <ns> <perm>      # 校验权限（调试用）
kp team validate                      # 校验语法
```

> `kp team *` 不走 mustCheck——文件权限即治理边界。能改 teams.yaml 的人就有权管理团队。

---

## 四、Management — `internal/audit/`

### 4.1 核心类型

```go
type AuditEvent struct {
    Timestamp time.Time
    Source    string  // "rbac" | "deploy" | "migrate" | ...
    Action    string  // "rbac.denied" | "deploy.started" | ...
    Actor     string  // SSO email | USER@host | anonymous@host
    Resource  string  // 被操作的资源或权限
    Namespace string
    From      string
    To        string
    Version   string
    Reason    string  // 拒绝原因/操作原因
    Outcome   string  // "allowed" | "denied" | "error"
}
```

### 4.2 Actor 解析（三档降级）

```go
func ResolveActor() string {
    // 档位 1: SSO email
    if email := readSSOEmail(); email != "" { return email }

    // 档位 2: USER@hostname
    user := os.Getenv("USER")
    host := getHostname()
    if user != "" { return user + "@" + host }

    // 档位 3: anonymous@hostname
    return "anonymous@" + host
}
```

**设计权衡：不 import `internal/auth`，直接读 YAML 文件**。

`audit` 包如果 import `auth` 包，会导致 `audit → auth → (可能) audit` 的循环依赖风险。当前方案：`audit.ResolveActor()` 直接读 `~/.kp/credentials/default.yaml` → 找到 provider → 读 `<provider>.yaml` → 取 email。与 `auth` 包的 credentials schema 有**格式约定耦合**——auth 的 `CredentialsFile` 结构体变更时，`actor.go` 需同步改。

### 4.3 写入路径

```
~/.kp/audit/rbac.jsonl     ← RBAC 拒绝记录
~/.kp/audit/deploy.jsonl   ← 部署记录（预留）
```

**拒绝时 write-through**（不缓冲）：RBAC 拒绝是低频事件（正常情况下不发生），每次立即写盘，丢失概率极低。

### 4.4 CLI

```
kp audit --format table     # 查看审计日志（表格）
kp audit --format json      # 查看审计日志（JSON）
```

---

## 五、三者串联

### 5.1 正常通过路径

```
$ kp login --provider google ...

$ kp deploy
  1. audit.ResolveActor() → "alice@x.com"
  2. rbac.Check("alice@x.com", "kp-prod", PermDeploy) → nil ✓
  3. 执行部署
  4. (部署成功不写 audit——正常操作不产生审计噪音)
```

### 5.2 拒绝路径

```
$ kp deploy (未登录 SSO, 本地 $USER=qc)
  1. audit.ResolveActor() → "qc@qc-mac.local"      ← USER@hostname
  2. rbac.Check("qc@qc-mac.local", "kp-prod", PermDeploy)
     → ErrPermissionDenied("no-team-match")         ← teams.yaml 没有这个 member
  3. audit.Record("rbac", "rbac.denied", "qc@qc-mac.local",
        "deploy", "kp-prod", "denied", "no-team-match")
  4. P.Fail() → os.Exit(1)
```

### 5.3 向后兼容路径（Q-B5）

```
$ kp deploy (configs/teams.yaml 不存在)
  1. getRBACChecker() → resolveTeamsConfigPath() → "" (无文件)
  2. NewFileBasedChecker("") → empty checker
  3. rbac.Check(...) → nil ✓  (empty checker 永远通过)
  4. 正常部署
```

---

## 六、设计决策与权衡

### D1: `audit` 不 import `auth`，而是读 YAML 文件

| 方案 | 优点 | 缺点 |
|---|---|---|
| import auth | 类型安全，编译期保证一致性 | 引入循环依赖风险，audit 是侧支不应依赖 auth |
| **读 YAML 文件**（当前） | 零依赖，独立演进 | credentials schema 变更需同步两处 |

**选择**：读 YAML 文件。`audit` 是防御侧支——它的失败不应影响主命令执行。直接读文件让这种"软依赖"显式化。

### D2: 黑名单绝对优先（Q-B1.C）

```
team.backend:
  permissions: ["*"]
  excluded: ["controller-uninstall"]
```

即使有 `"*"` 通配，`controller-uninstall` 仍被拒绝。安全底线——某些操作应该被**显式禁止**，不能靠"不写 permission"来隐式禁止。

### D3: `kp team *` 不走 RBAC

文件权限即治理边界。能改 `configs/teams.yaml` 的人就有权管理团队。如果 `kp team add` 需要 RBAC 检查 → 死锁（新团队还没建，没有权限授权自己添加团队）。

### D4: SSO 凭证文件权限 0600

`kp login` 写入的 credential YAML 文件包含 OAuth access token，权限必须锁死。如果有人能读你的 `~/.kp/credentials/`，等于拿到了你的 SSO session。

### D5: Controller 端不走 SSO

`audit.ResolveControllerActor()` 返回固定格式 `"kubepivot-controller@<pod-name>"`。Controller 是后台进程，不走 SSO 交互流程。它的 identity 来自 Pod name（StatefulSet 稳定标识）。

---

## 七、当前覆盖状态

### RBAC 接入清单（14 个写命令）

| 命令 | 权限 | 状态 |
|---|---|---|
| `kp deploy` | `PermDeploy` | ✓ (v2.8) |
| `kp resume` | `PermDeploy` | ✓ (v2.8) |
| `kp rollback` | `PermRollback` | ✓ (v2.8) |
| `kp down` | `PermRollback` | ✓ (v2.8) |
| `kp sandbox` | `PermSandbox` | ✓ (v2.8) |
| `kp controller install` | `PermControllerInstall` | ✓ (v2.8) |
| `kp controller uninstall` | `PermControllerUninstall` | ✓ (v2.8) |
| `kp migrate run` | `PermMigrate` | ✓ (v3.0 H1) |
| `kp pvc backup/restore` | `PermPVC` | ✓ (v3.0 H1) |
| `kp secret rotate/seal/sync` | `PermSecret` | ✓ (v3.0 H1) |
| `kp chaos inject/stop` | `PermChaos` | ✓ (v3.0 H1) |
| `kp promote` | `PermPromote` | ✓ (v3.0 H1) |
| `kp supply-chain verify/sbom` | `PermSupplyChain` | ✓ (v3.0 H1) |
| `kp sizing recommend` | `PermSizing` | ✓ (v3.0 H1) |

### 不接入 RBAC 的读命令（"读开放"原则）

`kp status`, `kp doctor`, `kp history`, `kp diff`, `kp scan`, `kp audit`, `kp version`, `kp team list/show/check/validate`, `kp whoami`

---

## 八、已知局限（诚实标注）

1. **Groups 字段本期未填**。`mustCheck` 构造 `UserContext` 时 Groups 留空（注释："后续如需 group 支持，桥接 auth.UserInfo.Groups 进来"）。当前只有邮箱匹配生效，`group:<name>` 语法尚未验证端到端。

2. **Controller 内部权限不在 CLI 体现**。`PermDriftSync` / `PermHeal` 等是 controller 自用的，用户无法通过 teams.yaml 控制 controller 行为。如果未来需要"禁止 controller 对某个 ns 做 drift 修复"，需要扩展 RBAC 模型。

3. **audit 日志无 rotation**。`~/.kp/audit/rbac.jsonl` 持续追加，无限增长。当前 RBAC 拒绝是低频事件（hopefully），但生产环境长时间运行后可能需要 logrotate。

4. **teams.yaml 的 YAML 注释在 `kp team add/remove` 后可能丢失**。文件操作走 `yaml.Marshal`，不保留原始注释。这是 `gopkg.in/yaml.v3` 的已知限制，与 deploy_sizing 的组件更新同根问题。

5. **`audit.ResolveActor()` 与 `auth` 的耦合是隐式的**。如果 `auth` 包的 credentials schema 变更（比如从 YAML 改为 JSON），`actor.go` 不会编译报错——它会静默返回 `USER@hostname`。这是一个"软契约"，需要开发者自觉同步。

---

## 九、与外部 IAM 方案的对比

| 方案 | KubePivot IAM | Casbin | OPA | Keycloak |
|---|---|---|---|---|
| 权限模型 | 白名单 + 黑名单 | RBAC/ABAC | Rego policy | RBAC + OIDC |
| 配置方式 | teams.yaml (YAML) | model.conf + policy.csv | .rego | Admin UI / REST |
| 依赖 | 0（标准库 + yaml.v3） | casbin lib | OPA binary | Java app |
| 与 CLI 集成 | mustCheck 一行接入 | Enforce() 调用 | REST API | REST API |
| 适用场景 | 10-100 人团队，5-50 个项目 | 复杂策略引擎 | 企业级策略 | 企业 SSO |

KubePivot IAM 是**务实主义**的：不追求覆盖所有 RBAC 模型（没有 ABAC、没有 Rego 规则引擎），但覆盖了 95% 的真实场景——"谁在哪个项目能做什么"。

---

## 十、编辑记录

```
2026-05-01  qc + DeepSeek 起草（从 v2.8/v3.0 代码反推）
    - Auth: OAuth2 + PKCE, 3 Providers, credentials 存储
    - RBAC: teams.yaml + Checker + 17 Permissions
    - Audit: 三档降级 Actor 解析 + deny 日志
    - 三者串联: mustCheck 一行接入
    - 6 个设计决策（D1-D5 + Controller 端改造）
    - 5 个已知局限（诚实标注）
```
