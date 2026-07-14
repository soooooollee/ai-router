<div align="center">
  <a href="https://github.com/soooooollee/ai-router">
    <img src="web/public/favicon.svg" width="168" alt="AI Router">
  </a>

  <h1>AI Router</h1>

  <p><strong>一个本地网关，四种 AI 协议，连接任意模型。</strong></p>
  <p>通过一个本地入口路由并转换 OpenAI、Anthropic、Gemini 及兼容模型 API。</p>

  <p>
    <a href="README.md">English</a> ·
    <a href="README_ZH.md">简体中文</a>
  </p>

  <p>
    <a href="https://github.com/soooooollee/ai-router/releases"><img src="https://img.shields.io/github/v/release/soooooollee/ai-router?style=flat-square&label=release&color=4f73ff" alt="Release"></a>
    <a href="https://github.com/soooooollee/ai-router/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/soooooollee/ai-router/ci.yml?branch=main&style=flat-square&label=build&color=4f73ff" alt="Build"></a>
    <a href="https://github.com/soooooollee/ai-router/blob/main/LICENSE"><img src="https://img.shields.io/github/license/soooooollee/ai-router?style=flat-square&label=license&color=4f73ff" alt="License"></a>
    <a href="https://github.com/soooooollee/ai-router/commits/main"><img src="https://img.shields.io/github/commit-activity/y/soooooollee/ai-router?style=flat-square&label=commits&color=4f73ff" alt="Commits"></a>
  </p>
</div>

AI Router 是一个本地优先的协议转换与模型路由网关。它以单个 Go 二进制运行，不依赖外部数据库，并使用一份版本化 YAML 配置作为唯一事实来源。

内嵌 Web 控制台用于管理模型服务、模型别名、协议路由、开发工具配置、运行指标和调用日志。每次配置修改都会经过校验、备份和原子写入。

## 为什么选择 AI Router？

- **统一入口**：支持 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 和 Gemini Generate Content。
- **跨协议路由**：在兼容的客户端与模型服务之间转换协议。
- **简单模型别名**：隐藏真实供应商与上游模型名称。
- **可靠执行**：支持有界重试、有序 Fallback、超时和输出限制。
- **开发工具配置**：支持 Claude Code、Claude App、Codex 和 MiMo Code。
- **本地可观测性**：查看状态、延迟、Token、路由尝试和可选聊天正文。
- **供应商兼容**：适配 Qwen 3.x、SiliconFlow 和 Xiaomi MiMo。

## 安装与启动

macOS 或 Linux 可以使用安装脚本：

```bash
curl -fsSL https://raw.githubusercontent.com/soooooollee/ai-router/main/install.sh | sh
```

macOS、Linux 或 Windows 可以使用 npm：

```bash
npm install --global airoute-cli@latest
```

macOS 或 Linux 可以使用 Homebrew：

```bash
brew install soooooollee/tap/airoute
```

也可以从 [GitHub Releases](https://github.com/soooooollee/ai-router/releases) 下载对应系统的压缩包，解压后将 `airoute` 放入 `PATH`。

从源码构建需要 Go 1.24+：

```bash
git clone https://github.com/soooooollee/ai-router.git
cd ai-router
make build
```

创建最小配置并启动：

```bash
cp examples/airoute.minimal.yaml airoute.yaml
chmod 600 airoute.yaml

airoute check --config airoute.yaml
airoute doctor --config airoute.yaml
airoute serve --config airoute.yaml
```

源码构建时，将 `airoute` 替换为 `./bin/airoute`。

浏览器打开 <http://127.0.0.1:12667>，也可以运行：

```bash
airoute ui
```

默认地址：

| 服务 | 地址 |
| --- | --- |
| 模型网关 | `http://127.0.0.1:12666` |
| Web 控制台 | `http://127.0.0.1:12667` |
| 健康检查 | `http://127.0.0.1:12666/healthz` |

最小配置不会预置模型服务或默认模型。启动后通过 Web 控制台接入第一个模型。

## 客户端接入

AI Router 提供四种客户端兼容入口：

| 协议 | 地址 |
| --- | --- |
| OpenAI Chat Completions | `/v1/chat/completions` |
| OpenAI Responses | `/v1/responses` |
| Anthropic Messages | `/v1/messages` |
| Gemini Generate Content | `/v1beta/models/{model}:generateContent` |

现有 SDK 通常只需要修改 Base URL：

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:12666/v1",
    api_key="airoute-local",
)

