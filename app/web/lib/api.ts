import { clearOfflinePages } from './pwa'
// API 客户端：默认同源访问（单二进制部署），开发模式可通过
// NEXT_PUBLIC_API_BASE 指向独立的 Go 服务，例如 http://localhost:6969
export const API_BASE: string = process.env.NEXT_PUBLIC_API_BASE || ''

const TOKEN_KEY = 'knowforge_token'
const USER_KEY = 'knowforge_user'

export function getToken(): string | null {
  if (typeof window === 'undefined') return null
  return localStorage.getItem(TOKEN_KEY)
}

export function getStoredUser<T = unknown>(): T | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = localStorage.getItem(USER_KEY)
    return raw ? (JSON.parse(raw) as T) : null
  } catch {
    return null
  }
}

export function storeSession(token: string, user: unknown): void {
  localStorage.setItem(TOKEN_KEY, token)
  localStorage.setItem(USER_KEY, JSON.stringify(user))
  void clearOfflinePages() // 离线缓存的页面属于上一位登录者
}

export function clearSession(): void {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(USER_KEY)
  void clearOfflinePages()
}

export type QueryParams = Record<string, string | number | boolean | undefined | null>

// 二次认证 step-up：当接口返回 TWO_FACTOR_REQUIRED 时，全局处理器弹出验证框；
// 用户验证通过（服务端授予 5 分钟窗口）后原请求自动重试一次。
let stepUpHandler: (() => Promise<boolean>) | null = null
export function setStepUpHandler(handler: (() => Promise<boolean>) | null): void {
  stepUpHandler = handler
}

// runStepUp 交给全局处理器完成二次认证（未设置处理器时返回 false），供非 JSON 响应的请求（如事件流）复用。
export async function runStepUp(): Promise<boolean> {
  return stepUpHandler ? stepUpHandler() : false
}

// requestHeaders 与 api() 一致的请求头：界面语言、登录令牌，有请求体时带 JSON 类型。
export function requestHeaders(withBody: boolean, token?: string | null): Record<string, string> {
  const headers: Record<string, string> = {}
  if (typeof document !== 'undefined') {
    const locale = document.cookie.split(';').map((part) => part.trim()).find((part) => part.startsWith('knowforge_locale='))?.split('=')[1]
    if (locale) { try { headers['X-KnowForge-Locale'] = decodeURIComponent(locale) } catch { /* invalid cookie */ } }
  }
  if (withBody) headers['Content-Type'] = 'application/json'
  const t = token ?? getToken()
  if (t) headers.Authorization = `Bearer ${t}`
  return headers
}

export async function api<T = any>(
  path: string,
  { method = 'GET', body, params, token }: {
    method?: string
    body?: unknown
    params?: QueryParams
    token?: string | null
  } = {},
): Promise<T> {
  const query = params
    ? '?' + new URLSearchParams(
        Object.entries(params)
          .filter(([, v]) => v !== undefined && v !== null && v !== '')
          .map(([k, v]) => [k, String(v)]),
      )
    : ''
  const headers = requestHeaders(body !== undefined, token)

  const run = async () => {
    const res = await fetch(`${API_BASE}/api/v1${path}${query}`, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
    const payload = await res.json().catch(() => ({}))
    return { res, payload }
  }

  let { res, payload } = await run()

  // 需要二次认证：交给全局处理器验证后重试一次
  if (res.status === 403 && payload?.code === 'TWO_FACTOR_REQUIRED' && stepUpHandler) {
    const verified = await stepUpHandler()
    if (verified) ({ res, payload } = await run())
  }

  if (!res.ok || payload.success === false) {
    const err = new Error(payload.message || `请求失败 (${res.status})`) as Error & { status?: number; code?: string }
    err.status = res.status
    err.code = payload.code
    throw err
  }
  return payload.data as T
}

export function formatDate(input: string | null | undefined): string {
  if (!input) return '-'
  const d = new Date(input)
  if (Number.isNaN(d.getTime())) return input
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export function formatNumber(n: number | null | undefined): string {
  if (n === undefined || n === null) return '0'
  if (n >= 10000) return (n / 10000).toFixed(1) + 'w'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'k'
  return String(n)
}
