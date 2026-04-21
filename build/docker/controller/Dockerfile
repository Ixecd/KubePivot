# ====================== Builder Stage ======================
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app

RUN apk add --no-cache curl ca-certificates upx binutils

RUN go env -w GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags '-s -w -extldflags "-static"' \
    -o kp ./cmd/kp

# 动态获取最新 Helm 版本（安全、不硬编码）
RUN HELM_VERSION=$(curl -Ls https://api.github.com/repos/helm/helm/releases/latest | grep '"tag_name"' | cut -d'"' -f4) && \
    curl -Ls "https://get.helm.sh/helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz" | tar -xz && \
    mv linux-${TARGETARCH}/helm /usr/local/bin/helm && \
    chmod +x /usr/local/bin/helm

# 动态获取最新 kubectl 版本
RUN KUBECTL_VERSION=$(curl -Ls https://dl.k8s.io/release/stable.txt) && \
    curl -Ls "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl" \
    -o /usr/local/bin/kubectl && \
    chmod +x /usr/local/bin/kubectl

RUN mkdir -p /tmp && touch /tmp/.keep

# ====================== Optimizer Stage ======================
FROM alpine:latest AS optimizer
RUN apk add --no-cache upx binutils
COPY --from=builder /app/kp /opt/kp

# 🔥 只压缩 kp，绝对不压 helm / kubectl（防崩溃、防segfault）
RUN strip --strip-all /opt/kp && upx --best --lzma /opt/kp

# ====================== Runtime Stage ======================
FROM scratch AS runtime

LABEL org.opencontainers.image.source="https://github.com/Ixecd/KubePivot"

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=builder --chown=65532:65532 /tmp /tmp
ENV HOME="/tmp"

# 🔥 生产级必加：PATH 必须有！！（主凶修复）
ENV PATH="/usr/local/bin:$PATH"

# 二进制文件（保持干净、不压缩、不损坏）
COPY --from=optimizer /opt/kp                /usr/local/bin/kp
COPY --from=builder   /usr/local/bin/kubectl /usr/local/bin/kubectl
COPY --from=builder   /usr/local/bin/helm    /usr/local/bin/helm

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/kp"]
CMD ["controller", "start"]