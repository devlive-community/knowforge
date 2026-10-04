import { describe, expect, it } from 'vitest'
import { buildAnchor, locateAnchor } from '../anchor'

describe('anchor', () => {
  const text = '第一段：苹果很好吃。\n第二段：苹果很好吃，香蕉也不错。\n第三段：完。'

  it('round-trips a selection', () => {
    const start = text.indexOf('苹果很好吃，')
    const anchor = buildAnchor(text, start, start + 5)
    expect(anchor.quote).toBe('苹果很好吃')
    expect(locateAnchor(text, anchor)).toEqual({ start, end: start + 5 })
  })

  it('prefers the occurrence whose context matches after edits move it', () => {
    const start = text.indexOf('苹果很好吃，')
    const anchor = buildAnchor(text, start, start + 5)
    const edited = '新增的开头。\n' + text
    const loc = locateAnchor(edited, anchor)
    expect(loc).toEqual({ start: edited.indexOf('苹果很好吃，'), end: edited.indexOf('苹果很好吃，') + 5 })
  })

  it('falls back to the nearest occurrence when context changed', () => {
    const anchor = { quote: '苹果', prefix: '已删除的上下文', suffix: '也已删除', quote_offset: text.lastIndexOf('苹果') }
    expect(locateAnchor(text, anchor)?.start).toBe(text.lastIndexOf('苹果'))
  })

  it('returns null when the quote no longer exists', () => {
    const start = text.indexOf('香蕉')
    const anchor = buildAnchor(text, start, start + 2)
    expect(locateAnchor(text.replace('香蕉', '橘子'), anchor)).toBeNull()
    expect(locateAnchor(text, { quote: '', prefix: '', suffix: '', quote_offset: 0 })).toBeNull()
  })
})
