import { expect, test } from '@playwright/test'
import { api, registerUser, signIn, unique } from './helpers'

// 离线阅读：在书籍页「离线阅读」下载全部章节后断网，已保存的章节仍可打开，未保存的页面显示离线页；
// 离线期间打开章节记录的阅读进度在联网后补传到服务端。

test('下载离线阅读后断网仍可阅读', async ({ page, context }) => {
  const author = await registerUser('offline')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title: unique('离线书 '), status: 'published', is_public: true } })
  await api(`/books/${book.id}/documents`, { token: author.token, body: { title: '第一章', slug: 'one', content: '第一章的正文 alpha-offline', status: 'published', sort_order: 0 } })
  await api(`/books/${book.id}/documents`, { token: author.token, body: { title: '第二章', slug: 'two', content: '第二章的正文 beta-offline', status: 'published', sort_order: 1 } })
  await signIn(page, author)

  await page.goto(`/book/detail/${book.slug}`)
  // 等 Service Worker 接管页面（离线时由它返回缓存）
  await page.waitForFunction(async () => (await navigator.serviceWorker.ready) && !!navigator.serviceWorker.controller, null, { timeout: 30000 })
  await page.getByTestId('offline-book').click()
  await expect(page.getByTestId('offline-book-status')).toContainText('0 / 2')
  await page.getByTestId('offline-book-download').click()
  await expect(page.getByTestId('offline-book-status')).toContainText('2 / 2', { timeout: 30000 })
  await page.keyboard.press('Escape')

  await context.setOffline(true)
  await page.goto(`/book/reader/${book.slug}/two`)
  await expect(page.getByText('第二章的正文 beta-offline')).toBeVisible()
  await expect(page.getByTestId('offline-banner')).toBeVisible()
  await page.goto('/explore')
  await expect(page.getByTestId('offline-page')).toBeVisible()

  // 离线时打开第二章记录的进度：联网后补传
  await context.setOffline(false)
  await page.goto(`/book/detail/${book.slug}`)
  await expect.poll(async () => (await api<{ doc_slug?: string } | null>(`/reading-progress/${book.id}`, { token: author.token }))?.doc_slug, { timeout: 15000 }).toBe('two')
})
