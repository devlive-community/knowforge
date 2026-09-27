import { expect, test } from '@playwright/test'
import { api, confirmOffline, registerUser, signIn, state } from './helpers'

// 退款：管理员在后台对会员订单全额退款并撤销，用户的会员随即结束。

test('后台全额退款并撤销会员', async ({ page }) => {
  const buyer = await registerUser('refund')
  const created = await api<{ order: { order_no: string } }>('/payment/orders', {
    token: buyer.token, body: { kind: 'membership', sku: String(state().monthlyPriceId), channel: 'offline' },
  })
  const orderNo = created.order.order_no
  await confirmOffline(orderNo)
  const before = await api<{ membership: { active: boolean } | null }>('/users/me/membership', { token: buyer.token })
  expect(before.membership?.active).toBe(true)

  await signIn(page, state().admin)
  await page.goto('/admin/payment')
  const row = page.locator('tr').filter({ hasText: orderNo })
  await row.getByRole('button', { name: '退款', exact: true }).click()
  const dialog = page.getByRole('dialog')
  const revoke = dialog.getByRole('checkbox', { name: '同时撤销已发放的商品' })
  if ((await revoke.getAttribute('aria-checked')) !== 'true') await revoke.click()
  await dialog.getByRole('textbox').last().fill('E2E 测试退款') // 退款原因必填
  await dialog.getByRole('button', { name: '确认退款' }).click()
  await expect(page.getByText('退款成功')).toBeVisible()

  const after = await api<{ membership: { active: boolean } | null }>('/users/me/membership', { token: buyer.token })
  expect(after.membership?.active ?? false).toBe(false)
})
