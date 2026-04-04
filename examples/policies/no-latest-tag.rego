package kp

# 禁止使用 latest tag 部署
deny[msg] {
    input.version == "latest"
    msg := "禁止使用 latest tag 部署，请指定具体版本号"
}

# 禁止部署到 production namespace 时版本号不含 v 前缀
deny[msg] {
    input.namespace == "production"
    not startswith(input.version, "v")
    msg := sprintf("production 环境要求版本号以 v 开头，当前: %s", [input.version])
}
