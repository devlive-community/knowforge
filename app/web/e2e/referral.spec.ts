import { expect, test } from '@playwright/test'
import { admin, api, confirmOffline, orderNoFromUrl, registerUser, signIn, state, unique } from './helpers'

// 邀请奖励：好友经邀请链接注册（邀请码自动带入）与首次购买会员后，邀请人与好友各得会员天数。

test('经邀请链接注册并首次购买，双方获得奖励', async ({ page, browser }) => {
  await admin('/admin/membership/referral', {
    method: 'PUT', body: { enabled: true, plan_id: state().planId, inviter_days: 30, invitee_days: 7, signup_days: 3, monthly_limit: 10 },
  })
  const inviter = await registerUser('inviter')
  const code = unique('INV')
  await api('/auth/invite-code', { token: inviter.token, body: { code } })

  // 好友打开邀请链接注册
  await page.goto(`/register?invite=${code}`)
  await expect(page.getByPlaceholder('请输入邀请码')).toHaveValue(code)
  const username = unique('invitee')
  await page.locator('input[autocomplete="username"]').fill(username)
  await page.locator('input[autocomplete="email"]').fill(`${username}@e2e.test`)
  await page.locator('input[autocomplete="new-password"]').nth(0).fill('Secret123!')
  await page.locator('input[autocomplete="new-password"]').nth(1).fill('Secret123!')
  await page.getByRole('button', { name: '创建账户' }).click()
  await page.waitForURL((url) => !url.pathname.startsWith('/register'))

  // 注册奖励：邀请人获得 3 天
  const afterSignup = await api<{ membership: { active: boolean } | null }>('/users/me/membership', { token: inviter.token })
  expect(afterSignup.membership?.active).toBe(true)

  // 好友首次购买会员（沿用注册后的登录态）
  await page.goto('/user/membership')
  await page.getByRole('button', { name: '购买', exact: true }).first().click()
  await page.getByRole('button', { name: /^支付 / }).click()
  await page.waitForURL(/\/pay\/orders\//)
  await confirmOffline(orderNoFromUrl(page.url()))
  await page.goto('/user/membership')
  await expect(page.getByText('邀请奖励', { exact: true }).first()).toBeVisible() // 会员记录中的额外 7 天

  // 邀请人在「我的会员」看到规则、奖励记录与累计天数
  const ctx = await browser.newContext()
  const inviterPage = await ctx.newPage()
  await signIn(inviterPage, inviter)
  await inviterPage.goto('/user/membership')
  await expect(inviterPage.getByRole('heading', { name: '邀请好友得会员' })).toBeVisible()
  await expect(inviterPage.getByText('首次购买', { exact: true })).toBeVisible() // 奖励记录
  await expect(inviterPage.getByText('33', { exact: true })).toBeVisible() // 注册 3 天 + 首次购买 30 天
  await ctx.close()
})
