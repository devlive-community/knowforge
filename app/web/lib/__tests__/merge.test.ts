import { describe, expect, it } from 'vitest'
import { applyHunks, diffHunks, matchLines, merge3, mergeTitle, resolveMerge } from '../merge'

describe('matchLines', () => {
  it('finds the longest common subsequence of lines', () => {
    const a = ['a', 'b', 'c', 'd', 'e']
    const b = ['a', 'x', 'c', 'd', 'y', 'e']
    const pairs = matchLines(a, b)
    expect(pairs).toEqual([[0, 0], [2, 2], [3, 3], [4, 5]])
  })

  it('handles empty inputs', () => {
    expect(matchLines([], [])).toEqual([])
    expect(matchLines(['a'], [])).toEqual([])
    expect(matchLines([], ['a'])).toEqual([])
  })

  it('handles large inputs with a few edits', () => {
    const a = Array.from({ length: 5000 }, (_, i) => `line ${i}`)
    const b = [...a]
    b[10] = 'changed'
    b.splice(4000, 0, 'inserted')
    const pairs = matchLines(a, b)
    expect(pairs.length).toBe(4999)
  })
})

describe('merge3', () => {
  const base = ['# Title', '', 'one', 'two', 'three', 'four', 'five'].join('\n')

  it('merges edits in different places', () => {
    const mine = base.replace('two', 'TWO')
    const theirs = base.replace('five', 'FIVE')
    const r = merge3(base, mine, theirs)
    expect(r.conflicts).toBe(0)
    expect(resolveMerge(r)).toBe(base.replace('two', 'TWO').replace('five', 'FIVE'))
  })

  it('merges insertions and deletions on both sides', () => {
    const mine = ['# Title', '', 'zero', 'one', 'two', 'three', 'four', 'five'].join('\n')
    const theirs = ['# Title', '', 'one', 'two', 'four', 'five', 'six'].join('\n')
    const r = merge3(base, mine, theirs)
    expect(r.conflicts).toBe(0)
    expect(resolveMerge(r)).toBe(['# Title', '', 'zero', 'one', 'two', 'four', 'five', 'six'].join('\n'))
  })

  it('takes identical changes once', () => {
    const changed = base.replace('three', 'THREE')
    const r = merge3(base, changed, changed)
    expect(r.conflicts).toBe(0)
    expect(resolveMerge(r)).toBe(changed)
  })

  it('reports a conflict when both change the same line differently', () => {
    const mine = base.replace('three', 'mine three')
    const theirs = base.replace('three', 'their three')
    const r = merge3(base, mine, theirs)
    expect(r.conflicts).toBe(1)
    const conflict = r.chunks.find((c) => c.type === 'conflict')
    expect(conflict).toEqual({ type: 'conflict', base: ['three'], mine: ['mine three'], theirs: ['their three'] })
    expect(resolveMerge(r, ['theirs'])).toBe(theirs)
    expect(resolveMerge(r, ['mine'])).toBe(mine)
    expect(resolveMerge(r, ['both'])).toBe(base.replace('three', 'mine three\ntheir three'))
  })

  it('keeps unrelated edits around a conflict', () => {
    const mine = base.replace('one', 'ONE').replace('four', 'mine four')
    const theirs = base.replace('four', 'their four').replace('five', 'FIVE')
    const r = merge3(base, mine, theirs)
    expect(r.conflicts).toBe(1)
    expect(resolveMerge(r, ['theirs'])).toBe(base.replace('one', 'ONE').replace('four', 'their four').replace('five', 'FIVE'))
  })

  it('handles appends at the end on both sides as a conflict', () => {
    const r = merge3('a\nb', 'a\nb\nmine', 'a\nb\ntheirs')
    expect(r.conflicts).toBe(1)
    expect(resolveMerge(r, ['both'])).toBe('a\nb\nmine\ntheirs')
  })

  it('handles an empty base', () => {
    const r = merge3('', 'mine', '')
    expect(r.conflicts).toBe(0)
    expect(resolveMerge(r)).toBe('mine')
  })
})

describe('mergeTitle', () => {
  it('merges titles three ways', () => {
    expect(mergeTitle('a', 'a', 'b')).toBe('b')
    expect(mergeTitle('a', 'b', 'a')).toBe('b')
    expect(mergeTitle('a', 'b', 'b')).toBe('b')
    expect(mergeTitle('a', 'b', 'c')).toBeNull()
  })
})

describe('diffHunks / applyHunks', () => {
  const a = ['one', 'two', 'three', 'four', 'five']
  const b = ['one', 'TWO', 'three', 'five', 'six']

  it('lists replaced, removed and added blocks', () => {
    expect(diffHunks(a, b)).toEqual([
      { aStart: 1, aEnd: 2, bStart: 1, bEnd: 2 },
      { aStart: 3, aEnd: 4, bStart: 3, bEnd: 3 },
      { aStart: 5, aEnd: 5, bStart: 4, bEnd: 5 },
    ])
  })

  it('applies all, none or some of the hunks', () => {
    const hunks = diffHunks(a, b)
    expect(applyHunks(a, b, hunks, [true, true, true])).toEqual(b)
    expect(applyHunks(a, b, hunks, [false, false, false])).toEqual(a)
    expect(applyHunks(a, b, hunks, [true, false, true])).toEqual(['one', 'TWO', 'three', 'four', 'five', 'six'])
  })

  it('returns no hunks for identical input', () => {
    expect(diffHunks(a, a)).toEqual([])
  })
})
