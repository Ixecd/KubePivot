# kubepivot 快照 — --with-frontend 支持

> 归档时间：2026-03-20
> 里程碑：with-frontend — dtk init 支持生成通用前端骨架

---

## 本次新增

### 新增 flag

```bash
dtk init --name <project> --module <module> --with-frontend
```

不带 flag 行为完全不变，纯后端项目不受影响。

### `InitOptions` 新增字段

```go
type InitOptions struct {
    // ... 原有字段
    WithFrontend bool  // ← 新增
    Stdout       io.Writer
}
```

### 新增文件

`internal/scaffold/frontend.go` — 独立文件，不污染 `init.go`，包含 `writeFrontendSkeleton` 函数。

### 生成的 `frontend/` 结构

```
frontend/
├── package.json              React 18 + Vite + Tailwind + shadcn/ui 依赖
├── vite.config.ts            /api 代理到 :8080，@ 别名
├── tsconfig.json             strict 模式
├── postcss.config.js
├── tailwind.config.ts        CSS 变量体系（支持 dark mode）
├── index.html
├── Dockerfile                node:20-alpine 构建 + nginx:alpine 部署
├── nginx.conf                SPA fallback + /api 反向代理
├── .gitignore
└── src/
    ├── main.tsx
    ├── index.css             Tailwind + CSS 变量（light/dark）
    ├── App.tsx               /login 和 /* 两条路由，注释留扩展位
    ├── api/client.ts         fetch 封装，ApiError，get/post/put/delete
    ├── contexts/AuthContext.tsx  token/username/userID，login/logout
    ├── components/Layout.tsx     侧边栏骨架，nav=[] 由业务填充
    └── pages/
        ├── Login.tsx         通用登录表单
        └── Home.tsx          纯占位页，无任何业务逻辑
```

### 设计原则

- **零业务逻辑**：无任何领域字段（无金额、无交易、无权限点）
- **nav 为空数组**：业务层自行追加导航项和路由
- **反引号安全**：所有 TypeScript 模板字符串均改为字符串拼接，避免与 Go raw string 冲突

### Bug 修复

- `client.ts` Bearer token 拼接从模板字符串改为 `'Bearer ' + token`，修复 Go raw string 反引号提前终止导致的 `illegal character U+003F` 编译错误
