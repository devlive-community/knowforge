// 书籍分类插件（categories）：管理员维护的最多三层分类树，书籍可选一个分类；发现页 /explore?category=<slug> 按分类浏览（含子分类）。

export const CATEGORIES_PLUGIN_KEY = 'categories'

export function categoriesEnabled(site: { feature_plugins?: string[] } | Record<string, unknown> | null | undefined): boolean {
  const list = (site as { feature_plugins?: unknown } | null | undefined)?.feature_plugins
  return Array.isArray(list) && list.includes(CATEGORIES_PLUGIN_KEY)
}

/** 书籍上回填的分类（path 为从顶级到自身的路径） */
export interface BookCategoryRef {
  id: number
  name: string
  slug: string
  path?: BookCategoryRef[]
}

/** 分类树节点（book_count 含子分类） */
export interface CategoryNode {
  id: number
  parent_id: number
  name: string
  slug: string
  description: string
  icon_type: string
  icon_value: string
  sort_order: number
  book_count: number
  children: CategoryNode[]
}

export const MAX_CATEGORY_DEPTH = 3

/** categoryHref 分类在发现页的地址 */
export function categoryHref(slug: string): string {
  return `/explore?category=${encodeURIComponent(slug)}`
}

/** flattenCategories 按树的顺序展开（depth 从 0 开始），用于下拉选择与表格 */
export function flattenCategories(nodes: CategoryNode[], depth = 0): { node: CategoryNode; depth: number }[] {
  const out: { node: CategoryNode; depth: number }[] = []
  for (const n of nodes) {
    out.push({ node: n, depth })
    out.push(...flattenCategories(n.children || [], depth + 1))
  }
  return out
}

/** categoryOptionLabel 下拉中的分类名（按层级缩进，如「后端 › Go」显示为「　　Go」） */
export function categoryOptionLabel(name: string, depth: number): string {
  return `${'　'.repeat(depth)}${name}`
}

/** subtreeIds 节点及其全部子孙的 ID */
export function subtreeIds(node: CategoryNode): number[] {
  return [node.id, ...(node.children || []).flatMap(subtreeIds)]
}

/** treeHeight 以节点为根的子树层数（叶子为 1） */
export function treeHeight(node: CategoryNode): number {
  return 1 + Math.max(0, ...(node.children || []).map(treeHeight))
}
