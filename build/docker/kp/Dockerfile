# ====================== Builder Stage ======================
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app

RUN apk add --no-cache ca-certificates
RUN go env -w GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags '-s -w -extldflags "-static"' \
    -o kp ./cmd/kp 

# ====================== Runtime Stage ======================
FROM scratch AS runtime

LABEL org.opencontainers.image.source="https://github.com/Ixecd/KubePivot"

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/kp /usr/local/bin/kp

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/kp"]