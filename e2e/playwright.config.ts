import { defineConfig, devices } from '@playwright/test';

// Runs against the local stack: `docker compose up --build`, then `npm test`.
export default defineConfig({
  testDir: './tests',
  timeout: 60_000,
  retries: process.env['CI'] ? 1 : 0,
  workers: 1, // admin tests share one database
  reporter: [['list']],
  use: {
    baseURL: process.env['BASE_URL'] ?? 'http://localhost:4000',
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'] } },
    { name: 'mobile', use: { ...devices['Pixel 7'] }, testIgnore: /admin/ },
  ],
});
