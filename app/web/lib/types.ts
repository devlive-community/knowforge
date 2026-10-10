import type { Paywall } from './paid'

// 与 Go 服务端对应的 API 数据类型
export type BookStatus = 'draft' | 'in_progress' | 'published' | 'completed' | 'archived'
export type DocumentStatus = 'draft' | 'published' | 'archived'
export type UserRole = 'admin' | 'user'

export interface User {
  id: number
  username: string
  email: string
  role: UserRole
  avatar: string
  bio: string
  github_url: string
  nickname?: string
  /** 名字显示方式：nickname（优先昵称，默认）| username（始终用户名） */
  name_display?: 'nickname' | 'username' | ''
  /** 服务端按名字显示方式计算的展示名 */
  display_name?: string
  website?: string
  location?: string
  company?: string
  is_active: boolean
  email_verified?: boolean
  invite_code?: string
  two_factor_enabled?: boolean
  deletion_requested_at?: string | null
  /** /auth/me 下发的当前权益生效值（权益键 → 值；-1 = 不限） */
  entitlements?: Record<string, number>
  last_login_at: string | null
  created_at: string
  updated_at: string
}

export interface Tag {
  id: number
  name: string
  slug: string
  icon_type?: string
  icon_value?: string
  book_count?: number
}

/** 书籍「更多信息」中的一项（类型见 lib/book-info.ts） */
export interface BookInfoItem { type: string; label?: string; value: string }

export interface Book {
  id: number
  title: string
  description: string
  cover_image: string
  slug: string
  slug_editable?: boolean
  user_id: number
  status: BookStatus
  is_public: boolean
  login_required?: boolean
  view_count: number
  order_col: string
  order_dir: 'asc' | 'desc'
  chapter_prefix: string
  child_status_follow_parent?: boolean
  default_chapter_status?: string
  language?: string
  trans_group?: string
  version?: string
  version_group?: string
  version_is_latest?: boolean
  /** 版本聚合列表：该版本组在当前筛选下可见的版本数（>1 才返回） */
  version_count?: number
  /** 版本组内被标记为最新版的版本号 */
  latest_version?: string
  chapter_count?: number
  collaborator_role?: 'editor' | 'viewer' | 'suggester'
  watermark_enabled: boolean
  watermark_text: string
  export_enabled?: boolean
  guest_export_enabled?: boolean
  export_style_shared?: boolean
  export_formats?: string
  extra_info?: BookInfoItem[] // 「更多信息」附加属性（GitHub、原始文档地址等）
  publish_held?: string // 本次保存「公开」被内容审核拦截时的说明（书籍保持私有）
  user?: Pick<User, 'id' | 'username' | 'nickname' | 'display_name' | 'avatar' | 'email' | 'bio' | 'github_url' | 'role'>
  tags?: Tag[]
  category?: import('@/lib/categories').BookCategoryRef // 书籍分类插件回填（未分类时没有）
  crawling?: boolean
  created_at: string
  updated_at: string
}

export type CollaborationInvitationStatus = 'pending' | 'accepted' | 'rejected'

export interface CollaborationInvitation {
  id: number
  book_id: number
  book_title: string
  book_slug: string
  role: 'editor' | 'viewer' | 'suggester'
  inviter_username: string
  created_at: string
}

export interface BookAccess {
  can_read: boolean
  can_manage: boolean
  can_edit_content: boolean
  can_export: boolean
  can_suggest?: boolean // 可以进入写作台提交修改建议（含能编辑的人）
  collaborator_role?: 'editor' | 'viewer' | 'suggester'
}

export interface Document {
  id: number
  book_id: number
  parent_id: number | null
  title: string
  slug: string
  content: string
  user_id: number
  sort_order: number
  view_count?: number
  publish_held?: string // 本次保存「发布」被内容审核拦截时的说明（章节保持未发布）
  paywall?: Paywall // 读者无权阅读全文时的付费墙信息（此时 content 为试读内容）
  status: DocumentStatus
  icon?: string
  external_url?: string
  external_new_tab?: boolean
  allow_comments?: boolean | null
  created_at: string
  updated_at: string
  children?: Document[]
  content_hash?: string // 标题与正文的摘要（写作台保存时回传，用于发现他人在此期间的修改）
}

export type DocumentRevisionReason = 'create' | 'save' | 'publish' | 'pre_restore' | 'restore'

export interface DocumentRevisionSummary {
  id: number
  document_id: number
  book_id: number
  title: string
  content_length: number
  status: DocumentStatus
  allow_comments: boolean
  reason: DocumentRevisionReason
  author?: Pick<User, 'id' | 'username' | 'avatar'>
  created_at: string
}

export interface DocumentRevision extends DocumentRevisionSummary {
  content: string
}

