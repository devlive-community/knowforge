import { expect, test } from '@playwright/test'
import { api, registerUser, signIn, unique } from './helpers'

// 书单：在书籍详情页新建书单并收录、再收录另一本；书单页写推荐语、调整顺序；其他读者收藏后出现在「我收藏的」与书籍详情页。

test('书单：收录、推荐语、排序与收藏', async ({ page, browser }) => {
  const curator = await registerUser('curator')
  const author = await registerUser('lsauthor')
  const mk = (title: string) => api<{ id: number; slug: string }>('/books', { token: author.token, body: { title, status: 'published', is_public: true } })
  const first = await mk(unique('第一本 '))
  const second = await mk(unique('第二本 '))
  const listName = unique('入门书单 ')

  await signIn(page, curator)
  await page.goto(`/book/detail/${first.slug}`)
  await page.getByRole('button', { name: '加入书单' }).click()
  let dialog = page.getByRole('dialog', { name: '加入书单' })
  await expect(dialog.getByText('你还没有书单')).toBeVisible()
  await dialog.getByRole('button', { name: '新建书单' }).click()
  const form = page.getByRole('dialog', { name: '新建书单' })
  await form.getByPlaceholder('例如：分布式系统入门').fill(listName)
  await form.getByRole('button', { name: '创建' }).click()
  await expect(dialog.getByRole('checkbox', { name: listName })).toHaveAttribute('aria-checked', 'true')
  await dialog.getByRole('button', { name: '关闭' }).first().click()

  await page.goto(`/book/detail/${second.slug}`)
  await page.getByRole('button', { name: '加入书单' }).click()
  dialog = page.getByRole('dialog', { name: '加入书单' })
  await dialog.getByRole('checkbox', { name: listName }).click()
  await expect(page.getByText(`已加入「${listName}」`)).toBeVisible()
  await expect(dialog.getByRole('checkbox', { name: listName })).toHaveAttribute('aria-checked', 'true')
  await dialog.getByRole('link', { name: '查看' }).click()

  // 书单页：推荐语与排序
  await expect(page.getByRole('heading', { name: listName })).toBeVisible()
  const entries = page.locator('ol > li')
  await expect(entries).toHaveCount(2)
  await entries.nth(1).getByRole('button', { name: '推荐语' }).click()
  await page.getByRole('dialog').getByRole('textbox').fill('第二本更适合入门')
  await page.getByRole('dialog').getByRole('button', { name: '确定' }).click()
  await expect(entries.nth(1)).toContainText('第二本更适合入门')
  await entries.nth(1).getByRole('button', { name: '上移' }).click()
  await expect(entries.nth(0)).toContainText('第二本更适合入门')
  await page.reload()
  await expect(page.locator('ol > li').nth(0)).toContainText('第二本更适合入门')
  const listUrl = page.url()

  // 其他读者收藏
  const reader = await registerUser('lsreader')
  const ctx = await browser.newContext()
  const other = await ctx.newPage()
  await signIn(other, reader)
  await other.goto(listUrl)
  await other.getByRole('button', { name: '收藏书单' }).click()
  await expect(other.getByRole('button', { name: '已收藏' })).toBeVisible()
  await expect(other.getByText('1 收藏')).toBeVisible()
  await other.goto('/lists?tab=followed')
  await expect(other.getByRole('link', { name: new RegExp(listName) })).toBeVisible()
  await other.goto(`/book/detail/${first.slug}`)
  await expect(other.getByRole('heading', { name: '收录于 1 个书单' })).toBeVisible()
  await ctx.close()
})
