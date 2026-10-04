import { readFileSync } from 'node:fs'
import { expect, test } from '@playwright/test'
import { signIn, state } from './helpers'

// 站点备份：管理员在「系统设置 · 备份」立即备份，进度经事件流更新为「已完成」，下载得到含 manifest.json 的 zip。

test('管理员备份站点并下载', async ({ page }) => {
  await signIn(page, state().admin)
  await page.goto('/admin/settings/backup')
  await page.getByTestId('backup-start').click()
  const row = page.getByTestId('backup-row').first()
  await expect(row).toContainText('已完成', { timeout: 60000 })
  await expect(row).toContainText('张表')

  const [download] = await Promise.all([page.waitForEvent('download'), row.getByRole('button', { name: '下载' }).click()])
  expect(download.suggestedFilename()).toMatch(/^knowforge-backup-.*\.zip$/)
  const file = readFileSync((await download.path())!)
  expect(file.subarray(0, 2).toString()).toBe('PK')
  expect(file.includes(Buffer.from('manifest.json'))).toBeTruthy()
})
