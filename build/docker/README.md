# build/docker

每个需要容器化的服务在此目录下创建对应子目录：

build/docker/<service-name>/
  Dockerfile   # 使用 BASE_IMAGE 占位符，Makefile会自动替换
  build.sh     # 构建前的钩子脚本，可为空