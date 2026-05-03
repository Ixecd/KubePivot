FROM alpine:3.21 AS builder
RUN apk add --no-cache zig
WORKDIR /app
COPY build.zig .
COPY src/ ./src/
RUN zig build-exe src/main.zig -O ReleaseSafe -target x86_64-linux-musl --static

FROM scratch
COPY --from=builder /app/main /app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app"]
