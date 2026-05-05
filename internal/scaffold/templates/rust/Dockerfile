FROM rust:1.85-alpine AS builder
RUN apk add --no-cache musl-dev
WORKDIR /app
# 仅缓存依赖（假 main.rs）
COPY Cargo.toml Cargo.lock* ./
RUN mkdir src && echo 'fn main() {}' > src/main.rs
RUN cargo fetch
# 替换为真实源码并编译
RUN rm -rf src
COPY src/ ./src/
RUN cargo build --release

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/target/release/{{name}} /app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app"]
