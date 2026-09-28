import { expect, test } from '@playwright/test'
import { api, registerUser, signIn, unique } from './helpers'

// 双向链接：写作台输入 [[ 选择章节插入链接；阅读页渲染链接并在目标章节显示「被引用」；书籍设置查看章节链接与失效链接。

test('双向链接与被引用', async ({ page }) => {
  const author = await registerUser('links')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title: unique('链接书 '), status: 'published', is_public: true } })
  await api(`/books/${book.id}/documents`, { token: author.token, body: { title: '安装指南', slug: 'setup', content: '安装步骤', status: 'published', sort_order: 0 } })
  const usage = await api<{ id: number }>(`/books/${book.id}/documents`, { token: author.token, body: { title: '进阶用法', slug: 'usage', content: '', status: 'published', sort_order: 1 } })
  await signIn(page, author)

  // 写作台：输入 [[ 弹出章节选择，回车插入
  await page.goto(`/book/writer/${book.slug}/usage`)
  const editor = page.getByPlaceholder(/./).and(page.locator('textarea')).first()
  await editor.click()
  await editor.pressSequentially('先读 [[安装')
  const menu = page.getByRole('listbox', { name: '链接到章节' })
  await expect(menu.getByRole('option', { name: /安装指南/ })).toBeVisible()
  await expect(menu.getByRole('option', { name: /进阶用法/ })).toHaveCount(0)
  await page.keyboard.press('Enter')
  await expect(editor).toHaveValue('先读 [[安装指南]]')

  await api(`/documents/${usage.id}`, { token: author.token, method: 'PUT', body: { content: '先读 [[安装指南]]，另见 [[未写的章节]]。' } })

  // 阅读页：链接与缺失链接
  await page.goto(`/book/reader/${book.slug}/usage`)
  await expect(page.locator('.markdown-body').getByRole('link', { name: '安装指南' })).toHaveAttribute('href', `/book/reader/${book.slug}/setup`)
  await expect(page.locator('.md-wikilink-missing')).toHaveText('未写的章节')

  // 目标章节的「被引用」
  await page.goto(`/book/reader/${book.slug}/setup`)
  const backlinks = page.getByRole('region', { name: '被引用' })
  await expect(backlinks.getByRole('heading', { name: '被引用（1）' })).toBeVisible()
  await expect(backlinks.getByRole('link', { name: /进阶用法/ })).toContainText('先读 安装指南，另见 未写的章节。')

  // 书籍设置：章节链接与失效链接
  await page.goto(`/book/settings/${book.slug}/links`)
  await expect(page.getByText('[[未写的章节]]')).toBeVisible()
  await expect(page.getByText('链接到').locator('..')).toContainText('安装指南')
})
