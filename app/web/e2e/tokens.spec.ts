import { expect, test } from '@playwright/test'
import { API, registerUser, signIn } from './helpers'

// 个人访问令牌：在账号设置中选择权限生成（明文只显示一次），令牌只能调用所选权限的接口，吊销后立即失效。

test('生成访问令牌、调用 API 并吊销', async ({ page }) => {
  const dev = await registerUser('dev')
  await signIn(page, dev)
  await page.goto('/user/tokens')
  await page.getByRole('button', { name: '新建令牌' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByPlaceholder('如：CI 自动发布').fill('CI 发布')
  // 默认自定义权限：未选择时不能生成；勾选「书籍」整组与「章节 · 创建」
  await expect(dialog.getByRole('button', { name: '生成' })).toBeDisabled()
  await dialog.getByRole('checkbox', { name: '书籍', exact: true }).click()
  await dialog.getByRole('checkbox', { name: '章节 · 创建' }).click()
  await expect(dialog.getByText(/已选 7\/\d+ 项/)).toBeVisible()
  await dialog.getByRole('button', { name: '生成' }).click()

  const shown = page.getByRole('dialog').getByRole('textbox')
  await expect(shown).toHaveValue(/^kf_pat_/)
  const token = await shown.inputValue()
  await page.getByRole('button', { name: '我已保存' }).click()
  await expect(page.getByText('CI 发布')).toBeVisible()
  await expect(page.getByText('自定义 · 7 项权限')).toBeVisible()
  await expect(page.getByText('章节 · 创建')).toBeVisible()
  await expect(page.getByText(token.slice(0, 11) + '…')).toBeVisible()

  const me = await fetch(`${API}/api/v1/auth/me`, { headers: { Authorization: `Bearer ${token}` } })
  expect(me.status).toBe(200)
  expect((await me.json()).data.username).toBe(dev.user.username)
  const headers = { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }
  const book = await fetch(`${API}/api/v1/books`, { method: 'POST', headers, body: JSON.stringify({ title: '令牌建的书' }) })
  expect(book.status).toBe(200)
  const bookId = (await book.json()).data.id
  expect((await fetch(`${API}/api/v1/books/${bookId}/documents`, { method: 'POST', headers, body: JSON.stringify({ title: '令牌写的章节' }) })).status).toBe(200)
  const comment = await fetch(`${API}/api/v1/notifications`, { headers })
  expect(comment.status).toBe(403)

  await page.getByRole('button', { name: '吊销' }).click()
  await page.getByRole('dialog').getByRole('button', { name: '吊销' }).click()
  await expect(page.getByText('已吊销', { exact: true })).toBeVisible()
  const after = await fetch(`${API}/api/v1/auth/me`, { headers: { Authorization: `Bearer ${token}` } })
  expect(after.status).toBe(401)
})
