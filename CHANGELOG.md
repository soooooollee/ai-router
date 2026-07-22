# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/) and semantic versioning.

## [Unreleased]

## [0.2.6] - 2026-07-22

### How to update

- Shell installer: rerun `curl -fsSL https://raw.githubusercontent.com/soooooollee/ai-router/main/install.sh | sh`.
- npm: rerun `npm install --global https://github.com/soooooollee/ai-router/releases/latest/download/airoute-cli.tgz`.
- Homebrew: run `brew update && brew upgrade airoute`.
- Restart an existing background instance with `air restart`, then confirm the installed release with `air version`.

### Added

- Add an editable model service name to onboarding so identical upstream model IDs from different services remain distinguishable.
- Generate collision-free provider IDs when the same upstream model is connected more than once.

### Changed

- Show application model choices as `model service → upstream model → client protocol`, while retaining the route alias internally.
- Use a full-width model selector in application configuration so longer service and model names remain readable.
- Rename the onboarding `Model Names` field to the localized model-name label.

### Fixed

- Complete Chinese and English localization for model detection progress, capability diagnostics, application verification results, runtime notices and Codex compatibility guidance.
- Align the API Key column colors and hover behavior with the other model-service table columns.
- Preserve user-entered model service names during protocol detection and keep generated route targets linked to the correct upstream service.
- Prevent a late live-preview response from overwriting unsaved manual application configuration edits.

## [0.2.5] - 2026-07-21

### Added

- Add deterministic provider onboarding that detects Anthropic Messages, OpenAI Chat, OpenAI Responses and Gemini endpoints with live JSON/SSE, tools, reasoning, combined-tool and multi-turn checks.
- Add native Codex custom-tool probing plus an end-to-end `apply_patch` verification path through AI Router, with explicit timeout and failure diagnostics.
- Add Codex official third-party provider configuration with command-backed token retrieval, while retaining Router compatibility modes for Chat and incomplete Responses services.
- Add automatic generation of Claude, OpenAI Chat, OpenAI Responses and Gemini routes for every newly added model, with an opt-out during onboarding.
- Add overview counters for configured models, routes, applications, retained logs and log persistence.

### Changed

- Merge Codex CLI and ChatGPT App into one application configuration surface because both share `~/.codex/config.toml`.
- Classify provider capabilities from direct protocol evidence instead of endpoint acceptance alone, and retain actionable request policies for tools, reasoning and tool-choice behavior.
- Simplify compatibility labels in model and route lists while keeping detailed explanations in model onboarding and the selected Codex application configuration.
- Keep the original request, success-rate, Token, concurrency and latency overview below the new configuration counters.

### Fixed

- Translate Codex Responses custom tools and tool results through Chat or incomplete Responses providers, then reconstruct custom-tool streaming events and call IDs for Codex clients.
- Avoid false incompatibility when a stricter tools-with-reasoning probe proves tool support after a slower standalone tool probe.
- Preserve valid Codex configuration while removing deprecated hooks and invalid MCP transport fields from AI Router-managed content.
- Detect Anthropic-compatible endpoints independently from OpenAI endpoints and avoid misclassifying slow or unavailable probes as confirmed compatibility.

## [0.2.4] - 2026-07-14

### Added

- Add separate Codex CLI and ChatGPT App configuration surfaces while preserving their shared `~/.codex/config.toml` behavior.
- Add editable application configuration previews with JSON/TOML validation, automatic backups and guarded raw writes.
- Add application cleanup actions that remove only AI Router-managed settings while preserving unrelated local configuration.

### Changed

- Load the application configuration page without blocking on executable detection and preload route bundles after initial paint.
- Order application configuration surfaces as Claude Code, Claude App, Codex CLI, ChatGPT App and MiMo Code.
- Refresh the runtime overview with the original request, success, Token, concurrency and latency content in a compact responsive layout.
- Limit overview latency samples to requests completed by the current process.

### Fixed

- Detect the Codex CLI independently from the Codex bundled with ChatGPT App and ignore stale executable paths that fail with `ENOENT`.
- Preserve non-AI Router Claude, Codex and MiMo settings when applying edited previews or cleaning managed configuration.

## [0.2.3] - 2026-07-14

### Added

- Show the running version below the navigation and highlight a newer GitHub Release when one is available.

### Changed

- Rename the official Homebrew tap to `homebrew-ai-router` and document curl, npm and Homebrew update commands.
- Run Docker containers in foreground `serve` mode now that `air start` manages a detached local process.
- Rewrite public Git history to remove internal planning documents and replace legacy personal commit metadata with the GitHub noreply identity.

## [0.2.2] - 2026-07-14

### Added

- Add contribution, conduct, security, issue, pull request and editor configuration files for the public repository.
- Attach an installable npm tarball to every GitHub Release so npm installation works before registry publishing is configured.
- Make `air start` launch a managed background instance and add `air stop`, `air restart`, and `air logs` lifecycle commands; keep `air serve` and `air start --foreground` for terminal-attached debugging.

### Removed

