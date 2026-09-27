import { expect, test } from '@playwright/test'
import { api, registerUser, signIn, unique } from './helpers'

// 书籍问答：读者在阅读页向 AI 提问，回答经 SSE 逐字推送并标注出处。

test('向 AI 提问并实时收到回答', async ({ page }) => {
  const author = await registerUser('qaauthor')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title: unique('缓存指南 '), status: 'published', is_public: true } })
  const doc = await api<{ id: number; slug: string }>(`/books/${book.id}/documents`, {
    token: author.token, body: { title: '缓存篇', content: '## 读缓存\n\n缓存可以加速读取，缓存的命中率决定了性能。', status: 'published' },
  })

  const reader = await registerUser('qareader')
  await signIn(page, reader)
  await page.goto(`/book/reader/${book.slug}/${doc.slug}?qa=ai`)
  const input = page.getByPlaceholder(/就这本书提问/)
  await input.fill('缓存有什么用？')
  await page.getByRole('button', { name: '提问', exact: true }).click()
  await expect(page.getByText(/缓存可以加速读取/).last()).toBeVisible()
  await expect(page.getByText(/端到端测试的模拟回答/)).toBeVisible()
})
