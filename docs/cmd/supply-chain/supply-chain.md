# kp supply-chain

镜像供应链安全：签名验证 + SBOM 生成。

## 用法

```
kp supply-chain <子命令>
```

## 子命令

### verify

用 cosign 验证镜像签名（key-based 模式）。

```
kp supply-chain verify <IMAGE> --key <PUBKEY> [--output json|text]
```

| Flag | 默认值 | 说明 |
|---|---|---|
| (位置参数) | (必填) | 镜像引用 |
| `--key` / `-k` | (必填) | cosign 公钥路径 |
| `--output` / `-o` | `text` | 输出格式：`text` / `json` |

**退出码**：0=验证成功 / 1=验证失败 / 2=工具错误

### sbom

用 syft 生成 SBOM（Software Bill of Materials）。

```
kp supply-chain sbom <IMAGE> [--format cyclonedx|spdx|table] [--output PATH]
```

| Flag | 默认值 | 说明 |
|---|---|---|
| (位置参数) | (必填) | 镜像引用 |
| `--format` / `-f` | `cyclonedx-json` | 输出格式 |
| `--output` / `-o` | (stdout) | 输出文件路径 |
| `--push` | `false` | 上传 SBOM 到 OCI registry |
| `--push-required` | `false` | 上传失败则阻断 |
| `--push-target` | | OCI registry 地址 |

**依赖**：syft（`kp doctor` 可检查）

## deploy 集成

`kp deploy` 部署前自动调 `verifySupplyChainPolicy`，检查：
- registry 白名单/黑名单（`REGISTRY_ALLOW` / `REGISTRY_DENY` env）
- 签名强制（`SUPPLY_CHAIN_SIGN_ENFORCE=true` env）
- SBOM 要求（`SUPPLY_CHAIN_SBOM_REQUIRE=true` env）

`--skip-supply-chain` flag 可跳过。

## RBAC

`PermSupplyChain`

## 相关命令

- `kp deploy --sign` — 部署时 cosign keyless 签名
- `kp doctor` — 环境依赖检查
