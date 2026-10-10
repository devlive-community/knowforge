import { describe, expect, it } from 'vitest'
import { categoriesEnabled, categoryHref, categoryOptionLabel, flattenCategories, subtreeIds, treeHeight, type CategoryNode } from '../categories'

const node = (id: number, name: string, children: CategoryNode[] = []): CategoryNode => ({
  id, parent_id: 0, name, slug: name.toLowerCase(), description: '', icon_type: '', icon_value: '', sort_order: 0, book_count: 0, children,
})

const tree = [node(1, 'Tech', [node(2, 'Backend', [node(3, 'Go')]), node(4, 'Frontend')]), node(5, 'Literature')]

describe('categories', () => {
  it('按树的顺序展开并带层级', () => {
    expect(flattenCategories(tree).map(({ node: n, depth }) => `${depth}:${n.name}`)).toEqual(['0:Tech', '1:Backend', '2:Go', '1:Frontend', '0:Literature'])
  })

  it('子树 ID 与层数', () => {
    expect(subtreeIds(tree[0])).toEqual([1, 2, 3, 4])
    expect(treeHeight(tree[0])).toBe(3)
    expect(treeHeight(tree[1])).toBe(1)
  })

  it('下拉缩进、地址与插件开关', () => {
    expect(categoryOptionLabel('Go', 2)).toBe('　　Go')
    expect(categoryHref('中 文')).toBe('/explore?category=%E4%B8%AD%20%E6%96%87')
    expect(categoriesEnabled({ feature_plugins: ['categories'] })).toBe(true)
    expect(categoriesEnabled({ feature_plugins: [] })).toBe(false)
    expect(categoriesEnabled(null)).toBe(false)
  })
})
