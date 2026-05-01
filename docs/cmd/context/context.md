# kp context

多集群 context 管理。

## 用法

```
kp context <子命令>
```

## 子命令

### add

添加 kube context 配置。

```
kp context add --name prod --context my-k8s --namespace production
```

### list

列出已配置的 context。

## 相关命令

- `kp deploy --env` — 指定部署环境
