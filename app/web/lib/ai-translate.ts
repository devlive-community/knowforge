import { useEffect, useRef } from 'react'
import { openTicketedStream } from '@/lib/event-stream'

// 整本 AI 翻译（书籍多语言插件）：新建译本 / 同步更新在后台逐章进行，进度经 SSE 推送：
// snapshot {job, items, current} → job（任务合计）/ item（章节状态）/ reset、delta（当前章节译文，按 seq 去重）→ done {job, items}。

export const TRANSLATE_LANGUAGES: { code: string; label: string }[] = [
  { code: 'en', label: 'English' },
  { code: 'zh-CN', label: '简体中文' },
  { code: 'zh-TW', label: '繁體中文' },
  { code: 'ja', label: '日本語' },
  { code: 'ko', label: '한국어' },
  { code: 'fr', label: 'Français' },
  { code: 'de', label: 'Deutsch' },
  { code: 'es', label: 'Español' },
  { code: 'pt', label: 'Português' },
  { code: 'it', label: 'Italiano' },
  { code: 'ru', label: 'Русский' },
  { code: 'vi', label: 'Tiếng Việt' },
  { code: 'th', label: 'ไทย' },
  { code: 'ar', label: 'العربية' },
]

export type TranslateJobStatus = 'running' | 'paused' | 'done' | 'failed'
export type TranslateItemStatus = 'pending' | 'running' | 'done' | 'failed'

export interface TranslateJob {
  id: number
  source_book_id: number
  target_book_id: number
  target_lang: string
  target_label: string
  mode: 'full' | 'sync'
  stage: 'outline' | 'content' | 'done'
  status: TranslateJobStatus
  instructions: string
  total: number
  done: number
  failed: number
  chars: number
  input_tokens: number
  output_tokens: number
  estimated: boolean
  trace_id: string
  error: string
  finished_at: string | null
  created_at: string
}

export interface TranslateItem {
  id: number
  job_id: number
  source_doc_id: number
  target_doc_id: number
  ord: number
  depth: number
  title: string
  target_title: string
  status: TranslateItemStatus
  chars: number
  input_tokens: number
  output_tokens: number
  duration_ms: number
  error: string
}

export interface CurrentText { item_id: number; seq: number; text: string }

export interface JobState {
  job: TranslateJob
  items: TranslateItem[]
  current?: CurrentText | null
  current_sync?: boolean // 进行中且在其他服务实例上执行：等待 partial 补齐当前译文，期间忽略 reset/delta
}

export interface TranslateTarget {
  book: { id: number; slug: string; title: string; language: string; status: string }
  target_lang: string
  target_label: string
  changed: number
  added: number
  chars: number
  last_job: TranslateJob | null
}

export interface TranslateOverview {
  available: boolean
  allowed: boolean
  chars_left: number
  source: { chapters: number; chars: number; language: string }
  targets: TranslateTarget[]
  jobs: TranslateJob[]
}

export interface GlossaryTerm { source: string; target: string }

// —— 事件合并（纯函数，便于测试）——

export function applyItem(state: JobState, item: TranslateItem): JobState {
  return { ...state, items: state.items.map((it) => (it.id === item.id ? item : it)) }
}

export function applyReset(state: JobState, ev: { item_id: number; seq: number }): JobState {
  if (state.current_sync || ev.seq <= (state.current?.seq ?? 0)) return state
  return { ...state, current: { item_id: ev.item_id, seq: ev.seq, text: '' } }
}

export function applyDelta(state: JobState, ev: { item_id: number; seq: number; text?: string }): JobState {
  const cur = state.current
  if (state.current_sync || ev.seq <= (cur?.seq ?? 0)) return state
  const text = cur && cur.item_id === ev.item_id ? cur.text : ''
  return { ...state, current: { item_id: ev.item_id, seq: ev.seq, text: text + (ev.text || '') } }
}

