# SNAPSHOT — dev-toolkit

> 项目整体快照，新会话开始时直接扔给 Claude，5 秒对齐，继续工作。
> 最后更新：2026-03-30 / v1.3.0

---

## 项目定位

**dev-toolkit = 企业级 Kubernetes 研发脚手架 + 部署运维工具链**
面向：出海业务、中小团队/企业客户、合规强要求场景
核心价值：让用户零成本获得合规基线

---

## 版本路线图

```
v1.0.0  多服务独立 release + 拓扑排序 + 级联 rollback  🏆
v1.1.0  controller e2e + status 多 release + rollback 进度 + init dry-run  ✅
v1.2.0  安全合规基线（Secret/Pod 安全/Network Policy/doctor 安全检查）  ✅
v1.3.0  供应链安全（Trivy CVE 扫描）  ✅（主体）
v2.0.0  企业级插件（Vault + 审计日志 + OPA）
```

---

## 命令全览

```
kp init          --name <n> --module <m> [--with-frontend] [--dry-run]
kp deploy        [--namespace] [--context] [--dry-run]
kp scan          [--severity CRITICAL,HIGH] [--image img:tag]
kp resume        从中断点恢复
kp rollback      手动整组 helm rollback（拓扑逆序）
kp release       --version v1.0.0 [--deploy]
kp down          彻底下线，删除所有资源
kp status        [--history] 查看部署状态 + 多 release 展示
kp history       [-n 20] 查看状态转换历史
kp diff          [--from N] [--to M] 对比版本差异
kp doctor        检查环境依赖 + 安全检查
kp ai-plan       [--suggest-only] [--desc] AI 扫描仓库生成 components.yaml
kp controller start  （controller pod 内部运行）
```

---

## kp init 生成内容（v1.2.0+）

```
{name}/
├── cmd/{name}/
├── internal/api/ auth/ metrics/ db/migrations/
├── configs/project.env components.yaml resources.yaml
├── deployments/{name}/
│   ├── {name}-postgres/     StatefulSet + PVC
│   ├── {name}-etcd/         StatefulSet + PVC
│   ├── {name}/              Pod SecurityContext + NetworkPolicy + limits
│   └── {name}-controller/   RBAC 最小权限
├── scripts/
│   └── create-secret.sh     幂等创建 K8s Secret
├── monitoring/ build/ test/ handoff/ snapshots/
```

---

## 安全特性（v1.2.0+）

| 特性 | 实现方式 |
|------|---------|
| Secret 不进 git | `create-secret.sh` + `kp deploy` 前检查 |
| Pod 安全基线 | `securityContext`（非 root/只读文件系统/降权） |
| 网络隔离 | `NetworkPolicy`（默认拒绝入站）|
| 资源限制 | requests + limits 默认值 |
| RBAC 最小权限 | controller 只授予必要资源 |
| CVE 扫描 | `kp scan` + `kp deploy` 自动集成 Trivy |
| 明文密码检测 | `kp doctor` 扫 values.yaml |

---

## 测试覆盖

| 包 | 测试数 |
|---|---|
| internal/planner | 32 |
| internal/state | 57 |
| internal/scaffold | 34 |
| internal/controller | 20 |
| **合计** | **143** |

---

## 验证项目

- `github.com/Ixecd/e2e`（3层拓扑）
- `github.com/Ixecd/web3-blitz`（2层拓扑，controller 自愈 + CVE 修复验证）

---

## 常用命令

```bash
cd ~/dev-toolkit
go test ./... -race
make install
cd ~/web3-blitz && kp deploy
kp doctor
kp scan
```

---

## 快照归档

```
snapshots/
├── SNAPSHOT-kp-2026-03-29-v1.0.0-final.md
├── SNAPSHOT-kp-2026-03-30-v1.1.0.md
├── SNAPSHOT-kp-2026-03-30-v1.2.0.md
├── SNAPSHOT-kp-2026-03-30-v1.3.0.md
└── SNAPSHOT-kubepivot-2026-03-31-v1.4.0
```

> 当前：v1.4.0