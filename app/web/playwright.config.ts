import { defineConfig, devices } from '@playwright/test'

// 端到端测试：启动一个全新的后端（独立数据目录，SQLite）、假 AI 服务与前端开发服务，经真实浏览器验证关键流程。
// 端口与本地开发服务错开（后端 6989、假 AI 6990、前端 3100），可与 make dev-* 同时运行。
const API = 'http://127.0.0.1:6989'
const WEB = 'http://localhost:3100'

export default defineConfig({
  testDir: './e2e',
  timeout: 90_000,
  expect: { timeout: 20_000 },
  fullyParallel: false,
  workers: 1, // 共用一个站点（全局初始化一次），按文件顺序执行
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  globalSetup: './e2e/global-setup.ts',
  use: {
    baseURL: WEB,
    locale: 'zh-CN',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: 'bash e2e/fixtures/backend.sh',
      url: `${API}/health`,
      timeout: 240_000,
      reuseExistingServer: false,
      stdout: 'ignore',
      stderr: 'pipe',
    },
    {
      command: 'node e2e/fixtures/fake-ai.mjs',
      url: 'http://127.0.0.1:6990/health',
      reuseExistingServer: false,
    },
    {
      command: 'node_modules/.bin/next dev -p 3100',
      url: WEB,
      timeout: 240_000,
      reuseExistingServer: false,
      env: { NEXT_DIST_DIR: '.next-e2e', NEXT_PUBLIC_API_BASE: API, KNOWFORGE_API_URL: API, NEXT_TELEMETRY_DISABLED: '1', NEXT_PUBLIC_ENABLE_SW: '1' },
    },
  ],
})
