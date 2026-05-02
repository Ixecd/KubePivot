# KubePivot CLI 全命令逐批验证清单

> 顺序：按项目生命周期排列，从 init 开始逐批推进
> 方法：在 /tmp/kp-test 目录下跑，不给正式项目添乱

---

## 第一批：项目启动

```
✅ 00. kp init      — 基础生成 / --dry-run / --force / --with-frontend / 缺参报错
```

**01. kp version**
```bash
kp version
# 验证: 输出 v3.0.0 + module path + go version
```

**02. kp doctor**
```bash
cd /tmp/kp-test/demo-svc
kp doctor
# 验证: 检测 docker/kubectl/helm 存在性，缺失时给出提示而非 panic
```

**03. kp context**
```bash
kp context add --name staging --context orbstack --namespace demo-staging
kp context list
# 验证: 输出 staging 配置
kp deploy --env staging --dry-run
# 验证: 使用了 staging 的 context/namespace
```

**04. kp ai-plan**
```bash
cd /tmp/kp-test/demo-svc
kp ai-plan --suggest-only
# 验证: 扫描仓库、调 LLM、打印建议（LLM 无 API key 时报错友好）
kp ai-plan --suggest-only --desc "高并发交易系统"
# 验证: --desc 传入LLM
```

---

## 第二批：部署与状态

**05. kp deploy**
```bash
cd /tmp/kp-test/demo-svc
kp deploy --dry-run
# 验证: 打印 deploy.build → push → install → rollout 计划
# 验证: 不执行 docker/helm/kubectl
kp deploy --components configs/components.yaml --dry-run
kp deploy --changed-only --dry-run
# 验证: changed-only 在无 git history 时 fallback 到全量
```

**06. kp status**
```bash
cd /tmp/kp-test/demo-svc
kp status
# 验证: 读取 project.env、显示状态
kp status --all-envs
# 验证: 多环境对比
```

**07. kp history**
```bash
cd /tmp/kp-test/demo-svc
kp history -n 5
# 验证: 显示最近部署记录
```

**08. kp diff**
```bash
cd /tmp/kp-test/demo-svc
kp diff
# 验证: 对比当前版本
```

**09. kp resume**
```bash
cd /tmp/kp-test/demo-svc
kp resume
# 验证: 检测 K8s 状态（无集群时报错但不 panic）
```

---

## 第三批：生命周期管理

**10. kp rollback**
```bash
cd /tmp/kp-test/demo-svc
kp rollback --dry-run 2>&1 || kp rollback 2>&1
# 验证: 无集群时优雅报错
```

**11. kp down**
```bash
cd /tmp/kp-test/demo-svc
kp down 2>&1
# 验证: RBAC 检查 + 确认提示（无集群时应有友好提示）
```

**12. kp sandbox**
```bash
cd /tmp/kp-test/demo-svc
kp sandbox
# 验证: 打印子命令列表
kp sandbox start --dry-run
# 验证: 打印五阶段计划（无集群时应在 LOCKED 阶段报错）
kp sandbox status
kp sandbox unlock --reason "test" --force 2>&1
# 验证: 无活跃 session 时不 panic
```

**13. kp upgrade**
```bash
cd /tmp/kp-test/demo-svc
kp upgrade --dry-run
# 验证: 打印升级计划
```

**14. kp promote**
```bash
cd /tmp/kp-test/demo-svc
kp promote
# 验证: 无蓝绿服务时输出 "没有需要 promote 的服务"
```

---

## 第四批：数据与迁移

**15. kp migrate**
```bash
cd /tmp/kp-test/demo-svc
kp migrate
# 验证: 打印子命令列表
kp migrate status
# 验证: 无 DATABASE_URL 时优雅提示
kp migrate plan
kp migrate run --dry-run
kp migrate fix-dirty 2>&1
```

**16. kp compat**
```bash
cd /tmp/kp-test/demo-svc
kp compat check --base v0.1.0 --revision v0.2.0 2>&1
# 验证: 无 OpenAPI spec 时优雅报错
```

