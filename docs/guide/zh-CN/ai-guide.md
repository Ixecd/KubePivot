# kp AI 使用手册

> 适用：KubePivot v0.9.0+

---

## 概览

`kp ai-plan` 扫描你的代码仓库，调用 LLM 分析项目结构，自动生成 `configs/components.yaml`，省去手动填写服务配置的步骤。

```
代码仓库
  ├── cmd/wallet-service/main.go   ─┐
  ├── cmd/chain-miner/main.go       ├─→ LLM 分析 → components.yaml → kp deploy
  ├── go.mod                        │
  └── ...                          ─┘
```

---

## 快速开始

```bash
# 1. 配置 LLM provider（以 Grok 为例）
export KP_LLM_PROVIDER=grok
export KP_LLM_API_KEY=xai-your-key

# 2. 先看建议，不写入文件
kp ai-plan --suggest-only

# 3. 确认无误后写入
kp ai-plan

# 4. 部署
kp deploy
```

---

## 支持的 LLM Provider

| Provider            | KP_LLM_PROVIDER | 默认模型                   | API 地址                                                    |
| ------------------- | --------------- | -------------------------- | ----------------------------------------------------------- |
| Grok（xAI）         | `grok`          | `grok-3`                   | `https://api.x.ai/v1/chat/completions`                      |
| Claude（Anthropic） | `claude`        | `claude-sonnet-4-20250514` | `https://api.anthropic.com/v1/messages`                     |
| OpenAI              | `openai`        | `gpt-4o`                   | `https://api.openai.com/v1/chat/completions`                |
| 豆包（字节跳动）    | `doubao`        | `doubao-pro-32k`           | `https://ark.cn-beijing.volces.com/api/v3/chat/completions` |

---

## 环境变量

| 变量              | 说明                       | 默认值           |
| ----------------- | -------------------------- | ---------------- |
| `KP_LLM_PROVIDER` | LLM provider 名称          | `claude`         |
| `KP_LLM_API_KEY`  | API key，**必填**          | -                |
| `KP_LLM_MODEL`    | 模型名，覆盖默认值         | 各 provider 默认 |
| `KP_LLM_ENDPOINT` | API 地址，私有化部署时覆盖 | 各 provider 默认 |

---

## 各 Provider 配置示例

### Grok（推荐，速度快）

```bash
export KP_LLM_PROVIDER=grok
export KP_LLM_API_KEY=xai-xxxxxxxxxxxxxxxx
# 可选：指定模型
export KP_LLM_MODEL=grok-3-mini   # 更快更便宜
```

