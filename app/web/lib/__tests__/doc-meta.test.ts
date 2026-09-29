import { describe, expect, it } from 'vitest'
import { parseDocMeta, safeMetaUrl, setDocIcon } from '../doc-meta'
import { renderMarkdown } from '../markdown'

const FM = `---
title: 常见用例指南
url: https://platform.claude.com/docs/zh-CN/about-claude/use-case-guides/overview
description: "探索用于构建常见 Claude 用例的生产指南：工单路由、客户支持智能体。"
---
`

describe('章节元数据', () => {
  it('解析开头的 front-matter 并从正文中移除', () => {
    const d = parseDocMeta(FM + '\n# 正文\n内容')
    expect(d.meta.title).toBe('常见用例指南')
    expect(d.meta.url).toBe('https://platform.claude.com/docs/zh-CN/about-claude/use-case-guides/overview')
    expect(d.meta.description).toBe('探索用于构建常见 Claude 用例的生产指南：工单路由、客户支持智能体。')
    expect(d.body).toBe('# 正文\n内容')
    const html = renderMarkdown(FM + '\n正文段落')
    expect(html).toContain('正文段落')
    expect(html).not.toContain('常见用例指南')
    expect(html).not.toContain('<hr')
  })

  it('图标注释与 front-matter 可同时使用，注释优先', () => {
    const d = parseDocMeta(FM.replace('---\n', '---\nicon: book\n') + '<!-- icon: database -->\n正文')
    expect(d.meta.icon).toBe('database')
    expect(d.meta.title).toBe('常见用例指南')
    expect(d.body).toBe('正文')
    expect(parseDocMeta('<!-- icon: rocket -->\n\n正文').meta.icon).toBe('rocket')
  })

  it('只认文档开头：正文中的 front-matter 与图标注释按普通内容处理', () => {
    const d = parseDocMeta('正文\n\n---\ntitle: 不是元数据\n---\n<!-- icon: x -->')
    expect(d.meta).toEqual({})
    expect(d.header).toBe('')
    // 以分隔线开头、内容不是 key: value 的也不是元数据
    expect(parseDocMeta('---\n普通段落\n---\n').header).toBe('')
    // 首行之前有空行也不是
    expect(parseDocMeta('\n---\ntitle: x\n---\n').meta).toEqual({})
  })

  it('原文链接只接受 http(s)', () => {
    expect(safeMetaUrl('https://a.com/x')).toBe('https://a.com/x')
    expect(safeMetaUrl('javascript:alert(1)')).toBe('')
  })

  it('设置章节图标：有 front-matter 时写入其中，有注释时替换注释', () => {
    expect(setDocIcon(FM + '正文', 'rocket')).toBe(FM.replace('\n---\n', '\n---\n').replace(/---\n$/, 'icon: rocket\n---\n') + '正文')
    expect(parseDocMeta(setDocIcon(FM + '正文', 'rocket')).meta.icon).toBe('rocket')
    expect(setDocIcon('<!-- icon: a -->\n正文', 'b')).toBe('<!-- icon: b -->\n正文')
    expect(setDocIcon('<!-- icon: a -->\n正文', '')).toBe('正文')
    expect(setDocIcon('正文', 'c')).toBe('<!-- icon: c -->\n正文')
    const removed = setDocIcon(FM.replace('---\n', '---\nicon: book\n') + '正文', '')
    expect(parseDocMeta(removed).meta.icon).toBeUndefined()
    expect(parseDocMeta(removed).meta.title).toBe('常见用例指南')
  })
})

describe('[toc]', () => {
  it('包含 H1–H6 全部标题并按层级缩进，H2/H3 锚点编号不变', () => {
    const html = renderMarkdown('[toc]\n\n# 一级\n## 二级\n### 三级\n#### 四级\n##### 五级\n###### 六级\n####### 不是标题')
    for (const t of ['一级', '二级', '三级', '四级', '五级', '六级']) expect(html).toContain(`>${t}</a>`)
    expect(html).not.toContain('>不是标题</a>')
    expect(html).toContain('<h2 id="h-1"')
    expect(html).toContain('<h3 id="h-2"')
    expect(html).toContain('<h1 id="hx-1"')
    expect(html).toContain('<h6 id="hx-4"')
    expect(html).toMatch(/class="pl-20"><a href="#hx-4"/)
  })
})
