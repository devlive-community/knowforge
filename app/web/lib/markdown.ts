import { marked, type TokenizerAndRendererExtension, type Tokens, type Renderer } from 'marked'
import hljs from 'highlight.js'
import DOMPurify from 'isomorphic-dompurify'
import { markdownExtensions } from './markdown-extensions'

type AlertKind = 'NOTE' | 'TIP' | 'IMPORTANT' | 'WARNING' | 'CAUTION'

// GitHub 风格提示块：> [!NOTE] / TIP / IMPORTANT / WARNING / CAUTION
const alertExtension: TokenizerAndRendererExtension = {
  name: 'md-alert',
  level: 'block',
  start(src: string): number | undefined {
    return src.match(/^> ?\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]/i)?.index
  },
  tokenizer(src: string) {
    const match = /^> ?\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*\n([\s\S]*?)(?=\n(?:[^>\s]|\s*$)|$)/i.exec(src)
    if (match) {
      const inner = match[2].replace(/^> ?/gm, '')
      return {
        type: 'md-alert',
        raw: match[0],
        kind: match[1].toUpperCase() as AlertKind,
        tokens: this.lexer.blockTokens(inner.trim()),
      } as Tokens.Generic
    }
    return undefined
  },
  renderer(token: Tokens.Generic) {
    const titles: Record<AlertKind, string> = {
      NOTE: '备注', TIP: '提示', IMPORTANT: '重要', WARNING: '警告', CAUTION: '注意',
    }
    const kind = token.kind as AlertKind
    return `<div class="md-alert md-alert-${kind.toLowerCase()}">
      <div class="md-alert-title">${titles[kind] || kind}</div>
      <div class="md-alert-body">${this.parser.parse(token.tokens ?? [])}</div>
    </div>`
  },
}

// 章节内标题序号：为 H2/H3 生成稳定 id（h-1, h-2…），供“本章目录”锚点跳转
let headingSeq = 0

// 渲染时的当前书籍 slug，用于把 `doc:` 内部链接补全为阅读地址（渲染是同步的，模块级变量安全）
let currentBookSlug = ''
// 渲染时的当前书籍章节索引，用于按标题或 slug 解析 [[双向链接]]；null 表示调用方未提供
let currentDocs: WikiDoc[] | null = null

// WikiDoc 解析 [[双向链接]] 用的章节索引项。
export interface WikiDoc { slug: string; title: string }

interface WikiTreeNode { slug: string; title: string; children?: WikiTreeNode[] | null }

// wikiDocsFromTree 把章节树展平为 [[双向链接]] 的章节索引。
export function wikiDocsFromTree(nodes: WikiTreeNode[] | null | undefined): WikiDoc[] {
  return (nodes || []).flatMap((n) => [{ slug: n.slug, title: n.title }, ...wikiDocsFromTree(n.children)])
}

const escapeText = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

// resolveWikiLink 解析 [[目标]] 的目标：含 “/” 为跨书的 书籍slug/章节slug；否则在本书章节中先按 slug、再按标题（忽略大小写）匹配。
// 未提供章节索引时把目标当作本书章节 slug。无法解析返回 null。
export function resolveWikiLink(target: string, bookSlug: string, docs: WikiDoc[] | null): { href: string; title?: string } | null {
  const ref = target.trim()
  if (!ref) return null
  const href = (b: string, d: string) => `/book/reader/${encodeURIComponent(b)}/${encodeURIComponent(d)}`
  const slash = ref.indexOf('/')
  if (slash >= 0) {
    const b = ref.slice(0, slash).trim()
    const d = ref.slice(slash + 1).trim()
    return b && d ? { href: href(b, d) } : null
  }
  if (!bookSlug) return null
  if (!docs) return { href: href(bookSlug, ref) }
  const lower = ref.toLowerCase()
  const hit = docs.find((d) => d.slug.toLowerCase() === lower) || docs.find((d) => d.title.trim().toLowerCase() === lower)
  return hit ? { href: href(bookSlug, hit.slug), title: hit.title } : null
}