response = client.chat.completions.create(
    model="coding-model",
    messages=[{"role": "user", "content": "hello"}],
)

print(response.choices[0].message.content)
```

启用客户端鉴权时，请使用已配置的客户端 Key；未启用时，任意非空占位值都可以满足 SDK 校验。

<details>
<summary><strong>cURL 示例</strong></summary>

```bash
# OpenAI Chat Completions
curl http://127.0.0.1:12666/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{"model":"coding-model","messages":[{"role":"user","content":"hello"}]}'

# OpenAI Responses
curl http://127.0.0.1:12666/v1/responses \
  -H 'content-type: application/json' \
  -d '{"model":"coding-model","input":"hello"}'

# Anthropic Messages
curl http://127.0.0.1:12666/v1/messages \
  -H 'content-type: application/json' \
  -d '{"model":"coding-model","max_tokens":256,"messages":[{"role":"user","content":"hello"}]}'

# Gemini Generate Content
curl http://127.0.0.1:12666/v1beta/models/coding-model:generateContent \
  -H 'content-type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}'
```

</details>

## 配置流程

推荐按以下顺序使用 Web 控制台：

1. **模型接入**：填写上游 API 地址、密钥和模型名，然后执行协议识别。
2. **路由配置**：创建客户端模型别名，并选择模型服务、真实模型和客户端协议。
3. **应用配置**：预览、写入、验证、备份和恢复开发工具配置。
4. **运行概览与调用日志**：查看运行指标、上游尝试、协议诊断和可选聊天正文。
5. **系统设置**：管理完整 YAML、日志持久化、正文采集和网页脱敏。

内存默认保留最近 50 条请求。请求和响应正文默认不记录，只在需要排障时开启。

## 开发工具

| 应用 | 所需客户端协议 |
| --- | --- |
| Claude Code | Anthropic Messages |
| Claude App | Anthropic Messages |
| Codex | OpenAI Responses |
| MiMo Code | OpenAI Chat |

Codex 配置会维护 `airoute-model-catalog.json`，为自定义模型别名提供完整模型元数据。Responses 流包含 reasoning、文本和工具调用所需的输出项生命周期。MiMo 模型能力参考[小米 MiMo 官方 Codex 配置指南](https://mimo.mi.com/docs/zh-CN/tokenplan/integration/codex-configuration)。

## Docker

```bash
mkdir -p data
cp examples/airoute.docker.yaml data/airoute.yaml

export AIROUTE_ADMIN_TOKEN='replace-with-at-least-24-characters'
export AIROUTE_CLIENT_KEY='replace-with-client-key'
export OPENAI_API_KEY='replace-with-provider-key'

AIROUTE_UID="$(id -u)" AIROUTE_GID="$(id -g)" docker compose up --build -d
```

Compose 默认将模型网关映射到 `0.0.0.0:12666`，Web 控制台映射到 `127.0.0.1:12667`。

## 文档

- [配置说明](docs/configuration.md)
- [协议转换](docs/protocols.md)
- [路由与 Fallback](docs/routing.md)
- [Qwen 3.x 与 MiMo 兼容性](docs/qwen3-compatibility.md)
- [安全边界](docs/security.md)
- [管理 API](docs/openapi.yaml)
- [配置迁移](docs/migrations.md)
- [发布验证](docs/verification.md)

## 开发与验证

```bash
make test
make release-check
make web-e2e
```

`release-check` 包含 Web 测试与构建、Go 格式检查、Vet、Race 测试，以及 Linux、macOS、Windows 的 amd64/arm64 交叉构建。

## License

AI Router 使用 [MIT License](LICENSE)。
