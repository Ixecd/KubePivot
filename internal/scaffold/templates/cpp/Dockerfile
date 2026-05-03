FROM ubuntu:24.04 AS builder
RUN apt-get update && apt-get install -y build-essential cmake ccache libdrogon-dev libjsoncpp-dev && rm -rf /var/lib/apt/lists/*
ENV CCACHE_DIR=/ccache
WORKDIR /app
COPY CMakeLists.txt .
COPY src/ ./src/
RUN --mount=type=cache,target=/ccache mkdir build && cd build && cmake .. -DCMAKE_CXX_COMPILER_LAUNCHER=ccache && make -j$(nproc)

FROM ubuntu:24.04
RUN apt-get update && apt-get install -y libdrogon-dev libjsoncpp-dev && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/build/{{name}} /app
EXPOSE 8080
ENTRYPOINT ["/app"]
