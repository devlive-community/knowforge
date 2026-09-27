import { useEffect, useRef } from 'react'
import { getToken } from '@/lib/api'
import { openTicketedStream } from '@/lib/event-stream'
import type { QAAsk, QATraceStep } from '@/lib/qa'

// 进行中问答的实时进度（SSE）：服务端先推 snapshot，之后逐步推 step（调用链）、delta（回答文本片段）、
// reset（本轮以工具调用结束，清空临时文本），结束推 done。连接断开时 EventSource 自动重连并重新获得 snapshot；
// step 按 index、delta/reset 按 seq 去重，保证不重复、不遗漏。

interface StepEvent {
  index: number
  step: QATraceStep
  calls: number
  input_tokens: number
  output_tokens: number
  estimated: boolean
  duration_ms: number
}

// applyStep 把一个 step 事件合并进问答记录（已包含的步骤忽略）。
export function applyStep(ask: QAAsk, ev: StepEvent): QAAsk {
  if (ev.index !== ask.trace.length) return ask
  return {
    ...ask, trace: [...ask.trace, ev.step], calls: ev.calls, input_tokens: ev.input_tokens,
    output_tokens: ev.output_tokens, estimated: ev.estimated, duration_ms: ev.duration_ms,
  }
}

interface DeltaEvent { seq: number; text?: string }

// applyDelta 追加回答文本片段（快照已包含的忽略；等待 partial 同步期间忽略）。
export function applyDelta(ask: QAAsk, ev: DeltaEvent): QAAsk {
  if (ask.answer_sync || ev.seq <= (ask.answer_seq ?? 0)) return ask
  return { ...ask, answer: (ask.answer || '') + (ev.text || ''), answer_seq: ev.seq }
}

// applyReset 本轮以工具调用结束：清空临时文本（快照已是更新的内容时忽略）。
export function applyReset(ask: QAAsk, ev: DeltaEvent): QAAsk {
  if (ask.answer_sync || (ask.answer_seq ?? 0) > ev.seq) return ask
  return { ...ask, answer: '', answer_seq: ev.seq }
}

// applyPartial 多实例：生成所在实例推送的完整部分回答（等待同步时直接采用；否则只采用不旧于当前的）。
export function applyPartial(ask: QAAsk, ev: DeltaEvent): QAAsk {
  if (!ask.answer_sync && ev.seq < (ask.answer_seq ?? 0)) return ask
  return { ...ask, answer: ev.text || '', answer_seq: ev.seq, answer_sync: false }
}

// useAskStreams 为列表中每条进行中的问答订阅进度；update 用最新记录替换（或按函数更新）列表项，onFinish 在某条结束时调用。
export function useAskStreams(
  asks: QAAsk[],
  update: (id: number, next: (ask: QAAsk) => QAAsk) => void,
  onFinish?: (ask: QAAsk) => void,
) {
  const updateRef = useRef(update)
  const finishRef = useRef(onFinish)
  updateRef.current = update
  finishRef.current = onFinish
  const runningKey = asks.filter((a) => a.status === 'running').map((a) => a.id).join(',')

  useEffect(() => {
    if (!runningKey || !getToken()) return
    const stops = runningKey.split(',').map(Number).map((id) => openTicketedStream(`/qa/asks/${id}/stream`, (source, stop) => {
      const parse = <T,>(e: MessageEvent): T | null => {
        try { return JSON.parse(e.data) as T } catch { return null }
      }
      source.addEventListener('snapshot', (e) => {
        const ask = parse<QAAsk>(e as MessageEvent)
        if (ask) updateRef.current(id, () => ask)
      })
      source.addEventListener('step', (e) => {
        const ev = parse<StepEvent>(e as MessageEvent)
        if (ev) updateRef.current(id, (ask) => applyStep(ask, ev))
      })
      source.addEventListener('delta', (e) => {
        const ev = parse<DeltaEvent>(e as MessageEvent)
        if (ev) updateRef.current(id, (ask) => applyDelta(ask, ev))
      })
      source.addEventListener('reset', (e) => {
        const ev = parse<DeltaEvent>(e as MessageEvent)
        if (ev) updateRef.current(id, (ask) => applyReset(ask, ev))
      })
      source.addEventListener('partial', (e) => {
        const ev = parse<DeltaEvent>(e as MessageEvent)
        if (ev) updateRef.current(id, (ask) => applyPartial(ask, ev))
      })
      source.addEventListener('done', (e) => {
        stop() // 结束后关闭，不再重连
        const ask = parse<QAAsk>(e as MessageEvent)
        if (ask) {
          updateRef.current(id, () => ask)
          finishRef.current?.(ask)
        }
      })
    }))
    return () => stops.forEach((stop) => stop())
  }, [runningKey])
}
