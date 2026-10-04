import { expect, test } from '@playwright/test'
import { api, registerUser, signIn, unique } from './helpers'

// AI 朗读：读者在阅读页开始朗读，逐段播放并高亮当前段落；本章读完自动进入下一章继续朗读，最后一章读完停止。

test('逐段朗读并自动进入下一章', async ({ page }) => {
  const author = await registerUser('raauthor')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title: unique('朗读书 '), status: 'published', is_public: true } })
  await api(`/books/${book.id}/documents`, { token: author.token, body: { title: '第一章', slug: 'one', content: '第一章第一段。\n\n第一章的 **第二段**，含 [链接](https://example.com)。', status: 'published', sort_order: 0 } })
  await api(`/books/${book.id}/documents`, { token: author.token, body: { title: '第二章', slug: 'two', content: '第二章只有一段。\n\n```\ncode is skipped\n```', status: 'published', sort_order: 1 } })

  const reader = await registerUser('rareader')
  await signIn(page, reader)
  await page.goto(`/book/reader/${book.slug}/one`)
  await page.getByTestId('read-aloud-toggle').click()
  const player = page.getByTestId('read-aloud-player')
  await expect(player).toBeVisible()
  // 标题 + 两个段落；播放到正文时高亮对应段落
  await expect(player.getByTestId('read-aloud-progress')).toHaveText(/\/ 3 段/)
  await expect(page.locator('.markdown-body p.bg-primary-50').first()).toBeVisible({ timeout: 15000 })
  await expect(player.getByTestId('read-aloud-usage')).toContainText('本月已朗读')

  // 读完自动进入下一章（去掉 listen 参数）并继续朗读；代码块不朗读
  await page.waitForURL(new RegExp(`/book/reader/${book.slug}/two$`), { timeout: 20000 })
  await expect(player).toBeVisible()
  await expect(player.getByTestId('read-aloud-progress')).toHaveText(/\/ 2 段/)
  await expect(player.getByTestId('read-aloud-current')).toHaveText('本章已读完', { timeout: 15000 })
  await expect(page.locator('.markdown-body .bg-primary-50')).toHaveCount(0)

  await page.getByTestId('read-aloud-toggle').click()
  await expect(player).toBeHidden()
  const status = await api<{ used: number }>('/read-aloud/status', { token: reader.token })
  expect(status.used).toBeGreaterThan(20)
})
