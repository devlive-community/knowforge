import { describe, expect, it } from 'vitest'
import { applyCaseEvent, type ModerationCaseItem } from '@/lib/moderation'

const item = (id: number, ai_status: '' | 'queued' | 'done') => ({ case: { id, ai_status } }) as unknown as ModerationCaseItem

describe('applyCaseEvent', () => {
  it('用推送替换当前页中的同一条记录', () => {
    const next = applyCaseEvent([item(1, 'queued'), item(2, '')], item(1, 'done'))
    expect(next.map((i) => i.case.ai_status)).toEqual(['done', ''])
  })

  it('不在当前页的记录不加入列表', () => {
    const items = [item(1, '')]
    expect(applyCaseEvent(items, item(9, 'done'))).toBe(items)
  })
})
