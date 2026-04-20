# ====================== Builder Stage ======================
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app

# 安装 curl 以便下载 kubectl
RUN apk add --no-cache curl

RUN go env -w GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 编译 kp
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags '-s -w -extldflags "-static"' \
    -o kp ./cmd/kp

# 下载对应架构的 kubectl
# 使用 stable 版本，并根据 TARGETARCH 动态选择二进制
RUN curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/${TARGETARCH}/kubectl" \
    && chmod +x kubectl

# ====================== Runtime Stage ======================
FROM scratch AS runtime

# 1. 模拟 Linux 最小路径环境（虽然是 scratch，但代码里调 exec 默认找这些地方）
COPY --from=builder /app/kubectl /usr/local/bin/kubectl
COPY --from=builder /app/kp /usr/local/bin/kp

# 2. 注入必要的环境变量，否则 Go 的 os.LookPath("kubectl") 会在 scratch 里抓瞎
ENV PATH="/usr/local/bin"
ENV HOME="/tmp"

# 3. 必须拷贝这个，否则 kubectl 连不上 https 的 API Server
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/kp"]
CMD ["controller", "start"]