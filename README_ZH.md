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

默认配置保存在系统用户配置目录中，而不是当前工作目录：Windows 为 `%AppData%\airoute\airoute.yaml`，macOS 为 `~/Library/Application Support/airoute/airoute.yaml`，Linux 通常为 `~/.config/airoute/airoute.yaml`。如需自定义位置，可使用 `--config PATH` 或 `AIROUTE_CONFIG`。

浏览器地址为：[http://127.0.0.1:12667](http://127.0.0.1:12667/)。

模型网关地址为：[http://127.0.0.1:12666](http://127.0.0.1:12666/)。

常用命令：

```text
air init       在系统用户配置目录创建 airoute.yaml
air start      后台启动
air stop       停止后台实例
air restart    重启后台实例
air logs -f    持续查看后台日志
air serve      前台运行，用于调试
air status     查看运行状态
air ui         打开 Web 控制台
air check      校验 airoute.yaml
air doctor     执行配置诊断
air client-state backup           备份托管客户端、用量、审计数据和本地主密钥
air client-state verify --backup  恢复前校验客户端状态备份
air client-state restore --backup 在 AI Router 停止时恢复已校验的备份
air --help     查看全部命令
```

## Pi Coding Agent

在“应用配置”页可以把 Pi Coding Agent 配置为 AI Router 的自定义 Provider。AI Router 只会更新全局
`~/.pi/agent/models.json` 中的 `airoute` Provider，以及 `~/.pi/agent/settings.json` 中对应的
`defaultProvider` / `defaultModel`；其他 Provider 和设置都会保留。

配置时选择一种请求协议，Pi Provider 会写入对应的 API 类型和 AI Router 入口：

| Pi 请求协议 | Pi API 类型 | AI Router 入口 |
| --- | --- | --- |
| OpenAI Chat | `openai-completions` | `/v1/chat/completions` |
| OpenAI Responses | `openai-responses` | `/v1/responses` |
| Anthropic Messages | `anthropic-messages` | `/v1/messages` |
| Gemini Generate Content | `google-generative-ai` | `/v1beta/models/{model}:streamGenerateContent` |

页面会把两个文件作为一个整体预览、备份、恢复与清理。它使用 AI Router 的客户端访问密钥，不会写入上游
Provider token，也不使用 Pi OAuth。项目目录中的 `.pi/settings.json` 可以覆盖这些全局默认值；可通过
`PI_CODING_AGENT_DIR`（或 AI Router 专用的 `AIROUTE_PI_AGENT_DIR`）改用其他 Pi agent 目录。

Pi 的 `models.json` 支持 JSONC。AI Router 可以安全读取其中的注释和尾逗号，但会拒绝自动重写，避免静默移除
手写格式。若确实要替换该文件，请在可编辑预览中提交包含两个文件的标准 JSON 配置。

## License

AI Router 使用 [MIT License](LICENSE)。