// applyPartial 多实例：执行所在实例推送的当前章节完整译文（等待同步时直接采用；否则只采用不旧于当前的）。
export function applyPartial(state: JobState, ev: { item_id: number; seq: number; text?: string }): JobState {
  if (!state.current_sync && ev.seq < (state.current?.seq ?? 0)) return state
  return { ...state, current: { item_id: ev.item_id, seq: ev.seq, text: ev.text || '' }, current_sync: false }
}

// progressPercent 进度百分比（已完成 + 失败 / 总数）。
export function progressPercent(job: Pick<TranslateJob, 'total' | 'done' | 'failed'>): number {
  if (!job.total) return 0
  return Math.min(100, Math.round(((job.done + job.failed) / job.total) * 100))
}

// useTranslateJobStream 订阅进行中任务的进度；update 按函数更新状态，onFinish 在任务结束或暂停时调用。
export function useTranslateJobStream(state: JobState | null, update: (next: (s: JobState) => JobState) => void, onFinish?: () => void) {
  const updateRef = useRef(update)
  const finishRef = useRef(onFinish)
  updateRef.current = update
  finishRef.current = onFinish
  const runningId = state && state.job.status === 'running' ? state.job.id : 0

  useEffect(() => {
    if (!runningId) return
    return openTicketedStream(`/ai-translate/jobs/${runningId}/stream`, (source, stop) => {
      const parse = <T,>(e: Event): T | null => {
        try { return JSON.parse((e as MessageEvent).data) as T } catch { return null }
      }
      source.addEventListener('snapshot', (e) => {
        const snap = parse<JobState>(e)
        if (snap) updateRef.current(() => snap)
      })
      source.addEventListener('job', (e) => {
        const job = parse<TranslateJob>(e)
        if (job) updateRef.current((s) => ({ ...s, job }))
      })
      source.addEventListener('item', (e) => {
        const item = parse<TranslateItem>(e)
        if (item) updateRef.current((s) => applyItem(s, item))
      })
      source.addEventListener('reset', (e) => {
        const ev = parse<{ item_id: number; seq: number }>(e)
        if (ev) updateRef.current((s) => applyReset(s, ev))
      })
      source.addEventListener('delta', (e) => {
        const ev = parse<{ item_id: number; seq: number; text?: string }>(e)
        if (ev) updateRef.current((s) => applyDelta(s, ev))
      })
      source.addEventListener('partial', (e) => {
        const ev = parse<{ item_id: number; seq: number; text?: string }>(e)
        if (ev) updateRef.current((s) => applyPartial(s, ev))
      })
      source.addEventListener('done', (e) => {
        stop()
        const final = parse<JobState>(e)
        if (final) updateRef.current(() => ({ ...final, current: null }))
        finishRef.current?.()
      })
    })
  }, [runningId])
}

// useJobListStreams 任务列表中每个进行中的任务订阅进度（与详情页同一事件流），update 用最新的任务合计替换列表项，onFinish 在某个任务结束或暂停时调用。
export function useJobListStreams(jobs: Pick<TranslateJob, 'id' | 'status'>[], update: (job: TranslateJob) => void, onFinish?: () => void) {
  const updateRef = useRef(update)
  const finishRef = useRef(onFinish)
  updateRef.current = update
  finishRef.current = onFinish
  const runningKey = jobs.filter((j) => j.status === 'running').map((j) => j.id).join(',')

  useEffect(() => {
    if (!runningKey) return
    const stops = runningKey.split(',').map(Number).map((id) => openTicketedStream(`/ai-translate/jobs/${id}/stream`, (source, stop) => {
      const parse = <T,>(e: Event): T | null => {
        try { return JSON.parse((e as MessageEvent).data) as T } catch { return null }
      }
      source.addEventListener('snapshot', (e) => {
        const snap = parse<JobState>(e)
        if (snap) updateRef.current(snap.job)
      })
      source.addEventListener('job', (e) => {
        const job = parse<TranslateJob>(e)
        if (job) updateRef.current(job)
      })
      source.addEventListener('done', (e) => {
        stop()
        const final = parse<JobState>(e)
        if (final) updateRef.current(final.job)
        finishRef.current?.()
      })
    }))
    return () => stops.forEach((stop) => stop())
  }, [runningKey])
}
