// 章节元数据：只认文档「开头」的两种写法（可同时使用），正文中出现的同样内容一律按普通 Markdown 处理：
//
//   ---                       front-matter：必须从第一行开始，到下一行 --- 结束；
//   title: 页面标题            每行 key: value（值可加引号），支持 title / description / url / icon，
//   description: 页面简介      其他键保留但不使用；
//   url: https://…            有任何一行不是 key: value（或其缩进续行）时不视为元数据
//   icon: database
//   ---
//   <!-- icon: database -->   图标注释：位于开头（或紧跟 front-matter 之后，只认第一条），可与 front-matter 同时使用，优先于其中的 icon
//
// 渲染时元数据不显示在正文中；阅读页用 description / url 显示简介与原文链接，页面标题与简介用于 SEO。
// 服务端（章节图标、DOCX/EPUB 导出）按同一规则解析，见 server/internal/app/doc_meta.go。

export interface DocMeta {
  title?: string
  description?: string
  url?: string
  icon?: string
  [key: string]: string | undefined
}

export interface ParsedDoc {
  meta: DocMeta
  /** 元数据之后的正文 */
  body: string
  /** 开头的元数据原文（front-matter 与图标注释），无元数据时为空串 */
  header: string
  /** 是否含 front-matter 块 */
  hasFrontMatter: boolean
}

const ICON_COMMENT_RE = /^[ \t]*<!--\s*icon:\s*([^>]*?)\s*-->[ \t]*(?:\r?\n|$)/i
const KEY_LINE_RE = /^([A-Za-z_][\w-]*)[ \t]*:[ \t]?(.*)$/

function unquote(value: string): string {
  const v = value.trim()
  if (v.length >= 2 && ((v[0] === '"' && v.endsWith('"')) || (v[0] === "'" && v.endsWith("'")))) return v.slice(1, -1)
  return v
}

// parseFrontMatter 解析开头的 front-matter；不是合法元数据块时返回 null
function parseFrontMatter(src: string): { fields: Record<string, string>; length: number } | null {
  const open = /^---[ \t]*\r?\n/.exec(src)
  if (!open) return null
  const fields: Record<string, string> = {}
  let pos = open[0].length
  let lastKey = ''
  let lines = 0
  while (pos <= src.length && lines < 200) {
    const nl = src.indexOf('\n', pos)
    const end = nl === -1 ? src.length : nl
    const line = src.slice(pos, end).replace(/\r$/, '')
    const next = nl === -1 ? src.length : nl + 1
    if (/^(---|\.\.\.)[ \t]*$/.test(line)) {
      return Object.keys(fields).length > 0 ? { fields, length: next } : null
    }
    if (line.trim() && !line.trimStart().startsWith('#')) {
      const kv = KEY_LINE_RE.exec(line)
      if (kv) {
        lastKey = kv[1]
        fields[lastKey] = unquote(kv[2])
      } else if (/^[ \t]/.test(line) && lastKey) {
        // 缩进续行（多行值或列表）：拼接到上一个键
        fields[lastKey] = [fields[lastKey], unquote(line.trim().replace(/^-\s+/, ''))].filter(Boolean).join(' ')
      } else {
        return null
      }
    }
    if (nl === -1) break
    pos = next
    lines++
  }
  return null
}

// parseDocMeta 拆分开头的元数据与正文。
export function parseDocMeta(source: string | null | undefined): ParsedDoc {
  const src = (source || '').replace(/^\ufeff/, '')
  const meta: DocMeta = {}
  let offset = 0
  let hasFrontMatter = false
  const fm = parseFrontMatter(src)
  if (fm) {
    Object.assign(meta, fm.fields)
    offset = fm.length
    hasFrontMatter = true
  }
  // 图标注释：开头（front-matter 之后）可有空行；只认第一条，之后的同样注释属于正文
  const rest = src.slice(offset)
  const lead = /^(?:[ \t]*\r?\n)*/.exec(rest)![0]
  const m = ICON_COMMENT_RE.exec(rest.slice(lead.length))
  if (m) {
    if (m[1].trim()) meta.icon = m[1].trim()
    offset += lead.length + m[0].length
  }
  if (offset === 0) return { meta, body: src, header: '', hasFrontMatter }
  return { meta, body: src.slice(offset).replace(/^(?:[ \t]*\r?\n)+/, ''), header: src.slice(0, offset), hasFrontMatter }
}

// safeMetaUrl 元数据中的原文链接只接受 http(s)
export function safeMetaUrl(url: string | undefined): string {
  const v = (url || '').trim()
  return /^https?:\/\/\S+$/i.test(v) ? v : ''
}

// setDocIcon 设置/移除章节图标：已有图标注释时替换它；有 front-matter 时写入其中的 icon 键；否则在开头插入图标注释。
export function setDocIcon(source: string, icon: string): string {
  const src = source.replace(/^\ufeff/, '')
  const parsed = parseDocMeta(src)
  const commentRe = /(^|\n)[ \t]*<!--\s*icon:[^>]*-->[ \t]*(?:\r?\n|$)/i
  // 只改元数据区域内的图标注释
  if (parsed.header && commentRe.test(parsed.header)) {
    const header = parsed.header.replace(commentRe, (_m, lead: string) => (icon ? `${lead}<!-- icon: ${icon} -->\n` : lead))
    return header + (header && !header.endsWith('\n') ? '\n' : '') + parsed.body
  }
  const fm = parseFrontMatter(src)
  if (fm) {
    const block = src.slice(0, fm.length)
    const rest = src.slice(fm.length)
    const lines = block.split('\n')
    const idx = lines.findIndex((l, i) => i > 0 && /^icon[ \t]*:/i.test(l))
    if (idx >= 0) {
      if (icon) lines[idx] = `icon: ${icon}`
      else lines.splice(idx, 1)
    } else if (icon) {
      const close = lines.findIndex((l, i) => i > 0 && /^(---|\.\.\.)[ \t]*\r?$/.test(l))
      lines.splice(close, 0, `icon: ${icon}`)
    }
    return lines.join('\n') + rest
  }
  return icon ? `<!-- icon: ${icon} -->\n${src}` : src
}
