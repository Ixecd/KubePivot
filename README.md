## 脚手架初始化

在当前仓库根目录下执行：

```bash
go run ./cmd/dtk init --name demo-svc --module github.com/you/demo-svc
```

安装 `dtk` 二进制（安装到 `GOBIN` 或 `GOPATH/bin`）：

```bash
make install
```

安装后可以直接使用：

```bash
dtk init --name demo-svc --module github.com/you/demo-svc --output=~/demo-svc
```

远程安装（Go 1.20+）：

```bash
go install github.com/Ixecd/dev-toolkit/cmd/dtk@latest
```

## 配置与组件

默认会扫描 `cmd/*` 作为组件，镜像目录扫描 `build/docker/*`，因此可以支持任意数量的组件。

可选配置文件：`configs/project.env`（Makefile 与 `scripts/install/environment.sh` 会读取）：

## 版本发布（Tag）

发布 tag（示例 `v0.1.0`）：

```bash
make release.tag VERSION=v0.1.0
```

或手动执行：

```bash
git tag -a v0.1.0 -m "release v0.1.0"
git push origin v0.1.0
```

如果需要从其他目录使用，可指定模板根目录：

```bash
go run ./cmd/dtk init --name demo-svc --module github.com/you/demo-svc --template /path/to/dev-toolkit
```
