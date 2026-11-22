import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  reporter: process.env.CI ? 'github' : 'list',
  use: { trace: 'retain-on-failure' },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: { args: ['--use-gl=swiftshader', '--enable-webgl', '--ignore-gpu-blocklist'] },
        viewport: { width: 1440, height: 860 },
      },
      expect: {
        // The software renderer can take seconds to produce a stable frame.
        timeout: 30_000,
        toHaveScreenshot: { maxDiffPixelRatio: 0.02, animations: 'disabled' },
      },
    },
  ],
});
