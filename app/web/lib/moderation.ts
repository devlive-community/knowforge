import type { UserLite } from '@/components/UserSearchSelect'

/** 一处敏感词命中（字段、行列号、命中片段与上下文） */
export interface ModerationHit {
  field: string
  word: string
  category?: string
  line: number
  column: number
  text: string
  context: string
}

export type ModerationStatus = 'auto_passed' | 'pending' | 'approved' | 'rejected'

export interface ModerationCase {
  id: number
  kind: string // document | book | 插件登记的用户内容类型（如 qa_question）
  target_id: number
  book_id: number
  user_id: number
  title: string
  status: ModerationStatus
  hits: ModerationHit[]
  review_note: string
  reviewed_at?: string | null
  created_at: string
  updated_at: string
  // AI 复核（仅审核员可见）
  ai_status?: '' | 'queued' | 'reviewing' | 'done' | 'failed'
  ai_verdict?: '' | 'safe' | 'violation' | 'uncertain'
  ai_confidence?: number
  ai_categories?: string[] | null
  ai_reason?: string
  ai_error?: string
}

export interface ModerationCaseItem {
  case: ModerationCase
  user?: UserLite
  book?: { id: number; title: string; slug: string }
  doc_slug?: string
  link?: string // 插件登记的用户内容由登记者提供查看链接
}

export const MODERATION_STATUS_TONE: Record<ModerationStatus, 'slate' | 'amber' | 'emerald' | 'rose'> = {
  auto_passed: 'slate', pending: 'amber', approved: 'emerald', rejected: 'rose',
}

export const AI_VERDICT_TONE: Record<string, 'emerald' | 'rose' | 'amber'> = { safe: 'emerald', violation: 'rose', uncertain: 'amber' }

// applyCaseEvent 用推送的最新记录替换列表中的同一条（不在当前页的忽略）。
export function applyCaseEvent(items: ModerationCaseItem[], item: ModerationCaseItem): ModerationCaseItem[] {
  return items.some((it) => it.case.id === item.case.id) ? items.map((it) => (it.case.id === item.case.id ? item : it)) : items
}

// caseLink 审核对象的阅读页链接（章节 → 阅读页，书籍 → 详情页）。
export function caseLink(item: ModerationCaseItem): string | undefined {
  if (item.link) return item.link
  if (!item.book) return undefined
  if (item.case.kind === 'document') return item.doc_slug ? `/book/reader/${encodeURIComponent(item.book.slug)}/${encodeURIComponent(item.doc_slug)}` : undefined
  return `/book/detail/${encodeURIComponent(item.book.slug)}`
}
