## 提交格式

^((Merge (branch|pull request).*?)|((revert: )?(feat|fix|perf|style|build|refactor|test|ci|docs|chore)(([a-zA-Z0-9-]+))?!?: .+[^.]))$

## 安全模板
<type>(<scope>): <description>

### type（必须是这 10 个之一）
- `feat`     新功能
- `fix`      修 bug
- `perf`     性能优化
- `style`    代码格式
- `build`    构建系统
- `refactor` 重构
- `test`     测试
- `ci`       CI/CD
- `docs`     文档
- `chore`    杂项

### scope（可选，[a-zA-Z0-9-] only）
- 例：`(eventstream)`、`(cache-policy)`、`(controller)`

### description（必填）
- **不能以 `.` 结尾**
- subject 总长度建议 < 80 字符
- **强烈建议全 ASCII**（避免 hook 编码问题）
- 简洁、动词开头（如 "add..." / "fix..." / "remove..."）

## 推荐模板

feat(scope): add X feature
fix(scope): resolve Y issue
docs(scope): update Z documentation
test(scope): cover edge case for W
refactor(scope): split A into B and C

## body 规则
- 每行 ≤ 1000 字符
- 中文 OK
- 不限制内容格式

---

## 发布节奏（tag → 实现 → tag）

```
1. 打起点 tag（锁定当前功能基线）
   kp release v3.1

2. 实现新功能
   - 设计文档 → 实施 → 单测 → make dev 全绿
   - 每批功能单独 commit（commit message 用中文，-F commits/<file>）

3. 实现满意后打终点 tag
   kp release v3.2
```

**为什么 tag 先行**：
- 用 tag 切分"已完成"和"施工中"，回滚有锚点
- 每次 `kp release` 自动生成 CHANGELOG 条目 + snapshot
- 不在功能写到一半时打 tag（tag 代表稳定基线，不绑定半成品）

**版本号规则**（仅主次版本，不打修订号）：
- 主版本号：重大架构变更（v2 → v3）
- 次版本号：新功能落地（v3.0 → v3.1）
- 不打 patch 版本（v3.1.1 不存在，直接跳到 v3.2）

**tag 命名**：
```
v<major>.<minor>
例：v3.1 / v3.2
```

**发布命令**：
```
kp release v3.1.0    # 打 tag + 生成 CHANGELOG + 推送
```