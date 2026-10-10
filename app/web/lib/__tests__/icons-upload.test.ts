import { describe, expect, it } from 'vitest'
import { formatIconValue, parseIcon, searchIcons, type IconCatalog } from '../icons'
import { IMAGE_RULE, SVG_RULE, acceptOf, formatSize, validateFile } from '../upload'

describe('icon values', () => {
  it('解析与生成带颜色的取值，旧数据不受影响', () => {
    expect(parseIcon('fa', 'fa-folder')).toEqual({ type: 'fa', value: 'fa-folder', color: '' })
    expect(parseIcon('fa', 'fa-folder|#3B82F6')).toEqual({ type: 'fa', value: 'fa-folder', color: '#3b82f6' })
    expect(parseIcon('svg', '/uploads/a.svg|#ef4444').value).toBe('/uploads/a.svg')
    expect(parseIcon('image', 'https://x.test/a|b.png').value).toBe('https://x.test/a|b.png') // 不是颜色的后缀保持原样
    expect(formatIconValue('fa-folder', '#EF4444')).toBe('fa-folder|#ef4444')
    expect(formatIconValue('fa-folder', '')).toBe('fa-folder')
    expect(formatIconValue('fa-folder', 'red')).toBe('fa-folder')
  })

  it('按名称与搜索词匹配，搜索时不限分组', () => {
    const cat: IconCatalog = {
      icons: [['folder', 'directory folder storage'], ['user', 'person profile'], ['star', 'favorite rating']],
      groups: { common: ['folder', 'star'], users: ['user'] },
    }
    expect(searchIcons(cat, 'common', '')).toEqual(['folder', 'star'])
    expect(searchIcons(cat, 'all', '')).toEqual(['folder', 'user', 'star'])
    expect(searchIcons(cat, 'common', 'profile')).toEqual(['user'])
    expect(searchIcons(cat, 'users', 'fa-fold')).toEqual(['folder'])
    expect(searchIcons(cat, 'all', 'favorite star')).toEqual(['star'])
  })
})

describe('upload rules', () => {
  const file = (name: string, type: string, size: number) => new File([new Uint8Array(size)], name, { type })

  it('按类型与大小校验', () => {
    expect(validateFile(file('a.png', 'image/png', 100), IMAGE_RULE)).toBeNull()
    expect(validateFile(file('a.JPG', '', 100), IMAGE_RULE)).toBeNull()
    expect(validateFile(file('a.svg', 'image/svg+xml', 100), IMAGE_RULE)?.key).toBe('upload.error.type')
    expect(validateFile(file('a.png', 'image/png', IMAGE_RULE.maxBytes + 1), IMAGE_RULE)).toEqual({ key: 'upload.error.size', params: { max: '2MB' } })
    expect(validateFile(file('a.svg', 'image/svg+xml', 0), SVG_RULE)?.key).toBe('upload.error.empty')
  })

  it('accept 与大小格式', () => {
    expect(acceptOf(SVG_RULE)).toBe('image/svg+xml,.svg')
    expect(formatSize(1 << 20)).toBe('1MB')
    expect(formatSize(512 << 10)).toBe('512KB')
  })
})
