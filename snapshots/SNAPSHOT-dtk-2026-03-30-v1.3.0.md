# SNAPSHOT — kubepivot

**里程碑**：v1.3.0 供应链安全 + 镜像防护
**日期**：2026-03-30
**版本**：v1.3.0

---

## 本轮完成（v1.2.0 → v1.3.0）

### dtk scan 独立命令

```bash
dtk scan                              # 扫描所有服务镜像
dtk scan --severity CRITICAL          # 只阻断 CRITICAL
dtk scan --image myapp:v1.0.0         # 扫单个镜像
```

流程：
1. 读 `components.yaml`，找出所有 `image` 不为空的服务
2. 构造镜像名：`{REGISTRY_PREFIX}/{image}-{ARCH}:{VERSION}`
3. 逐个 `trivy image --format json` 扫描
4. CRITICAL/HIGH（默认）→ 阻断，打印红色错误
5. 其他级别 → 告警，打印黄色警告
6. 有阻断级别漏洞 → 退出码非零

### dtk deploy 自动扫描集成

`deployLayers` 开头自动调用 scan，有 trivy 才跑，没有静默跳过，不强制依赖。

### 实战验证

web3-blitz 扫出 CVE-2026-33186（gRPC-Go 授权绕过，CVSS 9.1）：

```
❌ [CRITICAL] CVE-2026-33186 google.golang.org/grpc
   → gRPC-Go has an authorization bypass via missing leading slash in :path
```

升级 `google.golang.org/grpc` 到 v1.79.3 修复，重新扫描无漏洞 ✅

### tools.mk 跨平台安装

```makefile
install.trivy   # 官方脚本，macOS/Linux 通用，不依赖 brew
install.cosign  # 官方脚本，为 v1.3.0 cosign 签名预备
```

---

## 遗留（v1.3.0 剩余）

| # | 任务 |
|---|------|
| 1 | cosign 镜像签名校验（--sign flag，默认关闭） |
| 2 | SBOM 物料清单自动生成 |

---

## 快照归档

```
snapshots/
├── SNAPSHOT-dtk-2026-03-29-v1.0.0-final.md
├── SNAPSHOT-dtk-2026-03-30-v1.1.0.md
├── SNAPSHOT-dtk-2026-03-30-v1.2.0.md
└── SNAPSHOT-dtk-2026-03-30-v1.3.0.md  ← 本次
```
