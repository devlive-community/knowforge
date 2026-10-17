import type { Book } from './types'

// 排行榜插件（leaderboards）：榜单由各插件登记，前端按 key 取名称、单位与说明
// （leaderboards.name.<key> / leaderboards.unit.<key> / leaderboards.desc.<key>，key 连字符转驼峰）。

export type LeaderboardPeriod = 'all' | 'month' | 'week'
export type LeaderboardKind = 'user' | 'book'

// LEADERBOARD_KEYS 已知榜单（i18n 动态键取值域，见 scripts/check_i18n.py）。
export const LEADERBOARD_KEYS = ['xp', 'followers', 'creators', 'hot-books', 'liked-books', 'favorited-books', 'followed-books'] as const

export interface LeaderboardMeta {
  key: string
  kind: LeaderboardKind
  icon: string
  periods: LeaderboardPeriod[]
}

export interface LeaderboardLevel { level: number; name: string; icon_type?: string; icon_value?: string }
export interface LeaderboardUser { id: number; username: string; nickname?: string; display_name?: string; avatar?: string }

export interface LeaderboardEntry {
  rank: number
  value: number
  user?: LeaderboardUser
  book?: Book
  extra?: { level?: LeaderboardLevel }
}

export interface LeaderboardMe {
  rank?: number
  value: number
  user: LeaderboardUser
  public?: boolean
  level?: LeaderboardLevel
}

export interface LeaderboardPage {
  board: LeaderboardMeta
  items: LeaderboardEntry[]
  total: number
  page: number
  page_size: number
  period: LeaderboardPeriod
  me?: LeaderboardMe
}

export function enabledLeaderboards(site: { leaderboards?: string[] }): string[] {
  return site.leaderboards || []
}

// boardText 榜单名称等文案（键中连字符转驼峰，如 hot-books → hotBooks）：未知 key（第三方插件登记）回退为 key 本身。
export function boardText(t: (key: string) => string, kind: 'name' | 'unit' | 'desc', key: string): string {
  const k = `leaderboards.${kind}.${key.replace(/-(\w)/g, (_, c: string) => c.toUpperCase())}`
  const v = t(k)
  return v === k ? (kind === 'name' ? key : '') : v
}
