import { expect, test } from '@playwright/test'
import { admin, api, confirmOffline, orderNoFromUrl, registerUser, signIn, unique } from './helpers'

// 团队会员：团队所有者在「团队会员」按席位开通（线下转账确认后生效），团队成员在「我的会员」看到通过团队享有的会员。

test('为团队按席位开通会员', async ({ page, browser }) => {
  const planName = unique('团队版 ')
  await admin('/admin/membership/plans', { body: { name: planName, group_enabled: true, sort_order: 99, prices: [{ duration_days: 30, price_cents: 1000 }] } })
  const owner = await registerUser('tmowner')
  const member = await registerUser('tmmember')
  const team = await api<{ id: number; slug: string; name: string }>('/teams', { token: owner.token, body: { name: unique('付费团队 ') } })
  const invite = await api<{ id: number }>(`/teams/${team.id}/members`, { token: owner.token, body: { username: member.user.username } })
  await api(`/team-invitations/${invite.id}/accept`, { token: member.token, method: 'POST' })

  await signIn(page, owner)
  await page.goto(`/teams/${team.slug}`)
  await page.getByTestId('team-membership-link').click()
  await expect(page).toHaveURL(/\/user\/membership\/teams/)
  const card = page.getByTestId('group-membership').filter({ hasText: team.name })
  await expect(card).toContainText('未开通')
  await expect(card.getByText('合计 ¥20.00')).toBeVisible() // 2 位成员 × ¥10.00
  await card.getByTestId('group-buy').click()
  await expect(page).toHaveURL(/\/pay\/checkout\?kind=membership_group/)
  await expect(page.getByText(`（2 席）`).first()).toBeVisible()
  await page.getByRole('button', { name: /^支付 / }).click()
  await page.waitForURL(/\/pay\/orders\//)
  await confirmOffline(orderNoFromUrl(page.url()))

  await page.goto('/user/membership/teams')
  await expect(card.getByTestId('group-subscription')).toContainText('2 席')
  await expect(card.getByTestId('group-add-seats')).toBeVisible()

  // 成员在「我的会员」看到通过团队享有的会员
  const ctx = await browser.newContext()
  const mp = await ctx.newPage()
  await signIn(mp, member)
  await mp.goto('/user/membership')
  await expect(mp.getByTestId('group-coverage')).toContainText(`你通过团队「${team.name}」享有「${planName}」会员`)
  await ctx.close()
})
