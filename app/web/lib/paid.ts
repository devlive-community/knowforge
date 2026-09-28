/** 内容门禁给出的付费墙信息（付费内容插件，见服务端 paidcontent.gateDocument） */
export interface Paywall {
  locked: true
  book_id: number
  doc_id: number
  currency: string
  discount_percent: number
  book_price_cents: number
  book_final_cents: number
  chapter_price_cents: number
  chapter_final_cents: number
  free_tier: number
  free_for: TierGrant[] // 可免费阅读本书的会员方案 / 成长等级
  logged_in: boolean
  upgrade_link: string
}

/** 书籍付费信息与当前读者的访问状态（GET /paid/books/:id） */
export interface PaidBookInfo {
  enabled: boolean
  currency: string
  book_price_cents: number
  book_final_cents: number
  chapter_price_cents: number
  free_chapters: number
  preview_percent: number
  free_tier: number
  free_for: TierGrant[]
  discount_percent: number
  purchased_book: boolean
  can_read_all: boolean
  locked_doc_ids: number[]
  upgrade_link: string
  is_author: boolean
}

type TFn = (key: string, vars?: Record<string, string | number>) => string

/** 某个来源（会员方案、成长等级、基础值）给出的「内容访问等级」（plugincore.EntitlementGrant） */
export interface TierGrant {
  source: string // membership | level | base | 其他插件
  label: string
  rank: number
  value: number // -1 为不限
}

function joinList(locale: string, parts: string[]): string {
  try {
    return new Intl.ListFormat(locale, { style: 'long', type: 'conjunction' }).format(parts)
  } catch {
    return parts.join(', ')
  }
}

// freeReadersText 访问等级达到 tier 的读者：会员方案逐个列出，成长等级取达到的最低一级「及以上」，基础值达到时为所有读者。
export function freeReadersText(t: TFn, locale: string, grants: TierGrant[], tier: number): string {
  const reach = grants.filter((g) => g.value < 0 || g.value >= tier)
  if (reach.some((g) => g.source === 'base')) return t('paid.tier.everyone')
  const parts = reach.filter((g) => g.source === 'membership').map((g) => t('paid.tier.plan', { name: g.label }))
  const levels = reach.filter((g) => g.source === 'level').sort((a, b) => a.rank - b.rank)
  if (levels.length > 0) parts.push(t('paid.tier.level', { name: levels[0].label }))
  parts.push(...reach.filter((g) => !['membership', 'level', 'base'].includes(g.source)).map((g) => g.label))
  return joinList(locale, parts)
}

// tierOptions 「哪些读者免费阅读」的选项：各方案 / 等级给出的访问等级逐档列出；已保存但目前无人达到的值也保留，避免保存时被改掉。
export function tierOptions(t: TFn, locale: string, grants: TierGrant[], current: number): { value: string; label: string }[] {
  const values = Array.from(new Set(grants.filter((g) => g.source !== 'base' && g.value > 0).map((g) => g.value))).sort((a, b) => a - b)
  const options = [{ value: '0', label: t('paid.tier.none') }]
  for (const v of values) options.push({ value: String(v), label: t('paid.tier.option', { who: freeReadersText(t, locale, grants, v) }) })
  if (current > 0 && !values.includes(current)) options.push({ value: String(current), label: t('paid.tier.orphan', { n: current }) })
  return options
}
