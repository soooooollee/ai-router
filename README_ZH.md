<div align="center">
  <a href="https://github.com/soooooollee/ai-router">
    <img src="web/public/favicon.svg" width="168" alt="AI Router">
  </a>

  <h1>AI Router</h1>

  <p><strong>一个本地网关，四种 AI 协议，连接任意模型。</strong></p>
  <p>通过一个本地入口路由 OpenAI、Anthropic、Gemini 及兼容模型 API。</p>

  <p>
    <a href="README.md">English</a> ·
    <a href="README_ZH.md">简体中文</a>
  </p>

  <p>
    <a href="https://github.com/soooooollee/ai-router/releases"><img src="https://img.shields.io/github/v/release/soooooollee/ai-router?style=flat-square&label=release&color=4f73ff" alt="Release"></a>
    <a href="https://github.com/soooooollee/ai-router/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/soooooollee/ai-router/ci.yml?branch=main&style=flat-square&label=build&color=4f73ff" alt="Build"></a>
    <a href="https://github.com/soooooollee/ai-router/blob/main/LICENSE"><img src="https://img.shields.io/github/license/soooooollee/ai-router?style=flat-square&label=license&color=4f73ff" alt="License"></a>
  </p>
</div>

## Setup

macOS 或 Linux：

```bash
curl -fsSL https://raw.githubusercontent.com/soooooollee/ai-router/main/install.sh | sh
```

macOS、Linux 或 Windows 使用 npm：

```bash
npm install --global https://github.com/soooooollee/ai-router/releases/latest/download/airoute-cli.tgz
```

macOS 或 Linux 使用 Homebrew：

```bash
brew install soooooollee/ai-router/airoute
```

也可以使用 Go 1.24+ 从源码构建：

```bash
git clone https://github.com/soooooollee/ai-router.git
cd ai-router
make build
export PATH="$PWD/bin:$PATH"
```

创建配置并启动 AI Router：

```bash
air init
air start
```

浏览器打开 <http://127.0.0.1:12667> 接入模型并创建第一条路由。模型网关地址为 <http://127.0.0.1:12666>。

常用命令：

```text
air init       创建 airoute.yaml
air start      后台启动
air stop       停止后台实例
air restart    重启后台实例
air logs -f    持续查看后台日志
air serve      前台运行，用于调试
air status     查看运行状态
air ui         打开 Web 控制台
air check      校验 airoute.yaml
air doctor     执行配置诊断
air --help     查看全部命令
```

## 模型兼容性检测

接入模型时，Detection Engine v7 会分别验证上游原生协议、JSON/SSE 响应结构、function tools、reasoning、工具与 reasoning 组合、多轮工具续接、Codex 原生 custom tools 直连，以及通过 AI Router 的 `apply_patch` 端到端链路。只有上游直接通过 custom tool 和结果续接验证，才会标记为“Codex 官方直连”；Router 能够重建事件不再被误报为上游完整兼容。能力结果区分为“已支持、不支持、尚未确认、未测试”；确定性的 tools + reasoning 拒绝也不会再用不同 `tool_choice` 重复等待。

AI Router 会为 Codex 推荐三种接入方式：

- `direct`：Codex 官方直连上游 Responses；上游密钥由 `air provider-token` 命令动态提供，不复制到 `~/.codex/config.toml`。
- `passthrough`：Codex 连接 AI Router，保留路由、日志和密钥管理，Responses 不做兼容修复。
- `compatibility`：Codex 连接 AI Router，由 Router 在 Chat 或不完整 Responses 与 Codex Responses 之间转换。

如果上游只支持 OpenAI Chat，且不接受 `tools` 与 `reasoning_effort` 同时出现，可以把它标记为 Codex Chat 不完全兼容模式：

```yaml
providers:
  - id: compatible-chat
    protocol: openai-chat
    codex_integration: compatibility
    compatibility_mode: codex-chat
    base_url: https://example.com/v1
    api_key: ${PROVIDER_API_KEY}
    models: [gpt-compatible]
```

该模式在界面中提示为 `Codex CLI / ChatGPT App 经 AI Router 兼容`。AI Router 会把 Codex Responses 请求转换为 OpenAI Chat，并删除不兼容的 `reasoning_effort`，同时记录 `codex_chat_compatibility_mode` 诊断。工具调用会保留，但模型推理强度可能与 Codex 中的设置不同，因此模型接入时会显示明确的兼容说明。

对于支持 Responses 但只接受标准 function tools 的兼容服务，自动检测会使用 `compatibility_mode: codex-responses`，把 Codex custom tools 转为 function tools，并在返回时恢复 `apply_patch` 事件。检测还可能自动保存 `tool_choice_mode: auto-only`、`reasoning_history: preserve` 和 `reasoning_with_tools: disabled` 等可执行策略；这些是运行时转换规则，不是一次检测结果的永久快照。

升级时，重新执行上方 curl 或 npm 安装命令即可；Homebrew 用户运行 `brew update && brew upgrade airoute`。如果 AI Router 正在运行，再执行 `air restart` 切换到新版本。

## License

AI Router 使用 [MIT License](LICENSE)。
