import { useEffect, useRef } from 'react'
import { openTicketedStream } from '@/lib/event-stream'

// AI 写作助手插件（ai-writer）：写作台中对选中文字续写、改写、润色、扩写、精简，生成大纲/摘要或按自定义要求处理。
// 任务在后台流式生成，经 SSE 推送：先 snapshot（进行中时含已生成的部分与 result_seq），再逐段 delta {seq, text}，结束 done。

export const AI_WRITER_PLUGIN_KEY = 'ai-writer'

export function aiWriterEnabled(site: { feature_plugins?: string[] } | Record<string, unknown>): boolean {
  const list = (site as { feature_plugins?: unknown }).feature_plugins
  return Array.isArray(list) && list.includes(AI_WRITER_PLUGIN_KEY)
}

export type WriterAction = 'continue' | 'rewrite' | 'polish' | 'expand' | 'shorten' | 'outline' | 'summary' | 'custom'

// 动作元数据：wholeChapter 未选中文字时处理整章；replace 结果默认替换原文（否则默认插入）。
export const WRITER_ACTIONS: { key: WriterAction; icon: string; wholeChapter?: boolean; replace?: boolean }[] = [
  { key: 'continue', icon: 'fa-pen-nib' },
  { key: 'polish', icon: 'fa-wand-magic-sparkles', replace: true },
  { key: 'rewrite', icon: 'fa-arrows-rotate', replace: true },
  { key: 'expand', icon: 'fa-up-right-and-down-left-from-center', replace: true },
  { key: 'shorten', icon: 'fa-down-left-and-up-right-to-center', replace: true },
  { key: 'outline', icon: 'fa-list-ol', wholeChapter: true },
  { key: 'summary', icon: 'fa-align-left', wholeChapter: true },
  { key: 'custom', icon: 'fa-sliders', replace: true },
]

export type WriterTaskStatus = 'running' | 'done' | 'failed' | 'canceled'

export interface WriterTask {
  id: number
  book_id: number
  doc_id: number
  action: WriterAction
  instruction: string
  input: string
  result: string
  status: WriterTaskStatus
  error: string
  trace_id: string
  model: string
  input_tokens: number
  output_tokens: number
  estimated: boolean
  duration_ms: number
  adopted: '' | 'replace' | 'insert'
  adopted_at: string | null
  created_at: string
  result_seq?: number
  result_sync?: boolean // 进行中且在其他服务实例上生成：等待 partial 补齐已生成的文本，期间忽略 delta
}

export interface WriterStatus {
  available: boolean
  quota: { limit: number; used: number }
}

// 处理对象在正文中的位置（生成时记录），采纳时据此写回。
export interface WriterTarget {
  start: number
  end: number
  original: string
}

interface DeltaEvent { seq: number; text?: string }

// applyWriterDelta 追加文本片段（快照已包含的忽略；等待 partial 同步期间忽略）。
export function applyWriterDelta(task: WriterTask, ev: DeltaEvent): WriterTask {
  if (task.result_sync || ev.seq <= (task.result_seq ?? 0)) return task
  return { ...task, result: task.result + (ev.text || ''), result_seq: ev.seq }
}

// applyWriterPartial 多实例：生成所在实例推送的完整已生成文本（等待同步时直接采用；否则只采用不旧于当前的）。
export function applyWriterPartial(task: WriterTask, ev: DeltaEvent): WriterTask {
  if (!task.result_sync && ev.seq < (task.result_seq ?? 0)) return task
  return { ...task, result: ev.text || '', result_seq: ev.seq, result_sync: false }
}

// buildRequest 按动作从正文与选区确定处理对象与上下文：
// 续写处理光标前的上文；大纲/摘要未选中时处理整章；其余动作处理选中的文字（附前后文供参考）。
export function buildRequest(action: WriterAction, content: string, start: number, end: number): { text: string; before: string; after: string; target: WriterTarget } {
  if (action === 'continue') {
    return { text: content.slice(0, end), before: '', after: content.slice(end), target: { start: end, end, original: '' } }
  }
  if (start === end && WRITER_ACTIONS.find((a) => a.key === action)?.wholeChapter) {
    return { text: content, before: '', after: '', target: { start: content.length, end: content.length, original: '' } }
  }
  // 选区两端的空白（如三击选中段落带上的换行）不交给模型，替换时保留原有的段落分隔
  while (start < end && /\s/.test(content[start])) start++
  while (end > start && /\s/.test(content[end - 1])) end--
  return { text: content.slice(start, end), before: content.slice(0, start), after: content.slice(end), target: { start, end, original: content.slice(start, end) } }
}

// locateTarget 采纳时重新定位处理对象：正文在生成期间被修改时按原文查找（取离原位置最近的一处）；找不到返回 null。
export function locateTarget(content: string, target: WriterTarget): { start: number; end: number } | null {
  if (!target.original) return target.start <= content.length ? { start: target.start, end: target.start } : null
  if (content.slice(target.start, target.end) === target.original) return { start: target.start, end: target.end }
  let best = -1
  for (let i = content.indexOf(target.original); i !== -1; i = content.indexOf(target.original, i + 1)) {
    if (best === -1 || Math.abs(i - target.start) < Math.abs(best - target.start)) best = i
  }
  return best === -1 ? null : { start: best, end: best + target.original.length }
}

// adoptInto 把结果写回正文：replace 替换处理对象；insert 插入到处理对象之后（作为新的段落）。返回新正文与插入内容的位置。
export function adoptInto(content: string, range: { start: number; end: number }, result: string, mode: 'replace' | 'insert'): { content: string; start: number; end: number } {
  if (mode === 'replace') {
    return { content: content.slice(0, range.start) + result + content.slice(range.end), start: range.start, end: range.start + result.length }
  }
  const head = content.slice(0, range.end)
  const tail = content.slice(range.end)
  const lead = head === '' || head.endsWith('\n\n') ? '' : head.endsWith('\n') ? '\n' : '\n\n'
  const trail = tail === '' || tail.startsWith('\n\n') ? '' : tail.startsWith('\n') ? '\n' : '\n\n'
  const start = head.length + lead.length
  return { content: head + lead + result + trail + tail, start, end: start + result.length }
}

// useWriterTaskStream 订阅进行中任务的实时生成；update 用最新记录替换（或按函数更新），结束后关闭连接。
export function useWriterTaskStream(task: WriterTask | null, update: (next: (t: WriterTask) => WriterTask) => void) {
  const updateRef = useRef(update)
  updateRef.current = update
  const runningId = task && task.status === 'running' ? task.id : 0

  useEffect(() => {
    if (!runningId) return
    return openTicketedStream(`/ai-writer/tasks/${runningId}/stream`, (source, stop) => {
      const parse = <T,>(e: Event): T | null => {
        try { return JSON.parse((e as MessageEvent).data) as T } catch { return null }
      }
      const replace = (e: Event) => {
        const next = parse<WriterTask>(e)
        if (next) updateRef.current((cur) => (cur.id === next.id ? next : cur))
      }
      source.addEventListener('snapshot', replace)
      source.addEventListener('delta', (e) => {
        const ev = parse<DeltaEvent>(e)
        if (ev) updateRef.current((cur) => (cur.id === runningId ? applyWriterDelta(cur, ev) : cur))
      })
      source.addEventListener('partial', (e) => {
        const ev = parse<DeltaEvent>(e)
        if (ev) updateRef.current((cur) => (cur.id === runningId ? applyWriterPartial(cur, ev) : cur))
      })
      source.addEventListener('done', (e) => {
        stop() // 结束后关闭，不再重连
        replace(e)
      })
    })
  }, [runningId])
}
