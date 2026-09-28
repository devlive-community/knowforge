// M17/M19 Markdown 扩展管线的回归测试：覆盖全部 14 个扩展与核心渲染器
import { describe, expect, it } from 'vitest'

import { renderMarkdown, extractHeadings } from '../markdown'

describe('renderMarkdown 基础渲染', () => {
  it('标题锚点与本章目录编号一致', () => {
    const html = renderMarkdown('## 一\n\n内容\n\n### 小节\n\n尾')
    expect(html).toContain('id="h-1"')
    expect(html).toContain('id="h-2"')
    expect(extractHeadings('## 一\n\n### 小节')).toEqual([
      { level: 2, text: '一', id: 'h-1' },
      { level: 3, text: '小节', id: 'h-2' },
    ])
  })

  it('代码块高亮并包一层容器', () => {
    const html = renderMarkdown('```ts\nconst a = 1\n```')
    expect(html).toContain('md-code-block')
    expect(html).toContain('hljs')
  })

  it('外链带 target/rel（经 DOMPurify 保留）', () => {
    const html = renderMarkdown('[首页](https://example.com)')
    expect(html).toContain('target="_blank"')
    expect(html).toContain('rel="noopener noreferrer"')
  })

  it('内部文档链接 doc: 同书补全为阅读地址', () => {
    const html = renderMarkdown('见 [第二章](doc:chapter-two)', { bookSlug: 'my-book' })
    expect(html).toContain('href="/book/reader/my-book/chapter-two"')
    expect(html).toContain('md-doc-link')
    expect(html).not.toContain('doc:')
  })

  it('内部文档链接 doc: 跨书 book/doc 形式', () => {
    const html = renderMarkdown('[外部章节](doc:other-book/intro)', { bookSlug: 'my-book' })
    expect(html).toContain('href="/book/reader/other-book/intro"')
  })

  it('无书籍上下文的 doc: 链接降级为纯文本', () => {
    const html = renderMarkdown('[孤立](doc:chapter-two)')
    expect(html).not.toContain('doc:')
    expect(html).toContain('孤立')
    expect(html).not.toContain('href="/book/reader')
  })
})

describe('M17 扩展', () => {
  it('GitHub 风格 alert', () => {
    const html = renderMarkdown('> [!WARNING]\n> 注意写法')
    expect(html).toContain('md-alert md-alert-warning')
    expect(html).toContain('警告')
  })

  it(':::tabs 多标签页', () => {
    const html = renderMarkdown(':::tabs\n=== "npm"\nnpm i\n=== "pnpm"\npnpm add\n:::')
    expect(html).toContain('data-md-tab=')
    expect(html).toContain('role="tablist"')
    expect(html).toContain('rounded-xl border border-slate-200 bg-slate-100/80 p-1')
    expect(html).toContain('border-slate-200 bg-white text-primary-700 shadow-sm')
    expect(html).toContain('pnpm add')
  })

  it(':::grid 网格与列数', () => {
    const html = renderMarkdown(':::grid cols-3 gap-4\n- 快\n- 稳\n- 省\n:::')
    expect(html).toContain('grid-template-columns')
    expect(html).toContain('hover:shadow-md')
  })

  it(':::diff 行级着色', () => {
    const html = renderMarkdown(':::diff +1 -2\n- 旧行\n  新行\n- 中间\n:::')
    expect(html).toContain('bg-emerald-50')
    expect(html).toContain('bg-rose-50')
    expect(html).toContain('旧行')
  })

  it(':::katex 服务端公式渲染', () => {
    const html = renderMarkdown(':::katex\n\\int_0^1 x^2 dx\n:::')
    expect(html).toContain('katex')
  })

  it(':::mermaid 占位待客户端渲染', () => {
    const html = renderMarkdown(':::mermaid\ngraph TD; A-->B;\n:::')
    expect(html).toContain('md-mermaid')
    expect(html).toContain('graph TD')
  })

  it('!btn / !tip / !switch 内联扩展', () => {
    const html = renderMarkdown('!btn[开始](https://x.com) 与 !tip[悬停](提示语) 以及 !switch[自动](true)')
    expect(html).toContain('开始')
    expect(html).toContain('提示语')
    expect(html).toContain('translate-x-4') // 开关打开态
  })

  it(':icon{size,color}: 占位（客户端 lucide 填充）', () => {
    const html = renderMarkdown(':check{24,red}: 图标')
    expect(html).toContain('data-md-icon="check"')
  })
})

