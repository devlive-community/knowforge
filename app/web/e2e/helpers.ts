import fs from 'node:fs'
import path from 'node:path'
import type { Page } from '@playwright/test'

// 端到端测试的公共工具：直接调用后端接口准备数据（管理员操作、确认收款等），浏览器中只走被测的用户流程。

export const API = 'http://127.0.0.1:6989'
export const AI_BASE = 'http://127.0.0.1:6990/v1'
export const WEB = 'http://localhost:3100'
const STATE_FILE = path.join(__dirname, '.state.json')

export interface Session { token: string; user: { id: number; username: string } & Record<string, unknown> }
export interface State { admin: Session; planId: number; monthlyPriceId: number; yearlyPriceId: number }

export function saveState(s: State) {
  fs.writeFileSync(STATE_FILE, JSON.stringify(s))
}

export function state(): State {
  return JSON.parse(fs.readFileSync(STATE_FILE, 'utf8')) as State
}

// api 调用后端接口，返回 data；失败时抛出带接口信息的错误。
export async function api<T = Record<string, any>>(p: string, opts: { token?: string; method?: string; body?: unknown } = {}): Promise<T> {
  const res = await fetch(API + '/api/v1' + p, {
    method: opts.method || (opts.body === undefined ? 'GET' : 'POST'),
    headers: { 'Content-Type': 'application/json', ...(opts.token ? { Authorization: `Bearer ${opts.token}` } : {}) },
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
  })
  const payload = await res.json().catch(() => ({}))
  if (!res.ok || payload.success === false) {
    throw new Error(`${opts.method || 'GET'} ${p} → ${res.status} ${JSON.stringify(payload)}`)
  }
  return payload.data as T
}

export const admin = (p: string, opts: { method?: string; body?: unknown } = {}) => api(p, { ...opts, token: state().admin.token })

let seq = 0
// unique 生成本次运行内唯一的名称（用户名、优惠码等）。
export function unique(prefix: string): string {
  seq++
  return `${prefix}${Date.now().toString(36).slice(-5)}${seq}`
}

// registerUser 经注册接口新建用户（站点未开启邮箱激活，注册即视为已验证）。
export async function registerUser(prefix = 'reader', inviteCode = ''): Promise<Session> {
  const username = unique(prefix)
  return api<Session>('/auth/register', { body: { username, email: `${username}@e2e.test`, password: 'Secret123!', invite_code: inviteCode } })
}

// signIn 写入与登录后前端一致的登录态：本地令牌与用户、令牌 Cookie（服务端渲染的页面据此识别登录）。
export async function signIn(page: Page, s: Session) {
  await page.context().addCookies([{ name: 'knowforge_token', value: s.token, url: WEB }])
  await page.addInitScript(({ token, user }) => {
    localStorage.setItem('knowforge_token', token)
    localStorage.setItem('knowforge_user', JSON.stringify(user))
  }, s)
}

// orderNoFromUrl 结算后跳转的订单页地址中的订单号。
export function orderNoFromUrl(url: string): string {
  const m = url.match(/\/pay\/orders\/([^/?#]+)/)
  if (!m) throw new Error('不是订单页: ' + url)
  return m[1]
}

// confirmOffline 管理员确认线下转账到账（触发履约）。
export async function confirmOffline(orderNo: string) {
  await admin(`/admin/payment/orders/${orderNo}/confirm`, { method: 'POST' })
}
