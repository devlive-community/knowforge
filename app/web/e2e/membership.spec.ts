import { expect, test } from '@playwright/test'
import { admin, confirmOffline, orderNoFromUrl, registerUser, signIn, state, unique } from './helpers'

// 会员：在「我的会员」购买并使用优惠码；购买礼品卡送给他人兑换；领取免费试用。

test('购买会员并使用优惠码', async ({ page }) => {
  const code = unique('E2E')
  await admin('/admin/membership/coupons', { body: { name: 'E2E 八折', code, type: 'percent', percent_off: 20 } })
  const buyer = await registerUser('buyer')
  await signIn(page, buyer)

  await page.goto('/user/membership')
  await page.getByRole('button', { name: '购买', exact: true }).first().click() // 1 个月 ¥19.00
  await expect(page).toHaveURL(/\/pay\/checkout\?kind=membership/)
  await expect(page.getByText('¥19.00').first()).toBeVisible()

  await page.getByPlaceholder('输入优惠码').fill(code.toLowerCase())
  await page.getByRole('button', { name: '使用', exact: true }).click()
  await expect(page.getByText('E2E 八折')).toBeVisible()
  const pay = page.getByRole('button', { name: '支付 ¥15.20' })
  await expect(pay).toBeVisible()
  await pay.click()

  await page.waitForURL(/\/pay\/orders\//)
  await expect(page.getByText(`优惠码 ${code} 已减 ¥3.80`)).toBeVisible()
  await confirmOffline(orderNoFromUrl(page.url()))

  await page.goto('/user/membership')
  await expect(page.getByText('有效', { exact: true }).first()).toBeVisible()
  await expect(page.getByRole('button', { name: '续期', exact: true }).first()).toBeVisible()
})

test('礼品卡：购买后送给他人兑换', async ({ page, browser }) => {
  const giver = await registerUser('giver')
  await signIn(page, giver)
  await page.goto('/user/membership')
  await page.getByRole('button', { name: '送人', exact: true }).first().click()
  await expect(page.getByText('专业版（礼品卡）')).toBeVisible()
  await page.getByRole('button', { name: /^支付 / }).click()
  await page.waitForURL(/\/pay\/orders\//)
  await confirmOffline(orderNoFromUrl(page.url()))

  await page.goto('/user/membership')
  await expect(page.getByRole('heading', { name: '我的礼品卡' })).toBeVisible()
  const code = (await page.locator('code').filter({ hasText: /^[A-Z0-9]{4}-/ }).first().textContent())!.trim()
  expect(code).toMatch(/^[A-Z0-9]{4}(-[A-Z0-9]{4}){3}$/)

  // 朋友在自己的「我的会员」兑换
  const friend = await registerUser('friend')
  const ctx = await browser.newContext()
  const friendPage = await ctx.newPage()
  await signIn(friendPage, friend)
  await friendPage.goto('/user/membership')
  await friendPage.getByPlaceholder('XXXX-XXXX-XXXX-XXXX').fill(code.replace(/-/g, '').toLowerCase())
  await friendPage.getByRole('button', { name: '兑换', exact: true }).click()
  await expect(friendPage.getByText(/兑换成功：「专业版」会员有效期至/)).toBeVisible()
  await expect(friendPage.getByText('有效', { exact: true }).first()).toBeVisible()
  await ctx.close()

  // 购买者看到礼品卡已被兑换
  await page.reload()
  await expect(page.getByText('已兑换', { exact: true })).toBeVisible()
})

test('领取免费试用', async ({ page }) => {
  const newbie = await registerUser('trial')
  await signIn(page, newbie)
  await page.goto('/user/membership')
  await expect(page.getByText('免费试用 7 天')).toBeVisible()
  await page.getByRole('button', { name: '开始试用' }).click()
  await page.getByRole('dialog').getByRole('button', { name: '开始试用' }).click()
  await expect(page.getByText(/已开始「专业版」试用/)).toBeVisible()
  await expect(page.getByText('试用', { exact: true }).first()).toBeVisible()
  await expect(page.getByRole('button', { name: '开始试用' })).toHaveCount(0)
  expect(state().planId).toBeGreaterThan(0)
})
