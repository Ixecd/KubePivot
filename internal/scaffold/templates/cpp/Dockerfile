FROM ubuntu:24.04 AS builder
RUN apt-get update && apt-get install -y build-essential cmake && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY CMakeLists.txt .
COPY src/ ./src/
RUN mkdir build && cd build && cmake .. && make -j$(nproc)

FROM ubuntu:24.04
COPY --from=builder /app/build/{{name}} /app
EXPOSE 8080
ENTRYPOINT ["/app"]
