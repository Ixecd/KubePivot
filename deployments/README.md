## 制作项目 Chart 包

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
