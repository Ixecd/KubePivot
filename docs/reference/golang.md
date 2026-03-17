## Go 依赖包管理

**Go Modules包管理方案**
- 可以使得包的管理更加简单
- 支持版本管理
- 可以校验依赖包的哈希值，确保包的一致性，增加安全性
- 内置在几乎所有的go命令中，包括 `go get`, `go build`, `go test`, `go install`, `go list`, `go run` 等
- 具有 Global Caching特性，不同项目的相同模块版本，只会在服务器上缓存一份

**6-2-2-1-1**
- 六个环境变量: GO111MODULE, GOPATH, GOROOT, GOBIN, GOPROXY, GOSUMDB
- 两个概念: Go Module proxy, Go checksum database
- 两个主要文件: go.mod, go.sum
- 一个主要管理命令: go mod
- 一个build flag

**模块下载**
- 在执行 `go get` 等命令时，会自动下载模块
- 可以通过代理下载
- 指定版本号下载
- 按最小版本下载

**通过代理下载模块**
- Go 1.13版本，引入了一个新的环境变量GOPROXY，用于设置Go模块代理(Go Module proxy)。模块代理可以使Go命令直接从代理服务器下载模块。GOPROXY默认值为`https://proxy.golang.org,direct`，代理服务器可以指定多个，多个之间使用`,`分隔。当下载模块时，会优先从指定的代理服务器上下载。如果下载失败，Go命令会尝试从下一个代理服务器下载。
- direct是一个特殊的指示符，用于指示Go命令直接从模块的原始URL(比如 github)下载模块，而不使用代理服务器。
- 如果 `GOPROXY=off`，则Go命令不会尝试从代理服务器下载模块。

实际开发过程中，很多模块从私有gitlab仓库拉取，通过代理服务器访问会报错，这个时候需要我们将这些模块添加到环境变量`GOPRIVATE`中。

- GONOPROXY、GONOSUMDB、GOPRIVATE都支持通配符，多个域名之间用逗号分隔

**指定版本号下载**
- `go get golang.org/x/text@latest` 下载最新版本
- `go get golang.org/x/text` 效果和上面相同
- `go get golang.org/x/text@v0.3.0` 下载指定版本
- `go get golang.org/x/text@v0` 下载前缀是v0的最新版本
- `go get golang.org/x/text@master` 下载最新的master分支最新的commit

**go.mod & go.sum**
1. go.mod 语句
go.mod 文件中包含了4个语句，分别是module、require、replace、exclude。
- module: 用来定义当前项目的模块路径。
- go: 用来设置Go的版本，声明模块期望的语言/工具行为基线；更高版本的 Go 工具链会据此启用/保持相容行为。
- require: 用来设置一个特定的模块版本,格式为 `<导入包路径><版本> [//indirect]`
- exclude: 用来从使用中排除一个特定的模块版本，如果我们知道模块的某个版本有严重的问题，就可以使用exclude将该版本排除掉
- replace: 用来将一个模块版本替换为另外一个模块版本，可以替换为本地磁盘的相对路径，也可以是本地磁盘的绝对路径，也可以是网络路径

**使用replace的场景**
- 开启 Go Modules后，缓存的依赖包是只读的，但在日常开发调试中，我们可以需要修改依赖包的代码来进行调试，这时候可以将依赖包存放到一个新的位置，然后使用replace将依赖包替换为新的位置
- 如果有一些依赖包在Go命令运行时无法下载，就可以通过其他途径下载该依赖包，上唇到开发构建机，并在go.mod中替换为这个包
- 在项目开发初期，A项目依赖B项目的包，但B项目因为种种原因没有push到仓库，这个时候也可以在go.mod中把依赖包替换为B项目的本地磁盘路径
- 在国内访问golang.org/x的各个包都需要翻墙，可以在go.mod中使用replace，替换成github上对应的包
- exclude和replace只作用于当前主模块，不影响主模块依赖的其他模块

**go.mod版本号**
go.mod文件中有很多版本号格式
- 如果模块具有符合语义化版本格式的tag，会直接展示tag的值
- 除了v0和v1之外，主版本号必须显式的出现在模块路径的尾部
- 对于没有tag的模块，Go命令会选择Master分支上最新的commit，并根据commit时间和哈希值生成一个符合语义化版本的版本号
- 如果模块名字跟版本不符合规范，go会在go.mod的版本后加`+incompatible`表示
- 如果go.mod中的包是间接依赖，则会添加`// indirect`注释

**go.sum**
Go会根据go.mod文件中记载的依赖包及其版本下载包源码，但是下载的包可能被篡改，缓存在本地的包也可能被篡改。单单一个go.mod文件不能保证包的一致性，go.sum文件记录了每个依赖包的哈希值，确保下载的包与go.mod中指定的版本一致

go.sum文件用来记录每个依赖包的hash值，在构建时，如果本地的依赖包hash值与go.sum文件中记录簿不一致时，则会拒绝构建，go.sum中记录的依赖包是所有依赖包，包括直接和间接

正常情况下，每个依赖包会包含两条记录，分别是依赖包所有文件的哈希值和该依赖包go.mod的哈希值

**校验**
执行构建时，go命令会从本地缓存中查找所有的依赖包，并计算这些依赖包的哈希值，之后与go.sum中记录的哈希值进行对比，如果哈希值不一致，那么校验失败，停止构建

Go命令倾向于相信依赖包被修改过，当我们在go get依赖包时，包的哈希值会经过校验和数据库(checksum database)进行校验，校验通过才会被加入到go.sum文件中

校验和数据库可以通过环境变量`GOSUMDB`指定，`GOSUMDB`是一个Web服务器，默认值是`sum.golang.org`。该服务可以用来查询依赖包指定版本的哈希值，保证拉取到的模块版本数据没有经过篡改

如果设置`GOSUMDB=off`或者使用go get的时候启用了 -insecure参数，就存在一定隐患

Go checksum database可以被Go module proxy代理，设置了GOPROXY之后就可以不用设置GOSUMDB

注意 go.sum文件也应该提交到 Git 仓库中

**导入规则**
- import的时候是 模块名/包的路径
- 用的时候是 包名