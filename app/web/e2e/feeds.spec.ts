import { expect, test } from '@playwright/test'
import { WEB, api, registerUser, signIn, unique } from './helpers'

// RSS 订阅：书籍详情页提供自动发现链接与复制订阅地址的按钮，订阅内容包含最新发布的章节。

test('书籍详情页的 RSS 订阅', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: WEB })
  const author = await registerUser('rss')
  const title = unique('订阅书 ')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title, status: 'published', is_public: true } })
  await api(`/books/${book.id}/documents`, { token: author.token, body: { title: '最新一章', content: '## 小节\n\n正文内容', status: 'published' } })

  await signIn(page, author)
  await page.goto(`/book/detail/${book.slug}`)
  const alternate = page.locator('link[rel="alternate"][type="application/rss+xml"]')
  await expect(alternate).toHaveAttribute('href', `/api/v1/feeds/books/${book.slug}.xml`)

  await page.getByRole('button', { name: /RSS 订阅/ }).click()
  await expect(page.getByText('订阅地址已复制')).toBeVisible()
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied).toBe(`${WEB}/api/v1/feeds/books/${book.slug}.xml`)

  const res = await page.request.get(copied)
  expect(res.status()).toBe(200)
  const xml = await res.text()
  expect(xml).toContain(`<title>${title} - E2E 站点</title>`)
  expect(xml).toContain('<title>最新一章</title>')
  expect(xml).toContain('小节 正文内容')
})
