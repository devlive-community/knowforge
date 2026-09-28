import http from 'node:http'
import type { AddressInfo } from 'node:net'
import { expect, test } from '@playwright/test'
import { WEB, api, registerUser, signIn, unique } from './helpers'

// 嵌入组件：只有 /embed/* 可被其他网站嵌入（其余页面只允许同源）；嵌入页按游客身份显示，付费章节只有试读；详情页生成嵌入代码。

test('嵌入书籍与章节到其他网站', async ({ page, request }) => {
  const author = await registerUser('embed')
  const title = unique('嵌入书 ')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title, status: 'published', is_public: true } })
  const free = await api<{ slug: string }>(`/books/${book.id}/documents`, { token: author.token, body: { title: '第一章', content: '## 开篇\n\n免费正文', status: 'published', sort_order: 0 } })
  const paid = await api<{ slug: string }>(`/books/${book.id}/documents`, {
    token: author.token, body: { title: '第二章', content: Array.from({ length: 6 }, () => '付费开头。').join('\n\n') + '\n\n结尾秘密：玫瑰花园。', status: 'published', sort_order: 1 },
  })
  await api(`/books/${book.id}/paid-settings`, { token: author.token, method: 'PUT', body: { enabled: true, chapter_price_cents: 300, free_chapters: 1, preview_percent: 30 } })

  // 响应头：普通页面只允许同源嵌入，嵌入页允许任意网站
  const home = await request.get(`${WEB}/`)
  expect(home.headers()['x-frame-options']).toBe('SAMEORIGIN')
  expect(home.headers()['content-security-policy']).toContain("frame-ancestors 'self'")
  const embedRes = await request.get(`${WEB}/embed/book/${book.slug}`)
  expect(embedRes.headers()['x-frame-options']).toBeUndefined()
  expect(embedRes.headers()['content-security-policy']).toContain('frame-ancestors *')

  // 其他网站嵌入：嵌入页正常显示（付费章节只有试读），普通页面被浏览器拦下
  const host = http.createServer((_, res) => {
    res.setHeader('Content-Type', 'text/html; charset=utf-8')
    res.end(`<iframe id="book" src="${WEB}/embed/book/${book.slug}" width="600" height="400"></iframe>
<iframe id="paid" src="${WEB}/embed/doc/${book.slug}/${paid.slug}" width="600" height="400"></iframe>
<iframe id="detail" src="${WEB}/book/detail/${book.slug}" width="600" height="400"></iframe>
<iframe id="card" src="${WEB}/embed/card/${book.slug}" width="600" height="240"></iframe>`)
  })
  await new Promise<void>((resolve) => host.listen(0, '127.0.0.1', resolve))
  try {
    await page.goto(`http://127.0.0.1:${(host.address() as AddressInfo).port}/`)
    const bookFrame = page.frameLocator('#book')
    await expect(bookFrame.getByText(title)).toBeVisible()
    await expect(bookFrame.getByRole('link', { name: '第一章' })).toHaveAttribute('href', `${WEB}/book/reader/${book.slug}/${free.slug}`)
    const paidFrame = page.frameLocator('#paid')
    await expect(paidFrame.getByText('本章为付费内容，以上为试读部分。')).toBeVisible()
    await expect(paidFrame.getByText('玫瑰花园')).toHaveCount(0)
    await expect(page.frameLocator('#detail').getByText(title)).toHaveCount(0)
    // 书籍信息卡片：只有基本信息，不含目录
    const cardFrame = page.frameLocator('#card')
    await expect(cardFrame.getByRole('link', { name: title })).toHaveAttribute('href', `${WEB}/book/detail/${book.slug}`)
    await expect(cardFrame.getByText('2 章')).toBeVisible()
    await expect(cardFrame.getByRole('link', { name: /开始阅读/ })).toBeVisible()
    await expect(cardFrame.getByText('第一章')).toHaveCount(0)
  } finally {
    host.close()
  }

  // 书籍详情页生成嵌入代码
  await signIn(page, author)
  await page.goto(`/book/detail/${book.slug}`)
  await page.getByRole('button', { name: '嵌入到其他网站' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('textbox', { name: '嵌入代码' })).toHaveValue(new RegExp(`src="${WEB}/embed/book/${book.slug}"`))
  await dialog.getByRole('button', { name: '整本书（信息与目录）' }).click()
  await page.getByRole('option', { name: '第一章' }).click()
  await expect(dialog.getByRole('textbox', { name: '嵌入代码' })).toHaveValue(new RegExp(`/embed/doc/${book.slug}/${free.slug}`))
  await dialog.getByRole('button', { name: '第一章' }).click()
  await page.getByRole('option', { name: '书籍信息卡片（不含目录）' }).click()
  await expect(dialog.getByRole('textbox', { name: '嵌入代码' })).toHaveValue(new RegExp(`/embed/card/${book.slug}".*height="240"`))
})
