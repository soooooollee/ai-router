# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/) and semantic versioning.

## [Unreleased]

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

[Unreleased]: https://github.com/soooooollee/ai-router/compare/v0.2.2...HEAD
[0.2.2]: https://github.com/soooooollee/ai-router/releases/tag/v0.2.2
[0.2.1]: https://github.com/soooooollee/ai-router/releases/tag/v0.2.1
[0.2.0]: https://github.com/soooooollee/ai-router/releases/tag/v0.2.0
[0.1.0]: https://github.com/soooooollee/ai-router/releases/tag/v0.1.0