describe('M19 扩展', () => {
  it('图片：普通语法与尺寸/对齐扩展', () => {
    const html = renderMarkdown('![a](/uploads/a.png) 和 ![b](/uploads/b.png "标题" =600x center)')
    expect(html).toContain('<img src="/uploads/a.png"')
    expect(html).toContain('width:600px')
    expect(html).toContain('mx-auto')
  })

  it('对齐表格：列对齐 + 单元格行内 Markdown', () => {
    const html = renderMarkdown('| 左 | 中 | 右 |\n|:---|:---:|---:|\n| **粗** | `码` | [链](https://x.com) |')
    expect(html).toContain('text-center')
    expect(html).toContain('text-right')
    expect(html).toContain('<strong>粗</strong>')
    expect(html).toContain('<code>码</code>')
  })

  it('GitHub issue 引用：仓库前缀与默认仓库', () => {
    const html = renderMarkdown('devlive-community/knowforge#123 和 #456')
    expect(html).toContain('https://github.com/devlive-community/knowforge/issues/123')
    expect(html).toContain('https://github.com/devlive-community/knowforge/issues/456')
  })

  it(':::api REST 文档卡', () => {
    const html = renderMarkdown(':::api GET /books/:id\n获取书籍，需要 `book:read`。\n=== "参数"\n  - `id`：书籍 ID\n:::')
    expect(html).toContain('GET</span>')
    expect(html).toContain('/books/:id')
    expect(html).toContain('参数')
    expect(html).toContain('<code>book:read</code>')
  })

  it('[toc] 用标题填充目录并复用锚点 id', () => {
    const html = renderMarkdown('[toc]\n\n## 安装\n\n### 依赖')
    expect(html).not.toContain('data-md-toc') // 占位符应被目录替换
    expect(html).toContain('href="#h-1"')
    expect(html).toContain('href="#h-2"')
  })

  it('<Tabs><Tab title> HTML 标签风格多标签页', () => {
    const html = renderMarkdown('<Tabs>\n<Tab title="PostgreSQL">\nPG 说明\n</Tab>\n<Tab title="MySQL">\nMySQL 说明\n</Tab>\n</Tabs>')
    expect(html).toContain('data-md-tab=')
    expect(html).toContain('role="tablist"')
    expect(html).toContain('PostgreSQL')
    expect(html).toContain('PG 说明')
    expect(html).toContain('MySQL 说明')
  })

  it('<Note>/<Warning>/<Tip> 提示块与可选加粗标题', () => {
    const withTitle = renderMarkdown('<Warning>\n**务必备份**\n执行前请备份数据。\n</Warning>')
    expect(withTitle).toContain('md-alert md-alert-warning')
    expect(withTitle).toContain('务必备份')
    expect(withTitle).toContain('执行前请备份数据')

    const noTitle = renderMarkdown('<Tip>\n随手记一笔。\n</Tip>')
    expect(noTitle).toContain('md-alert md-alert-tip')
    expect(noTitle).toContain('提示') // 缺省标题
    expect(noTitle).toContain('随手记一笔')
  })

  it('<AccordionGroup>/<Accordion> 折叠面板', () => {
    const html = renderMarkdown('<AccordionGroup>\n<Accordion title="没有文件锁">\n请改用其他方案。\n</Accordion>\n</AccordionGroup>')
    expect(html).toContain('<details')
    expect(html).toContain('<summary')
    expect(html).toContain('没有文件锁')
    expect(html).toContain('请改用其他方案')
  })

  it('<Steps>/<Step> 编号步骤', () => {
    const html = renderMarkdown('<Steps>\n<Step title="安装 Git LFS">\n运行安装命令。\n</Step>\n<Step title="初始化">\n完成初始化。\n</Step>\n</Steps>')
    expect(html).toContain('<ol')
    expect(html).toContain('安装 Git LFS')
    expect(html).toContain('完成初始化')
  })

  it('块可递归组合：Tab 内嵌代码块与 Tip', () => {
    const html = renderMarkdown('<Tabs>\n<Tab title="PostgreSQL">\n```sql\nSELECT 1;\n```\n\n<Tip>\n记得建索引\n</Tip>\n</Tab>\n</Tabs>')
    expect(html).toContain('data-md-tab=')
    expect(html).toContain('md-code-block') // 内层代码块被解析
    expect(html).toContain('md-alert md-alert-tip') // 内层 Tip 被解析
    expect(html).toContain('记得建索引')
  })

  it('XSS 载荷被净化', () => {
    const html = renderMarkdown('[点击](javascript:alert(1)) <img src=x onerror=alert(1)>')
    expect(html).not.toContain('onerror')
    expect(html).not.toContain('javascript:')
  })
})

describe('双向链接 [[...]]', () => {
  const docs = [{ slug: 'intro', title: '入门指南' }, { slug: 'cache', title: 'Cache Basics' }]

  it('按标题或 slug 解析同书章节，显示章节标题', () => {
    const html = renderMarkdown('见 [[入门指南]] 与 [[cache]]、[[cache basics]]', { bookSlug: 'my-book', docs })
    expect(html).toContain('<a href="/book/reader/my-book/intro" class="md-doc-link md-wikilink">入门指南</a>')
    expect(html).toContain('<a href="/book/reader/my-book/cache" class="md-doc-link md-wikilink">Cache Basics</a>')
    expect(html.match(/md-wikilink"/g)).toHaveLength(3)
  })

  it('支持显示文字与跨书链接', () => {
    const html = renderMarkdown('[[入门指南|从这里开始]] [[other-book/setup|安装]]', { bookSlug: 'my-book', docs })
    expect(html).toContain('href="/book/reader/my-book/intro" class="md-doc-link md-wikilink">从这里开始</a>')
    expect(html).toContain('href="/book/reader/other-book/setup" class="md-doc-link md-wikilink">安装</a>')
  })

  it('无法解析的目标渲染为缺失链接，文本转义', () => {
    const html = renderMarkdown('[[不存在的章节]] [[<b>x</b>]]', { bookSlug: 'my-book', docs })
    expect(html).toContain('<span class="md-wikilink-missing">不存在的章节</span>')
    expect(html).not.toContain('<b>')
  })

  it('未提供章节索引时按 slug 处理；代码中不解析', () => {
    expect(renderMarkdown('[[intro]]', { bookSlug: 'b' })).toContain('href="/book/reader/b/intro"')
    expect(renderMarkdown('`[[intro]]`', { bookSlug: 'b' })).not.toContain('md-wikilink')
    expect(renderMarkdown('[[intro]]')).toContain('md-wikilink-missing')
  })
})