- Remove completed internal plans, historical acceptance and release evidence, redundant documentation and two unreferenced Web components.

## [0.2.1] - 2026-07-14

### Added

- Add complete Codex and MiMo Code application adapters with installation detection, live previews, atomic configuration writes, gateway verification, backup deletion and rollback.
- Add verified shell, npm and Homebrew installation paths with release automation and checksum validation.
- Rename the installed CLI to `air`, add `air init` and `air start`, and retain `serve` as a compatibility alias.

### Changed

- Show the complete Claude application configuration path in a hover and keyboard-focus tooltip.
- Keep the application write action at the standard button font size and give its longer label more width.
- Use normal-sized, emphasized text for application configuration notices.
- Initialize new routes from the selected upstream model and keep raw schema validation details out of the route form.
- Restore standard typography and spacing throughout the application backup list.
- Add guarded backup deletion and side-by-side current/merged application configuration previews.
- Keep the current and merged configuration preview controls equal in size.
- Hide internal provider IDs from the model service table to avoid repeating the visible model name.
- Collapse duplicate service/model labels in route targets while retaining service context when the names differ.
- Keep generated route aliases and IDs synchronized with upstream selection until the user edits them manually.
- Generate new route IDs from the client alias, protocol and a millisecond timestamp to avoid collisions between repeated model mappings.
- Filter application model selectors by each application's client protocol and label options as `alias → protocol`, removing duplicate aliases that cannot be distinguished by the application configuration.
- Replace manual application preview refreshes with debounced live previews while retaining a final pre-write preview check.
- Moved the default gateway and Web control-plane listeners from `8080/8081` to `12666/12667` to reduce local port conflicts; explicitly configured listeners remain unchanged.
- Rewrote the README as a concise bilingual installation and quick-start page.
- Allowed an empty onboarding configuration so first launch no longer seeds an OpenAI Provider or default route.
- Made Provider deletion remove its route targets and any routes left without targets, including an obsolete default route.
- Migrated legacy local application URLs using the old `8080` default to the active gateway shown by the control plane.
- Render OpenAI Responses string input as one chat message instead of one message per character.
- Reduce the default in-memory request history from 500 entries to 50 and clarify the body-capture-only save notification.

## [0.2.0] - 2026-07-13

### Added

- Claude Desktop application detection, configuration preview, atomic apply and rollback support.
- Web control-plane overview and request-log pages, with localized navigation and status presentation.
- Optional request-history persistence and Web redaction controls for configuration, credentials and captured bodies.

### Changed

- Refined provider, route, application and settings workflows with confirmation dialogs and clearer validation feedback.
- Expanded gateway and administration observability while keeping list views free of captured request and response bodies.
- Updated the bundled Web application and end-to-end coverage for the new control-plane experience.

## [0.1.0] - 2026-07-13

### Added

- Single-binary AI protocol gateway and embedded Web control plane.
- Bidirectional OpenAI Chat, OpenAI Responses, Anthropic Messages and Gemini Generate Content adapters.
- Canonical request and streaming event IR with explicit conversion diagnostics.
- Conditional routing, model aliases, bounded retry and ordered fallback.
- YAML v1 configuration with environment references, atomic Web writes, backups and hot reload.
- Provider probing, route explanation, Playground, bounded request logs and Prometheus metrics.
- Client/admin authentication, SSRF protection, header filtering and body redaction.
- Docker, multi-platform release, checksums, SBOM and keyless checksum signing.
- Qwen 3.x and Xiaomi MiMo provider profiles, thinking controls, Claude Code system-message normalization, and provider output-token caps.
- Generic application Adapter registry and management APIs, with Claude Code detection, structured preview, real L1-L4 verification, atomic backups and rollback.
- Provider-native Anthropic token counting with a Unicode/CJK/tool/media-aware local fallback and explicit estimation metadata.
- Configuration effect reporting for hot reload, runtime rebuild and restart-required changes.

### Changed

- Rebuilt the Web control plane around four focused pages: Providers, Routes, Applications and Settings.
- Split route-level React bundles, removed inaccessible legacy pages and historical UI override CSS, and added five independent Playwright workflows.
- Unified main and application configuration writes on the same `0600` atomic-file and unique-backup implementation.

[Unreleased]: https://github.com/soooooollee/ai-router/compare/v0.2.6...HEAD
[0.2.6]: https://github.com/soooooollee/ai-router/releases/tag/v0.2.6
[0.2.5]: https://github.com/soooooollee/ai-router/releases/tag/v0.2.5
[0.2.4]: https://github.com/soooooollee/ai-router/releases/tag/v0.2.4
[0.2.3]: https://github.com/soooooollee/ai-router/releases/tag/v0.2.3
[0.2.2]: https://github.com/soooooollee/ai-router/releases/tag/v0.2.2
[0.2.1]: https://github.com/soooooollee/ai-router/releases/tag/v0.2.1
[0.2.0]: https://github.com/soooooollee/ai-router/releases/tag/v0.2.0
[0.1.0]: https://github.com/soooooollee/ai-router/releases/tag/v0.1.0
