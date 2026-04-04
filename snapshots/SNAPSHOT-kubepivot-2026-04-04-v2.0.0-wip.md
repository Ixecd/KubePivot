# SNAPSHOT — KubePivot v2.0.0 WIP

> 日期：2026-04-04
> 状态：代码完成，文档统一进行中

## 新增能力

- kp version / kp update（GitHub releases API self-update）
- kp plugin install/list/remove（插件市场，execPlugin 兜底）
- kp release 自动同步 kpVersion 常量
- kp chaos inject/list/stop/status（Chaos Mesh API，4 种混沌类型）
- OPA stdin pipe（input JSON 正确传递）
- drift etcd 审计（clientv3 WithPrefix 读取）
- Vault net/http 实现（替换 curl）

## 目录结构整理

- deployments/dev-toolkit/ 已删除（dtk 遗留）
- handoff/ 历史文件归 archived/handoff/
- docs/design/ 文件名统一（去版本后缀）
- docs/guide/zh-CN/ 文件名统一
- 根目录 md 文件精简为 README/SNAPSHOT/TODO
- .DS_Store 加入 .gitignore

## 下一步

全文档统一大版本更新
