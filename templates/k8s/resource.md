## K8s中资源定义方式（Yet Another Markup Language）

其实K8s支持yaml和json两种格式，json格式通过用来作为接口之间消息传递的数据格式，yaml格式则是用于资源的配置和管理。这两种格式之间可以通过 json2yaml 工具来进行转换。

**Pod资源定义**
```yaml
apiVersion: v1   # 必须 版本号， 常用v1  apps/v1
kind: Pod	 # 必须
metadata:  # 必须，元数据
  name: string  # 必须，名称
  namespace: string # 必须，命名空间，默认上default,生产环境为了安全性建议新建命名空间分类存放
  labels:   # 非必须，标签，列表值
    - name: string
  annotations:  # 非必须，注解，列表值
    - name: string
spec:  # 必须，容器的详细定义
  containers:  #必须，容器列表，
    - name: string　　　#必须，容器1的名称
      image: string		#必须，容器1所用的镜像
      imagePullPolicy: [Always|Never|IfNotPresent]  #非必须，镜像拉取策略，默认是Always
      command: [string]  # 非必须 列表值，如果不指定，则是一镜像打包时使用的启动命令
      args:　[string] # 非必须，启动参数
      workingDir: string # 非必须，容器内的工作目录
      volumeMounts: # 非必须，挂载到容器内的存储卷配置
        - name: string  # 非必须，存储卷名字，需与【@1】处定义的名字一致
          readOnly: boolean #非必须，定义读写模式，默认是读写
      ports: # 非必须，需要暴露的端口
        - name: string  # 非必须 端口名称
          containerPort: int  # 非必须 端口号
          hostPort: int # 非必须 宿主机需要监听的端口号，设置此值时，同一台宿主机不能存在同一端口号的pod， 建议不要设置此值
          proctocol: [tcp|udp]  # 非必须 端口使用的协议，默认是tcp
      env: # 非必须 环境变量
        - name: string # 非必须 ，环境变量名称
          value: string  # 非必须，环境变量键值对
      resources:  # 非必须，资源限制
        limits:  # 非必须，限制的容器使用资源的最大值，超过此值容器会推出
          cpu: string # 非必须，cpu资源，单位是core，从0.1开始
          memory: string 内存限制，单位为MiB,GiB
        requests:  # 非必须，启动时分配的资源
          cpu: string 
          memory: string
      livenessProbe:   # 非必须，容器健康检查的探针探测方式
        exec: # 探测命令
          command: [string] # 探测命令或者脚本
        httpGet: # httpGet方式
          path: string  # 探测路径，例如 http://ip:port/path
          port: number  
          host: string  
          scheme: string
          httpHeaders:
            - name: string
              value: string
          tcpSocket:  # tcpSocket方式，检查端口是否存在
            port: number
          initialDelaySeconds: 0 #容器启动完成多少秒后的再进行首次探测，单位为s
          timeoutSeconds: 0  #探测响应超时的时间,默认是1s,如果失败，则认为容器不健康，会重启该容器
          periodSeconds: 0  # 探测间隔时间，默认是10s
          successThreshold: 0  # 
          failureThreshold: 0
        securityContext:
          privileged: false
        restartPolicy: [Always|Never|OnFailure]  # 容器重启的策略，
        nodeSelector: object  # 指定运行的宿主机
        imagePullSecrets:  # 容器下载时使用的Secrets名称，需要与valumes.secret中定义的一致
          - name: string
        hostNetwork: false
        volumes: ## 挂载的共享存储卷类型
          - name: string  # 非必须，【@1】
          emptyDir: {}
          hostPath:
            path: string
          secret:  # 类型为secret的存储卷，使用内部的secret内的items值作为环境变量
            secrectName: string
            items:
              - key: string
                path: string
            configMap:  ## 类型为configMap的存储卷
              name: string
              items:
                - key: string
                  path: string
```

**Deployment资源定义**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  labels: # 设定资源的标签
    app: server
  name: server
  namespace: default
