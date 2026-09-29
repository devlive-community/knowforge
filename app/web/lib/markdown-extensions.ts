// M17: KnowForge 旧版 marked 扩展语法的 TS 移植。
// 语法与旧版 backend/lib/extension/marked 保持兼容，输出改为纯 Tailwind 工具类 + data-* 钩子：
//   :::tabs / === "标题"        多标签页（点击切换，见 bindMarkdownInteractivity）
//   :::grid cols-3 gap-4        网格卡片（列表项分格）
//   :::diff +1 -2,4-6           行级 diff（支持行号标记与 +/- 前缀）
//   :::katex                    数学公式（服务端 katex 渲染）
//   :::mermaid                  流程图（客户端动态渲染）
//   [toc]                       章节目录占位（renderMarkdown 用标题填充）
//   !btn[文本](链接){类名}       内联按钮
//   !tip[文本](提示)            内联悬浮提示（纯 CSS）
//   !switch[文本](状态)         内联开关（静态展示）
//   :icon-name{size,color}:     内联图标（客户端 lucide 填充）
//   ![alt](url "标题" =WxH left) 图片（尺寸/对齐扩展，兼容普通图片语法）
//   |:---:|---:| 表格（对齐语法，单元格支持行内 Markdown）
//   owner/repo#123 或 #123      GitHub issue 链接徽章
//   :::api METHOD /path         REST API 文档卡（=== "小节" 分节）
import { marked, type TokenizerAndRendererExtension, type Tokens } from 'marked'
import katex from 'katex'

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;')
}

// ── 块级定界扫描（::: 系列与 <Tabs>/<Note> 等 HTML 块共用）─────────────────
// 旧实现用 indexOf('\n:::') / 非贪婪正则找块尾，不理解 Markdown 结构，
// 导致代码块中的 :::、=== "x"、</Tabs> 等文本被误判，同名块也无法嵌套。
// 这里统一为“围栏感知 + 深度计数”的逐行扫描。

// 围栏标记允许任意缩进：迁移自其他文档系统的 <Tab>/<Tip> 内容往往整体缩进，
// 围栏行也随内容一起缩进，若只识别行首围栏会丢失围栏状态。
const FENCE_OPEN = /^\s*(`{3,}|~{3,})/

// lineFence 根据当前行更新围栏状态：返回 '' 表示不在围栏内，否则为围栏字符（` 或 ~）
function lineFence(fence: string, line: string): string {
  if (fence) {
    if (new RegExp(`^\\s*${fence}{3,}[ \\t]*$`).test(line)) return ''
    return fence
  }
  const open = FENCE_OPEN.exec(line)
  return open ? open[1][0] : fence
}

// dedentBlock 去除块内各行的公共缩进（HTML 标签块迁移内容通常整体缩进，
// 不去缩会被 Markdown 解析为缩进代码块）。空行不参与最小缩进计算，
// 各行超出公共缩进的部分保留（相对缩进不丢失）。
export function dedentBlock(content: string): string {
  const lines = content.split('\n')
  let min = Infinity
  for (const line of lines) {
    if (!line.trim()) continue
    const indent = line.match(/^[ \t]*/)![0].length
    if (indent < min) min = indent
  }
  if (!Number.isFinite(min) || min === 0) return content
  return lines.map((line) => line.slice(min)).join('\n')
}

// 行区间 [start, end) 在原始字符串中的偏移换算用：把 lines 切回字符串时行间有 '\n'
function linesOffset(lines: string[], end: number): number {
  return lines.slice(0, end).join('\n').length + (end > 0 ? 1 : 0)
}

// scanDelimitedBlock 扫描 ::: 系列块（tabs/grid/diff/katex/mermaid/api）的内容。
// headerLen 为头部（含换行）长度；带块名的 `:::xxx` 行加深嵌套，单独的 `:::` 行减浅，
// 外层深度归零时块结束。围栏代码内的 ::: 与 === 均不参与判定。
function scanDelimitedBlock(src: string, headerLen: number): { content: string; raw: string } | undefined {
  const lines = src.slice(headerLen).split('\n')
  let fence = ''
  let depth = 0
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    fence = lineFence(fence, line)
    if (fence) continue
    if (/^:::\s*\S/.test(line)) {
      depth++
      continue
    }
    if (/^:::\s*$/.test(line)) {
      if (depth === 0) {
        const offset = linesOffset(lines, i)
        return { content: lines.slice(0, i).join('\n'), raw: src.slice(0, headerLen + offset + line.length) }
      }
      depth--
    }
  }
  return undefined
}

interface TagBlockEvent {
  pos: number
  len: number
  open: boolean
  attrs: string
}

// collectTagEvents 收集一行内按出现顺序排列的开/闭标签事件（pos 为相对 line 的偏移）
function collectTagEvents(line: string, tag: string): TagBlockEvent[] {
  const events: TagBlockEvent[] = []
  const openRe = new RegExp(`<${tag}(\\s[^>]*)?>`, 'gi')
  const closeRe = new RegExp(`</${tag}[ \\t]*>`, 'gi')
  let om: RegExpExecArray | null
  let cm: RegExpExecArray | null
  while ((om = openRe.exec(line))) events.push({ pos: om.index, len: om[0].length, open: true, attrs: om[1] || '' })
  while ((cm = closeRe.exec(line))) events.push({ pos: cm.index, len: cm[0].length, open: false, attrs: '' })
  return events.sort((a, b) => a.pos - b.pos)
}

// scanTagBlock 扫描 HTML 标签块（<Tabs>、<Note> 等）的内容，支持同名嵌套，
// 围栏代码内的闭合标签不参与判定。openMatch 为开头标签的匹配结果。
export function scanTagBlock(src: string, tag: string, openMatch: RegExpExecArray): { content: string; raw: string } | undefined {
  const bodyStart = openMatch.index + openMatch[0].length
  const lines = src.slice(bodyStart).split('\n')
  let fence = ''
  let depth = 1 // 外层开标签已由 openMatch 消费
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    fence = lineFence(fence, line)
    if (fence) continue
    let closeStart = -1
    let closeLen = 0
    for (const ev of collectTagEvents(line, tag)) {
      if (ev.open) depth++
      else {
        depth--
        if (depth === 0) {
          closeStart = ev.pos
          closeLen = ev.len
          break
        }
      }
    }
    if (closeStart >= 0) {
      const offset = linesOffset(lines, i)
      const bodyLen = offset + closeStart
      return {
        content: src.slice(bodyStart, bodyStart + bodyLen),
        raw: src.slice(0, bodyStart + offset + closeLen),
      }
    }
  }
  return undefined
}

