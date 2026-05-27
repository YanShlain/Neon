import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/e2e",
  timeout: 60000,
  use: {
    baseURL: "http://127.0.0.1:8080",
    trace: "on-first-retry",
  },
  webServer: {
    command: "go run ./cmd/api",
    port: 8080,
    reuseExistingServer: true,
    env: {
      TEMPORAL_AUTO_DEV: "1",
      HOLD_DURATION: "2m",
      PAYMENT_ALWAYS_FAIL: "1",
    },
  },
});
