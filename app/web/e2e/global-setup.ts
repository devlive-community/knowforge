import { AI_BASE, api, saveState, type Session } from './helpers'

// 全局初始化：经安装向导安装站点，启用会员、支付、付费内容与问答插件，配置线下转账与假 AI 服务，创建会员方案。
export default async function globalSetup() {
  const installed = await api<Session>('/setup/install', {
    body: { database: { type: 'sqlite' }, site: { name: 'E2E 站点' }, admin: { username: 'admin', email: 'admin@e2e.test', password: 'Secret123!' } },
  })
  const token = installed.token
  const as = (p: string, method: string, body?: unknown) => api(p, { token, method, body })
  for (const key of ['payment', 'membership', 'paid-content', 'qa', 'webhooks', 'feeds', 'embed']) {
    await as(`/admin/plugins/${key}/install`, 'POST')
  }
  // 测试会注册很多用户：只放宽注册限流（按 IP，默认每小时 5 次），其余限流保持默认
  await as('/rate-limits', 'PUT', { policies: [{ name: 'auth-register', limit: 1000, window_seconds: 3600 }] })
  await as('/admin/payment/settings', 'PUT', { offline_enabled: true, offline_instructions: 'E2E 转账说明' })
  await as('/admin/ai', 'PUT', { provider: 'openai', base_url: AI_BASE, api_key: 'e2e', model: 'fake', embed_model: 'fake-embed' })
  const plan = await as('/admin/membership/plans', 'POST', {
    name: '专业版', trial_days: 7,
    prices: [{ duration_days: 30, price_cents: 1900 }, { duration_days: 365, price_cents: 16800 }],
  }) as { id: number; prices: { id: number; duration_days: number }[] }
  const monthly = plan.prices.find((x) => x.duration_days === 30)!
  const yearly = plan.prices.find((x) => x.duration_days === 365)!
  saveState({ admin: installed, planId: plan.id, monthlyPriceId: monthly.id, yearlyPriceId: yearly.id })
}