// extractTagBlocks 从 content 中按顺序提取成对的 <Tag attrs>…</Tag> 子块，
// 围栏代码内的标签不参与判定，同名嵌套按深度配对（内层块交给后续重新分词处理）。
export function extractTagBlocks(content: string, tag: string): { attrs: string; body: string }[] {
  const lines = content.split('\n')
  let fence = ''
  let depth = 0
  let pending: { attrs: string; bodyStart: number } | null = null
  const blocks: { attrs: string; body: string }[] = []
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const lineStart = linesOffset(lines, i)
    fence = lineFence(fence, line)
    if (fence) continue
    for (const ev of collectTagEvents(line, tag)) {
      const abs = lineStart + ev.pos
      if (ev.open) {
        if (depth === 0) pending = { attrs: ev.attrs, bodyStart: abs + ev.len }
        depth++
      } else if (depth > 0) {
        depth--
        if (depth === 0 && pending) {
          blocks.push({ attrs: pending.attrs, body: content.slice(pending.bodyStart, abs) })
          pending = null
        }
      }
    }
  }
  return blocks
}

// inInlineCode 旧版规则：占位起始位置之前的反引号数量为奇数时视为处于行内代码中
function inInlineCode(src: string, index: number): boolean {
  const backticks = src.slice(0, index).match(/`/g)
  return !!backticks && backticks.length % 2 === 1
}

// ── :::tabs ──────────────────────────────────────────────────────────────

interface MdTab {
  title: string
  tokens: Tokens.Generic[]
}

const tabsExtension: TokenizerAndRendererExtension = {
  name: 'md-tabs',
  level: 'block',
  start(src) {
    return src.match(/^:::\s*tabs\s*\n/)?.index
  },
  tokenizer(src) {
    const header = /^:::\s*tabs\s*\n/.exec(src)
    if (!header) return undefined
    const block = scanDelimitedBlock(src, header[0].length)
    if (!block) return undefined

    // 围栏感知地切分 === "标题"：代码块内的 === 行不会被误判为新 Tab
    const tabs: { title: string; src: string }[] = []
    let current: { title: string; src: string } | null = null
    let fence = ''
    for (const line of block.content.split('\n')) {
      if (fence) {
        fence = lineFence(fence, line)
        if (current) current.src += line + '\n'
        continue
      }
      if (FENCE_OPEN.test(line)) {
        fence = lineFence(fence, line)
        if (current) current.src += line + '\n'
        continue
      }
      const m = /^===\s*"([^"]+)"\s*$/.exec(line.trim())
      if (m) {
        current = { title: m[1], src: '' }
        tabs.push(current)
        continue
      }
      if (current) current.src += line + '\n'
    }
    if (tabs.length === 0) return undefined
    const raw = block.raw
    return {
      type: 'md-tabs',
      raw,
      tabs: tabs.map((t) => ({ title: t.title, tokens: this.lexer.blockTokens(t.src) })),
    } as Tokens.Generic
  },
  renderer(token) {
    const tabs = (token as Tokens.Generic & { tabs: MdTab[] }).tabs
    return tabsMarkup(tabs.map((t) => t.title), tabs.map((t) => this.parser.parse(t.tokens)))
  },
}

// tabsMarkup 输出多标签页 HTML（:::tabs 与 <Tabs> 共用）；panelsHtml 为各标签已解析的 HTML。
function tabsMarkup(titles: string[], panelsHtml: string[]): string {
  const gid = `md-tabs-${Math.random().toString(36).slice(2, 9)}`
  const buttons = titles
    .map(
      (title, i) =>
        `<button type="button" data-md-tab="${gid}" data-md-tab-index="${i}" role="tab" aria-selected="${i === 0}"` +
        ` class="min-h-10 shrink-0 rounded-lg border px-4 py-2 text-sm font-medium transition-colors focus:outline-none` +
        (i === 0 ? ' border-slate-200 bg-white text-primary-700 shadow-sm' : ' border-transparent text-slate-500 hover:bg-white/70 hover:text-slate-800') +
        `">${escapeHtml(title)}</button>`
    )
    .join('')
  const panels = panelsHtml
    .map(
      (html, i) =>
        `<div data-md-tab-panel="${gid}" data-md-tab-index="${i}" role="tabpanel"` +
        ` class="pt-4 ${i === 0 ? '' : 'hidden'}">${html}</div>`
    )
    .join('')
  return `<div class="my-4"><div class="flex max-w-full gap-1 overflow-x-auto rounded-xl border border-slate-200 bg-slate-100/80 p-1 shadow-inner" role="tablist">${buttons}</div>${panels}</div>`
}

// ── <Tabs><Tab title="X"> ─────────────────────────────────────────────────
// HTML 标签风格的多标签页（等价于 :::tabs，便于从其他文档系统迁移）
const tabsTagExtension: TokenizerAndRendererExtension = {
  name: 'md-tabs-tag',
  level: 'block',
  start(src) {
    return src.match(/<Tabs\s*>/i)?.index
  },
  tokenizer(src) {
    const open = /<Tabs\s*>/i.exec(src)
    // 标签必须从当前位置开始匹配，否则内层同名标签会把外层块吞掉
    if (!open || open.index !== 0) return undefined
    const block = scanTagBlock(src, 'Tabs', open)
    if (!block) return undefined
    // 围栏感知 + 同名嵌套配对地提取 <Tab title="…"> 子块
    const tabs: { title: string; tokens: Tokens.Generic[] }[] = []
    for (const tab of extractTagBlocks(block.content, 'Tab')) {
      const titleMatch = /title\s*=\s*["']([^"']*)["']/i.exec(tab.attrs)
      if (titleMatch) tabs.push({ title: titleMatch[1], tokens: this.lexer.blockTokens(dedentBlock(tab.body).trim()) })
    }
    if (tabs.length === 0) return undefined
    return { type: 'md-tabs-tag', raw: block.raw, tabs } as Tokens.Generic
  },
  renderer(token) {
    const tabs = (token as Tokens.Generic & { tabs: MdTab[] }).tabs
    return tabsMarkup(tabs.map((t) => t.title), tabs.map((t) => this.parser.parse(t.tokens)))
  },
}

// ── <Note> / <Tip> / <Warning> / <Info> / <Caution> / <Important> ─────────
// HTML 标签风格的提示块；首个 **加粗** 视为标题（可省略，省略时用默认标题）
type CalloutKind = 'note' | 'tip' | 'warning' | 'info' | 'caution' | 'important'
const calloutStyle: Record<CalloutKind, string> = {
  note: 'note', tip: 'tip', warning: 'warning', info: 'note', caution: 'caution', important: 'important',
}
const calloutDefaultTitle: Record<CalloutKind, string> = {
  note: '备注', tip: '提示', warning: '警告', info: '信息', caution: '注意', important: '重要',
}

