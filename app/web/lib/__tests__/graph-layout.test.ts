import { describe, expect, it } from 'vitest'
import { forceLayout, graphBounds } from '../graph-layout'

const dist = (a: { x: number; y: number }, b: { x: number; y: number }) => Math.hypot(a.x - b.x, a.y - b.y)

describe('力导向布局', () => {
  it('相同输入得到相同结果', () => {
    const a = forceLayout([1, 2, 3], [[1, 2]])
    const b = forceLayout([1, 2, 3], [[1, 2]])
    expect([...a.values()]).toEqual([...b.values()])
  })

  it('有链接的章节保持在理想距离附近，所有节点互不重叠', () => {
    const ids = [1, 2, 3, 4, 5, 6]
    const pos = forceLayout(ids, [[1, 2], [2, 3], [3, 1]], { linkDistance: 110 })
    for (const [a, b] of [[1, 2], [2, 3], [3, 1]]) {
      const d = dist(pos.get(a)!, pos.get(b)!)
      expect(d).toBeGreaterThan(110 * 0.6)
      expect(d).toBeLessThan(110 * 1.6)
    }
    for (const a of ids) for (const b of ids) if (a < b) expect(dist(pos.get(a)!, pos.get(b)!)).toBeGreaterThan(20)
  })

  it('忽略指向不存在节点与自身的边；空图有默认范围', () => {
    const pos = forceLayout([1], [[1, 1], [1, 99]])
    expect(Number.isFinite(pos.get(1)!.x)).toBe(true)
    expect(graphBounds([])).toEqual({ x: -100, y: -100, w: 200, h: 200 })
    expect(graphBounds([{ x: 0, y: 0 }, { x: 100, y: 50 }], 10)).toEqual({ x: -10, y: -10, w: 120, h: 70 })
  })
})
