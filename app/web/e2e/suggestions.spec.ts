import { expect, test } from '@playwright/test'
import { api, registerUser, signIn, unique } from './helpers'

// 修改建议：「建议者」修改正文后提交建议（不能直接保存）；作者在批注侧栏「修改建议」中审阅，只采纳部分修改并保存；
// 批注中输入 @ 可选择协作者。

test('建议者提交修改建议，作者部分采纳', async ({ browser }) => {
  const author = await registerUser('sgauthor')
  const suggester = await registerUser('sgsugg')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title: unique('建议书 ') } })
  const doc = await api<{ id: number }>(`/books/${book.id}/documents`, { token: author.token, body: { title: '第一章', slug: 'one', content: '甲\n乙\n丙\n丁' } })
  const invite = await api<{ id: number }>(`/books/${book.id}/collaborators`, { token: author.token, body: { username: suggester.user.username, role: 'suggester' } })
  await api(`/collaboration/invitations/${invite.id}/accept`, { token: suggester.token, method: 'POST' })

  // 建议者：没有「保存 / 发布」，只能提交建议
  const ctxS = await browser.newContext()
  const s = await ctxS.newPage()
  await signIn(s, suggester)
  await s.goto(`/book/writer/${book.slug}/one`)
  await expect(s.getByTestId('suggest-banner')).toBeVisible()
  await expect(s.getByRole('button', { name: '发布' })).toHaveCount(0)
  const editorS = s.locator('textarea').first()
  await expect(editorS).toHaveValue('甲\n乙\n丙\n丁')
  await editorS.fill('甲\n乙（建议）\n丙\n丁（建议）')
  await s.getByTestId('suggest-submit').click()
  await s.getByRole('dialog').locator('textarea').fill('润色两处')
  await s.getByTestId('suggest-confirm').click()
  await expect(editorS).toHaveValue('甲\n乙\n丙\n丁')
  expect((await api<{ content: string }>(`/documents/${doc.id}`, { token: author.token })).content).toBe('甲\n乙\n丙\n丁')

  // 作者：审阅建议，只采纳第一处
  const ctxA = await browser.newContext()
  const a = await ctxA.newPage()
  await signIn(a, author)
  await a.goto(`/book/writer/${book.slug}/one?comments=suggestions`)
  await expect(a.getByTestId('tree-suggestions')).toContainText('1')
  const item = a.getByTestId('suggestion-item')
  await expect(item).toContainText('润色两处')
  await item.getByTestId('suggestion-review').click()
  await expect(a.getByTestId('suggest-hunk')).toHaveCount(2)
  await a.getByRole('checkbox', { name: '采纳修改 2' }).click()
  await a.getByTestId('suggest-apply').click()
  await expect(a.locator('textarea').first()).toHaveValue('甲\n乙（建议）\n丙\n丁')
  await expect.poll(async () => (await api<{ content: string }>(`/documents/${doc.id}`, { token: author.token })).content).toBe('甲\n乙（建议）\n丙\n丁')
  await expect(a.getByTestId('suggestion-item')).toHaveCount(0)
  await expect(a.getByTestId('tree-suggestions')).toHaveCount(0)

  // 批注中 @ 提及建议者
  await a.locator('textarea').first().evaluate((el: HTMLTextAreaElement) => { el.focus(); el.setSelectionRange(0, 1) })
  await a.getByRole('button', { name: '批注（选中文字后添加）' }).click()
  const box = a.getByTestId('comment-composer').locator('textarea')
  await box.pressSequentially('谢谢 @sgsugg')
  await expect(a.getByTestId('mention-menu')).toContainText(suggester.user.username)
  await box.press('Enter')
  await expect(box).toHaveValue(`谢谢 @${suggester.user.username} `)

  await ctxA.close()
  await ctxS.close()
})
