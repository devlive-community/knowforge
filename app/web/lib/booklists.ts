import type { Book } from '@/lib/types'
import { displayName } from '@/lib/users'

// 书单（「书单」插件）：用户整理的主题书单，可公开、收藏。

export const BOOK_LISTS_PLUGIN_KEY = 'book-lists'

export function bookListsEnabled(site: { feature_plugins?: string[] } | Record<string, unknown>): boolean {
  const list = (site as { feature_plugins?: unknown }).feature_plugins
  return Array.isArray(list) && list.includes(BOOK_LISTS_PLUGIN_KEY)
}

export interface BookListOwner { id: number; username: string; nickname: string; display_name?: string; avatar: string }

// BookList 书单卡片：covers 为前几本查看者可读书籍的封面；contains 仅在「我的书单」按书籍查询时返回。
export interface BookList {
  id: number
  user_id: number
  title: string
  description: string
  is_public: boolean
  item_count: number
  follower_count: number
  created_at: string
  updated_at: string
  owner: BookListOwner | null
  covers: { book_id: number; title: string; cover_image: string }[]
  following: boolean
  contains?: boolean
  featured?: boolean // 管理员设为精选，展示在发现页
}

export interface BookListEntry { book: Book; note: string; sort_order: number; added_at: string }

export interface BookListDetail { list: BookList; items: BookListEntry[]; mine: boolean; hidden: number }

export const bookListPath = (id: number) => `/lists/${id}`

export const ownerName = (o: BookListOwner | null) => displayName(o)
