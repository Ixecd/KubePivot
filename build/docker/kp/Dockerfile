# ====================== Builder Stage ======================
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app

# 安装工具：curl 下载，upx/binutils 负责“手术”
RUN apk add --no-cache curl upx binutils

RUN go env -w GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 编译 kp：-s -w 抹除调试信息
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags '-s -w -extldflags "-static"' \
    -o kp ./cmd/kp

# 下载静态版 kubectl
RUN curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/${TARGETARCH}/kubectl" \
    && chmod +x kubectl

# ====================== Optimizer Stage ======================
FROM alpine:latest AS optimizer
RUN apk add --no-cache upx binutils

COPY --from=builder /app/kp /opt/kp
COPY --from=builder /app/kubectl /opt/kubectl

# 暴力移除符号表 + 最高等级压缩
RUN strip --strip-all /opt/kp && \
    strip --strip-all /opt/kubectl && \
    upx --best --lzma /opt/kp && \
    upx --best --lzma /opt/kubectl

# ====================== Runtime Stage (真空运行区) ======================
FROM scratch AS runtime

# 仅拷贝极致压缩后的二进制，保持镜像纯净
COPY --from=optimizer /opt/kubectl /usr/local/bin/kubectl
COPY --from=optimizer /opt/kp /usr/local/bin/kp

ENV PATH="/usr/local/bin"
ENV HOME="/tmp"

# 维持 CA 证书以保证 HTTPS 通信
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/kp"]
CMD ["controller", "start"]