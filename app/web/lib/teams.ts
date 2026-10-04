import type { Book } from '@/lib/types'

// 团队空间插件（teams）：团队、成员与团队书籍。书籍加入团队后，成员按角色自动获得书籍的协作权限。

export const TEAMS_PLUGIN_KEY = 'teams'

export function teamsEnabled(site: { feature_plugins?: string[] } | null | undefined): boolean {
  return (site?.feature_plugins || []).includes(TEAMS_PLUGIN_KEY)
}

export type TeamRole = 'owner' | 'admin' | 'member'
export type TeamBookRole = 'editor' | 'suggester' | 'viewer'
export type TeamTab = 'books' | 'members' | 'settings'

export interface Team {
  id: number
  name: string
  slug: string
  description: string
  owner_id: number
  created_at: string
  my_role?: TeamRole
  member_count: number
  book_count: number
}

export interface TeamUser {
  id: number
  username: string
  nickname?: string
  name_display?: string
  avatar?: string
}

export interface TeamMember {
  id: number
  user: TeamUser
  role: TeamRole
  status: 'pending' | 'accepted'
  created_at: string
}

export type TeamBook = Book & { member_role: TeamBookRole; added_by: number; added_at: string }

export interface TeamDetail {
  team: Team
  members: TeamMember[]
  books: TeamBook[]
  can_manage: boolean
  seats: { used: number; limit: number }
}

export interface TeamInvitation {
  id: number
  team: Team
  role: TeamRole
  inviter: TeamUser
  created_at: string
}

export const TEAM_ROLE_KEYS: Record<TeamRole, string> = {
  owner: 'teams.role.owner',
  admin: 'teams.role.admin',
  member: 'teams.role.member',
}

export const TEAM_BOOK_ROLE_KEYS: Record<TeamBookRole, string> = {
  editor: 'teams.bookRole.editor',
  suggester: 'teams.bookRole.suggester',
  viewer: 'teams.bookRole.viewer',
}

export function teamTab(value: unknown): TeamTab {
  if (value === 'members') return 'members'
  if (value === 'settings') return 'settings'
  return 'books'
}