API key 在 [console.x.ai](https://console.x.ai) 获取。

### Claude

```bash
export KP_LLM_PROVIDER=claude
export KP_LLM_API_KEY=sk-ant-xxxxxxxxxxxxxxxx
export KP_LLM_MODEL=claude-haiku-4-5-20251001   # 更快
```

API key 在 [console.anthropic.com](https://console.anthropic.com) 获取。

### OpenAI

```bash
export KP_LLM_PROVIDER=openai
export KP_LLM_API_KEY=sk-xxxxxxxxxxxxxxxx
export KP_LLM_MODEL=gpt-4o-mini   # 更便宜
```

### 豆包

```bash
export KP_LLM_PROVIDER=doubao
export KP_LLM_API_KEY=your-ark-key
export KP_LLM_MODEL=doubao-pro-32k
```

API key 在[火山引擎控制台](https://console.volcengine.com/ark)获取。

### 私有化部署

任何 OpenAI 兼容的私有部署都可以通过 `KP_LLM_ENDPOINT` 接入：

```bash
export KP_LLM_PROVIDER=openai   # 或其他兼容的 provider
export KP_LLM_API_KEY=your-key
export KP_LLM_ENDPOINT=http://your-private-llm:8080/v1/chat/completions
export KP_LLM_MODEL=your-model-name
```

---

## 命令参考

```bash
kp ai-plan [flags]
```

| Flag             | 说明                               | 默认值           |
| ---------------- | ---------------------------------- | ---------------- |
| `--suggest-only` | 只打印建议，不写入 components.yaml | false            |
| `--desc`         | 补充描述，帮助 LLM 更准确分析      | 空               |
| `--namespace`    | K8s namespace                      | 读 project.env   |
| `--context`      | kubectl context                    | 读 project.env   |
| `--kubeconfig`   | kubeconfig 路径                    | `~/.kube/config` |

---

## LLM 扫描的内容

`kp ai-plan` 会自动收集以下信息发给 LLM：

```
项目目录结构（depth 3）
go.mod（模块路径 + 依赖）
cmd/ 下每个服务的 main.go（前 50 行）
configs/components.yaml（已有内容，供参考）
build/docker/*/Dockerfile（前 500 字符）
README.md（前 50 行）
--desc 补充描述（如果有）
```

**不会发送**：业务代码、数据库连接串、`.env` 文件、任何敏感配置。

---

## LLM 分析输出格式

LLM 输出 JSON，kp 解析后渲染成 components.yaml：

```json
{
  "components": [
    {
      "name": "wallet-service",
      "port": 2113,
      "image": "wallet-service",
      "replicas": 2,
      "cpu": "200m",
      "memory": "256Mi",
      "storage": "0"
    },
    {
      "name": "chain-miner",
      "port": 0,
      "image": "",
      "replicas": 1,
      "cpu": "100m",
      "memory": "128Mi",
      "storage": "0"
    }
  ],
  "reasoning": "wallet-service 是核心 HTTP 服务..."
}
```

**image 为空**：LLM 判断为纯 CLI 工具，kp 会跳过 build/push，只作为规划参考。

---

## 规划原则

LLM 会按以下原则给出资源建议，你可以在 components.yaml 里手动调整：

| 服务类型                 | replicas | cpu      | memory             |
| ------------------------ | -------- | -------- | ------------------ |
| 核心 HTTP 服务（高可用） | 2+       | 200-500m | 256-512Mi          |
| 普通 HTTP 服务           | 1        | 100-200m | 128-256Mi          |
| 后台 worker              | 1        | 100m     | 128Mi              |
| CLI 工具                 | 1        | 100m     | 64Mi（image 为空） |

**不会列入 components.yaml 的**：postgres、etcd、bitcoind、geth-rpc 等基础设施组件，这些由 Helm chart 直接管理。

---

## 实战示例

### web3-blitz（BTC/ETH 充提币系统）

```bash
export KP_LLM_PROVIDER=grok
export KP_LLM_API_KEY=xai-xxx

cd ~/web3-blitz
kp ai-plan --desc "BTC/ETH 充提币系统，wallet-service 是核心 HTTP 服务，bitcoind 和 geth-rpc 是基础设施不要列进来"
```

输出：

```
📋 AI 规划结果：
  wallet-service    replicas=2  cpu=200m  memory=256Mi
  chain-miner       replicas=1  cpu=100m  memory=128Mi  (CLI 工具，跳过 build/push)

💡 分析依据：wallet-service 是核心业务服务，处理充提币逻辑，需高可用故
   replicas 设为 2...
```

---

## 常见问题

**KP_LLM_API_KEY 未配置**

```
初始化 LLM 客户端失败: KP_LLM_API_KEY 未配置
  export KP_LLM_API_KEY=your-api-key
  export KP_LLM_PROVIDER=grok
```

**LLM 返回的 JSON 解析失败**

LLM 偶尔会在 JSON 外面包 markdown 代码块，kp 会自动清理。如果还是失败，加 `--suggest-only` 看原始输出，或者换个模型重试。

**扫描不到服务**

确认在项目根目录下执行，且 `cmd/` 目录下有子目录（每个子目录对应一个服务）。

**LLM 把基础设施也列进来了**

用 `--desc` 明确告诉 LLM：

```bash
kp ai-plan --desc "不要把 postgres、etcd、bitcoind、geth-rpc 列进来，只列业务服务"
```

**port 识别错误**

LLM 是从代码推断端口的，如果识别错了，直接编辑生成的 `configs/components.yaml` 手动修正，然后 `kp deploy`。

---

## 与 kp deploy 的关系

`kp ai-plan` 和 `kp deploy` 是独立的命令，配合使用：

```bash
# 方式一：先 AI 规划，再部署
kp ai-plan
kp deploy

# 方式二：跳过 AI，手动维护 components.yaml
vim configs/components.yaml
kp deploy

# 方式三：AI 规划完直接部署（写入后立即 deploy）
kp ai-plan && kp deploy
```

`kp deploy` 始终读取 `configs/components.yaml`，不管这个文件是 AI 生成的还是手写的。