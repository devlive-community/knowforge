import { api } from './api'

// 用户关注插件（user-follow）的接口封装。

export interface FollowStats {
  followers: number
  following: number
  followed_by_me: boolean
  follows_me: boolean
  is_me: boolean
}

export interface FollowUser {
  id: number
  username: string
  nickname: string
  display_name: string
  avatar: string
  bio: string
}

export interface FollowListItem {
  user: FollowUser
  followed_at: string
  followed_by_me: boolean
  is_me: boolean
}

export interface FollowListPage {
  items: FollowListItem[]
  total: number
  page: number
  page_size: number
}

export type FollowListKind = 'followers' | 'following'

export function userFollowEnabled(site: { feature_plugins?: string[] }): boolean {
  return (site.feature_plugins || []).includes('user-follow')
}

const userPath = (username: string) => `/users/${encodeURIComponent(username)}`

export function fetchFollowStats(username: string): Promise<FollowStats> {
  return api<FollowStats>(`${userPath(username)}/follow-stats`)
}

// setFollowing 关注或取消关注，返回对方最新的关注数与状态。
export function setFollowing(username: string, follow: boolean): Promise<FollowStats> {
  return api<FollowStats>(`${userPath(username)}/follow`, { method: follow ? 'POST' : 'DELETE' })
}

export function fetchFollowList(username: string, kind: FollowListKind, page: number, pageSize = 20): Promise<FollowListPage> {
  return api<FollowListPage>(`${userPath(username)}/${kind}`, { params: { page, page_size: pageSize } })
}
