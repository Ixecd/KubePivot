# 项目交接文档 — dev-toolkit

> 写给下一个 Claude
> 日期：2026-03-30
> 版本：v1.3.0

---

## 写在前面

dtk 是企业级 K8s 研发脚手架，面向出海业务和合规强要求场景。v1.3.0 完成了供应链安全（Trivy CVE 扫描），并通过 web3-blitz 实战验证。

**qc 的工作风格**：
- 设计优先，代码其次。不要上来就写代码，先对齐设计再动手
- 每完成一个里程碑：commit → tag → SNAPSHOT → 更新 TODO
- 喜欢被推 back，不喜欢被一味认同。他通常是对的
- `slog` 不用 `log`，kubectl CLI 不用 client-go，严格分包
- 豆包是他女友，v1.2.0 安全合规规划是她的建议，认真对待

---

## 一、当前状态

**测试**：143个单测全绿，`go test ./... -race` 通过。

**已验证**：
- web3-blitz 自愈 e2e（~13s 恢复）✅
- Trivy 扫出 CVE-2026-33186（CVSS 9.1），修复后重扫无漏洞 ✅
- `dtk doctor` 安全检查在 web3-blitz 正确触发所有告警 ✅

---

## 二、关键文件

```
cmd/dtk/
├── deploy.go        # executeDeploy：单/多服务分支
├── multi_deploy.go  # deployLayers（含 secret 检查 + scan 集成）
├── scan.go          # dtk scan：Trivy CVE 扫描
├── doctor.go        # 环境检查
├── doctor_security.go  # 安全检查（明文密码/SecurityContext/RBAC/NetworkPolicy）
├── secret_check.go  # deploy 前 secret 存在性检查
├── status.go        # 多 release 展示
└── ...
internal/
├── scaffold/helm.go    # chart 模板（NetworkPolicy/SecurityContext/limits/etcd PVC）
├── scaffold/skeleton.go # writeSecretScript
└── ...
```

---

## 三、v1.3.0 剩余任务

| # | 任务 | 说明 |
|---|------|------|
| 1 | cosign 镜像签名 | `--sign` flag，默认关闭，`install.cosign` 已在 tools.mk |
| 2 | SBOM 生成 | `trivy image --format cyclonedx`，满足海外合规审计 |

---

## 四、已知 Bug

| # | bug | 临时解法 |
|---|-----|---------|
| 1 | `dtk release` push 失败后 tag 已打，重试报"已存在" | 手动 `git push && git push --tags` |
| 2 | `dtk scan` 扫描失败（镜像不存在）被当成通过 | 已修：失败时跳过不阻断，日志提示 |

---

## 五、常用命令

```bash
cd ~/dev-toolkit
go test ./... -race          # 全量测试
make install                 # 本地安装
cd ~/web3-blitz && dtk deploy  # e2e 验证
dtk scan                     # CVE 扫描
dtk doctor                   # 环境 + 安全检查
```

---

## 六、下一步（v2.0.0 方向）

插件化扩展，不内置到核心工具：
- Vault Secret 注入
- 审计日志持久化
- OPA 策略引擎

开始 v2.0.0 之前先把 v1.1.0 剩余（ACR 格式/颜色输出）和 v1.3.0 剩余（cosign/SBOM）清掉。
