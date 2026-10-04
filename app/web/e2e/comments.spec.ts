import { expect, test } from '@playwright/test'
import { api, registerUser, signIn, unique } from './helpers'

// 写作批注：协作者选中正文添加批注，作者在写作台实时看到（目录标记未解决数），点击原文定位到正文，回复后标记解决。

test('协作者批注正文，作者回复并解决', async ({ browser }) => {
  const author = await registerUser('cmauthor')
  const editor = await registerUser('cmeditor')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title: unique('批注书 ') } })
  await api(`/books/${book.id}/documents`, { token: author.token, body: { title: '第一章', slug: 'one', content: '开头。\n这里有一句需要讨论的话。\n结尾。' } })
  const invite = await api<{ id: number }>(`/books/${book.id}/collaborators`, { token: author.token, body: { username: editor.user.username, role: 'editor' } })
  await api(`/collaboration/invitations/${invite.id}/accept`, { token: editor.token, method: 'POST' })

  const ctxA = await browser.newContext()
  const ctxB = await browser.newContext()
  const a = await ctxA.newPage()
  const b = await ctxB.newPage()
  await signIn(a, author)
  await signIn(b, editor)
  await a.goto(`/book/writer/${book.slug}/one?comments=open`)
  await expect(a.getByTestId('comments-drawer')).toContainText('这一章没有未解决的批注')
  await b.goto(`/book/writer/${book.slug}/one`)
  const editorB = b.locator('textarea').first()
  await expect(editorB).toHaveValue(/需要讨论的话/)

  // 协作者选中文字添加批注
  await editorB.evaluate((el: HTMLTextAreaElement) => {
    const start = el.value.indexOf('需要讨论的话')
    el.focus()
    el.setSelectionRange(start, start + '需要讨论的话'.length)
  })
  await b.getByRole('button', { name: '批注（选中文字后添加）' }).click()
  const composer = b.getByTestId('comment-composer')
  await expect(composer).toContainText('需要讨论的话')
  await composer.locator('textarea').fill('这句是否太长？')
  await b.getByTestId('comment-post').click()
  await expect(b.getByTestId('comment-thread')).toContainText('这句是否太长？')

  // 作者实时看到批注与目录标记；点击原文在正文中选中
  const thread = a.getByTestId('comment-thread')
  await expect(thread).toContainText('这句是否太长？')
  await expect(a.getByTestId('tree-comments')).toContainText('1')
  await thread.getByText('需要讨论的话').click()
  const selected = await a.locator('textarea').first().evaluate((el: HTMLTextAreaElement) => el.value.slice(el.selectionStart, el.selectionEnd))
  expect(selected).toBe('需要讨论的话')

  // 回复并标记解决：未解决列表清空、目录标记消失；协作者那边同步
  await thread.getByRole('button', { name: '回复' }).click()
  await thread.locator('textarea').fill('拆成两句吧')
  await thread.getByRole('button', { name: '回复' }).click()
  await expect(thread).toContainText('拆成两句吧')
  await expect(b.getByTestId('comment-thread')).toContainText('拆成两句吧')
  await thread.getByTestId('comment-resolve').click()
  await expect(a.getByTestId('comments-drawer')).toContainText('这一章没有未解决的批注')
  await expect(a.getByTestId('tree-comments')).toHaveCount(0)
  await a.getByRole('tab', { name: /已解决/ }).click()
  await expect(a.getByTestId('comment-thread')).toContainText('拆成两句吧')

  await ctxA.close()
  await ctxB.close()
})
