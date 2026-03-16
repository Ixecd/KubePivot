# syntax=docker/dockerfile:1.7
FROM golang:1.23-alpine AS builder

# 服务名通过构建参数传入（dtk 会自动替换）
ARG SERVICE_NAME

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/${SERVICE_NAME} ./cmd/${SERVICE_NAME}

# 最终运行镜像（极小）
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/bin/${SERVICE_NAME} /app/bin/

EXPOSE ${PORT:-8080}

CMD ["/app/bin/${SERVICE_NAME}"]