import http from 'node:http'
import type { AddressInfo } from 'node:net'
import { expect, test } from '@playwright/test'
import { registerUser, signIn } from './helpers'

// Webhook：在账号设置中创建订阅（密钥只显示一次），发送测试事件，投递记录显示成功并可查看请求内容。

test('创建 Webhook 并发送测试事件', async ({ page }) => {
  const received: { event: string; signature: string }[] = []
  const server = http.createServer((req, res) => {
    received.push({ event: String(req.headers['x-knowforge-event']), signature: String(req.headers['x-knowforge-signature']) })
    req.resume()
    res.end('received')
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const url = `http://127.0.0.1:${(server.address() as AddressInfo).port}/hook`

  try {
    const owner = await registerUser('hook')
    await signIn(page, owner)
    await page.goto('/user/webhooks')
    await page.getByRole('button', { name: '新建 Webhook' }).click()
    const dialog = page.getByRole('dialog')
    await dialog.getByPlaceholder('https://example.com/knowforge-hook').fill(url)
    await dialog.getByRole('button', { name: '保存' }).click()
    await expect(page.getByRole('dialog').getByRole('textbox')).toHaveValue(/^whsec_/)
    await page.getByRole('button', { name: '我已保存' }).click()
    await expect(page.getByText(url)).toBeVisible()

    await page.getByRole('button', { name: '发送测试' }).click()
    await expect.poll(() => received.length, { timeout: 20_000 }).toBe(1)
    expect(received[0].event).toBe('ping')
    expect(received[0].signature).toMatch(/^sha256=[0-9a-f]{64}$/)

    await page.getByRole('button', { name: '投递记录' }).click()
    const log = page.getByRole('dialog')
    await expect(log.getByText('成功', { exact: true })).toBeVisible()
    await expect(log.getByText(/HTTP 200/)).toBeVisible()
    await log.getByRole('button', { name: '详情' }).click()
    await expect(log.getByText('received')).toBeVisible()
  } finally {
    server.close()
  }
})
