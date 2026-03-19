FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o dtk ./cmd/dtk

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/dtk .
EXPOSE 8080
CMD ["./dtk"]