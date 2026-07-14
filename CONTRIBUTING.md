# Contributing to AI Router

Thank you for helping improve AI Router. Bug reports, focused feature proposals, documentation fixes, and pull requests are welcome.

## Before you start

- Search existing issues and pull requests before opening a new one.
- Use GitHub Security Advisories for vulnerabilities; do not disclose them in a public issue.
- Never commit API keys, tokens, local configuration, backups, request bodies, or application settings.
- Keep changes focused. Large behavior or protocol changes should start with an issue.

## Development setup

AI Router requires Go 1.24+ and Node.js 22+.

```bash
git clone https://github.com/soooooollee/ai-router.git
cd ai-router
cd web && npm ci && cd ..
make build
```

Create a local configuration and start the development binary:

```bash
./bin/air init
./bin/air start
```

Local configuration and backup files are ignored by Git.

## Tests

Run the checks that match your change, then run the release check before submitting:

```bash
make test
make web-e2e
make release-check
cd npm && npm ci --ignore-scripts && npm test
```

Changes to `web/` must include the rebuilt embedded assets under `internal/admin/webdist/`. `make build` performs that build.

## Pull requests

- Explain the problem and the user-visible result.
- Add or update tests for behavior changes.
- Note configuration, compatibility, security, or migration impact.
- Keep generated files in the same commit as their source changes.
- Confirm that no credentials or private request data are present.

By contributing, you agree that your contribution is licensed under the project's MIT License.