**17. kp pvc**
```bash
cd /tmp/kp-test/demo-svc
kp pvc
# 验证: 打印子命令列表
kp pvc backup --service demo-svc --dry-run 2>&1 || true
kp pvc list --service demo-svc 2>&1 || true
kp pvc restore --service demo-svc --snapshot nope --force 2>&1 || true
```

**18. kp secret**
```bash
cd /tmp/kp-test/demo-svc
kp secret
# 验证: 打印子命令列表
kp secret rotate --secret demo-secret --strategy immediate 2>&1 || true
# 验证: 无集群时报错友好
kp secret audit
kp secret cleanup 2>&1
# sync 和 seal 需要外部依赖，只验证参数校验
kp secret seal test-secret --from-literal=K=V 2>&1 || true
# 验证: kubeseal 缺失时提示安装
```

---

## 第五批：安全与权限

**19. kp supply-chain**
```bash
cd /tmp/kp-test/demo-svc
kp supply-chain
# 验证: 打印子命令列表
kp supply-chain verify alpine:latest --key nope 2>&1 || true
# 验证: cosign 缺失时提示安装
kp supply-chain sbom alpine:latest 2>&1 || true
# 验证: syft 缺失时提示安装
```

**20. kp scan**
```bash
cd /tmp/kp-test/demo-svc
kp scan --image alpine:latest 2>&1 || true
# 验证: trivy 缺失时提示安装
```

**21. kp policy**
```bash
cd /tmp/kp-test/demo-svc
kp policy add 2>&1 || true
kp policy check 2>&1 || true
```

**22. kp audit**
```bash
kp audit --format table
kp audit --format json
# 验证: 输出审计日志（可能为空）
```

**23. kp login + whoami**
```bash
kp login --provider google --client-id test --client-secret test 2>&1 || true
# 验证: OAuth 流程启动（会尝试打开浏览器，手动取消）
kp whoami
# 验证: 显示当前身份或未登录提示
```

**24. kp team**
```bash
cd /tmp/kp-test/demo-svc
kp team list
kp team check alice@x.com kp-prod deploy
kp team validate
```

---

## 第六批：高级特性

**25. kp sizing**
```bash
cd /tmp/kp-test/demo-svc
kp sizing
# 验证: 打印子命令列表
kp sizing recommend --pod test --namespace default 2>&1 || true
# 验证: kubectl 不可用时优雅报错
```

**26. kp chaos**
```bash
cd /tmp/kp-test/demo-svc
kp chaos
# 验证: 打印子命令列表 + 安装提示
kp chaos inject --service test --kind pod-kill --dry-run
# 验证: 打印实验配置
kp chaos list 2>&1 || true
kp chaos stop --uid test 2>&1 || true
kp chaos status --uid test 2>&1 || true
```

**27. kp warmup**
```bash
cd /tmp/kp-test/demo-svc
kp warmup 2>&1 || true
```

**28. kp preview**
```bash
cd /tmp/kp-test/demo-svc
kp preview 2>&1 || true
```

---

## 第七批：工具链

**29. kp release**
```bash
cd /tmp/kp-test/demo-svc
kp release --version v9.9.9 --no-push 2>&1 || true
# 验证: 版本号格式校验
kp release --version bad 2>&1
# 验证: 格式错误时提示
```

**30. kp update**
```bash
kp update 2>&1 || true
```

**31. kp plugin**
```bash
kp plugin install nonexistent 2>&1 || true
# 验证: 打印帮助
```

**32. kp sync**
```bash
cd /tmp/kp-test/demo-svc
kp sync 2>&1 || true
```

**33. kp network**
```bash
cd /tmp/kp-test/demo-svc
kp network gen 2>&1 || true
```

**34. kp controller**
```bash
kp controller status
kp controller projects
kp controller enroll 2>&1 || true
# 验证: 无集群时 RBAC 检查失败=预期
```

---

## 验收标准

每批测试通过后，在该批标题后标记 ✅，最后一个命令测完时全表绿：

```
✅ 第一批：项目启动
✅ 第二批：部署与状态
...
✅ 第七批：工具链
```

发现的问题记录在每项下面，用 `⚠️` 标记。
