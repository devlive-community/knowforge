import { fileURLToPath } from 'node:url'
import { configDefaults, defineConfig } from 'vitest/config'

// 只声明与 tsconfig 一致的 @/ 路径别名；端到端测试（e2e/，Playwright）不由 vitest 运行
export default defineConfig({
  resolve: { alias: { '@': fileURLToPath(new URL('.', import.meta.url)) } },
  test: { exclude: [...configDefaults.exclude, 'e2e/**'] },
})
