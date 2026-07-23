# Security Policy

## Supported versions

Security fixes are provided for the latest release line.

| Version | Supported |
| --- | --- |
| 0.3.x (development) | Yes |
| 0.2.x | Yes |
| 0.1.x | No |

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use [GitHub's private vulnerability reporting](https://github.com/soooooollee/ai-router/security/advisories/new) and include:

- affected version and operating system;
- a minimal reproduction or proof of concept;
- expected impact and required preconditions;
- any suggested mitigation;
- whether the report or related details have been shared elsewhere.

Remove API keys, tokens, prompts, responses, local paths, and other personal data before submitting. You should receive an acknowledgement within seven days. The maintainer will validate the report, coordinate a fix and disclosure timeline, and credit the reporter unless anonymity is requested.

## Operational security

- v0.3.x always rejects non-loopback access to the Web console and management API. Public gateway deployments must keep the management listener private; public administration requires the later RBAC/TLS control plane.
- Container deployments therefore publish only the model gateway in v0.3.x. Run management commands inside the container or manage the mounted configuration and state from the host; do not publish port 12667 through a bridge network.
- Provider keys and application configuration backups are sensitive local files.
- Client keys are separate from provider and administrator credentials. A client key never authorizes management API access.
- Complete managed client keys are returned to the browser only during creation or rotation. The state database stores an HMAC-SHA256 digest plus an AES-256-GCM encrypted copy for locally managed keys; decryption occurs only in the backend for selected application writes, and credential comparisons remain constant-time.
- `gateway-state.db`, `credential-master.key`, and client-state backups are sensitive `0600` files under a `0700` runtime directory. Losing the master key makes its credentials unverifiable.
- Use `air client-state backup`, `verify`, and `restore`; do not copy the database alone. Restore is intentionally offline and refuses a locked database, a checksum mismatch, or missing HMAC key generations.
- Query-string API keys are disabled by default. If Gemini compatibility requires `auth.allow_query_key: true`, ensure every reverse proxy also redacts query strings from access logs.
- A public model gateway should use TLS at the edge, short-lived per-client keys, CIDR restrictions where possible, and explicit RPM, concurrency, and daily quotas.
- Request and response body capture should only be enabled temporarily for troubleshooting.
- Diagnostic exports omit provider secrets, client secrets, HMAC material, and request/response bodies.
- Any credential exposed in a terminal log, chat, issue, backup, or commit must be revoked and replaced at the provider.
