# ====================== Builder Stage ======================
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
# 🔥 v2.4.0：可重现的 kubectl 版本
# 默认固定为已验证兼容 K8s 1.30~1.32 的稳定版本
# 升级时改这一行 + 重新 build；CI 可用 --build-arg KUBECTL_VERSION=vX.Y.Z 覆盖
ARG KUBECTL_VERSION=v1.32.0
WORKDIR /app

RUN apk add --no-cache curl ca-certificates upx binutils

RUN go env -w GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags '-s -w -extldflags "-static"' \
    -o kp ./cmd/kp

# 🔥 先生钦点修复：HELM 跨架构下载 + 强校验
RUN HELM_VERSION="v3.17.1" && \
    ARCH=${TARGETARCH} && \
    curl -fsSL "https://get.helm.sh/helm-${HELM_VERSION}-linux-${ARCH}.tar.gz" -o helm.tgz && \
    tar -xzOf helm.tgz "linux-${ARCH}/helm" > /usr/local/bin/helm && \
    chmod +x /usr/local/bin/helm

# 🔥 v2.4.0：KUBECTL 跨架构下载 + 强校验 + 可重现版本
# 优先用 ARG KUBECTL_VERSION（可重现）
# 当 ARG 未指定且能上网时用 stable.txt（动态最新）
# 二者都不行时报错退出（避免空文件被 COPY 进镜像）
RUN set -eux; \
    if [ -z "${KUBECTL_VERSION:-}" ]; then \
        echo "ARG KUBECTL_VERSION 未定义，尝试动态查询 dl.k8s.io/release/stable.txt"; \
        KUBECTL_VERSION=$(curl -fsSL --max-time 10 https://dl.k8s.io/release/stable.txt || echo ""); \
    fi; \
    if [ -z "${KUBECTL_VERSION}" ]; then \
        echo "❌ KUBECTL_VERSION 为空（ARG 未传 + 网络查询失败）" >&2; \
        echo "   解决方法：docker build --build-arg KUBECTL_VERSION=v1.31.0 ..." >&2; \
        exit 1; \
    fi; \
    ARCH=${TARGETARCH}; \
    echo "📦 下载 kubectl ${KUBECTL_VERSION} for linux/${ARCH}"; \
    curl -fsSL --retry 3 --retry-delay 2 \
        "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${ARCH}/kubectl" \
        -o /usr/local/bin/kubectl; \
    chmod +x /usr/local/bin/kubectl

# 🔥 先生要求：验证！必须验证！（防止空文件/下载失败）
RUN /usr/local/bin/kubectl version --client && /usr/local/bin/helm version

RUN mkdir -p /tmp && touch /tmp/.keep

# ====================== Optimizer Stage ======================
FROM alpine:latest AS optimizer
RUN apk add --no-cache upx binutils
COPY --from=builder /app/kp /opt/kp
RUN strip --strip-all /opt/kp && upx --best --lzma /opt/kp

# ====================== Runtime Stage ======================
FROM scratch AS runtime

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder --chown=65532:65532 /tmp /tmp
ENV HOME="/tmp"
ENV PATH="/usr/local/bin"

COPY --from=optimizer /opt/kp                /usr/local/bin/kp
COPY --from=builder   /usr/local/bin/kubectl /usr/local/bin/kubectl
COPY --from=builder   /usr/local/bin/helm    /usr/local/bin/helm

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/kp"]
CMD ["controller", "start", "--global"]