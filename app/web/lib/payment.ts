/** 支付订单（与服务端 payment.Order 一致） */
export interface PaymentOrder {
  id: number
  order_no: string
  user_id: number
  kind: string
  sku: string
  title: string
  duration_days: number
  amount_cents: number // 实付金额（优惠后）
  original_cents: number // 商品原价
  discount_cents: number // 优惠金额
  coupon_code: string
  currency: string
  return_link: string
  channel: PaymentChannel
  status: 'pending' | 'paid' | 'cancelled' | 'expired' | 'refunded'
  channel_trade_no: string
  payer_note: string
  proof_at?: string | null
  expires_at: string
  paid_at?: string | null
  fulfilled_at?: string | null
  fulfill_error: string
  refunded_cents: number // 已成功退款金额（部分退款的订单仍为 paid）
  created_at: string
}

/** 退款单（与服务端 payment.Refund 一致） */
export interface PaymentRefund {
  id: number
  refund_no: string
  order_id: number
  order_no: string
  user_id: number
  amount_cents: number
  currency: string
  channel: PaymentChannel
  reason: string
  admin_note: string
  revoke: boolean
  status: 'requested' | 'rejected' | 'pending' | 'processing' | 'succeeded' | 'failed'
  channel_refund_id: string
  error: string
  succeeded_at?: string | null
  settled_at?: string | null
  settle_error: string
  created_at: string
}

export const REFUND_STATUS_TONE: Record<PaymentRefund['status'], 'amber' | 'emerald' | 'slate' | 'rose' | 'sky'> = {
  requested: 'amber', rejected: 'slate', pending: 'sky', processing: 'sky', succeeded: 'emerald', failed: 'rose',
}

// toCents 金额输入（主单位，如 9.90）→ 最小货币单位（与服务端一致统一按 1/100 存储）；非法返回 NaN。
export function toCents(input: string): number {
  const v = input.trim()
  if (!/^\d+(\.\d{1,2})?$/.test(v)) return NaN
  return Math.round(parseFloat(v) * 100)
}

export type PaymentChannel = 'offline' | 'alipay' | 'wechat' | 'stripe'

/** 下单/继续支付时前端要执行的动作 */
export interface PaymentAction {
  type: 'redirect' | 'qrcode' | 'offline'
  url?: string
  qr?: string
  instructions?: string
  qr_image?: string
}

/** 结算时优惠码的试算结果（plugincore.Discount） */
export interface PaymentDiscount {
  code: string
  label: string
  amount_off_cents: number
}

/** 可售商品（plugincore.Product） */
export interface PaymentProduct {
  kind: string
  sku: string
  title: string
  description?: string
  duration_days?: number
  amount_cents: number
  currency: string
  return_link?: string
}

export const CHANNEL_ICONS: Record<PaymentChannel, string> = {
  offline: 'fa-solid fa-building-columns',
  alipay: 'fa-brands fa-alipay',
  wechat: 'fa-brands fa-weixin',
  stripe: 'fa-brands fa-stripe',
}

export const STATUS_TONE: Record<PaymentOrder['status'], 'amber' | 'emerald' | 'slate' | 'rose'> = {
  pending: 'amber', paid: 'emerald', cancelled: 'slate', expired: 'slate', refunded: 'slate',
}

// isMobileDevice 移动端浏览器（支付宝走手机网站支付）。
export function isMobileDevice(): boolean {
  return typeof navigator !== 'undefined' && /Mobi|Android|iPhone|iPad/i.test(navigator.userAgent)
}
