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

Open <http://127.0.0.1:12667> to add a model and create your first route. The model gateway listens on <http://127.0.0.1:12666>.

Useful commands:

```text
air init       Create airoute.yaml
air start      Start in the background
air stop       Stop the background instance
air restart    Restart the background instance
air logs -f    Follow background logs
air serve      Run in the foreground for debugging
air status     Show runtime status
air ui         Open the Web console
air check      Validate airoute.yaml
air doctor     Run configuration diagnostics
air --help     Show all commands
```

## Provider compatibility detection

When adding a model service, Detection Engine v7 separately verifies the native protocol, JSON/SSE contracts, function tools, reasoning, tools with reasoning, multi-turn continuation, native Codex custom tools, and the `apply_patch` path through AI Router. A provider is advertised as Codex official direct only when its upstream Responses endpoint passes the native custom-tool round trip; Router event reconstruction is no longer reported as native compatibility. Deterministic tools-with-reasoning failures also stop negotiation immediately instead of repeating the same slow request with multiple `tool_choice` values.

AI Router recommends one of three Codex integration modes:

- `direct`: Codex connects to the upstream Responses endpoint. The upstream token is supplied by `air provider-token` and is not copied into `~/.codex/config.toml`.
- `passthrough`: Codex connects through AI Router for routing, logs, and key management without compatibility repair.
- `compatibility`: Codex connects through AI Router, which translates Chat or incomplete Responses behavior into Codex Responses.

An OpenAI Chat service that cannot accept `tools` together with `reasoning_effort` can be marked with the explicit Codex Chat compatibility mode:

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

The console explains this mode as `Codex CLI / ChatGPT App compatible through AI Router`. AI Router converts Codex Responses requests to OpenAI Chat, removes the incompatible `reasoning_effort`, and records a `codex_chat_compatibility_mode` diagnostic. Tool calls are preserved, but reasoning intensity may differ from the Codex setting, so model onboarding displays an explicit compatibility notice.

For Responses-compatible services that only accept standard function tools, automatic detection uses `compatibility_mode: codex-responses` to translate Codex custom tools to functions and restore `apply_patch` events on the way back. Detection may also save actionable policies such as `tool_choice_mode: auto-only`, `reasoning_history: preserve`, and `reasoning_with_tools: disabled`; these are runtime transformation rules rather than a permanent snapshot of one probe run.

To update, rerun the curl or npm install command above, or run `brew update && brew upgrade airoute` for Homebrew. Then run `air restart` if AI Router is already running.

## License

AI Router is licensed under the [MIT License](LICENSE).
