// 反向链接（「反向链接」插件）：阅读页的「被引用」与书籍设置中的章节链接关系。

export const BACKLINKS_PLUGIN_KEY = 'backlinks'

export function backlinksEnabled(site: { feature_plugins?: string[] } | Record<string, unknown>): boolean {
  const list = (site as { feature_plugins?: unknown }).feature_plugins
  return Array.isArray(list) && list.includes(BACKLINKS_PLUGIN_KEY)
}

// Backlink 链接到本章的一个已发布章节；excerpt 为链接所在段落的上下文（付费章节为 null）。
export interface Backlink {
  id: number
  title: string
  slug: string
  book_id: number
  book_slug: string
  book_title: string
  excerpt: { before: string; text: string; after: string } | null
}

export interface LinkGraphNode {
  id: number
  parent_id: number | null
  title: string
  slug: string
  status: string
  outgoing: number[]
  incoming: number[]
  external: number
}

// LinkGraph 本书章节之间的链接（含草稿）与无法解析的失效链接。
export interface LinkGraph {
  nodes: LinkGraphNode[]
  edges: number
  broken: { from: number; target: string }[]
}
