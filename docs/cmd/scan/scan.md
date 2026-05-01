# kp scan

镜像 CVE 扫描。基于 Trivy。

## 用法

```
kp scan [flags]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--severity` | `CRITICAL,HIGH` | 最低严重度 |
| `--image` | | 指定镜像（空时扫描 components.yaml 所有镜像） |

## 依赖

trivy（`kp doctor` 可检查）

## 相关命令

- `kp doctor` — 环境检查
- `kp supply-chain sbom` — SBOM 生成
