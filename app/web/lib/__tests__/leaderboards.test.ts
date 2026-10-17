import { describe, expect, it } from 'vitest'
import { zh } from '@/lib/i18n/locales/zh'
import { en } from '@/lib/i18n/locales/en'
import { boardText, LEADERBOARD_KEYS } from '@/lib/leaderboards'

const translator = (dict: Record<string, string>) => (key: string) => dict[key] ?? key

describe('leaderboard texts', () => {
  it('has a name, unit and description for every known board', () => {
    for (const dict of [zh, en]) {
      const t = translator(dict)
      for (const key of LEADERBOARD_KEYS) {
        expect(boardText(t, 'name', key)).not.toBe(key)
        expect(boardText(t, 'unit', key)).not.toBe('')
        expect(boardText(t, 'desc', key)).not.toBe('')
      }
    }
  })

  it('falls back to the key for boards registered by unknown plugins', () => {
    const t = translator(zh)
    expect(boardText(t, 'name', 'custom-board')).toBe('custom-board')
    expect(boardText(t, 'unit', 'custom-board')).toBe('')
  })
})