const calloutTagExtension: TokenizerAndRendererExtension = {
  name: 'md-callout-tag',
  level: 'block',
  start(src) {
    return src.match(/<(Note|Tip|Warning|Info|Caution|Important)\s*>/i)?.index
  },
  tokenizer(src) {
    const open = /<(Note|Tip|Warning|Info|Caution|Important)\s*>/i.exec(src)
    // 标签必须从当前位置开始匹配，否则内层 Tip 等标签会把外层块吞掉
    if (!open || open.index !== 0) return undefined
    const block = scanTagBlock(src, open[1], open)
    if (!block) return undefined
    let body = dedentBlock(block.content).trim()
    let title = ''
    // 可选：开头 **标题** 作为提示块标题
    const tm = /^\*\*(.+?)\*\*[ \t]*\n?/.exec(body)
    if (tm) {
      title = tm[1].trim()
      body = body.slice(tm[0].length).trim()
    }
    return {
      type: 'md-callout-tag',
      raw: block.raw,
      kind: open[1].toLowerCase() as CalloutKind,
      title,
      tokens: this.lexer.blockTokens(body),
    } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & { kind: CalloutKind; title: string }
    const style = calloutStyle[t.kind] || 'note'
    const title = t.title || calloutDefaultTitle[t.kind] || t.kind
    return `<div class="md-alert md-alert-${style}">
      <div class="md-alert-title">${escapeHtml(title)}</div>
      <div class="md-alert-body">${this.parser.parse(token.tokens ?? [])}</div>
    </div>`
  },
}

// ── <AccordionGroup> / <Accordion title="…"> ─────────────────────────────
// 可折叠面板；内容递归解析（可嵌套代码块、Tabs、Tip 等任意块）。用原生 <details> 实现，无需 JS。
const accordionExtension: TokenizerAndRendererExtension = {
  name: 'md-accordion',
  level: 'block',
  start(src) {
    return src.match(/<Accordion(?=[\s>])/i)?.index
  },
  tokenizer(src) {
    const open = /<Accordion(\s[^>]*)?>/i.exec(src)
    if (!open || open.index !== 0) return undefined
    const block = scanTagBlock(src, 'Accordion', open)
    if (!block) return undefined
    const titleMatch = /title\s*=\s*["']([^"']*)["']/i.exec(open[1] || '')
    return {
      type: 'md-accordion',
      raw: block.raw,
      title: titleMatch ? titleMatch[1] : '',
      tokens: this.lexer.blockTokens(dedentBlock(block.content).trim()),
    } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & { title: string }
    return (
      '<details class="group my-2 overflow-hidden rounded-lg border border-slate-200">' +
      '<summary class="flex cursor-pointer list-none items-center justify-between gap-2 bg-slate-50 px-4 py-2.5 text-sm font-medium text-slate-800 hover:bg-slate-100">' +
      `<span>${escapeHtml(t.title)}</span>` +
      '<i class="fa-solid fa-chevron-down text-xs text-slate-400 transition-transform group-open:rotate-180" aria-hidden="true"></i>' +
      `</summary><div class="px-4 py-3">${this.parser.parse(token.tokens ?? [])}</div></details>`
    )
  },
}

const accordionGroupExtension: TokenizerAndRendererExtension = {
  name: 'md-accordion-group',
  level: 'block',
  start(src) {
    return src.match(/<AccordionGroup\s*>/i)?.index
  },
  tokenizer(src) {
    const open = /<AccordionGroup\s*>/i.exec(src)
    if (!open || open.index !== 0) return undefined
    const block = scanTagBlock(src, 'AccordionGroup', open)
    if (!block) return undefined
    // 内容里的 <Accordion> 由 accordionExtension 递归解析
    return {
      type: 'md-accordion-group',
      raw: block.raw,
      tokens: this.lexer.blockTokens(dedentBlock(block.content).trim()),
    } as Tokens.Generic
  },
  renderer(token) {
    return `<div class="my-4 space-y-2">${this.parser.parse(token.tokens ?? [])}</div>`
  },
}

// ── <Steps> / <Step title="…"> ────────────────────────────────────────────
// 有序步骤列表；每步内容递归解析。Steps 自行提取 Step 子块以便编号。
const stepsExtension: TokenizerAndRendererExtension = {
  name: 'md-steps',
  level: 'block',
  start(src) {
    return src.match(/<Steps\s*>/i)?.index
  },
  tokenizer(src) {
    const open = /<Steps\s*>/i.exec(src)
    if (!open || open.index !== 0) return undefined
    const block = scanTagBlock(src, 'Steps', open)
    if (!block) return undefined
    const steps: { title: string; tokens: Tokens.Generic[] }[] = []
    for (const step of extractTagBlocks(block.content, 'Step')) {
      const titleMatch = /title\s*=\s*["']([^"']*)["']/i.exec(step.attrs)
      steps.push({ title: titleMatch ? titleMatch[1] : '', tokens: this.lexer.blockTokens(dedentBlock(step.body).trim()) })
    }
    if (steps.length === 0) return undefined
    return { type: 'md-steps', raw: block.raw, steps } as Tokens.Generic
  },
  renderer(token) {
    const steps = (token as Tokens.Generic & { steps: MdTab[] }).steps
    const items = steps
      .map((s, i) => (
        '<li class="relative pb-5 pl-9 last:pb-0">' +
        (i < steps.length - 1 ? '<span class="absolute left-[13px] top-7 h-[calc(100%-1.5rem)] w-px bg-slate-200" aria-hidden="true"></span>' : '') +
        `<span class="absolute left-0 top-0 flex h-7 w-7 items-center justify-center rounded-full bg-primary-50 text-xs font-semibold text-primary-700">${i + 1}</span>` +
        (s.title ? `<div class="mb-1 pt-1 text-sm font-semibold text-slate-900">${escapeHtml(s.title)}</div>` : '') +
        `<div class="text-sm leading-6 text-slate-600">${this.parser.parse(s.tokens)}</div></li>`
      ))
      .join('')
    return `<ol class="my-4 list-none space-y-0 pl-0">${items}</ol>`
  },
}

// ── <CardGroup cols={2}> / <Card title="…" icon="…" href="…"> ────────────────
// 卡片与卡片组（兼容 Mintlify 写法）：icon 为 Font Awesome 图标名（如 headset，可用 iconType 指定 regular/brands）
// 或图片地址；href 使整张卡片可点击（站外链接新窗口打开）；horizontal 时图标与标题同行；color 为图标颜色。
// 卡片正文递归解析；卡片组按 cols（1–4）分列，窄屏为单列。

