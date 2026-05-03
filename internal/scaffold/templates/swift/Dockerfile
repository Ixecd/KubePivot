FROM swift:6.0-noble AS builder
WORKDIR /app
COPY Package.swift Package.resolved* ./
RUN swift package resolve
COPY . .
RUN swift build -c release --static-swift-stdlib

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/.build/release/{{name}} /app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app"]