spec:
  progressDeadlineSeconds: 10 # 指定多少时间内不能完成滚动升级就视为失败，滚动升级自动取消
  replicas: 1 # 声明副本数，建议 >= 2
  revisionHistoryLimit: 5 # 设置保留的历史版本个数，默认是10
  selector: # 选择器
    matchLabels: # 匹配标签
      app: server # 标签格式为key: value对
  strategy: # 指定部署策略
    rollingUpdate:
      maxSurge: 1 # 最大额外可以存在的副本数，可以为百分比，也可以为整数
      maxUnavailable: 1 # 表示在更新过程中能够进入不可用状态的 Pod 的最大值，可以为百分比，也可以为整数
    type: RollingUpdate # 更新策略，包括：重建(Recreate)、RollingUpdate(滚动更新)
  template: # 指定Pod创建模板。注意：以下定义为Pod的资源定义
    metadata: # 指定Pod的元数据
      labels: # 指定Pod的标签
        app: server
    spec:
      affinity:
        podAntiAffinity: # Pod反亲和性，尽量避免同一个应用调度到相同Node
          preferredDuringSchedulingIgnoredDuringExecution: # 软需求
          - podAffinityTerm:
              labelSelector:
                matchExpressions: # 有多个选项，只有同时满足这些条件的节点才能运行 Pod
                - key: app
                  operator: In # 设定标签键与一组值的关系，In、NotIn、Exists、DoesNotExist
                  values:
                  - server
              topologyKey: kubernetes.io/hostname
            weight: 100 # weight 字段值的范围是1-100。
      containers:
      - command: # 指定运行命令
        - /opt/project/bin/server # 运行参数
        - --config=/etc/project/server.yaml
        image: busybox:latest # 镜像名，遵守镜像命名规范
        imagePullPolicy: Always # 镜像拉取策略。IfNotPresent：优先使用本地镜像；Never：使用本地镜像，本地镜像不存在，则报错；Always：默认值，每次都重新拉取镜像
        # lifecycle: # kubernetes支持postStart和preStop事件。当一个容器启动后，Kubernetes将立即发送postStart事件；在容器被终结之前，Kubernetes将发送一个preStop事件
        name: server # 容器名称，与应用名称保持一致
        ports: # 端口设置
        - containerPort: 8443 # 容器暴露的端口
          name: secure # 端口名称
          protocol: TCP # 协议，TCP和UDP
        livenessProbe: # 存活检查，检查容器是否正常，不正常则重启实例
          httpGet: # HTTP请求检查方法
            path: /healthz # 请求路径
            port: 8080 # 检查端口
            scheme: HTTP # 检查协议
          initialDelaySeconds: 5 # 启动延时，容器延时启动健康检查的时间
          periodSeconds: 10 # 间隔时间，进行健康检查的时间间隔
          successThreshold: 1 # 健康阈值，表示后端容器从失败到成功的连续健康检查成功次数
          failureThreshold: 1 # 不健康阈值，表示后端容器从成功到失败的连续健康检查成功次数
          timeoutSeconds: 3 # 响应超时，每次健康检查响应的最大超时时间
        readinessProbe: # 就绪检查，检查容器是否就绪，不就绪则停止转发流量到当前实例
          httpGet: # HTTP请求检查方法
            path: /healthz # 请求路径
            port: 8080 # 检查端口
            scheme: HTTP # 检查协议
          initialDelaySeconds: 5 # 启动延时，容器延时启动健康检查的时间
          periodSeconds: 10 # 间隔时间，进行健康检查的时间间隔
          successThreshold: 1 # 健康阈值，表示后端容器从失败到成功的连续健康检查成功次数
          failureThreshold: 1 # 不健康阈值，表示后端容器从成功到失败的连续健康检查成功次数
          timeoutSeconds: 3 # 响应超时，每次健康检查响应的最大超时时间
        startupProbe: # 启动探针，可以知道应用程序容器什么时候启动了
          failureThreshold: 10
          httpGet:
            path: /healthz
            port: 8080
            scheme: HTTP
          initialDelaySeconds: 5
          periodSeconds: 10
          successThreshold: 1
          timeoutSeconds: 3
        resources: # 资源管理
          limits: # limits用于设置容器使用资源的最大上限,避免异常情况下节点资源消耗过多
            cpu: "1" # 设置cpu limit，1核心 = 1000m
            memory: 1Gi # 设置memory limit，1G = 1024Mi
          requests: # requests用于预分配资源,当集群中的节点没有request所要求的资源数量时,容器会创建失败
            cpu: 250m # 设置cpu request
            memory: 500Mi # 设置memory request
        terminationMessagePath: /dev/termination-log # 容器终止时消息保存路径
        terminationMessagePolicy: File # 仅从终止消息文件中检索终止消息
        volumeMounts: # 挂载日志卷
        - mountPath: /etc/project/server.yaml # 容器内挂载镜像路径
          name: project # 引用的卷名称
          subPath: server.yaml # 指定所引用的卷内的子路径，而不是其根路径。
        - mountPath: /etc/project/cert
          name: project-cert
      dnsPolicy: ClusterFirst
      restartPolicy: Always # 重启策略，Always、OnFailure、Never
      schedulerName: default-scheduler # 指定调度器的名字
      imagePullSecrets: # 在Pod中设置ImagePullSecrets只有提供自己密钥的Pod才能访问私有仓库
        - name: registry # 镜像仓库的Secrets需要在集群中手动创建
      securityContext: {} # 指定安全上下文
      terminationGracePeriodSeconds: 5 # 优雅关闭时间，这个时间内优雅关闭未结束，k8s 强制 kill
      volumes: # 配置数据卷，类型详见https://kubernetes.io/zh/docs/concepts/storage/volumes
      - configMap: # configMap 类型的数据卷
          defaultMode: 420 #权限设置0~0777，默认0664
          items:
          - key: server.yaml
            path: server.yaml
          name: server # configmap名称
        name: server # 设置卷名称，与volumeMounts名称对应
      - configMap:
          defaultMode: 420
          name: cert
        name: cert
