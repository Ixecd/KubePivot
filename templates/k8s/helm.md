## Helm 高效管理 Kubernetes 应用

Hlem是K8s的包管理器，类似于 Python的 pip， centos的 yum， MacOs的 brew 等。Helm主要用来管理Chart包。Helm Chart包中包含一些列YAML格式的Kubernetes资源定义文件，以及这些资源的配置，可以通过Helm Chart包来整体维护这些资源

Helm提供了命令行工具，该工具可以基于Chart包一键创建应用，在创建应用时，可以自定义Chart配置。应用发布者可以通过Helm打包应用、管理应用依赖关系、管理应用版本，并发布应用到软件仓库；对于使用者来说，使用Helm后不需要编写复杂的应用部署文件，可以非常方便地在K8s上查找、安装、升级、回滚、卸载应用程序

**Helm中的三大基本概念**
1. Chart: 代表一个Helm包。它包含了在K8s集群中运行应用程序、工具或服务所需的所有YAML格式的资源定义文件
2. Repository: 用于存放和共享 Helm Chart 的地方，类似于存放源码的Github Repository，以及存放镜像的Docker的Repository
3. Release: 是运行在 k8s 集群中的 Chart 的实例。一个Chart通常可以在同一个集群中安装多次。每一次安装都会创建一个新的 Release

在实际场景下有测试环境、预发环境、生产环境三个环境，每个环境中部署一个应用，应用中包含了多个服务，每个服务又包含了自己的配置，不同服务之间的配置有些是共享的

每个服务由一个复杂的K8s YAML文件定义并创建，传统的方式，去维护这些YAML格式文件，并在不同环境下使用不同的配置去创建应用，是一键非常复杂的工作，并且后期YAML文件和K8s集群中部署应用的维护都很复杂。随着微服务规模越来越大，会面临以下挑战：
1. 微服务化服务数量急剧增多，给服务管理带来了极大的挑战
2. 服务数量急剧增多，增加了管理难度，对运维部署是一种挑战
3. 随着服务数量的增多，对服务配置管理也提出了更高的要求
4. 随着服务数量增加，废物依赖关系也变得更加复杂，服务依赖关系的管理难度增大
5. 在环境信息管理方面，在新环境快速部署一个复杂应用变得更加困难

Helm中，可以理解为主要包含两类文件：模板文件和配置文件

模板文件一般有多个，配置文件通常只包含一个。Helm的模板文件基于text/template模板文件，提供了更加强大的模板渲染能力。Helm可以将配置文件中的值渲染进模板文件中，最终生成一个可以部署的K8s YAML格式的资源定义文件

Chart模板一个应用只用编写一次，可以重复使用。在部署时，可以指定不同的配置，从而将应用部署在不同的环境中，或者在同一环境中部署不同配置的应用

可以指定一些其他的选项，来定义 Helm 在安装、升级、回滚期间的行为
1. --timeout: 定义 Helm 操作的超时时间，默认值为 5 分钟，这里如果超时了，直接会返回错误信息，但是不会删除已经创建的资源
2. --wait: 表示必须要等到所有的 Pods 都处于 ready 状态，PVC都被绑定、Deployments处在 ready状态的 Pods 个数达到最小值（Desired - maxUnavailable），才会标记该 Release 成功
3. --no-hooks: 禁用 Helm Chart 中的钩子函数，默认值为 false
4. --keep-history: 执行 `helm uninstall` 时，保留 Release 的历史记录，默认值为 false，通过 `helm status`来查看版本信息

Q:
    使用Helm创建服务，是否会存在先启动服务，再创建服务配置，从而导致服务启动时加载配置失败的问题？如果有，Helm可以怎样解决这个问题？
A:
    首先Helm在安装时有**内置的依赖排序逻辑**，会根据资源类型顺序创建，但是这样并**不能完全消除**时序问题。
    Helm Hooks是最可靠的解决方案，在 YAML文件中的 annotations 中定义 `"helm.sh/hook": pre-install, pre-upgrade` 等钩子函数，在安装或升级时，会按照定义的顺序执行。同时可以使用 `"helm.sh/hook-weight": "10"` 来定义钩子函数的执行顺序，权重值越小，越先执行

## 制作 Chart 包
- 我们假设项目源码的根目录为${PROJECT_ROOT}，进入${PROJECT_ROOT}/deployments目录，这里的环境变量都是在scripts/install/environment.sh中设置定义的。
1. 创建一个 Chart 模板
    - 详细的创建流程看官网[Chart 开发指南](https://helm.sh/zh/docs/chart_template_guide/getting_started/)
    - 可以通过 `helm create` 来快速创建一个模板Chart，基于该Chart进行修改，来得到自己的Chart
    - 通过执行命令 `helm create ${PROJECT_NAME}`会在当前目录下生成一个${PROJECT_NAME}目录，里面存放的就是Chart文件
    - 注意 Chart 名称必须是小写字母和数字，单词之间可以使用横杠`-`分隔
    - 尽可能使用 SemVer 2 来表示版本号
    - YAML 文件应该按照双空格的形式编写（一定不要使用TAB键）
        1. 在项目的根目录下放一个 .editorconfig 文件，定义编辑器的缩进风格为双空格
        2. Git提交前强制检查（pre-commit hook），确保所有的YAML文件都按照双空格的形式编写
        3. 再搭配一个 `.yamllint` 配置文件，来检查YAML文件是否符合规范
2. 编写项目的Chart文件
    - 基于 `helm create` 创建出来的模板Chart包，来构建自己的Chart包。在`templates`目录下编写项目服务组件的相关K8s YAML文件，来定义应用的资源。
    - 在编辑 Chart 时，可以通过 `helm lint`来验证格式是否正确
3. 修改Chart的配置文件，添加自定义配置信息
    - 编辑 `values.yaml`文件，定制化应用的配置信息，比如数据库连接信息、服务端口、环境变量等
    - 最佳实践如下所示
        1. 变量名称以小写字母开头，单词按驼峰区分，例如 servicePort
        2. 给所有字符串类型的值加上引号
        3. 为了避免整数装换问题，将整型存储为字符串更好，并用 {{ int $value }} 在模板中将字符串转回整型
        4. values.yaml中定义的每个属性都应该文档化。文档字符串应该以他要描述的属性开头，并至少给出一句描述
    - 注意：所有Helm内置变量都以大写字母开头，以便与用户定义的 value 进行区分，例如 `.Release.Name`
    - 为了安全，values.yaml文件中只配置k8s资源相关的配置项，例如Deployment副本数、Servie端口等
    - 组件的配置文件，创建单独的ConfigMap，在Deployment中引用
4. 打包Chart，并上传到Chart仓库（可选）