FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go env -w GOPROXY=https://goproxy.cn,direct
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o kp ./cmd/kp

FROM alpine:3.20
WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata curl bash openssl && \
    curl -LO "https://dl.k8s.io/release/v1.32.0/bin/linux/$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')/kubectl" && \
    chmod +x kubectl && mv kubectl /usr/local/bin/

RUN curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

COPY --from=builder /app/kp /usr/local/bin/kp

CMD ["kp", "controller", "start"]
