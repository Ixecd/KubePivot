# KubePivot 语言模板

## 维护策略

以下 4 种语言由 KubePivot 维护，`kp init --lang <lang>` → `make deploy.build` → `kp deploy` 全链路验证通过：

| 语言 | 框架 | 目录 | 构建方式 |
|------|------|------|---------|
| Go | 内置骨架 | （非模板目录） | `make deploy.build` → Go 编译 + Docker |
| Python | FastAPI + uvicorn | `python/` | `docker build` |
| Rust | Actix-web | `rust/` | `cargo build --release` + Docker |
| C++ | cpp-httplib (header-only) | `cpp/` | CMake + FetchContent + Docker |

## 其余 8 种语言

以下语言提供业务源代码骨架（`kp init --lang <lang>` 正常生成项目结构），
**Dockerfile 由开发者自行编写和维护**：

| 语言 | 目录 |
|------|------|
| Java (Spring Boot) | `java/` |
| C# (ASP.NET) | `cs/` |
| Zig | `zig/` |
| Kotlin (Ktor) | `kotlin/` |
| TypeScript (Express) | `ts/` |
| PHP (FrankenPHP) | `php/` |
| Swift (Vapor) | `swift/` |
| Lua (OpenResty) | `lua/` |

## Dockerfile 约定

编写 Dockerfile 时请遵循以下约定：

1. **路径**：`build/docker/<service-name>/Dockerfile`（与 Go 版统一）
2. **构建上下文**：项目根目录
3. **命名**：使用 `{{name}}` 占位符，`kp init` 会自动替换为项目名
4. **端口**：使用 `{{.Port}}` 占位符，默认替换为 `8080`
5. **多阶段构建**：优先使用多阶段构建减小镜像体积

示例（多阶段 Rust）：
```dockerfile
FROM rust:1.85-alpine AS builder
WORKDIR /app
COPY Cargo.toml Cargo.lock* ./
RUN cargo fetch
COPY src/ ./src/
RUN cargo build --release

FROM scratch
COPY --from=builder /app/target/release/{{name}} /app
EXPOSE {{.Port}}
ENTRYPOINT ["/app"]
```

## 提交 Dockerfile

1. `kp init --name test-<lang> --lang <lang>` 生成完整项目结构
2. `make deploy.build` 构建镜像成功
3. 镜像可正常启动并响应 `/healthz`
