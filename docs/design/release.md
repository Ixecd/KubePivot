# kp release 设计文档

> 适用：dev-toolkit v0.4.0+

---

## 设计动机

版本发布是个容易出错的手动流程：

```
手动改 project.env VERSION=v1.0.0
git add configs/project.env
git commit -m "chore: release v1.0.0"
git tag -a v1.0.0 -m "release v1.0.0"
git push && git push --tags
```

容易忘步骤、版本号写错、tag 和 commit 不一致。`kp release` 把这些全部自动化。

---

## 使用方式

```bash
# 基本用法：打 tag 发布
kp release --version v1.0.0

# 打完 tag 直接触发部署
kp release --version v1.0.0 --deploy

# 只打本地 tag，不推送到远端
kp release --version v1.0.0 --push=false
```

---

## 完整流程

```
kp release --version v1.0.0
  │
  ├── 1. 校验版本号格式（v{major}.{minor}.{patch}）
  ├── 2. 检查工作区干净（git status --porcelain）
  ├── 3. 检查 tag 是否已存在（git tag -l v1.0.0）
  ├── 4. 更新 configs/project.env → VERSION=v1.0.0
  ├── 5. git add configs/project.env
  ├── 6. git commit -m "chore: release v1.0.0"
  ├── 7. git tag -a v1.0.0 -m "release v1.0.0"
  ├── 8. git push（--push=false 时跳过）
  ├── 9. git push --tags（--push=false 时跳过）
  └── 10. kp deploy（仅 --deploy 时）
```

---

## 版本号规范

强制遵循 semver 格式：`v{major}.{minor}.{patch}`

```
✅ v0.1.0 / v1.0.0 / v10.20.30

❌ 1.0.0      缺少 v 前缀
❌ v1.0       缺少 patch
❌ v1.0.0.0   多余的段
❌ latest     非法格式
```

---

## 安全检查

### 工作区干净检查

有未提交改动时直接报错退出：

```
工作区有未提交的改动，请先 commit 或 stash：
 M internal/api/mux.go
?? configs/temp.txt
```

**设计考量**：强制干净工作区保证 release commit 只包含版本号变更，历史清晰可追溯。

### tag 重复检查

tag 已存在时报错退出：

```
tag v1.0.0 已存在，请使用其他版本号
```

---

## updateVersion 实现

逐行扫描 `configs/project.env`，找到 `VERSION=` 开头的行替换，没有则追加：

```
# 原文件              # 更新后
PROJECT_NAME=myapp    PROJECT_NAME=myapp
VERSION=v0.1.0    →   VERSION=v1.0.0
ARCH=arm64            ARCH=arm64
```

注释行、空行、其他字段完全保留，不破坏文件结构。

---

## --deploy 标志

打完 tag 后直接调用 `runDeploy`，适用于发布后立即上线的场景：

```bash
# 等价于：
kp release --version v1.0.0
kp deploy
```

---

## --push=false 标志

只在本地打 tag，不推送到远端。适用于：
- 本地验证发布流程
- 项目没有配置远端仓库
- 需要先本地测试再推送

**注意**：zsh 下感叹号有特殊含义，带 `!` 的 commit message 需要用单引号：

```bash
git commit -m 'feat!: breaking change'
```

---

## 测试覆盖

```
cmd/kp/release_test.go — 8 个测试

TestSemverPattern                版本号格式校验（合法 + 非法）
TestUpdateVersion_ExistingKey    已有 VERSION 行时原地替换
TestUpdateVersion_NoExistingKey  无 VERSION 行时追加到末尾
TestUpdateVersion_PreservesComments  注释行和其他字段不受影响
TestTagExists_NotFound           不存在的 tag 返回 false
TestTagExists_Found              已存在的 tag 返回 true
TestCheckCleanWorkspace_Clean    干净工作区通过
TestCheckCleanWorkspace_Dirty    有未提交改动时返回错误
```

---

## 文件结构

```
cmd/kp/
└── release.go
    ├── semverPattern        版本号正则
    ├── checkCleanWorkspace  检查工作区
    ├── tagExists            检查 tag
    ├── updateVersion        更新 project.env
    └── runOutputInDir       在指定目录执行命令
```
