import { expect, test } from '@playwright/test'
import { api, registerUser, signIn, unique } from './helpers'

// 协作写作：两位作者同时打开同一章——互相看到在线状态；改动不同位置时后保存的一方自动合并；
// 未修改的一方自动载入他人保存的内容；改动同一处时弹出合并对话框，选择后保存。

test('两人同时编辑同一章：在线状态、自动合并与冲突处理', async ({ browser }) => {
  const author = await registerUser('coauthor')
  const editor = await registerUser('coeditor')
  const book = await api<{ id: number; slug: string }>('/books', { token: author.token, body: { title: unique('合写书 ') } })
  const doc = await api<{ id: number }>(`/books/${book.id}/documents`, { token: author.token, body: { title: '第一章', slug: 'one', content: '甲\n乙\n丙' } })
  const invite = await api<{ id: number }>(`/books/${book.id}/collaborators`, { token: author.token, body: { username: editor.user.username, role: 'editor' } })
  await api(`/collaboration/invitations/${invite.id}/accept`, { token: editor.token, method: 'POST' })

  const ctxA = await browser.newContext()
  const ctxB = await browser.newContext()
  const a = await ctxA.newPage()
  const b = await ctxB.newPage()
  await signIn(a, author)
  await signIn(b, editor)
  const url = `/book/writer/${book.slug}/one`
  await a.goto(url)
  await b.goto(url)
  const editorA = a.locator('textarea').first()
  const editorB = b.locator('textarea').first()
  await expect(editorA).toHaveValue('甲\n乙\n丙')
  await expect(editorB).toHaveValue('甲\n乙\n丙')

  // 在线状态：彼此可见，并提示同一章节有人在编辑
  await expect(a.getByTestId('collab-avatars')).toBeVisible()
  await expect(a.getByTestId('collab-banner')).toContainText(editor.user.username)
  await expect(b.getByTestId('collab-banner')).toBeVisible()

  // 改动不同位置：B 先保存；A 有未保存修改，看到提示；A 保存时自动合并
  await editorA.fill('甲\n乙\n丙\n丁（A）')
  await editorB.fill('甲\n乙（B）\n丙')
  await b.getByRole('button', { name: '保存', exact: true }).click()
  await expect(b.getByText('已保存').first()).toBeVisible()
  await expect(a.getByTestId('collab-banner')).toContainText('刚保存了这一章的新版本')
  await a.getByRole('button', { name: '保存', exact: true }).click()
  await expect(editorA).toHaveValue('甲\n乙（B）\n丙\n丁（A）')
  await expect.poll(async () => (await api<{ content: string }>(`/documents/${doc.id}`, { token: author.token })).content).toBe('甲\n乙（B）\n丙\n丁（A）')
  // B 没有未保存修改：自动载入 A 保存（合并后）的内容
  await expect(editorB).toHaveValue('甲\n乙（B）\n丙\n丁（A）')

  // 改动同一处：B 先保存，A 保存时弹出合并对话框，选择采用对方的
  await editorA.fill('甲（A 改）\n乙（B）\n丙\n丁（A）')
  await editorB.fill('甲（B 改）\n乙（B）\n丙\n丁（A）')
  await b.getByRole('button', { name: '保存', exact: true }).click()
  await expect(a.getByTestId('collab-banner')).toContainText('刚保存了这一章的新版本')
  await a.getByRole('button', { name: '保存', exact: true }).click()
  const dialog = a.getByTestId('conflict-dialog')
  await expect(dialog.getByTestId('conflict-block')).toHaveCount(1)
  await dialog.getByRole('tab', { name: '采用对方的' }).click()
  await a.getByTestId('conflict-save').click()
  await expect(editorA).toHaveValue('甲（B 改）\n乙（B）\n丙\n丁（A）')
  await expect.poll(async () => (await api<{ content: string }>(`/documents/${doc.id}`, { token: author.token })).content).toBe('甲（B 改）\n乙（B）\n丙\n丁（A）')

  await ctxA.close()
  await ctxB.close()
})
