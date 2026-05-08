# Build

Docker 镜像构建目录。由 `kp init` 自动生成。

```
build/docker/<project>/
├── Dockerfile    # 多阶段构建（builder → optimizer → runtime）
└── build.sh      # docker build + push 脚本
```

## 使用

```bash
# 构建镜像
make deploy.build

# 推送镜像
make deploy.push

# 一键构建+推送
make deploy.build && make deploy.push
```

镜像 tag 格式：`${REGISTRY_PREFIX}/${PROJECT_NAME}-${ARCH}:${VERSION}`

默认 registry 为 `qingchun22`，可通过 `configs/project.env` 修改 `REGISTRY_PREFIX` 或设置环境变量覆盖。

## 多服务项目

每个需要容器化的服务在此目录下创建对应子目录：

```
build/docker/<service-name>/
├── Dockerfile    # 使用 BASE_IMAGE 占位符，Makefile 自动替换
└── build.sh      # 构建前的钩子脚本，可为空
```

服务名需与 `configs/components.yaml` 中声明的 `name` 一致。

## Dockerfile 结构

采用三阶段构建：

1. **builder**（golang:alpine）：编译 Go 二进制 + 下载 kubectl/helm
2. **optimizer**（alpine）：strip + upx 压缩
3. **runtime**（scratch）：最小镜像，仅含 kp + kubectl + helm + CA 证书

静态编译 + upx 压缩后二进制约 ~15MB，镜像约 ~40MB。