export interface SiteConfig {
  site_name?: string
  site_description?: string
  site_logo?: string
  site_favicon?: string
  site_keywords?: string
  site_footer_text?: string
  site_footer_links?: string
  site_beian?: string
  help_doc_url?: string
  terms_url?: string
  privacy_url?: string
  version?: string
  installation_date?: string
  comments_enabled?: string
  registration_require_email_activation?: string
  announcement_enabled?: string
  announcement_text?: string
  announcement_tone?: string
  translation_enabled?: boolean
  ai_usage_enabled?: boolean // 站点配置了 AI 服务或翻译服务（显示「我的 AI 用量」入口）
  achievements_enabled?: string
  feature_plugins?: string[]
  pwa_enabled?: boolean
  collect_page_enabled?: boolean
  collect_site_enabled?: boolean
  book_versions_sort?: string
  /** 经验排行榜是否开放（成长插件启用且管理员未关闭，由插件下发） */
  growth_leaderboard_enabled?: boolean
  payment_channels?: string[] // 支付插件：当前可用的支付方式（仅支付插件自身页面使用）
  checkout_enabled?: boolean // 是否可在线购买（中性约定，见 lib/commerce.ts）
}

export type AchievementCategory = 'reading' | 'creation' | 'community' | 'account' | 'special'
export type AchievementStatus = 'draft' | 'active' | 'paused' | 'archived'
export type AchievementRarity = 'common' | 'rare' | 'epic' | 'legendary'

export interface AchievementAsset {
  id: number
  kind: 'image' | 'svg'
  url: string
  mime_type: string
  width: number
  height: number
}

export interface AchievementRule {
  id?: number
  achievement_id?: number
  metric_key: string
  operator: 'gte' | 'eq' | 'between'
  target_value: number
  target_max: number
  window_type: 'lifetime' | 'calendar_day' | 'calendar_week' | 'calendar_month' | 'rolling_days'
  window_value: number
  distinct_by: string
  filters: string | Record<string, unknown>
  sort_order?: number
}

export interface AchievementDefinition {
  translations?: Record<string, { fields: Record<string, string>; published?: Record<string, string>; revision: number; publish: boolean }>
  resolved_locale?: string
  id: number
  key: string
  name: string
  name_en: string
  description: string
  description_en: string
  locked_hint: string
  locked_hint_en: string
  category: AchievementCategory
  status: AchievementStatus
  rarity: AchievementRarity
  icon_type: 'fa' | 'image' | 'svg'
  icon_value: string
  asset_id: number | null
  asset?: AchievementAsset | null
  series_key: string
  tier: number
  reward_xp: number
  supersedes_previous: boolean
  rule_logic: 'all' | 'any'
  grant_mode: 'auto' | 'manual'
  visibility: 'public' | 'private' | 'hidden'
  progress_mode: 'aggregate' | 'primary' | 'hidden'
  active_from: string | null
  active_until: string | null
  version: number
  sort_order: number
  rules: AchievementRule[]
  created_at: string
  updated_at: string
}

export interface AchievementProgress {
  id: number
  current_value: number
  percent: number
  rule_values: string
  status: 'pending' | 'unlocked'
  last_evaluated_at: string
}

export interface AchievementGrant {
  id: number
  user_id: number
  achievement_id: number
  achievement?: AchievementDefinition
  source: 'auto' | 'manual'
  reason: string
  is_public: boolean
  showcase_order: number
  unlocked_at: string
  revoked_at?: string | null
}

export interface UserAchievementItem {
  definition: AchievementDefinition
  progress: AchievementProgress | null
  grant: AchievementGrant | null
  unlocked: boolean
}

export interface AchievementSettings {
  enabled: boolean
  public_profile_enabled: boolean
  notifications_enabled: boolean
  allow_user_hide: boolean
  showcase_limit: number
}

export interface AchievementMetric {
  key: string
  label: string
  category: AchievementCategory
  description: string
  aggregation: string
  unit: string
  windows: string[]
  allowed_filters: string[]
}

export interface BookReviewUser {
  id: number
  username: string
  avatar?: string
  role?: string
}

export interface BookReview {
  id: number
  user_id: number
  user: BookReviewUser
  rating: number
  content: string
  created_at: string
  updated_at: string
}

export interface BookReviewSummary {
  average: number
  count: number
  distribution: Record<string, number>
}

export interface FooterLink {
  label: string
  href: string
}

export interface FooterLinkGroup {
  title: string
  links: FooterLink[]
}

export interface SetupStatus {
  installed: boolean
  version: string
  db_types: ('sqlite' | 'mysql' | 'postgres')[]
  db_type?: string
  data_dir?: string
  sqlite_default_path?: string
}

export interface PageResult<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export interface TrashItem {
  type: 'book' | 'document'
  id: number
  title: string
  slug: string
  book_id?: number
  book_title?: string
  book_slug?: string
  owner_username?: string
  descendant_count: number
  deleted_at: string
  expires_at: string
}

export interface SiteStats {
  user_count: number
  book_count: number
  document_count: number
  total_views: number
}

export interface DatabasePayload {
  type: 'sqlite' | 'mysql' | 'postgres'
  host?: string
  port?: number
  name?: string
  user?: string
  password?: string
  path?: string
}
