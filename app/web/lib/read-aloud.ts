// AI 朗读：从阅读页正文切分朗读段落（按实际排版的段落、标题、列表项等），长段落按句切分；
// 服务端只合成出自本章的文字，切分结果与高亮的元素一一对应。

export function readAloudEnabled(site: { feature_plugins?: string[] } | null | undefined): boolean {
  return (site?.feature_plugins || []).includes('read-aloud')
}

export interface ReadAloudSegment {
  el: HTMLElement | null // 高亮的元素（章节标题等不在正文中的段落为 null）
  text: string
}

export const MAX_SEGMENT = 400

// 朗读的块级元素：只取不再包含其他块级元素的「叶子」块，避免重复朗读
const BLOCK = 'p, h1, h2, h3, h4, h5, h6, li, blockquote, dt, dd, td, th, figcaption'
// 不朗读的区域：代码、公式、图表、隐藏内容
const SKIP = 'pre, code, .katex, .mermaid, svg, script, style, [hidden], [aria-hidden="true"], [data-read-aloud-skip]'

const SPOKEN = /[\p{L}\p{N}]/u

function clean(text: string): string {
  return text.replace(/\s+/g, ' ').trim()
}

// splitText 把长段落按句切分为不超过 max 字的片段（句末标点后断开；单句过长时按长度硬切）。
export function splitText(text: string, max = MAX_SEGMENT): string[] {
  const t = clean(text)
  if ([...t].length <= max) return t ? [t] : []
  const sentences = t.match(/[^。！？!?；;…]+[。！？!?；;…]*[”’"')）\]]*\s*|[^。！？!?；;…]+$/g) || [t]
  const out: string[] = []
  let cur = ''
  const push = () => { if (clean(cur)) out.push(clean(cur)); cur = '' }
  for (const s of sentences) {
    if ([...cur + s].length <= max) { cur += s; continue }
    push()
    const chars = [...s]
    for (let i = 0; i < chars.length; i += max) {
      const part = chars.slice(i, i + max).join('')
      if (i + max >= chars.length) cur = part
      else out.push(clean(part))
    }
  }
  push()
  return out
}

function visible(el: HTMLElement): boolean {
  return el.getClientRects().length > 0
}

// collectSegments 按文档顺序收集正文中的朗读段落；title 为章节标题（作为第一段，不高亮）。
export function collectSegments(root: HTMLElement, title?: string): ReadAloudSegment[] {
  const out: ReadAloudSegment[] = []
  if (title && SPOKEN.test(title)) out.push(...splitText(title).map((text) => ({ el: null, text })))
  root.querySelectorAll<HTMLElement>(BLOCK).forEach((el) => {
    if (el.closest(SKIP) || el.querySelector(BLOCK) || !visible(el)) return
    const text = clean(el.innerText || el.textContent || '')
    if (!SPOKEN.test(text)) return
    for (const part of splitText(text)) out.push({ el, text: part })
  })
  return out
}
