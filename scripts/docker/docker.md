# 构建一个Docker镜像

## 通过 `docker commit` 命令构建镜像（不建议）

1. 执行 `docker ps` 获取需要构建镜像的容器ID
2. 执行 `docker pause 容器ID` 暂停容器运行
3. 执行 `docker commit 容器ID 镜像名称:标签` 基于停止的容器来构建Docker镜像
4. 执行 `docker images 镜像名称:标签`，查看镜像是否成功构建

**应用场景：**
1. 构建临时的测试镜像
2. 容器被入侵后，使用docker commit，基于被入侵的容器构建镜像，从而保留现场，方便以后追溯

**不建议原因**
1. 包含编译构建、安装软件，以及程序运行产生的大量无用文件
2. 会丢失掉所有对该镜像的操作历史，无法还原镜像的构建过程，不利于维护

## 通过 `Dockerfile` 来构建镜像

 - `docker build`命令会读取`Dockerfile`的内容，并将Dockerfile的内容发送给Docker引擎，最终Docker引擎会解析Dockerfile中的每一条指令，构建出需要的镜像
 - `docker build`命令的格式为 `docker build [OPTIONS] PATH | URL | -`。 PATH、URL、- 指出了构建镜像的上下文（context），context中包含了构建镜像需要的Dockerfile文件， 可以通过 `-f, --file`选项，手动指定Dockerfile文件
 ```zsh
    $ docker build -f Dockerfile -t github.com/Ixecd/dev-toolkit:test .
 ```

- Dockerfile 包含了镜像制作的完整操作流程，其他开发者可以通过 Dockerfile 了解并复现制作过程
- Dockerfile 中的每一条指令都会创建新的镜像层，这些镜像可以被 Docker Daemnon 缓存。再次制作镜像时，Docker会尽量用缓存的镜像层，而不是重新逐层构建，这样可以节省时间和磁盘空间
- Dockerfile 的操作流程可以通过 `docker image history [镜像名称]` 查询，方便开发者查看变更记录

```Dockerfile
FROM contos:centos8
LABEL maintainer="<qc@example.com>"

RUN ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
RUN echo "Asia/Shanghai" > /etc/timezone

WORKDIR /opt/project
COPY bin /opt/project/bin/

ENTRYPOINT ["/opt/project/bin/bin]
```

- 这里选择centos:centos8作为基础镜像，是因为centos:centos8镜像中包含了基本的排障工具，例如vi、cat、curl、mkdir、cp等工具。

- 接着执行docker build命令来构建镜像
```zsh
    $ docker build -f Dockerfile -t github.com/Ixecd/dev-toolkit:test .
```

## 执行**docker build**后的构建流程为
1. docker build会将context中的文件打包传给Docker daemon。如果context中有.dockerignore文件，则会从上传列表中删除满足.dockerignore规则的文件。
    - 这里有个例外，如果.dockerignore文件中有.dockerignore或者Dockerfile，docker build命令在排除文件时会忽略掉这两个文件。如果指定了镜像的tag，还会对repository和tag进行验证。

2. docker build命令向Docker server发送HTTP请求，请求Docker server构建镜像，请求中包含了需要的context信息。

3. Docker server接收到构建请求之后，会执行以下流程来构建镜像：
    - 创建一个临时目录，并将context中的文件解压到该目录下。
    - 读取并解析Dockerfile，遍历其中的指令，根据命令类型分发到不同的模块去执行。
    - Docker构建引擎为每一条指令创建一个临时容器，在临时容器中执行指令，然后commit容器，生成一个新的镜像层。
    - 最后，将所有指令构建出的镜像层合并，形成build的最后结果。最后一次commit生成的镜像ID就是最终的镜像ID。
    
    为了提高构建效率，docker build默认会缓存已有的镜像层。如果构建镜像时发现某个镜像层已经被缓存，就会直接使用该缓存镜像，而不用重新构建。如果不希望使用缓存的镜像，可以在执行docker build命令时，指定--no-cache=true参数。

Docker匹配缓存镜像的规则为：遍历缓存中的基础镜像及其子镜像，检查这些镜像的构建指令是否和当前指令完全一致，如果不一样，则说明缓存不匹配。对于ADD、COPY指令，还会根据文件的校验和（checksum）来判断添加到镜像中的文件是否相同，如果不相同，则说明缓存不匹配。

这里要注意，缓存匹配检查不会检查容器中的文件。比如，当使用RUN apt-get -y update命令更新了容器中的文件时，缓存策略并不会检查这些文件，来判断缓存是否匹配。