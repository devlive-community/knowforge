import { expect, test } from '@playwright/test'
import { API, api, registerUser, signIn, unique } from './helpers'

// 我的任务：ZIP 导入走后台任务，导入弹窗经通知事件流得知完成（不轮询）；「我的任务」按分组列出任务并链接到结果。

test('后台导入与我的任务', async ({ page }) => {
  const author = await registerUser('tasks')
  const title = unique('导出源 ')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title, status: 'published', is_public: true } })
  await api(`/books/${book.id}/documents`, { token: author.token, body: { title: '第一章', slug: 'one', content: '正文', status: 'published' } })
  const zip = Buffer.from(await (await fetch(`${API}/api/v1/books/${book.id}/export`, { headers: { Authorization: `Bearer ${author.token}` } })).arrayBuffer())
  expect(zip.length).toBeGreaterThan(100)
  await signIn(page, author)

  await page.goto('/user/tasks')
  await expect(page.getByText('当前没有进行中的任务')).toBeVisible()

  // 导入：弹窗在任务完成推送后显示结果
  await page.goto('/books')
  await page.getByRole('button', { name: '导入书籍' }).click()
  const dialog = page.getByRole('dialog', { name: /导入/ })
  await dialog.getByRole('tab', { name: /书籍压缩包/ }).click()
  await dialog.locator('input[type="file"]').setInputFiles({ name: 'book.zip', mimeType: 'application/zip', buffer: zip })
  const imported = unique('导入结果 ')
  await dialog.getByPlaceholder(/./).last().fill(imported)
  await dialog.getByRole('button', { name: '开始导入' }).click()
  await expect(dialog.getByText('书籍已构建完成')).toBeVisible({ timeout: 30000 })

  // 我的任务：已完成分组中有该导入，链接到导入的书籍
  await page.goto('/user/tasks?tab=done')
  const row = page.getByTestId('user-task-row').filter({ hasText: imported })
  await expect(row).toContainText('导入书籍')
  await expect(row).toContainText('已完成')
  await expect(row.getByRole('link', { name: imported })).toHaveAttribute('href', /^\/book\/detail\//)
  await page.goto('/user/tasks?tab=failed')
  await expect(page.getByText('没有失败的任务')).toBeVisible()
})
