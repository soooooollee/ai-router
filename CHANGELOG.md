# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/) and semantic versioning.

## [Unreleased]

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
