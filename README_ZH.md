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
npm install --global airoute-cli@latest
```

macOS 或 Linux 使用 Homebrew：

```bash
brew install soooooollee/tap/airoute
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
air start      启动模型网关和 Web 控制台
air status     查看运行状态
air ui         打开 Web 控制台
air check      校验 airoute.yaml
air doctor     执行配置诊断
air --help     查看全部命令
```

## License

AI Router 使用 [MIT License](LICENSE)。