// 双向链接：[[章节标题或slug]]、[[目标|显示文字]]、跨书 [[书籍slug/章节slug]]；无法解析的渲染为「缺失链接」样式。
const wikiLinkExtension: TokenizerAndRendererExtension = {
  name: 'wikilink',
  level: 'inline',
  start(src: string): number | undefined {
    const i = src.indexOf('[[')
    return i >= 0 ? i : undefined
  },
  tokenizer(src: string) {
    const m = /^\[\[([^\[\]\n|]+)(?:\|([^\[\]\n]+))?\]\]/.exec(src)
    if (!m) return undefined
    return { type: 'wikilink', raw: m[0], target: m[1].trim(), label: (m[2] || '').trim() } as Tokens.Generic
  },
  renderer(token: Tokens.Generic) {
    const target = token.target as string
    const hit = resolveWikiLink(target, currentBookSlug, currentDocs)
    const text = escapeText((token.label as string) || hit?.title || target)
    if (!hit) return `<span class="md-wikilink-missing">${text}</span>`
    return `<a href="${hit.href}" class="md-doc-link md-wikilink">${text}</a>`
  },
}

const renderer: Renderer = new marked.Renderer()

// 标题内的永久链接锚点：<a …>#</a> / 零宽字符 / 图标符号，指向页内锚点（未经清理的旧采集内容）
const headingPermalinkRe = /<a\b[^>]*href="[^"]*#[^"]*"[^>]*>(?:\s|&#8203;|&#x200b;|&#35;|[​-‍⁠﻿#🔗¶§↩⚓†‡])*<\/a>/giu

renderer.heading = (rawText: string, level: number): string => {
  const text = rawText.replace(headingPermalinkRe, '').trim()
  if (level === 2 || level === 3) {
    const id = `h-${++headingSeq}`
    return `<h${level} id="${id}" class="md-h">${text}</h${level}>`
  }
  return `<h${level}>${text}</h${level}>`
}

renderer.code = (code: string, infostring: string | undefined, _escaped: boolean): string => {
  const lang = (infostring || '').match(/\S*/)?.[0] || ''
  let highlighted = ''
  if (lang && hljs.getLanguage(lang)) {
    try {
      highlighted = hljs.highlight(code, { language: lang, ignoreIllegals: true }).value
    } catch { /* fallthrough */ }
  }
  if (!highlighted) {
    try {
      highlighted = hljs.highlightAuto(code).value
    } catch {
      highlighted = code.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    }
  }
  return `<div class="md-code-block"><pre><code class="hljs">${highlighted}</code></pre></div>`
}

renderer.link = (href: string | null, _title: string | null, text: string): string => {
  const raw = href ?? ''
  // 内部文档链接：[文字](doc:章节slug) 同书跳转；[文字](doc:书slug/章节slug) 跨书跳转。
  // 渲染时补全为 /book/reader/<书>/<章节>，指向阅读页；scheme 在净化前已被改写，不会被 DOMPurify 拦掉。
  if (raw.startsWith('doc:')) {
    const ref = raw.slice(4).trim().replace(/^\/+/, '')
    let bookSlug = currentBookSlug
    let docSlug = ref
    const slash = ref.indexOf('/')
    if (slash >= 0) { bookSlug = ref.slice(0, slash); docSlug = ref.slice(slash + 1) }
    if (bookSlug && docSlug) {
      const internal = `/book/reader/${encodeURIComponent(bookSlug)}/${encodeURIComponent(docSlug)}`
      return `<a href="${internal}" class="md-doc-link">${text}</a>`
    }
    return text // 无书籍上下文且未指定书籍：降级为纯文本，避免产生死链
  }
  const external = /^https?:\/\//.test(raw)
  return `<a href="${raw}"${external ? ' target="_blank" rel="noopener noreferrer"' : ''}>${text}</a>`
}

marked.use({ renderer, extensions: [alertExtension, wikiLinkExtension, ...markdownExtensions], breaks: true, gfm: true })

// buildTocHtml 用 H2/H3 标题构建 [toc] 占位的目录内容（自身产出的安全 HTML）
function buildTocHtml(headings: Heading[]): string {
  if (!headings.length) return ''
  const items = headings
    .map((h) => {
      const text = h.text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      const indent = h.level === 3 ? 'pl-5' : ''
      return `<li class="${indent}"><a href="#${h.id}" class="text-slate-600 hover:text-primary-600">${text}</a></li>`
    })
    .join('')
  return (
    '<nav class="my-4 rounded-lg border border-slate-200 bg-slate-50 p-4">' +
    '<div class="mb-2 text-sm font-semibold text-slate-700">目录</div>' +
    `<ul class="space-y-1 text-sm" style="list-style:none;margin:0;padding:0">${items}</ul></nav>`
  )
}

