package kp

# 要求所有服务声明资源 limits
deny[msg] {
    service := input.services[_]
    not input.env[sprintf("RESOURCE_LIMITS_%s", [service])]
    msg := sprintf("服务 %s 未声明资源 limits，请在 components.yaml 中配置 cpu/memory", [service])
}
