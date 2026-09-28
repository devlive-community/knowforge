import type { SiteConfig } from '@/lib/types'

// RSS 订阅（「RSS 订阅」插件）：书籍与作者的订阅地址。地址为站点同源的 /api 路径，由 Go 服务直接提供。

export const FEEDS_PLUGIN_KEY = 'feeds'

export function feedsEnabled(site: SiteConfig): boolean {
  return (site.feature_plugins || []).includes(FEEDS_PLUGIN_KEY)
}

export const bookFeedPath = (slug: string) => `/api/v1/feeds/books/${encodeURIComponent(slug)}.xml`
export const userFeedPath = (username: string) => `/api/v1/feeds/users/${encodeURIComponent(username)}.xml`

// absoluteFeedURL 可粘贴到阅读器的完整地址。
export function absoluteFeedURL(path: string): string {
  return typeof window === 'undefined' ? path : window.location.origin + path
}
