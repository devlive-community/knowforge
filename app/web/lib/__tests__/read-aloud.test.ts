import { describe, expect, it } from 'vitest'
import { readAloudEnabled, splitText } from '../read-aloud'

describe('splitText', () => {
  it('短段落原样保留并压缩空白', () => {
    expect(splitText('  第一段\n  文字。 ')).toEqual(['第一段 文字。'])
    expect(splitText('   ')).toEqual([])
  })

  it('长段落按句切分，每段不超过上限', () => {
    const text = '第一句话。第二句话！第三句话？Fourth sentence. 第五句。'
    const parts = splitText(text, 10)
    expect(parts.join('').replace(/\s/g, '')).toBe(text.replace(/\s/g, ''))
    for (const p of parts) expect([...p].length).toBeLessThanOrEqual(10)
    expect(parts[0]).toBe('第一句话。第二句话！')
  })

  it('单句过长时按长度硬切', () => {
    const parts = splitText('甲'.repeat(25), 10)
    expect(parts.map((p) => p.length)).toEqual([10, 10, 5])
  })
})

describe('readAloudEnabled', () => {
  it('按站点启用的插件判断', () => {
    expect(readAloudEnabled({ feature_plugins: ['read-aloud'] })).toBe(true)
    expect(readAloudEnabled({ feature_plugins: [] })).toBe(false)
    expect(readAloudEnabled(null)).toBe(false)
  })
})
