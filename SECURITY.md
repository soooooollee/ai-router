# Security Policy

## Supported versions

Security fixes are provided for the latest release line.

| Version | Supported |
| --- | --- |
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

- The Web console should remain bound to localhost unless it is protected by TLS, authentication, and network access controls.
- Provider keys and application configuration backups are sensitive local files.
- Request and response body capture should only be enabled temporarily for troubleshooting.
- Any credential exposed in a terminal log, chat, issue, backup, or commit must be revoked and replaced at the provider.
