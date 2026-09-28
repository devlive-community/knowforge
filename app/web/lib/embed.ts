import type { SiteConfig } from '@/lib/types'

// 嵌入组件（「嵌入组件」插件）：书籍目录与单个章节的 iframe 嵌入。

export const EMBED_PLUGIN_KEY = 'embed'

export function embedEnabled(site: SiteConfig | Record<string, unknown>): boolean {
  const list = (site as { feature_plugins?: unknown }).feature_plugins
  return Array.isArray(list) && list.includes(EMBED_PLUGIN_KEY)
}

export const bookEmbedPath = (bookSlug: string) => `/embed/book/${encodeURIComponent(bookSlug)}`
export const bookCardEmbedPath = (bookSlug: string) => `/embed/card/${encodeURIComponent(bookSlug)}`
export const docEmbedPath = (bookSlug: string, docSlug: string) => `/embed/doc/${encodeURIComponent(bookSlug)}/${encodeURIComponent(docSlug)}`

function escapeAttr(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;')
}

// embedSnippet 可粘贴到其他网站的 iframe 代码。
export function embedSnippet(url: string, title: string, height: number): string {
  return `<iframe src="${escapeAttr(url)}" title="${escapeAttr(title)}" width="100%" height="${height}" style="border:1px solid #e2e8f0;border-radius:12px;max-width:100%" loading="lazy"></iframe>`
}
