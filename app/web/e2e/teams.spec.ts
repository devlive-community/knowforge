import { expect, test } from '@playwright/test'
import { api, registerUser, signIn, unique } from './helpers'

// 团队空间：创建团队并邀请成员，成员接受邀请后，把私有书籍加入团队，成员即可访问；书籍设置中显示所属团队与来自团队的协作者。

test('创建团队、邀请成员并共享书籍', async ({ browser }) => {
  const owner = await registerUser('teamowner')
  const member = await registerUser('teammember')
  const book = await api<{ id: number; slug: string; title: string }>('/books', { token: owner.token, body: { title: unique('团队私有书 ') } })
  const teamName = unique('写作组 ')

  const ownerCtx = await browser.newContext()
  const op = await ownerCtx.newPage()
  await signIn(op, owner)
  await op.goto('/teams')
  await op.getByTestId('team-create').click()
  await op.getByPlaceholder('如：技术文档组').fill(teamName)
  await op.getByTestId('team-create-submit').click()
  await expect(op.getByTestId('team-name')).toHaveText(teamName)

  // 邀请成员
  await op.getByRole('tab', { name: '成员', exact: true }).click()
  await expect(op.getByRole('tab', { name: '成员', exact: true })).toHaveAttribute('aria-selected', 'true')
  await op.getByPlaceholder('输入要邀请的用户名').fill(member.user.username)
  await op.getByTestId('team-invite').click()
  await expect(op.getByTestId('team-member').filter({ hasText: member.user.username })).toContainText('待接受')

  // 成员接受邀请
  const memberCtx = await browser.newContext()
  const mp = await memberCtx.newPage()
  await signIn(mp, member)
  await mp.goto('/teams')
  await expect(mp.getByTestId('team-invitations')).toContainText(teamName)
  await mp.getByRole('button', { name: '加入', exact: true }).click()
  await expect(mp.getByTestId('team-list')).toContainText(teamName)

  // 所有者把私有书加入团队（成员默认可编辑）
  await op.getByRole('tab', { name: '书籍', exact: true }).click()
  await op.getByTestId('team-add-book').click()
  await op.getByRole('dialog').getByRole('button', { name: '选择一本书' }).click()
  await op.getByRole('option', { name: book.title }).click()
  await op.getByTestId('team-add-book-submit').click()
  await expect(op.getByTestId('team-books')).toContainText(book.title)

  // 成员在团队中看到这本书，并可以访问
  await mp.getByTestId('team-list').getByText(teamName).click()
  await expect(mp.getByTestId('team-books')).toContainText(book.title)
  const detail = await api<{ id: number }>(`/books/${book.id}`, { token: member.token })
  expect(detail.id).toBe(book.id)

  // 书籍设置：显示所属团队，团队成员标记为「来自团队」且不能在这里移除
  await op.goto(`/book/settings/${book.slug}/collaborators`)
  await expect(op.getByTestId('book-team')).toContainText(teamName)
  const row = op.locator('li').filter({ hasText: member.user.username })
  await expect(row).toContainText('来自团队')
  await expect(row.getByRole('button', { name: '移除' })).toHaveCount(0)

  await ownerCtx.close()
  await memberCtx.close()
})
