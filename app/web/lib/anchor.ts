// 文本锚点：批注保存选中的原文、前后各一小段上下文与大致位置；正文修改后按此重新定位
// （优先前后文都吻合的出现位置，其次离原位置最近的），找不到时视为原文已修改。

export interface TextAnchor {
  quote: string
  prefix: string
  suffix: string
  quote_offset: number
}

const CONTEXT = 32
const MAX_OCCURRENCES = 500

export function buildAnchor(content: string, start: number, end: number): TextAnchor {
  return {
    quote: content.slice(start, end),
    prefix: content.slice(Math.max(0, start - CONTEXT), start),
    suffix: content.slice(end, end + CONTEXT),
    quote_offset: start,
  }
}

// 两段文本从末尾 / 开头起相同的字符数
function commonSuffix(a: string, b: string): number {
  let n = 0
  while (n < a.length && n < b.length && a[a.length - 1 - n] === b[b.length - 1 - n]) n++
  return n
}

function commonPrefix(a: string, b: string): number {
  let n = 0
  while (n < a.length && n < b.length && a[n] === b[n]) n++
  return n
}

export function locateAnchor(content: string, anchor: Pick<TextAnchor, 'quote' | 'prefix' | 'suffix' | 'quote_offset'>): { start: number; end: number } | null {
  const { quote } = anchor
  if (!quote) return null
  let best: { start: number; score: number } | null = null
  let from = 0
  for (let count = 0; count < MAX_OCCURRENCES; count++) {
    const i = content.indexOf(quote, from)
    if (i < 0) break
    const prefix = anchor.prefix ? commonSuffix(content.slice(Math.max(0, i - anchor.prefix.length), i), anchor.prefix) : 0
    const suffixStart = i + quote.length
    const suffix = anchor.suffix ? commonPrefix(content.slice(suffixStart, suffixStart + anchor.suffix.length), anchor.suffix) : 0
    const score = (prefix + suffix) * 10000 - Math.abs(i - (anchor.quote_offset || 0))
    if (!best || score > best.score) best = { start: i, score }
    from = i + 1
  }
  return best ? { start: best.start, end: best.start + quote.length } : null
}
