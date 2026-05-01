# kp team

多团队 RBAC 管理。基于 `configs/teams.yaml`（项目级）或 `~/.kp/teams.yaml`（用户级）。

## 用法

```
kp team <子命令>
```

## 子命令

### list

列出所有 team。

### show

显示 team 详情。

```
kp team show <name>
```

### add

新增 team。

```
kp team add <name> [--members a@x.com,b@x.com] [--namespaces ns-*] [--permissions deploy,sandbox]
```

### remove

删除 team（二次确认）。

```
kp team remove <name>
```

### member add / remove

管理 team 成员。

```
kp team member add <team> <member>
kp team member remove <team> <member>
```

### check

校验权限。

```
kp team check <user> <namespace> <permission>
```

### validate

校验 teams.yaml 语法。

## 设计哲学

- 默认项目级 `configs/teams.yaml`
- `--user` flag 切换到 `~/.kp/teams.yaml`
- `kp team *` 不走 mustCheck（文件权限即治理边界）

## RBAC

`kp team` 命令自身不走 RBAC——文件系统权限是治理边界。

## 相关命令

- `kp login` — SSO 登录
- `kp whoami` — 查看当前身份
