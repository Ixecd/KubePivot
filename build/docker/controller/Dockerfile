# ====================== Builder Stage ======================
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app

RUN go env -w GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags '-s -w -extldflags "-static"' \
    -o kp ./cmd/kp

# ====================== Runtime Stage ======================
# scratch + ca-certificates（零 OS 漏洞，企业合规）
FROM scratch AS runtime

# OCI 标准标签
LABEL org.opencontainers.image.source="https://github.com/Ixecd/KubePivot" \
      org.opencontainers.image.version="v0.1.0" \
      org.opencontainers.image.vendor="KubePivot" \
      org.opencontainers.image.title="kubepivot-controller"

# CA 证书（Controller 需要访问 etcd / K8s API）
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# 复制二进制
COPY --from=builder /app/kp /kp

USER 65532:65532

ENTRYPOINT ["/kp"]
CMD ["controller", "start"]