// Go fixtures own isolated loopback servers and seed through storage/project APIs.
// No dependency on an existing user's database or an already running server.
const { defineConfig, devices } = require('@playwright/test');
module.exports = defineConfig({
  testDir: './acceptance',
  workers: 1,
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: [['list']],
  outputDir: `./test-results/choice-acceptance-${process.env.OB_ACCEPTANCE_SUITE}`,
  use: {
    baseURL: process.env.OB_ACCEPTANCE_URL,
    locale: 'ru-RU',
    launchOptions: process.env.OB_ACCEPTANCE_BROWSER ? { executablePath: process.env.OB_ACCEPTANCE_BROWSER } : {},
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
