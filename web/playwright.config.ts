import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: "http://127.0.0.1:18081",
    trace: "retain-on-failure",
  },
  webServer: [
    {
      command: "node e2e/fixtures/mock-provider.mjs",
      url: "http://127.0.0.1:19090/v1/models",
      reuseExistingServer: !process.env.CI,
    },
    {
      command:
        "rm -rf /tmp/airoute-claude-app-e2e && rm -f /tmp/airoute-claude-e2e.json /tmp/airoute-codex-e2e.toml /tmp/airoute-mimocode-e2e.json && cp e2e/fixtures/airoute.e2e.yaml /tmp/airoute.e2e.yaml && cd .. && AIROUTE_RELEASE_API_URL=http://127.0.0.1:19090/release AIROUTE_CLAUDE_SETTINGS_PATH=/tmp/airoute-claude-e2e.json AIROUTE_CLAUDE_APP_DATA_DIR=/tmp/airoute-claude-app-e2e AIROUTE_CODEX_CONFIG_PATH=/tmp/airoute-codex-e2e.toml AIROUTE_MIMOCODE_CONFIG_PATH=/tmp/airoute-mimocode-e2e.json E2E_ADMIN_TOKEN=e2e-admin-token-123456789012345 E2E_CLIENT_KEY=e2e-client-key E2E_PROVIDER_KEY=e2e-provider-key PROVIDER_API_KEY=e2e-new-provider-key go run -ldflags '-X main.version=0.2.3' ./cmd/airoute serve --config /tmp/airoute.e2e.yaml",
      url: "http://127.0.0.1:18081/",
      reuseExistingServer: !process.env.CI,
      timeout: 120_000,
    },
  ],
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
