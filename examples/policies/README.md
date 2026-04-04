# KubePivot OPA 策略示例

策略文件使用 Rego 语言，包名必须是 `package kp`。

## 使用方式
```bash
# 添加策略
kp policy add --name no-latest-tag --file examples/policies/no-latest-tag.rego

# 列出策略
kp policy list

# 手动检查
kp policy check

# kp deploy 前自动执行（有 opa 命令才跑）
kp deploy
```

## 输入 context（input 对象）
```json
{
  "project":   "web3-blitz",
  "namespace": "web3-blitz",
  "version":   "v0.1.12",
  "services":  ["wallet-service"],
  "env":       { "REGISTRY_PREFIX": "...", ... }
}
```

## deny 规则

返回字符串数组，每个字符串是一条阻断原因。空数组 = 通过。
