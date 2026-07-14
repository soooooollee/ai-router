<div align="center">
  <a href="https://github.com/soooooollee/ai-router">
    <img src="web/public/favicon.svg" width="168" alt="AI Router">
  </a>

  <h1>AI Router</h1>

  <p><strong>One local gateway. Four AI protocols. Any model.</strong></p>
  <p>Route and translate OpenAI, Anthropic, Gemini, and compatible model APIs from a single local endpoint.</p>

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

AI Router is a local-first protocol conversion and model routing gateway. It runs as a single Go binary, requires no external database, and uses one versioned YAML file as its source of truth.

The embedded web console manages providers, model aliases, protocol-aware routes, developer-tool configuration, runtime metrics, and request logs. Every configuration change is validated, backed up, and atomically written.

## Why AI Router?

- **One endpoint** for OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and Gemini Generate Content.
- **Cross-protocol routing** between compatible clients and providers.
- **Simple model aliases** that hide upstream provider and model names.
- **Reliable execution** with bounded retries, ordered fallback, timeouts, and output limits.
- **Developer-tool setup** for Claude Code, Claude App, Codex, and MiMo Code.
- **Local observability** for status, latency, tokens, routing attempts, and optional conversation bodies.
- **Provider compatibility** for Qwen 3.x, SiliconFlow, and Xiaomi MiMo.

## Setup

Install with the shell installer on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/soooooollee/ai-router/main/install.sh | sh
```

Or install with npm on macOS, Linux, or Windows:

```bash
npm install --global airoute-cli@latest
```

Or install with Homebrew on macOS or Linux:

```bash
brew install soooooollee/tap/airoute
```

You can also download an archive from [GitHub Releases](https://github.com/soooooollee/ai-router/releases), extract it, and place `airoute` in `PATH`.

To build from source, use Go 1.24+:

```bash
git clone https://github.com/soooooollee/ai-router.git
cd ai-router
make build
```

Create a minimal configuration and start the gateway:

```bash
cp examples/airoute.minimal.yaml airoute.yaml
chmod 600 airoute.yaml

airoute check --config airoute.yaml
airoute doctor --config airoute.yaml
airoute serve --config airoute.yaml
```

For a source build, use `./bin/airoute` in place of `airoute`.

Open the web console at <http://127.0.0.1:12667>, or run:

```bash
airoute ui
```

Default endpoints:

| Service | Address |
| --- | --- |
| Model gateway | `http://127.0.0.1:12666` |
| Web console | `http://127.0.0.1:12667` |
| Health check | `http://127.0.0.1:12666/healthz` |

The minimal configuration intentionally contains no provider or default model. Add your first model through the web console.

## Connect

AI Router exposes four client-compatible endpoints:

| Protocol | Endpoint |
| --- | --- |
| OpenAI Chat Completions | `/v1/chat/completions` |
| OpenAI Responses | `/v1/responses` |
| Anthropic Messages | `/v1/messages` |
| Gemini Generate Content | `/v1beta/models/{model}:generateContent` |

Existing SDKs usually only need a different base URL:

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

When client authentication is enabled, use a configured client key. Otherwise, any non-empty placeholder satisfies SDK validation.

<details>
<summary><strong>cURL examples</strong></summary>

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

## Configure

Use the web console in this order:

1. **Model Providers** — enter the upstream API URL, key, and model name, then run protocol detection.
2. **Routes** — create a client-facing model alias and choose its provider, upstream model, and client protocol.
3. **Applications** — preview, write, verify, back up, and restore developer-tool configuration.
4. **Overview and Logs** — inspect runtime metrics, upstream attempts, diagnostics, and optional conversation bodies.
5. **Settings** — manage the full YAML configuration, persistence, body capture, and web redaction.

The default in-memory request history is 50 entries. Request and response bodies are disabled by default and should only be enabled when needed.

## Developer Tools

| Application | Required client protocol |
| --- | --- |
| Claude Code | Anthropic Messages |
| Claude App | Anthropic Messages |
| Codex | OpenAI Responses |
| MiMo Code | OpenAI Chat |

Codex configuration includes a managed `airoute-model-catalog.json` so custom model aliases have complete model metadata. The Responses stream includes the output-item lifecycle required for reasoning, text, and tool calls. MiMo capabilities follow the [official Xiaomi MiMo Codex configuration guide](https://mimo.mi.com/docs/zh-CN/tokenplan/integration/codex-configuration).

## Docker

```bash
mkdir -p data
cp examples/airoute.docker.yaml data/airoute.yaml

export AIROUTE_ADMIN_TOKEN='replace-with-at-least-24-characters'
export AIROUTE_CLIENT_KEY='replace-with-client-key'
export OPENAI_API_KEY='replace-with-provider-key'

AIROUTE_UID="$(id -u)" AIROUTE_GID="$(id -g)" docker compose up --build -d
```

Compose exposes the gateway on `0.0.0.0:12666` and the web console on `127.0.0.1:12667`.

## Documentation

- [Configuration](docs/configuration.md)
- [Protocol conversion](docs/protocols.md)
- [Routing and fallback](docs/routing.md)
- [Qwen 3.x and MiMo compatibility](docs/qwen3-compatibility.md)
- [Security boundaries](docs/security.md)
- [Administration API](docs/openapi.yaml)
- [Configuration migrations](docs/migrations.md)
- [Release verification](docs/verification.md)

## Development

```bash
make test
make release-check
make web-e2e
```

`release-check` runs web tests and builds, Go formatting checks, Vet, race tests, and amd64/arm64 cross-builds for Linux, macOS, and Windows.

## License

AI Router is licensed under the [MIT License](LICENSE).
