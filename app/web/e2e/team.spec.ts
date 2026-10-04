import { expect, test } from '@playwright/test'
import { api, registerUser, signIn, unique } from './helpers'

// 协作动态与分工总览：协作者的保存、批注与分工变化出现在作者写作台的「协作」侧栏（实时刷新），
// 分工总览按阶段统计并列出各章节，点击即可打开章节。

test('协作动态与分工总览', async ({ page }) => {
  const author = await registerUser('tmauthor')
  const editor = await registerUser('tmeditor')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title: unique('协作书 ') } })
  const one = await api<{ id: number }>(`/books/${book.id}/documents`, { token: author.token, body: { title: '第一章', slug: 'one', content: '甲', sort_order: 0 } })
  const two = await api<{ id: number }>(`/books/${book.id}/documents`, { token: author.token, body: { title: '第二章', slug: 'two', content: '乙', sort_order: 1 } })
  const invite = await api<{ id: number }>(`/books/${book.id}/collaborators`, { token: author.token, body: { username: editor.user.username, role: 'editor' } })
  await api(`/collaboration/invitations/${invite.id}/accept`, { token: editor.token, method: 'POST' })
  await api(`/documents/${one.id}`, { token: editor.token, method: 'PUT', body: { content: '甲\n新的一段' } })
  await api(`/documents/${one.id}/writer-comments`, { token: editor.token, body: { content: '这段要不要再展开？', quote: '甲' } })
  await api(`/documents/${two.id}/task`, { token: author.token, method: 'PUT', body: { assignee_id: editor.user.id, stage: 'review' } })

  await signIn(page, author)
  await page.goto(`/book/writer/${book.slug}/one`)
  await page.getByTestId('team-open').click()
  const feed = page.getByTestId('activity-feed')
  await expect(feed).toContainText('调整了「第二章」的分工')
  await expect(feed).toContainText('批注了「第一章」')
  await expect(feed).toContainText('这段要不要再展开？')
  await expect(feed).toContainText(`${editor.user.username} 修改了「第一章」`)
  await expect(feed).toContainText('+1')

  // 实时：协作者再保存第二章，动态自动出现
  await api(`/documents/${two.id}`, { token: editor.token, method: 'PUT', body: { content: '乙\n丙' } })
  await expect(feed).toContainText('修改了「第二章」')

  // 分工总览：点击章节打开
  await page.getByRole('tab', { name: '分工总览' }).click()
  await expect(page).toHaveURL(/team=tasks/)
  const overview = page.getByTestId('task-overview')
  await expect(overview).toContainText('逾期 0 章 · 未指定负责人 1 章')
  await overview.getByTestId('overview-row').filter({ hasText: '第二章' }).click()
  await expect(page).toHaveURL(/\/two/)
  await expect(page.locator('textarea').first()).toHaveValue('乙\n丙')
})
