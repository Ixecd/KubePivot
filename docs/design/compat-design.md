# API 兼容性检测设计

## 概述

`kp compat check` 基于 [oasdiff](https://github.com/oasdiff/oasdiff) 检测两个版本的 API spec 之间的破坏性变更，防止上线时破坏现有客户端。

---

## 使用场景

```bash
# CI/CD 流水线：PR 合并前检测 API 变更
kp compat check --base main/docs/swagger.yaml --revision HEAD/docs/swagger.yaml

# 发布前检查
kp compat check --base v1.0.0/swagger.yaml --revision docs/swagger.yaml

# 输出 JSON 供流水线解析
kp compat check --base old.yaml --revision new.yaml --output-json
```

---

## 破坏性变更定义（oasdiff 标准）

| 变更 | 级别 |
|------|------|
| 删除 endpoint | ❌ Breaking |
| 新增 required 请求参数 | ❌ Breaking |
| 参数类型变更（不兼容） | ❌ Breaking |
| response 结构删除字段 | ❌ Breaking |
| 新增可选字段 | ✅ 非破坏性 |
| 删除 required 字段 | ✅ 非破坏性（更宽松） |

---

## 实现

调用 `oasdiff breaking` 命令，解析退出码和输出：

```
exit 0 + 无输出  → 无变更
exit 0 + 有输出  → 非破坏性警告
exit 1           → 有破坏性变更，阻断
exit 102         → spec 格式错误
```

---

## 自动探测 spec 文件

按以下路径顺序探测：

```
docs/swagger.yaml
docs/swagger.json
docs/openapi.yaml
api/openapi.yaml
api/swagger.yaml
internal/api/swagger.yaml
```

---

## OpenAPI 3.0 建议

oasdiff 对 **OpenAPI 3.0** 的检测覆盖比 Swagger 2.0 更完整。

Swagger 2.0 已停止维护，建议将 API spec 升级：

```yaml
# OpenAPI 3.0 格式
openapi: "3.0.3"
info:
  title: web3-blitz API
  version: "0.1.1"
paths:
  /api/v1/register:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/RegisterRequest"
```

---

## kp doctor 集成

`kp doctor` 检查 oasdiff 是否安装（可选工具，未安装时 warn 不 error）：

```
✓ oasdiff   oasdiff version 1.12.4
⚠ oasdiff   未安装，kp compat check 不可用
            brew install oasdiff
```
