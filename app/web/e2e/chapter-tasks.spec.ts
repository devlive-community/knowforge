import { expect, test } from '@playwright/test'
import { api, registerUser, signIn, unique } from './helpers'

// 章节分工：作者在章节设置中指定负责人与阶段，目录显示分工标记；负责人在写作台筛选「分配给我的」章节；
// 编辑者打开「建议模式」后，保存按钮变为提交修改建议（刷新后仍保持）。

test('章节分工与建议模式', async ({ browser }) => {
  const author = await registerUser('tkauthor')
  const editor = await registerUser('tkeditor')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title: unique('分工书 ') } })
  await api(`/books/${book.id}/documents`, { token: author.token, body: { title: '第一章', slug: 'one', content: '一', sort_order: 0 } })
  await api(`/books/${book.id}/documents`, { token: author.token, body: { title: '第二章', slug: 'two', content: '二', sort_order: 1 } })
  const invite = await api<{ id: number }>(`/books/${book.id}/collaborators`, { token: author.token, body: { username: editor.user.username, role: 'editor' } })
  await api(`/collaboration/invitations/${invite.id}/accept`, { token: editor.token, method: 'POST' })

  const ctxA = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const a = await ctxA.newPage()
  await signIn(a, author)
  await a.goto(`/book/writer/${book.slug}/one`)
  const panel = a.getByTestId('task-panel')
  await panel.getByRole('button', { name: '未指定' }).click()
  await a.getByRole('option', { name: editor.user.username }).click()
  await expect(panel.getByRole('button', { name: editor.user.username })).toBeVisible()
  await panel.getByRole('button', { name: '未开始' }).click()
  await a.getByRole('option', { name: '待审阅' }).click()
  await expect(panel.getByRole('button', { name: '待审阅' })).toBeVisible()
  await expect(a.getByTestId('tree-task')).toHaveCount(1)
  await ctxA.close()

  // 负责人：只看分配给自己的章节
  const ctxB = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const b = await ctxB.newPage()
  await signIn(b, editor)
  await b.goto(`/book/writer/${book.slug}/one?filter=mine`)
  await expect(b.getByRole('button', { name: '第一章' })).toBeVisible()
  await expect(b.getByRole('button', { name: '第二章' })).toHaveCount(0)
  await b.getByTestId('task-filter').getByRole('button', { name: '分配给我的' }).click()
  await b.getByRole('option', { name: '全部章节' }).click()
  await expect(b.getByRole('button', { name: '第二章' })).toBeVisible()

  // 建议模式
  await b.getByRole('switch', { name: '建议模式' }).click()
  await expect(b.getByTestId('suggest-submit')).toBeVisible()
  await expect(b.getByTestId('suggest-banner')).toBeVisible()
  await expect(b.getByRole('switch', { name: '建议模式' })).toHaveAttribute('aria-checked', 'true')
  await b.reload()
  await expect(b.getByTestId('suggest-submit')).toBeVisible()
  await expect(b.getByRole('switch', { name: '建议模式' })).toHaveAttribute('aria-checked', 'true')
  await ctxB.close()
})
