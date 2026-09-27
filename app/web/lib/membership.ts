import type { ResourceTranslations } from '@/components/LocalizedFields'

/** 会员方案的一档时长价格（金额为最小货币单位，如分） */
export interface MembershipPrice {
  id: number
  plan_id: number
  duration_days: number
  price_cents: number
  original_price_cents: number
}

export interface MembershipPlan {
  id: number
  name: string
  description: string
  icon_type?: string
  icon_value?: string
  color?: string
  entitlements?: Record<string, number> | null
  status: 'active' | 'archived'
  sort_order: number
  trial_days: number // 免费试用天数（0 为不提供）
  prices: MembershipPrice[]
  translations?: ResourceTranslations
}

export interface MembershipRecord {
  id: number
  user_id: number
  plan_id: number
  plan_name: string
  action: 'grant' | 'extend' | 'switch' | 'adjust' | 'revoke'
  days: number
  prev_expires_at?: string | null
  expires_at?: string | null
  source: string
  source_ref?: string
  reason?: string
  created_at: string
}

/** 我的会员（含最近一次已到期的） */
export interface MyMembership {
  plan: MembershipPlan | null
  started_at: string
  expires_at: string
  active: boolean
  days_left: number
  trial: boolean // 试用中
}

/** 我能否领取免费试用 */
export interface TrialInfo {
  eligible: boolean
  needs_verified_email: boolean
}

/** 我购买的礼品卡（status：unused 未兑换 | redeemed 已兑换 | void 已作废，作废时不返回兑换码） */
export interface MembershipGift {
  id: number
  code: string
  plan_name: string
  days: number
  order_no: string
  status: 'unused' | 'redeemed' | 'void'
  redeemed_at?: string
  redeemed_by_me: boolean
  created_at: string
}

// centsFromInput / inputFromCents 价格输入框（货币单位，两位小数）与分之间的换算。
export function centsFromInput(value: string): number {
  const n = Number(value)
  return Number.isFinite(n) ? Math.round(n * 100) : 0
}
export function inputFromCents(cents: number): string {
  return cents ? (cents / 100).toFixed(2).replace(/\.00$/, '') : ''
}
