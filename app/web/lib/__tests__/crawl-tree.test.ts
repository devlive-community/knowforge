import { describe, expect, it } from 'vitest'
import { buildCrawlTree, flattenCrawlTree, moveCrawlNode, type CrawlNode } from '@/lib/crawl-tree'

const n = (url: string, parent = '', depth = 0): CrawlNode => ({ url, title: url, depth, parent_url: parent })

// a
//   a1
//   a2
// b
const flat: CrawlNode[] = [n('a'), n('a1', 'a', 1), n('a2', 'a', 1), n('b')]
const shape = (list: CrawlNode[]) => list.map((x) => `${'-'.repeat(x.depth)}${x.url}<${x.parent_url}`)

describe('crawl tree', () => {
  it('round-trips the preview order', () => {
    expect(flattenCrawlTree(buildCrawlTree(flat))).toEqual(flat)
  })

  it('treats nodes whose parent is missing as top level', () => {
    expect(shape(flattenCrawlTree(buildCrawlTree([n('x', 'gone', 2), n('y', 'x', 3)])))).toEqual(['x<', '-y<x'])
  })

  it('moves a node with its children before another node', () => {
    expect(shape(moveCrawlNode(flat, 'a', 'b', 'after'))).toEqual(['b<', 'a<', '-a1<a', '-a2<a'])
  })

  it('nests a node inside another and rewrites depth', () => {
    expect(shape(moveCrawlNode(flat, 'b', 'a1', 'inside'))).toEqual(['a<', '-a1<a', '--b<a1', '-a2<a'])
  })

  it('moves a child out to the top level', () => {
    expect(shape(moveCrawlNode(flat, 'a2', 'a', 'before'))).toEqual(['a2<', 'a<', '-a1<a', 'b<'])
  })

  it('refuses to move a node into its own subtree', () => {
    expect(moveCrawlNode(flat, 'a', 'a1', 'inside')).toBe(flat)
    expect(moveCrawlNode(flat, 'a', 'a', 'after')).toBe(flat)
  })
})
