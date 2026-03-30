FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o kp ./cmd/kp

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/kp .
EXPOSE 8080
CMD ["./kp"]