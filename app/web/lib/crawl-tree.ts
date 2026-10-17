// 整站采集目录预览的树操作：预览接口返回按先序排列的扁平目录（depth + parent_url），
// 拖拽调整时转成树处理，提交前再展开为先序扁平列表（父页面总在子页面之前，后端据此建章节层级）。

export interface CrawlNode { url: string; title: string; depth: number; parent_url: string }
export interface CrawlTreeNode { node: CrawlNode; children: CrawlTreeNode[] }
export type CrawlDropPos = 'before' | 'inside' | 'after'

// buildCrawlTree 由先序扁平列表建树；父页面不在列表中（或出现在其后）时作为顶层。
export function buildCrawlTree(flat: CrawlNode[]): CrawlTreeNode[] {
  const roots: CrawlTreeNode[] = []
  const byURL = new Map<string, CrawlTreeNode>()
  for (const node of flat) {
    const item: CrawlTreeNode = { node, children: [] }
    const parent = node.parent_url ? byURL.get(node.parent_url) : undefined
    if (parent) parent.children.push(item)
    else roots.push(item)
    byURL.set(node.url, item)
  }
  return roots
}

// flattenCrawlTree 展开为先序扁平列表，并按所在位置重写 depth 与 parent_url。
export function flattenCrawlTree(tree: CrawlTreeNode[]): CrawlNode[] {
  const out: CrawlNode[] = []
  const walk = (items: CrawlTreeNode[], depth: number, parentURL: string) => {
    for (const item of items) {
      out.push({ ...item.node, depth, parent_url: parentURL })
      walk(item.children, depth + 1, item.node.url)
    }
  }
  walk(tree, 0, '')
  return out
}

// crawlSubtreeURLs url 及其全部子页面的地址（拖拽时不能放到自身子树内）。
export function crawlSubtreeURLs(flat: CrawlNode[], url: string): Set<string> {
  const out = new Set<string>([url])
  for (const node of flat) {
    if (node.parent_url && out.has(node.parent_url)) out.add(node.url)
  }
  return out
}

// moveCrawlNode 把 dragURL（连同子页面）移到 targetURL 之前、之后或其下（作为最后一个子页面）。
// 目标在被拖节点子树内或两者相同时原样返回。
export function moveCrawlNode(flat: CrawlNode[], dragURL: string, targetURL: string, pos: CrawlDropPos): CrawlNode[] {
  if (dragURL === targetURL || crawlSubtreeURLs(flat, dragURL).has(targetURL)) return flat
  const tree = buildCrawlTree(flat)
  let dragged: CrawlTreeNode | null = null
  const detach = (items: CrawlTreeNode[]): boolean => {
    const i = items.findIndex((it) => it.node.url === dragURL)
    if (i >= 0) { dragged = items.splice(i, 1)[0]; return true }
    return items.some((it) => detach(it.children))
  }
  detach(tree)
  if (!dragged) return flat
  const moving: CrawlTreeNode = dragged
  const attach = (items: CrawlTreeNode[]): boolean => {
    const i = items.findIndex((it) => it.node.url === targetURL)
    if (i >= 0) {
      if (pos === 'inside') items[i].children.push(moving)
      else items.splice(pos === 'before' ? i : i + 1, 0, moving)
      return true
    }
    return items.some((it) => attach(it.children))
  }
  if (!attach(tree)) return flat
  return flattenCrawlTree(tree)
}
