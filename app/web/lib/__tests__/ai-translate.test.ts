import { describe, expect, it } from 'vitest'
import { applyDelta, applyItem, applyPartial, applyReset, progressPercent, type JobState, type TranslateItem, type TranslateJob } from '@/lib/ai-translate'

const item = (id: number, status: TranslateItem['status']) => ({ id, status, title: `第${id}章` }) as TranslateItem
const base: JobState = { job: { id: 1, total: 4, done: 1, failed: 0 } as TranslateJob, items: [item(1, 'done'), item(2, 'running')], current: { item_id: 2, seq: 5, text: 'Hel' } }

describe('ai-translate 事件合并', () => {
  it('item 按 ID 替换章节状态', () => {
    const next = applyItem(base, item(2, 'done'))
    expect(next.items.map((i) => i.status)).toEqual(['done', 'done'])
  })

  it('delta 追加当前章节译文，快照已包含的片段忽略', () => {
    expect(applyDelta(base, { item_id: 2, seq: 6, text: 'lo' }).current).toEqual({ item_id: 2, seq: 6, text: 'Hello' })
    expect(applyDelta(base, { item_id: 2, seq: 5, text: 'lo' })).toBe(base)
  })

  it('reset 开始新章节时清空临时译文', () => {
    const next = applyReset(base, { item_id: 3, seq: 7 })
    expect(next.current).toEqual({ item_id: 3, seq: 7, text: '' })
    expect(applyReset(base, { item_id: 3, seq: 4 })).toBe(base)
  })

  it('换章后的 delta 不拼接上一章的文字', () => {
    expect(applyDelta(base, { item_id: 3, seq: 8, text: 'New' }).current).toEqual({ item_id: 3, seq: 8, text: 'New' })
  })

  it('快照中当前章节尚无译文（text 缺省）时，delta 不会拼出 undefined', () => {
    const snap = { ...base, current: { item_id: 2, seq: 5 } as JobState['current'] }
    expect(applyDelta(snap, { item_id: 2, seq: 6, text: '## 优化' }).current?.text).toBe('## 优化')
  })

  it('进度按已完成与失败计算', () => {
    expect(progressPercent({ total: 4, done: 1, failed: 1 })).toBe(50)
    expect(progressPercent({ total: 0, done: 0, failed: 0 })).toBe(0)
  })
})

describe('multi-instance partial sync', () => {
  it('ignores deltas while waiting for partial, then adopts it', () => {
    const job = { id: 1 } as TranslateJob
    const waiting: JobState = { job, items: [], current: null, current_sync: true }
    expect(applyDelta(waiting, { item_id: 2, seq: 3, text: 'x' })).toBe(waiting)
    expect(applyReset(waiting, { item_id: 2, seq: 4 })).toBe(waiting)
    const synced = applyPartial(waiting, { item_id: 2, seq: 5, text: 'Hello' })
    expect(synced.current).toEqual({ item_id: 2, seq: 5, text: 'Hello' })
    expect(synced.current_sync).toBe(false)
    expect(applyDelta(synced, { item_id: 2, seq: 6, text: '!' }).current?.text).toBe('Hello!')
    expect(applyPartial({ ...synced, current: { item_id: 2, seq: 9, text: 'newer' } }, { item_id: 2, seq: 5, text: 'Hello' }).current?.text).toBe('newer')
  })
})