```

**ConfigMap资源定义**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-config
data:
  db.host: 172.168.10.1
  db.port: 3306
```
- 除此之外，kubectl命令行工具还提供3种创建ConfigMap的方式
1. 通过`--from-literal`参数创建
```zsh
  $ kubectl create configmap test-config --from-literal=db.host=172.168.10.1 --from-literal=db.port=3306
```
2. 通过`--from-file`参数创建
```zsh
  $ echo -n 172.168.10.1 > db.host
  $ echo -n 3306 > db.port
  $ kubectl create configmap test-config --from-file=server.yaml
```
- 这里--from-file的值也可以是一个目录。当值是目录时，目录中的文件名为key，目录的内容为value
3. 通过`--from-env-file`参数创建
```zsh
  $ cat << EOF > env.txt
  db.host=172.168.10.1
  db.port=3306
  EOF
  $ kubectl create configmap test-config --from-env-file=env.txt
```

**Service资源定义**
```yaml
apiVersion: v1
kind: Service
metadata:
  labels:
    app: server
  name: server
  namespace: default
spec:
  clusterIP: 192.168.0.231 # 虚拟服务地址VIP
  externalTrafficPolicy: Cluster # 表示此服务是否希望将外部流量路由到节点本地或集群范围的端点
  ports: # service需要暴露的端口列表
  - name: https #端口名称
    nodePort: 30443 # 当type = NodePort时，指定映射到物理机的端口号
    port: 8443 # 服务监听的端口号
    protocol: TCP # 端口协议，支持TCP和UDP，默认TCP
    targetPort: 8443 # 需要转发到后端Pod的端口号
  selector: # label selector配置，将选择具有label标签的Pod作为其后端RS
    app: server
  sessionAffinity: None # 是否支持session
  type: NodePort # service的类型，指定service的访问方式，默认为clusterIp
```

**yaml文件编写技巧**
1. 使用在线工具自动生成模板yaml文件
    - YAML文件很复杂，完全从0开始编写一个YAML定义文件，工作量大、容易出错，没必要
    - 可以通过 **k8syaml** 在线工具，自动生成模板yaml文件，目前能够生成 Deployment、Statefulset、DaemonSet类型的YAML文件
2. 使用`kubectl run`命令获取YAML模板
```zsh
  $ kubectl run nginx --image=nginx --dry-run=client -o yaml > nginx.yaml
  $ cat my-nginx.yaml
  apiVersion: v1
  kind: Pod
  metadata:
    creationTimestamp: null
    labels:
      run: nginx
    name: nginx
  spec:
    containers:
    - image: nginx
      imagePullPolicy: IfNotPresent
      name: nginx
      resources: {}
    dnsPolicy: ClusterFirst
    restartPolicy: Always
  status: {}
```
- 这里`--dry-run=client`参数表示不实际创建Pod，只是生成YAML模板
- `-o yaml`参数表示输出YAML格式的模板
- `nginx.yaml`表示输出的文件名

3. 导出集群中已有的资源描述
- 如果想创建的K8s资源跟集群中已经创建的资源描述相近或者一致的时候，可以选择导出集群中已经创建资源的YAML描述，并基于导出的YAML文件进行修改。
```yaml
  $ kubectl get deployment nginx -o yaml > nginx.yaml
```
- 接着修改nginx.yaml文件即可

**使用Kubernetes YAML时的一些推荐工具**
1. kubeval
- kubeval用来验证kubernetes YAML是否符合Kubernetes API模式
- 在部署的早期，不用访问集群就能发现YAML文件的错误
2. kube-score
    - kube-score用来评估Kubernetes资源的配置是否符合最佳实践
    1. 以非ROOT用户启动容器
    2. 为Pods设置健康检查
    3. 定义资源请求和限制
    - 检查结果有 OK、 SKIPPED、 WARNING 和 CRITICAL
    1. CRITICAL 表示需要修复的
    2. WARNING 表示需要注意的
    3. SKIPPED 是因为某些原因略过的检查
    4. OK 是验证通过的
    - 详细的错误原因和解决方案，可以通过使用`-o human`选项来打开
    - `-o ci`选项可以将检查结果输出为CI/CD系统可以解析的格式


