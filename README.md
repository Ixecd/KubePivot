## 上云步骤

1. 去 阿里云 或者 腾讯云 或者 华为云 启动一个 Kubernetes 集群
2. 配置 `scripts/install/environment.sh` 脚本文件
    - 配置集群的连接信息
    - 配置数据库相关信息
    - 以及其他相关配置
3. 根据 步骤2 中配置的 $PROJECT_ROOT 变量
    - 执行 `make gen.defaultcofigs` 来生成项目各个组件的默认配置文件
    - 执行 `make gen.ca` 来生成项目各个组件的CA证书
    - 配置文件存放在 ${PROJECT_ROOT}/_output/configs/ 目录下
4. 在 阿里云 或者 腾讯云 平台上获取到启动的 Kubernetes 集群的 kubeconfig文件
    - 先备份当前的kubeconfig文件
    - 再将获取到的kubeconfig文件替换原来的 `~/.kube/config`
    - 通过 `kubectl get nodes` 来验证是否连接成功
5. 创建当前Project的命名空间
    - 执行 `kubectl create namespace ${PROJECT_NAME}` 来创建命名空间
6. 将Project各个服务的配置文件，以ConfigMap的形式保存在k8s集群中
    - 执行 `kubectl -n ${PROJECT_NAME} create configmap ${PROJECT_NAME} --from-file=${PROJECT_ROOT}/_output/configs/` 来将ConfigMap应用到k8s集群中
    - tips: 如果不想每次都输入 `-n ${PROJECT_NAME}` 来指定命名空间，可以执行 `kubectl config set-context --current --namespace=${PROJECT_NAME}` 来设置当前上下文的命名空间
7. 将Project各个服务使用的证书，以ConfigMap的形式创建在集群中
    - 执行 `kubectl -n ${PROJECT_NAME} create configmap ${PROJECT_NAME}-cert --from-file=${PROJECT_ROOT}/_output/certs/` 来将ConfigMap应用到k8s集群中
8. 创建镜像仓库访问密钥
    - 执行 `kubectl -n ${PROJECT_NAME} create secret docker-registry ${PROJECT_NAME}-regcred --docker-server=https://index.docker.io/v1/ --docker-username=${DOCKERHUB_USERNAME} --docker-password=${DOCKERHUB_PASSWORD}` 来创建密钥
    - 登录镜像仓库 `docker login -u ${DOCKERHUB_USERNAME} -p ${DOCKERHUB_PASSWORD}`
9. 创建Docker镜像，上传到镜像仓库
    - 执行 `docker build -t ${PROJECT_NAME}:${PROJECT_VERSION} .` 来创建镜像
    - 执行 `docker push docker.io/${PROJECT_NAME}:${PROJECT_VERSION}` 来上传镜像到镜像仓库
    - 也可以通过已经编写好的 makefile文件 来上传镜像（推荐）
    - 执行 `make push REGISTRY_PREFIX=docker.io/${PROJECT_NAME} VERSION=${PROJECT_VERSION}` 来上传镜像到镜像仓库
10. 修改 ${PROJECT_ROOT}/deployments/Project.yaml 配置文件
    - 需要注意，这里面镜像的tag必须和push的tag一致，如果不一致，改yaml文件中的 version
11. 部署Project应用
    - 执行`$ kubectl apply -f ${PROJECT_ROOT}/deployments/Project.yaml` 一键部署
    - 里面一般都是 deployment / statefulset / service / ingress 等资源
    - 或者是一些自定义资源，CRD等
12. 检查应用是否部署成功
    - 执行 `kubectl -n ${PROJECT_NAME} get all`
13. 测试Project应用
    - 登录 命令行工具 对应的Pod，来进行一些运维操作和冒烟测试
    - 执行 `kubectl -n ${PROJECT_NAME} exec -it ${PROJECT_NAME}-cli-0 -- /bin/bash` 来登录Pod
14. 运维操作（命令行工具对应的Pod中）
    - 就是用 命令行工具 的各个功能验证结果
15. 冒烟测试（命令行工具对应的Pod中）
    - 在pod里的`/opt/${PROJECT_NAME}/scripts/install` 目录下，执行 `./test.sh ${PROJECT_NAME}::test::smoke` 来验证应用是否正常运行
    - 这里的`/opt/${PROJECT_NAME}/scripts/install` 目录，以及测试的脚本文件，已经通过 makefile 文件生成好之后才打包为镜像的
16. 销毁集群以及资源
    - 执行 `kubectl delete ns ${PROJECT_NAME}` 来删除
17. 登录云平台
    - 删除对应的 Kubernetes 集群即可