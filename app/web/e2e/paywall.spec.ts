import { expect, test } from '@playwright/test'
import { api, confirmOffline, orderNoFromUrl, registerUser, unique } from './helpers'

// 付费墙：付费章节只显示试读与付费墙；读者购买本章并付款后可阅读全文。

test('购买付费章节后解锁全文', async ({ page }) => {
  const author = await registerUser('author')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title: unique('付费书 '), status: 'published', is_public: true } })
  const text = (s: string) => Array.from({ length: 8 }, () => s).join('\n\n')
  await api(`/books/${book.id}/documents`, { token: author.token, body: { title: '免费章', content: text('免费内容。'), status: 'published', sort_order: 0 } })
  const paid = await api<{ id: number; slug: string }>(`/books/${book.id}/documents`, {
    token: author.token, body: { title: '付费章', content: text('付费开头。') + '\n\n结尾秘密：玫瑰花园。', status: 'published', sort_order: 1 },
  })
  await api(`/books/${book.id}/paid-settings`, {
    token: author.token, method: 'PUT', body: { enabled: true, chapter_price_cents: 300, free_chapters: 1, preview_percent: 30 },
  })

  // 读者经登录页登录后直接打开付费章节：服务端渲染的阅读页应识别登录态（显示购买而不是「登录后解锁」）
  const reader = await registerUser('payreader')
  await page.goto('/login')
  await page.locator('input[autocomplete="username"]').fill(reader.user.username)
  await page.locator('input[autocomplete="current-password"]').fill('Secret123!')
  await page.locator('form').getByRole('button', { name: '登录', exact: true }).click()
  await page.waitForURL((url) => !url.pathname.startsWith('/login'))
  await page.goto(`/book/reader/${book.slug}/${paid.slug}`)
  await expect(page.getByText('本章为付费内容')).toBeVisible()
  await expect(page.getByText('结尾秘密：玫瑰花园。')).toHaveCount(0)

  await page.getByRole('link', { name: /购买本章/ }).click()
  await expect(page).toHaveURL(/\/pay\/checkout/)
  await page.getByRole('button', { name: /^支付 / }).click()
  await page.waitForURL(/\/pay\/orders\//)
  await confirmOffline(orderNoFromUrl(page.url()))

  await page.goto(`/book/reader/${book.slug}/${paid.slug}`)
  await expect(page.getByText('结尾秘密：玫瑰花园。')).toBeVisible()
  await expect(page.getByText('本章为付费内容')).toHaveCount(0)
})
