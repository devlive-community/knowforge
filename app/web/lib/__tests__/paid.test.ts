import { describe, expect, it } from 'vitest'
import { freeReadersText, tierOptions, type TierGrant } from '@/lib/paid'

const t = (key: string, vars?: Record<string, string | number>) => `${key}${vars ? JSON.stringify(vars) : ''}`
const grants: TierGrant[] = [
  { source: 'membership', label: '基础版', rank: 0, value: 1 },
  { source: 'membership', label: '专业版', rank: 1, value: 2 },
  { source: 'level', label: 'Lv3', rank: 3, value: 1 },
  { source: 'level', label: 'Lv5', rank: 5, value: 2 },
]

describe('free reading tier options', () => {
  it('lists plans and the lowest qualifying growth level for each tier', () => {
    expect(freeReadersText(t, 'zh-CN', grants, 2)).toBe('paid.tier.plan{"name":"专业版"}和paid.tier.level{"name":"Lv5"}')
    expect(freeReadersText(t, 'zh-CN', grants, 1)).toContain('paid.tier.level{"name":"Lv3"}')
  })

  it('offers none plus one option per granted tier, keeping an orphan current value', () => {
    const options = tierOptions(t, 'zh-CN', grants, 0)
    expect(options.map((o) => o.value)).toEqual(['0', '1', '2'])
    expect(tierOptions(t, 'zh-CN', grants, 7).map((o) => o.value)).toEqual(['0', '1', '2', '7'])
  })

  it('treats a base grant as everyone', () => {
    expect(freeReadersText(t, 'zh-CN', [...grants, { source: 'base', label: '', rank: 0, value: 3 }], 2)).toBe('paid.tier.everyone')
  })
})
