import { expect, test } from '@playwright/test'
import { api, registerUser, signIn } from './helpers'

// 邮件摘要：在通知设置中把邮件发送方式改为每日摘要并关闭一类通知，首次保存即生效；
// 一键退订页校验签名，链接不完整或被篡改时提示失败。

test('通知设置中选择每日摘要', async ({ page }) => {
  const reader = await registerUser('digest')
  await signIn(page, reader)
  await page.goto('/user/notify')
  const mode = page.getByTestId('digest-mode')
  await mode.getByRole('button', { name: '每条即时发送' }).click()
  await page.getByRole('option', { name: '每日摘要' }).click()
  await expect(mode.getByRole('button', { name: '每日摘要' })).toBeVisible()
  await page.getByRole('switch', { name: '点赞与收藏' }).click()
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByText('通知设置已保存')).toBeVisible()

  const d = await api<{ prefs: { digest_mode: string; reaction: boolean; comment: boolean } }>('/auth/notification-prefs', { token: reader.token })
  expect(d.prefs).toMatchObject({ digest_mode: 'daily', reaction: false, comment: true })

  // 不发送邮件时逐类型开关不可用
  await mode.getByRole('button', { name: '每日摘要' }).click()
  await page.getByRole('option', { name: '不发送邮件' }).click()
  await expect(page.getByRole('switch', { name: '点赞与收藏' })).toBeDisabled()
})

test('退订链接无效时提示失败', async ({ page }) => {
  const reader = await registerUser('unsub')
  await page.goto(`/email/unsubscribe?u=${reader.user.id}&token=forged`)
  await expect(page.getByTestId('unsubscribe-result')).toContainText('退订失败')
  await page.goto('/email/unsubscribe')
  await expect(page.getByTestId('unsubscribe-result')).toContainText('退订链接不完整')
  const d = await api<{ prefs: { digest_mode: string } }>('/auth/notification-prefs', { token: reader.token })
  expect(d.prefs.digest_mode).toBe('instant')
})
