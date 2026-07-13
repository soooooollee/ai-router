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
        "rm -f /tmp/airoute-claude-e2e.json && cp e2e/fixtures/airoute.e2e.yaml /tmp/airoute.e2e.yaml && cd .. && AIROUTE_CLAUDE_SETTINGS_PATH=/tmp/airoute-claude-e2e.json E2E_ADMIN_TOKEN=e2e-admin-token-123456789012345 E2E_CLIENT_KEY=e2e-client-key E2E_PROVIDER_KEY=e2e-provider-key PROVIDER_API_KEY=e2e-new-provider-key go run ./cmd/airoute serve --config /tmp/airoute.e2e.yaml",
      url: "http://127.0.0.1:18081/",
      reuseExistingServer: !process.env.CI,
      timeout: 120_000,
    },
  ],
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
