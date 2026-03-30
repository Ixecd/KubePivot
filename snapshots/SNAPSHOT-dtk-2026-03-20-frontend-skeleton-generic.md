# kubepivot 快照 — 前端骨架精简为通用零业务版本

> 归档时间：2026-03-20
> 里程碑：frontend-skeleton-generic — 移除所有业务逻辑，骨架适配任意项目

---

## 背景

初版 `writeFrontendSkeleton` 生成的页面含有交易、金额、充提币等业务字段，
违反了 dtk 作为通用脚手架的定位。本次完整重写，彻底清除业务耦合。

---

## 本次变更

### 设计原则（强制约束）

- **零业务逻辑**：生成文件中不出现任何领域字段（金额、交易、链、权限点等）
- **nav 为空数组**：`Layout.tsx` 中 `nav = []`，业务层自行追加导航项和路由
- **页面只有两个**：`Login.tsx`（通用登录表单）+ `Home.tsx`（纯占位欢迎页）
- **反引号安全**：所有 TS 模板字符串改为字符串拼接，杜绝 Go raw string 编译错误

### 文件变更

`internal/scaffold/frontend.go` 完整重写，删除以下内容：

| 删除项 | 原因 |
|--------|------|
| `Dashboard.tsx` | 含充值/提币历史表格，业务耦合 |
| `Deposit.tsx` | 含地址生成、QR Code，业务耦合 |
| `Withdraw.tsx` | 含提币表单、金额字段，业务耦合 |
| `Admin.tsx` | 含用户列表、等级升级，业务耦合 |
| `AuthContext` 中的 `refreshToken` 字段 | 非通用字段 |
| `package.json` 中多余的 Radix UI 组件 | 按需引入，骨架不预装 |

### 最终生成结构

```
frontend/
├── package.json              React 18 + Vite + Tailwind（最小依赖集）
├── vite.config.ts            /api 代理到 :8080，@ 别名
├── tsconfig.json             strict 模式
├── postcss.config.js
├── tailwind.config.ts        CSS 变量体系（light/dark）
├── index.html
├── Dockerfile                node:20-alpine + nginx:alpine
├── nginx.conf                SPA fallback + /api 反代
├── .gitignore
└── src/
    ├── main.tsx
    ├── index.css
    ├── App.tsx               /login 和 /* 两条路由，注释留扩展位
    ├── api/client.ts         get/post/put/delete，Bearer 用字符串拼接
    ├── contexts/AuthContext.tsx  token/username/userID，login/logout
    ├── components/Layout.tsx     侧边栏骨架，nav=[]
    └── pages/
        ├── Login.tsx         通用登录表单（适配任意后端）
        └── Home.tsx          纯占位页，一句欢迎语
```

### Bug 修复

`client.ts` Bearer token 从 `` `Bearer ${token}` `` 改为 `'Bearer ' + token`，
修复 Go raw string 中反引号提前终止导致的 `illegal character U+003F '?'` 编译错误。