// renderMarkdown 渲染 Markdown 为经过 XSS 净化的 HTML（SSR 与客户端共用）。
// options.bookSlug 提供当前书籍上下文，用于把 `doc:章节slug` 内部链接补全为阅读地址；
// options.docs 为本书章节索引，用于按标题解析 [[双向链接]]（不提供时 [[目标]] 按章节 slug 处理）。
export function renderMarkdown(source: string | null | undefined, options?: { bookSlug?: string; docs?: WikiDoc[] }): string {
  if (!source) return ''
  headingSeq = 0 // 与 extractHeadings 保持相同的编号顺序
  currentBookSlug = options?.bookSlug || ''
  currentDocs = options?.docs || null
  const html = marked.parse(source, { async: false }) as string
  let out = DOMPurify.sanitize(html, { ADD_ATTR: ['target', 'rel', 'id'] })
  // [toc] 扩展：用文档标题填充占位（须与 marked 解析使用同一份 source）
  if (out.includes('data-md-toc')) {
    out = out.replace(/<div[^>]*data-md-toc[^>]*>\s*<\/div>/g, buildTocHtml(extractHeadings(source)))
  }
  return out
}

export { bindMarkdownInteractivity } from './markdown-extensions'

// fillChildrenToc 用当前章节的直接子章节替换 [children] 占位（阅读页调用；产出为自身生成的安全 HTML）。
// 无子章节时移除占位；children 为空数组即视为无子章节。
export function fillChildrenToc(
  html: string,
  children: { slug: string; title: string; external_url?: string; external_new_tab?: boolean }[],
  bookSlug: string,
  chapterPrefix = '',
): string {
  if (!html.includes('data-md-children')) return html
  const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
  const replacement = children.length
    ? '<nav class="my-4 rounded-lg border border-slate-200 bg-slate-50 p-4">' +
      '<div class="mb-2 text-sm font-semibold text-slate-700">子章节</div>' +
      '<ul class="space-y-1 text-sm" style="list-style:none;margin:0;padding:0">' +
      children
        .map((ch) => {
          const label = esc(chapterPrefix + ch.title)
          // 外链章节：仅接受 http(s)，跳转到外部地址（默认新标签）；非法协议回退为内部阅读链接，避免 javascript: 等注入。
          const ext = (ch.external_url || '').trim()
          if (ext && /^https?:\/\//i.test(ext)) {
            const newTab = ch.external_new_tab !== false
            const attrs = newTab ? ' target="_blank" rel="noopener noreferrer nofollow"' : ''
            return `<li><a href="${esc(ext)}"${attrs} class="text-slate-600 hover:text-primary-600">${label}<span aria-hidden="true"> ↗</span></a></li>`
          }
          const href = `/book/reader?slug=${encodeURIComponent(bookSlug)}&doc=${encodeURIComponent(ch.slug)}`
          return `<li><a href="${href}" class="text-slate-600 hover:text-primary-600">${label}</a></li>`
        })
        .join('') +
      '</ul></nav>'
    : ''
  return html.replace(/<div[^>]*data-md-children[^>]*>[\s\S]*?<\/div>/g, replacement)
}


export interface Heading {
  level: number
  text: string
  id: string
}

// headingPlainText 把标题里的行内 Markdown 归约为纯文本，供目录/大纲展示。
// 例如 `[](url)3. Executing the task` → `3. Executing the task`；`**加粗**` → `加粗`。
export function headingPlainText(md: string): string {
  return md
    .replace(/\[[\s​-‍⁠﻿]*[#🔗¶§↩⚓†‡]*[\s​-‍⁠﻿]*\]\([^)]*\)/gu, '') // 永久链接锚点（[#](…#x) 等）整体移除
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1') // 链接/图片取其文本（空文本即移除）
    .replace(/[​-‍⁠﻿]/g, '') // 零宽字符
    .replace(/`([^`]+)`/g, '$1')               // 行内代码
    .replace(/(\*\*|\*|__|_|~~)/g, '')         // 加粗/斜体/删除线标记
    .replace(/<[^>]+>/g, '')                    // 残留 HTML 标签
    .replace(/\s+/g, ' ')
    .trim()
}

// extractHeadings 提取 H2/H3 目录（id 与 renderMarkdown 生成的标题 id 一致）
export function extractHeadings(source: string | null | undefined): Heading[] {
  if (!source) return []
  const headings: Heading[] = []
  let inCode = false
  let seq = 0
  source.split('\n').forEach((line) => {
    if (/^```/.test(line.trim())) inCode = !inCode
    if (inCode) return
    const m = /^(#{2,3})\s+(.+)$/.exec(line)
    if (m) headings.push({ level: m[1].length, text: headingPlainText(m[2].trim()), id: `h-${++seq}` })
  })
  return headings
}
