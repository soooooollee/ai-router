<div align="center">
  <a href="https://github.com/soooooollee/ai-router">
    <img src="web/public/favicon.svg" width="168" alt="AI Router">
  </a>

  <h1>AI Router</h1>

  <p><strong>One local gateway. Four AI protocols. Any model.</strong></p>
  <p>Route OpenAI, Anthropic, Gemini, and compatible model APIs through one local endpoint.</p>

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

macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/soooooollee/ai-router/main/install.sh | sh
```

npm on macOS, Linux, or Windows:

```bash
npm install --global https://github.com/soooooollee/ai-router/releases/latest/download/airoute-cli.tgz
```

Homebrew on macOS or Linux:

```bash
brew install soooooollee/tap/airoute
```

Or build from source with Go 1.24+:

```bash
git clone https://github.com/soooooollee/ai-router.git
cd ai-router
make build
export PATH="$PWD/bin:$PATH"
```

Create the configuration and start AI Router:

```bash
air init
air start
```

Open <http://127.0.0.1:12667> to add a model and create your first route. The model gateway listens on <http://127.0.0.1:12666>.

Useful commands:

```text
air init       Create airoute.yaml
air start      Start the gateway and Web console
air status     Show runtime status
air ui         Open the Web console
air check      Validate airoute.yaml
air doctor     Run configuration diagnostics
air --help     Show all commands
```

## License

AI Router is licensed under the [MIT License](LICENSE).
