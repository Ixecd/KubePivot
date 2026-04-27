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