// parseTagAttrs 解析标签属性：name="v" / name='v' / name={v} / 仅写属性名（视为 true）
export function parseTagAttrs(attrs: string): Record<string, string> {
  const out: Record<string, string> = {}
  const re = /([a-zA-Z][\w-]*)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|\{([^}]*)\}))?/g
  let m: RegExpExecArray | null
  while ((m = re.exec(attrs))) {
    const raw = m[2] ?? m[3] ?? m[4]
    out[m[1]] = raw === undefined ? 'true' : raw.trim().replace(/^["'](.*)["']$/, '$1')
  }
  return out
}

const cardGroupCols: Record<number, string> = {
  1: 'grid-cols-1',
  2: 'grid-cols-1 sm:grid-cols-2',
  3: 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-3',
  4: 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-4',
}

// safeCardHref 只接受站内路径、锚点与 http(s)/mailto 链接
function safeCardHref(href: string): string {
  const v = href.trim()
  return /^(https?:\/\/|mailto:|\/(?!\/)|#)/i.test(v) ? v : ''
}

function cardIconMarkup(icon: string, iconType: string, color: string): string {
  if (!icon) return ''
  const style = /^(#[0-9a-f]{3,8}|[a-z]+)$/i.test(color) ? ` style="color:${color}"` : ''
  if (/^(https?:\/\/|\/(?!\/))/i.test(icon)) {
    return `<img src="${escapeHtml(icon)}" alt="" class="h-6 w-6 shrink-0 object-contain" />`
  }
  if (!/^[a-z0-9-]+$/i.test(icon)) return ''
  const family = iconType === 'regular' ? 'fa-regular' : iconType === 'brands' ? 'fa-brands' : 'fa-solid'
  return `<i class="${family} fa-${icon.toLowerCase()} shrink-0 text-xl text-primary-600"${style} aria-hidden="true"></i>`
}

const cardExtension: TokenizerAndRendererExtension = {
  name: 'md-card',
  level: 'block',
  start(src) {
    return src.match(/<Card(?=[\s>])/)?.index
  },
  tokenizer(src) {
    const open = /<Card(\s[^>]*)?>/.exec(src)
    if (!open || open.index !== 0) return undefined
    const block = scanTagBlock(src, 'Card', open)
    if (!block) return undefined
    const attrs = parseTagAttrs(open[1] || '')
    return {
      type: 'md-card',
      raw: block.raw,
      title: attrs.title || '',
      icon: attrs.icon || '',
      iconType: attrs.iconType || '',
      color: attrs.color || '',
      href: safeCardHref(attrs.href || ''),
      horizontal: attrs.horizontal === 'true',
      tokens: this.lexer.blockTokens(dedentBlock(block.content).trim()),
    } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & { title: string; icon: string; iconType: string; color: string; href: string; horizontal: boolean }
    const icon = cardIconMarkup(t.icon, t.iconType, t.color)
    const title = t.title ? `<div class="font-semibold text-slate-900">${escapeHtml(t.title)}</div>` : ''
    const body = this.parser.parse(token.tokens ?? [])
    const bodyHtml = body.trim() ? `<div class="md-card-body mt-1 text-sm leading-6 text-slate-600 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">${body}</div>` : ''
    const head = t.horizontal
      ? `<div class="flex items-center gap-3">${icon}${title}</div>`
      : `${icon ? `<div class="mb-3">${icon}</div>` : ''}${title}`
    const cls = 'md-card my-4 block rounded-xl border border-slate-200 bg-white p-5 transition-colors'
    if (!t.href) return `<div class="${cls}">${head}${bodyHtml}</div>`
    const external = /^https?:\/\//i.test(t.href)
    const rel = external ? ' target="_blank" rel="noopener noreferrer"' : ''
    return `<a href="${escapeHtml(t.href)}"${rel} class="${cls} no-underline hover:border-primary-400 hover:shadow-sm">${head}${bodyHtml}</a>`
  },
}

const cardGroupExtension: TokenizerAndRendererExtension = {
  name: 'md-card-group',
  level: 'block',
  start(src) {
    return src.match(/<CardGroup(?=[\s>])/)?.index
  },
  tokenizer(src) {
    const open = /<CardGroup(\s[^>]*)?>/.exec(src)
    if (!open || open.index !== 0) return undefined
    const block = scanTagBlock(src, 'CardGroup', open)
    if (!block) return undefined
    const cols = Math.min(Math.max(parseInt(parseTagAttrs(open[1] || '').cols || '2', 10) || 2, 1), 4)
    // 内容里的 <Card> 由 cardExtension 递归解析
    return {
      type: 'md-card-group',
      raw: block.raw,
      cols,
      tokens: this.lexer.blockTokens(dedentBlock(block.content).trim()),
    } as Tokens.Generic
  },
  renderer(token) {
    const cols = (token as Tokens.Generic & { cols: number }).cols
    return `<div class="md-card-group my-4 grid gap-4 ${cardGroupCols[cols]} [&>*]:my-0">${this.parser.parse(token.tokens ?? [])}</div>`
  },
}

// ── :::grid ──────────────────────────────────────────────────────────────

const gridExtension: TokenizerAndRendererExtension = {
  name: 'md-grid',
  level: 'block',
  start(src) {
    return src.match(/^:::\s*grid(?:\s+[\w-]+)*\s*\n/)?.index
  },
  tokenizer(src) {
    const header = /^:::\s*grid((?:\s+[\w-]+)*)\s*\n/.exec(src)
    if (!header) return undefined
    const block = scanDelimitedBlock(src, header[0].length)
    if (!block) return undefined
    const content = block.content
    const raw = block.raw

    let cols = 2
    let gap = 4
    let responsive = true
    for (const opt of header[1].trim().split(/\s+/).filter(Boolean)) {
      if (opt.startsWith('cols-')) cols = Math.min(Math.max(parseInt(opt.slice(5), 10) || 2, 1), 6)
      else if (opt.startsWith('gap-')) gap = Math.min(Math.max(parseInt(opt.slice(4), 10) || 4, 0), 12)
      else if (opt === 'no-responsive') responsive = false
    }

    // 列表项切分：列表标记开新格，缩进续行并入当前格
    const items: string[] = []
    let currentItem: string[] = []
    let collecting = false
    let itemFence = ''
    for (const line of content.split('\n')) {
      // 围栏代码内的行不参与列表项切分
      itemFence = lineFence(itemFence, line)
      if (itemFence) {
        if (collecting) currentItem.push(line)
        continue
      }
      const marker = line.match(/^(\s*)([-*+]|\d+\.)\s+(.*)$/)
      if (marker) {
        if (collecting) items.push(currentItem.join('\n').trim())
        currentItem = [marker[3]]
        collecting = true
        continue
      }
      if (collecting) currentItem.push(line)
    }
    if (collecting) items.push(currentItem.join('\n').trim())
    if (items.length === 0) return undefined
    return {
      type: 'md-grid',
      raw,
      items: items.map((item) => ({ tokens: this.lexer.blockTokens(item) })),
      cols,
      gap,
      responsive,
    } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & { items: { tokens: Tokens.Generic[] }[]; cols: number; gap: number; responsive: boolean }
    const cells = t.items
      .map(
        (item) =>
          `<div class="p-4 border border-slate-200 bg-white rounded-lg transition-shadow duration-200 hover:shadow-md">` +
          `${this.parser.parse(item.tokens)}</div>`
      )
      .join('')
    const columns = t.responsive
      ? `repeat(auto-fit, minmax(min(100%, ${Math.floor(100 / t.cols)}%), 1fr))`
      : `repeat(${t.cols}, minmax(0, 1fr))`
    return `<div class="my-4 w-full grid" style="gap:${(t.gap * 0.25).toFixed(2)}rem;grid-template-columns:${columns}">${cells}</div>`
  },
}

// ── :::diff ──────────────────────────────────────────────────────────────

const diffExtension: TokenizerAndRendererExtension = {
  name: 'md-diff',
  level: 'block',
  start(src) {
    return src.match(/^:::\s*diff(?:\s+[+-][\d,-]+)*\s*\n/)?.index
  },
  tokenizer(src) {
    const header = /^:::\s*diff((?:\s+[+-][\d,-]+)*)\s*\n/.exec(src)
    if (!header) return undefined
    const block = scanDelimitedBlock(src, header[0].length)
    if (!block) return undefined
    const content = block.content
    const raw = block.raw

    const addLines = new Set<number>()
    const deleteLines = new Set<number>()
    for (const opt of header[1].trim().split(/\s+/).filter(Boolean)) {
      const type = opt[0]
      if (type !== '+' && type !== '-') continue
      for (const range of opt.slice(1).split(',')) {
        if (range.includes('-')) {
          const [start, stop] = range.split('-').map(Number)
          if (!Number.isNaN(start) && !Number.isNaN(stop)) {
            for (let i = start; i <= stop; i++) (type === '+' ? addLines : deleteLines).add(i)
          }
        } else {
          const num = Number(range)
          if (!Number.isNaN(num)) (type === '+' ? addLines : deleteLines).add(num)
        }
      }
    }
    const lines = content.split('\n')
    if (lines[lines.length - 1] === '') lines.pop()
    return {
      type: 'md-diff',
      raw,
      lines: lines.map((line, index) => {
        const lineNo = index + 1
        let type = 'context'
        if (addLines.has(lineNo)) type = 'addition'
        else if (deleteLines.has(lineNo)) type = 'deletion'
        else if (line.startsWith('+')) type = 'addition'
        else if (line.startsWith('-')) type = 'deletion'
        return { type, text: type === 'context' ? line : line.slice(1) }
      }),
    } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & { lines: { type: string; text: string }[] }
    const rows = t.lines
      .map((line) => {
        const prefix = line.type === 'addition' ? '+' : line.type === 'deletion' ? '−' : ' '
        const bg = line.type === 'addition' ? 'bg-emerald-50' : line.type === 'deletion' ? 'bg-rose-50' : ''
        const fg = line.type === 'addition' ? 'text-emerald-600' : line.type === 'deletion' ? 'text-rose-600' : 'text-slate-400'
        return (
          `<div class="flex px-2 py-0.5 ${bg}">` +
          `<span class="w-4 shrink-0 select-none font-mono ${fg}">${prefix}</span>` +
          `<span class="flex-1 ml-1 font-mono whitespace-pre-wrap">${escapeHtml(line.text)}</span></div>`
        )
      })
      .join('')
    return `<div class="my-4 block w-full bg-slate-50 border border-slate-200 rounded-lg overflow-hidden"><div class="overflow-x-auto"><pre class="min-w-full w-max py-2"><code>${rows}</code></pre></div></div>`
  },
}

// ── :::katex / :::mermaid ────────────────────────────────────────────────

const katexExtension: TokenizerAndRendererExtension = {
  name: 'md-katex',
  level: 'block',
  start(src) {
    return src.match(/^:::\s*katex\n/)?.index
  },
  tokenizer(src) {
    const header = /^:::\s*katex\n/.exec(src)
    if (!header) return undefined
    const block = scanDelimitedBlock(src, header[0].length)
    if (!block) return undefined
    return { type: 'md-katex', raw: block.raw, content: block.content.trim() } as Tokens.Generic
  },
  renderer(token) {
    const content = (token as Tokens.Generic & { content: string }).content
    let html: string
    try {
      html = katex.renderToString(content, { displayMode: true, throwOnError: false })
    } catch {
      html = `<code class="text-rose-600">${escapeHtml(content)}</code>`
    }
    return `<div class="my-4 overflow-x-auto">${html}</div>`
  },
}

const mermaidExtension: TokenizerAndRendererExtension = {
  name: 'md-mermaid',
  level: 'block',
  start(src) {
    return src.match(/^:::\s*mermaid\n/)?.index
  },
  tokenizer(src) {
    const header = /^:::\s*mermaid\n/.exec(src)
    if (!header) return undefined
    const block = scanDelimitedBlock(src, header[0].length)
    if (!block) return undefined
    return { type: 'md-mermaid', raw: block.raw, content: block.content.trim() } as Tokens.Generic
  },
  renderer(token) {
    const content = escapeHtml((token as Tokens.Generic & { content: string }).content)
    return `<div class="md-mermaid my-4 flex justify-center overflow-x-auto rounded-lg border border-slate-200 bg-white p-4"><pre class="text-sm text-slate-600">${content}</pre></div>`
  },
}

// ── [toc] ────────────────────────────────────────────────────────────────

const tocExtension: TokenizerAndRendererExtension = {
  name: 'md-toc',
  level: 'block',
  start(src) {
    return src.match(/^\[toc\]\s*$/im)?.index
  },
  tokenizer(src) {
    const m = /^\[toc\]\s*\n?/i.exec(src)
    if (!m) return undefined
    return { type: 'md-toc', raw: m[0] } as Tokens.Generic
  },
  renderer() {
    return '<div data-md-toc="1" class="my-4"></div>'
  },
}

// ── [children] 子章节目录 ─────────────────────────────────────────────────
// 占位符：阅读页用当前章节的直接子章节链接填充；编辑器预览保留占位提示。
const childrenTocExtension: TokenizerAndRendererExtension = {
  name: 'md-children',
  level: 'block',
  start(src) {
    return src.match(/^\[children\]\s*$/im)?.index
  },
  tokenizer(src) {
    const m = /^\[children\]\s*\n?/i.exec(src)
    if (!m) return undefined
    return { type: 'md-children', raw: m[0] } as Tokens.Generic
  },
  renderer() {
    return '<div data-md-children="1" class="my-4 rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-3 text-sm text-slate-500">子章节目录</div>'
  },
}

// ── 内联扩展 ─────────────────────────────────────────────────────────────

const buttonExtension: TokenizerAndRendererExtension = {
  name: 'md-button',
  level: 'inline',
  start(src) {
    const index = src.indexOf('!btn[')
    if (index === -1 || inInlineCode(src, index)) return undefined
    return index
  },
  tokenizer(src) {
    const rule = /^!btn\[(.*?)\](?:\((.*?)\))?(?:\{(.*?)\})?/
    const match = rule.exec(src)
    if (!match || inInlineCode(src, match.index)) return undefined
    return {
      type: 'md-button',
      raw: match[0],
      text: match[1],
      link: match[2] || '',
      className: match[3] || '',
      tokens: [],
    } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & { text: string; link: string; className: string }
    const base = 'inline-flex items-center justify-center px-2.5 py-1.5 rounded-md text-xs font-medium transition-colors'
    const style = t.className || 'bg-primary-600 text-white hover:bg-primary-700'
    if (!t.link) {
      return `<button type="button" class="${base} ${style}"><span class="inline-flex items-center">${escapeHtml(t.text)}</span></button>`
    }
    const external = /^https?:\/\//.test(t.link)
    const rel = external ? ' target="_blank" rel="noopener noreferrer"' : ''
    return `<a href="${escapeHtml(t.link)}"${rel} class="${base} ${style}"><span class="inline-flex items-center">${escapeHtml(t.text)}</span></a>`
  },
}

const tipExtension: TokenizerAndRendererExtension = {
  name: 'md-tip',
  level: 'inline',
  start(src) {
    const index = src.indexOf('!tip[')
    if (index === -1 || inInlineCode(src, index)) return undefined
    return index
  },
  tokenizer(src) {
    const match = /^!tip\[(.*?)\]\((.*?)\)/.exec(src)
    if (!match || inInlineCode(src, match.index)) return undefined
    return { type: 'md-tip', raw: match[0], text: match[1], tooltip: match[2], tokens: [] } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & { text: string; tooltip: string }
    return (
      '<span class="relative inline-block group">' +
      `<span class="cursor-help border-b border-dotted border-slate-500">${escapeHtml(t.text)}</span>` +
      '<span class="pointer-events-none invisible opacity-0 group-hover:visible group-hover:opacity-100 transition-opacity duration-200 absolute z-50 bottom-full left-1/2 -translate-x-1/2 mb-2 block">' +
      '<span class="relative block" style="min-width:max-content;max-width:24rem">' +
      `<span class="block bg-slate-800 text-white px-3 py-2 rounded-lg text-sm">${escapeHtml(t.tooltip)}</span>` +
      '<span class="absolute w-0 h-0 border-4 bottom-0 left-1/2 -translate-x-1/2 translate-y-full border-t-slate-800 border-x-transparent border-b-transparent"></span>' +
      '</span></span></span>'
    )
  },
}

const switchExtension: TokenizerAndRendererExtension = {
  name: 'md-switch',
  level: 'inline',
  start(src) {
    const index = src.indexOf('!switch[')
    if (index === -1 || inInlineCode(src, index)) return undefined
    return index
  },
  tokenizer(src) {
    const match = /^!switch\[(.*?)\](?:\((.*?)\))?(?:\{(.*?)\})?/.exec(src)
    if (!match || inInlineCode(src, match.index)) return undefined
    const state = (match[2] || '').toLowerCase()
    return {
      type: 'md-switch',
      raw: match[0],
      text: match[1],
      checked: ['true', 'on', '1', 'yes'].includes(state),
      tokens: [],
    } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & { text: string; checked: boolean }
    const knob = t.checked ? 'translate-x-4' : 'translate-x-0.5'
    const track = t.checked ? 'bg-primary-500' : 'bg-slate-300'
    return (
      '<span class="inline-flex items-center gap-2 align-middle">' +
      `<span class="relative inline-flex h-5 w-9 shrink-0 rounded-full transition-colors ${track}">` +
      `<span class="absolute left-0 top-0.5 h-4 w-4 rounded-full bg-white shadow transition-transform ${knob}"></span></span>` +
      `<span class="text-sm">${escapeHtml(t.text)}</span></span>`
    )
  },
}

const iconExtension: TokenizerAndRendererExtension = {
  name: 'md-icon',
  level: 'inline',
  start(src) {
    const match = src.match(/:([a-zA-Z][a-zA-Z-]+)(?:\{[^}]+\})?:/)
    if (!match || match.index === undefined || inInlineCode(src, match.index)) return undefined
    return match.index
  },
  tokenizer(src) {
    const match = /^:([a-zA-Z][a-zA-Z-]+)(?:\{([^}]+)\})?:/.exec(src)
    if (!match || inInlineCode(src, match.index)) return undefined
    const [size = '20', color = 'currentColor'] = (match[2] || '').split(',').map((p) => p.trim())
    return {
      type: 'md-icon',
      raw: match[0],
      iconName: match[1],
      size: /^\d+$/.test(size) ? size : '20',
      color,
      tokens: [],
    } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & { iconName: string; size: string; color: string }
    return (
      `<i data-md-icon="${escapeHtml(t.iconName.toLowerCase())}"` +
      ` style="width:${t.size}px;height:${t.size}px;color:${escapeHtml(t.color)}"` +
      ' class="inline-flex items-center justify-center relative top-[3px]"></i>'
    )
  },
}


// markdownExtensions 注册进 marked 的扩展集合
// ── 图片（尺寸/对齐）─────────────────────────────────────────────────────

const imageExtension: TokenizerAndRendererExtension = {
  name: 'md-image',
  level: 'inline',
  start(src) {
    const index = src.indexOf('![')
    if (index === -1 || inInlineCode(src, index)) return undefined
    return index
  },
  tokenizer(src) {
    const rule = /^!\[(.*?)\]\((.*?)(?:\s+"(.*?)")?\s*(?:=(\d+)?x(\d+)?)?(?:\s+(left|center|right))?\)/
    const match = rule.exec(src)
    if (!match || inInlineCode(src, match.index)) return undefined
    return {
      type: 'md-image',
      raw: match[0],
      alt: match[1],
      href: match[2],
      title: match[3] || null,
      width: match[4] || null,
      height: match[5] || null,
      align: match[6] || null,
      tokens: [],
    } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & { alt: string; href: string; title: string | null; width: string | null; height: string | null; align: string | null }
    const classes = ['max-w-full', 'h-auto', 'my-1']
    if (t.align === 'left') classes.push('float-left', 'mr-4')
    else if (t.align === 'right') classes.push('float-right', 'ml-4')
    else if (t.align === 'center') classes.push('mx-auto', 'block')
    const styles: string[] = []
    if (t.width) styles.push(`width:${t.width}px`)
    if (t.height) styles.push(`height:${t.height}px`)
    const styleAttr = styles.length > 0 ? ` style="${styles.join(';')}"` : ''
    return `<img src="${escapeHtml(t.href)}" alt="${escapeHtml(t.alt)}"${styleAttr} class="${classes.join(' ')}" loading="lazy" />`
  },
}

// ── 对齐表格 ─────────────────────────────────────────────────────────────

const tableExtension: TokenizerAndRendererExtension = {
  name: 'md-table',
  level: 'block',
  start(src) {
    return src.match(/^\|(.+)\|/)?.index
  },
  tokenizer(src) {
    const lines = src.split('\n')
    if (lines.length < 2) return undefined
    if (!/^\|(.+)\|$/.test(lines[0])) return undefined
    // 对齐行允许 GFM 形式（冒号可选），由本扩展统一输出带样式的表格
    if (!/^\|((?:[:]?-+[:]?\|)+)$/.test(lines[1])) return undefined
    const alignLine = lines[1]
    let currentLine = 2
    while (currentLine < lines.length && /^\|(.+)\|$/.test(lines[currentLine])) currentLine++
    const raw = lines.slice(0, currentLine).join('\n')

    const parseRow = (row: string) => row.slice(1, -1).split('|').map((c) => c.trim())
    const alignments = alignLine.slice(1, -1).split('|').map((col) => {
      const left = col.trimStart().startsWith(':')
      const right = col.trimEnd().endsWith(':')
      return left && right ? 'center' : right ? 'right' : left ? 'left' : 'left'
    })
    const alignClass = (a: string) => (a === 'center' ? 'text-center' : a === 'right' ? 'text-right' : 'text-left')
    return {
      type: 'md-table',
      raw,
      tokens: [],
      header: parseRow(lines[0]),
      rows: lines.slice(2, currentLine).map(parseRow),
      alignments,
    } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & { header: string[]; rows: string[][]; alignments: string[] }
    const inline = (text: string) => marked.parseInline(text)
    const alignClass = (a: string) => (a === 'center' ? 'text-center' : a === 'right' ? 'text-right' : 'text-left')
    const thead = t.header
      .map((text, i) => `<th class="px-3 py-2 font-semibold ${alignClass(t.alignments[i] || 'left')}">${inline(text)}</th>`)
      .join('')
    const tbody = t.rows
      .map((row) => `<tr class="border-t border-slate-100">${row.map((text, i) => `<td class="px-3 py-2 ${alignClass(t.alignments[i] || 'left')}">${inline(text)}</td>`).join('')}</tr>`)
      .join('')
    return `<div class="my-4 overflow-x-auto rounded-lg border border-slate-200"><table class="w-full border-collapse text-sm"><thead class="bg-slate-50"><tr>${thead}</tr></thead><tbody>${tbody}</tbody></table></div>`
  },
}

// ── GitHub issue 链接 ────────────────────────────────────────────────────

// 与旧版一致的默认仓库
const issuesDefaultRepo = { owner: 'devlive-community', name: 'knowforge' }

const issuesExtension: TokenizerAndRendererExtension = {
  name: 'md-issues',
  level: 'inline',
  start(src) {
    const repoMatch = src.match(/[a-zA-Z0-9-]+\/[a-zA-Z0-9-_.]+#\d/)
    if (repoMatch?.index !== undefined) return repoMatch.index
    const simpleMatch = src.match(/#\d/)
    if (simpleMatch?.index !== undefined && !inInlineCode(src, simpleMatch.index)) return simpleMatch.index
    return undefined
  },
  tokenizer(src) {
    const match = /^(?:(?:([a-zA-Z0-9-]+)\/([a-zA-Z0-9-_.]+))?#(\d+))/.exec(src)
    if (!match || (src.indexOf('`') > -1 && inInlineCode(src, match.index))) return undefined
    const [, owner, repo, issueNumber] = match
    const href = owner && repo
      ? `https://github.com/${owner}/${repo}/issues/${issueNumber}`
      : `https://github.com/${issuesDefaultRepo.owner}/${issuesDefaultRepo.name}/issues/${issueNumber}`
    return { type: 'md-issues', raw: match[0], text: `#${issueNumber}`, href, tokens: [] } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & { text: string; href: string }
    const label = t.href.includes('/issues/')
      ? t.href.slice('https://github.com/'.length).replace('/issues/', '#')
      : t.text
    return (
      `<a href="${escapeHtml(t.href)}" target="_blank" rel="noopener noreferrer"` +
      ' class="inline-flex items-center px-2 py-1 mx-0.5 rounded-md bg-slate-100 hover:bg-slate-200 text-slate-800 transition-colors no-underline align-middle">' +
      '<svg class="w-4 h-4 mr-1.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
      '<path d="M15 22v-4a4.8 4.8 0 0 0-1-3.5c3 0 6-2 6-5.5.08-1.25-.27-2.48-1-3.5.28-1.15.28-2.35 0-3.5 0 0-1 0-3 1.5-2.64-.5-5.36-.5-8 0C6 2 5 2 5 2c-.3 1.15-.3 2.35 0 3.5A5.403 5.403 0 0 0 4 9c0 3.5 3 5.5 6 5.5-.39.49-.68 1.05-.85 1.65-.17.6-.22 1.23-.15 1.85v4"></path>' +
      '<path d="M9 18c-4.51 2-5-2-7-2"></path></svg>' +
      `<span class="font-semibold text-xs">${escapeHtml(label)}</span></a>`
    )
  },
}

// ── REST API 文档卡 ──────────────────────────────────────────────────────

const apiExtension: TokenizerAndRendererExtension = {
  name: 'md-restapi',
  level: 'block',
  start(src) {
    return src.match(/^:::\s*api(?:\s|$)/m)?.index
  },
  tokenizer(src) {
    const header = /^:::\s*api\s+(GET|POST|PUT|DELETE|PATCH)\s+([^\n]+)\n/.exec(src)
    if (!header) return undefined
    const block = scanDelimitedBlock(src, header[0].length)
    if (!block) return undefined
    const content = block.content
    const raw = block.raw

    let description: string | null = null
    const sections: { title: string; src: string }[] = []
    let currentSection: string | null = null
    let currentContent: string[] = []
    let baseIndent = 0

    const flush = () => {
      while (currentContent.length > 0 && currentContent[currentContent.length - 1] === '') currentContent.pop()
      const text = currentContent.join('\n')
      if (currentSection) sections.push({ title: currentSection, src: text })
      else if (text) description = text
    }

    let sectionFence = ''
    for (const line of content.split('\n')) {
      // 围栏代码内的 === "…" 不参与分节判定
      sectionFence = lineFence(sectionFence, line)
      if (sectionFence) {
        currentContent.push(line)
        continue
      }
      const sectionMatch = line.match(/^(\s*)===\s*"([^"]*)"$/)
      if (sectionMatch) {
        flush()
        currentSection = sectionMatch[2]
        baseIndent = sectionMatch[1].length
        currentContent = []
        continue
      }
      if (line.trim() === '') {
        currentContent.push('')
        continue
      }
      if (currentSection) {
        const indentMatch = line.match(/^(\s+)/)
        if (!indentMatch || indentMatch[1].length <= baseIndent) return undefined
        let relativeIndent = ''
        if (indentMatch[1].length > baseIndent + 4) relativeIndent = ' '.repeat(indentMatch[1].length - (baseIndent + 4))
        currentContent.push(relativeIndent + line.trim())
      } else {
        currentContent.push(line)
      }
    }
    flush()

    return {
      type: 'md-restapi',
      raw,
      method: header[1],
      path: header[2].trim(),
      description: description ? this.lexer.blockTokens(description) : null,
      sections: sections.map((sec) => ({ title: sec.title, tokens: this.lexer.blockTokens(sec.src) })),
    } as Tokens.Generic
  },
  renderer(token) {
    const t = token as Tokens.Generic & {
      method: string
      path: string
      description: Tokens.Generic[] | null
      sections: { title: string; tokens: Tokens.Generic[] }[]
    }
    const methodColors: Record<string, string> = {
      GET: 'bg-sky-100 text-sky-700',
      POST: 'bg-emerald-100 text-emerald-700',
      PUT: 'bg-amber-100 text-amber-700',
      PATCH: 'bg-orange-100 text-orange-700',
      DELETE: 'bg-rose-100 text-rose-700',
    }
    const descriptionHtml = t.description
      ? `<div class="mt-4 text-sm text-slate-600">${this.parser.parse(t.description)}</div>`
      : ''
    const sectionsHtml = t.sections
      .map((sec) => `<div class="mt-5"><h3 class="text-base font-medium text-slate-900 mb-2">${escapeHtml(sec.title)}</h3>${this.parser.parse(sec.tokens)}</div>`)
      .join('')
    return (
      '<div class="my-4 border border-slate-200 rounded-lg p-4">' +
      `<div class="flex items-center gap-2 border-b border-slate-100 pb-2">` +
      `<span class="px-3 py-1 rounded-md font-mono font-bold text-xs ${methodColors[t.method] || 'bg-slate-100 text-slate-700'}">${t.method}</span>` +
      `<span class="font-mono text-sm text-slate-900">${escapeHtml(t.path)}</span></div>` +
      `${descriptionHtml}${sectionsHtml}</div>`
    )
  },
}

// markdownExtensions 注册进 marked 的扩展集合
export const markdownExtensions: TokenizerAndRendererExtension[] = [
  tabsExtension,
  tabsTagExtension,
  calloutTagExtension,
  accordionGroupExtension,
  accordionExtension,
  stepsExtension,
  cardGroupExtension,
  cardExtension,
  gridExtension,
  diffExtension,
  katexExtension,
  mermaidExtension,
  tocExtension,
  childrenTocExtension,
  buttonExtension,
  tipExtension,
  switchExtension,
  iconExtension,
  imageExtension,
  tableExtension,
  issuesExtension,
  apiExtension,
]

// ── 客户端交互绑定（tabs 切换 / mermaid 渲染 / lucide 图标填充）────────────

interface MermaidModule {
  initialize: (config: Record<string, unknown>) => void
  render: (id: string, text: string) => Promise<{ svg: string }>
}

let mermaidModule: MermaidModule | null = null
let mermaidSeq = 0

// bindMarkdownInteractivity 在渲染容器上绑定扩展所需的客户端行为；
// root 元素在 React 重渲染间复用，用 dataset 标记避免重复绑定
export async function bindMarkdownInteractivity(root: HTMLElement): Promise<void> {
  if (!root.dataset.mdBound) {
    root.dataset.mdBound = '1'
    root.addEventListener('click', (e) => {
      const button = (e.target as HTMLElement).closest('[data-md-tab]') as HTMLElement | null
      if (!button) return
      const group = button.dataset.mdTab as string
      const index = button.dataset.mdTabIndex
      root.querySelectorAll(`[data-md-tab="${group}"]`).forEach((b) => {
        const active = b.getAttribute('data-md-tab-index') === index
        b.setAttribute('aria-selected', String(active))
        b.classList.toggle('border-slate-200', active)
        b.classList.toggle('bg-white', active)
        b.classList.toggle('text-primary-700', active)
        b.classList.toggle('shadow-sm', active)
        b.classList.toggle('border-transparent', !active)
        b.classList.toggle('text-slate-500', !active)
      })
      root.querySelectorAll(`[data-md-tab-panel="${group}"]`).forEach((p) => {
        p.classList.toggle('hidden', p.getAttribute('data-md-tab-index') !== index)
      })
    })
  }

  // mermaid：动态加载，仅在出现图时引入体积较大的渲染器
  const mermaidBlocks = root.querySelectorAll<HTMLElement>('.md-mermaid pre')
  if (mermaidBlocks.length > 0) {
    try {
      if (!mermaidModule) {
        mermaidModule = (await import('mermaid')).default as unknown as MermaidModule
        mermaidModule.initialize({ startOnLoad: false, securityLevel: 'strict' })
      }
      for (const block of Array.from(mermaidBlocks)) {
        if (block.dataset.mdMermaidDone) continue
        block.dataset.mdMermaidDone = '1'
        const code = block.textContent || ''
        try {
          const { svg } = await mermaidModule.render(`md-mermaid-${++mermaidSeq}`, code)
          block.innerHTML = svg
        } catch {
          // 语法错误时保留源码展示
        }
      }
    } catch {
      // mermaid 加载失败时保留源码
    }
  }

  // lucide 图标：动态加载全量图标集并替换占位
  if (root.querySelector('[data-md-icon]')) {
    try {
      const { createIcons, icons } = await import('lucide')
      createIcons({ icons, nameAttr: 'data-md-icon' })
    } catch {
      // 图标加载失败时保留占位
    }
  }
}
