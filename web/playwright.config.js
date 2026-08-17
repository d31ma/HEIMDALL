// Playwright drives the real stack: a live control plane (Go, SESAME, FYLO)
// behind the real ty serve. scripts/e2e-web.sh stands both up and runs this.
//
// ponytail: chromium only, no visual regression. The three DuVay themes are
// smoke-checked by attribute, not by screenshot diff — add screenshot
// baselines when the UI is stable enough that they would not churn weekly.
import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: 'tests',
  testMatch: '**/*.spec.js',
  timeout: 30_000,
  retries: 0,
  use: {
    baseURL: process.env.HD_WEB_URL || 'http://127.0.0.1:9310',
  },
})
