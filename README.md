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
brew install soooooollee/ai-router/airoute
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

The default configuration is stored in the operating system's user configuration directory, not the current working directory: `%AppData%\airoute\airoute.yaml` on Windows, `~/Library/Application Support/airoute/airoute.yaml` on macOS, and usually `~/.config/airoute/airoute.yaml` on Linux. Use `--config PATH` or `AIROUTE_CONFIG` to override it.

Web console: [http://127.0.0.1:12667](http://127.0.0.1:12667/).

Model gateway: [http://127.0.0.1:12666](http://127.0.0.1:12666/).

Useful commands:

```text
air init       Create airoute.yaml in the user configuration directory
air start      Start in the background
air stop       Stop the background instance
air restart    Restart the background instance
air logs -f    Follow background logs
air serve      Run in the foreground for debugging
air status     Show runtime status
air ui         Open the Web console
air check      Validate airoute.yaml
air doctor     Run configuration diagnostics
air client-state backup           Back up managed clients, usage, audit data, and the local credential master key
air client-state verify --backup  Verify a client-state backup before restore
air client-state restore --backup Restore a verified backup while AI Router is stopped
air --help     Show all commands
```

## Pi Coding Agent

The **Applications** page can configure Pi Coding Agent as an AI Router custom
provider. It writes only the `airoute` provider in Pi's global
`~/.pi/agent/models.json` and the matching `defaultProvider` / `defaultModel`
in `~/.pi/agent/settings.json`; other providers and settings are preserved.

Choose one request protocol when configuring Pi. The resulting provider uses
the corresponding Pi API type and AI Router endpoint:

| Pi request protocol | Pi API type | AI Router endpoint |
| --- | --- | --- |
| OpenAI Chat | `openai-completions` | `/v1/chat/completions` |
| OpenAI Responses | `openai-responses` | `/v1/responses` |
| Anthropic Messages | `anthropic-messages` | `/v1/messages` |
| Gemini Generate Content | `google-generative-ai` | `/v1beta/models/{model}:streamGenerateContent` |

The page previews, backs up, restores, and cleans up both files as one unit.
It uses an AI Router client key, never an upstream provider token or Pi OAuth.
Project-local `.pi/settings.json` can override these global defaults. Set
`PI_CODING_AGENT_DIR` (or the AI Router-specific `AIROUTE_PI_AGENT_DIR`) to
use another Pi agent directory.

Pi accepts JSONC in `models.json`. AI Router reads that file safely but refuses
automatic rewrites when comments or trailing commas are present, so it cannot
silently strip hand-written formatting. Use the editable preview to submit a
standard JSON two-file bundle if you intentionally want to replace it.

## License

AI Router is licensed under the [MIT License](LICENSE).
