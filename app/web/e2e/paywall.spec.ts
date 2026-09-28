import { expect, test } from '@playwright/test'
import { admin, api, confirmOffline, orderNoFromUrl, registerUser, signIn, unique } from './helpers'

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
  // 试读在服务端截断：页面源码（含注入给前端的数据）与接口响应中都没有付费部分，而不是由前端隐藏
  expect(await page.content()).not.toContain('玫瑰花园')
  const readerToken = await page.evaluate(() => localStorage.getItem('knowforge_token'))
  const apiDoc = await api<{ content: string; paywall?: { locked: boolean } }>(`/documents/${paid.id}`, { token: readerToken || '' })
  expect(apiDoc.paywall?.locked).toBe(true)
  expect(apiDoc.content).not.toContain('玫瑰花园')
  expect(apiDoc.content.length).toBeLessThan(40) // 8 段「付费开头。」+ 结尾共约 70 字，试读 30%

  await page.getByRole('link', { name: /购买本章/ }).click()
  await expect(page).toHaveURL(/\/pay\/checkout/)
  await page.getByRole('button', { name: /^支付 / }).click()
  await page.waitForURL(/\/pay\/orders\//)
  await confirmOffline(orderNoFromUrl(page.url()))

  await page.goto(`/book/reader/${book.slug}/${paid.slug}`)
  await expect(page.getByText('结尾秘密：玫瑰花园。')).toBeVisible()
  await expect(page.getByText('本章为付费内容')).toHaveCount(0)
})

// 作者在付费设置中选择「哪些会员免费阅读」（选项来自会员方案的内容访问等级），读者的付费墙显示可免费阅读的方案。
test('按会员方案选择免费阅读', async ({ page, browser }) => {
  const planName = unique('高级版')
  await admin('/admin/membership/plans', { body: { name: planName, entitlements: { 'content.access_tier': 3 }, prices: [{ duration_days: 30, price_cents: 3000 }] } })
  const author = await registerUser('tierauthor')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title: unique('会员免费书 '), status: 'published', is_public: true } })
  await api(`/books/${book.id}/documents`, { token: author.token, body: { title: '免费章', content: '免费', status: 'published', sort_order: 0 } })
  const paid = await api<{ slug: string }>(`/books/${book.id}/documents`, { token: author.token, body: { title: '付费章', content: '付费', status: 'published', sort_order: 1 } })
  await api(`/books/${book.id}/paid-settings`, { token: author.token, method: 'PUT', body: { enabled: true, chapter_price_cents: 300, free_chapters: 1 } })

  await signIn(page, author)
  await page.goto(`/book/settings/${book.slug}/paid`)
  await page.getByRole('button', { name: '不免费（读者需购买）' }).click()
  await page.getByRole('option', { name: `「${planName}」会员免费阅读` }).click()
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByText('已保存').first()).toBeVisible()

  const ctx = await browser.newContext()
  const readerPage = await ctx.newPage()
  await signIn(readerPage, await registerUser('tierreader'))
  await readerPage.goto(`/book/reader/${book.slug}/${paid.slug}`)
  await expect(readerPage.getByText(`「${planName}」会员可免费阅读本书`)).toBeVisible()
  await ctx.close()
})
