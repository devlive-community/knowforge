import { expect, test } from '@playwright/test'
import { API, registerUser, signIn } from './helpers'

// 个人访问令牌：在账号设置中生成（明文只显示一次），用它调用 API，吊销后立即失效。

test('生成访问令牌、调用 API 并吊销', async ({ page }) => {
  const dev = await registerUser('dev')
  await signIn(page, dev)
  await page.goto('/user/tokens')
  await page.getByRole('button', { name: '新建令牌' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByPlaceholder('如：CI 自动发布').fill('CI 发布')
  await dialog.getByRole('button', { name: '生成' }).click()

  const shown = page.getByRole('dialog').getByRole('textbox')
  await expect(shown).toHaveValue(/^kf_pat_/)
  const token = await shown.inputValue()
  await page.getByRole('button', { name: '我已保存' }).click()
  await expect(page.getByText('CI 发布')).toBeVisible()
  await expect(page.getByText(token.slice(0, 11) + '…')).toBeVisible()

  const me = await fetch(`${API}/api/v1/auth/me`, { headers: { Authorization: `Bearer ${token}` } })
  expect(me.status).toBe(200)
  expect((await me.json()).data.username).toBe(dev.user.username)

  await page.getByRole('button', { name: '吊销' }).click()
  await page.getByRole('dialog').getByRole('button', { name: '吊销' }).click()
  await expect(page.getByText('已吊销', { exact: true })).toBeVisible()
  const after = await fetch(`${API}/api/v1/auth/me`, { headers: { Authorization: `Bearer ${token}` } })
  expect(after.status).toBe(401)
})
