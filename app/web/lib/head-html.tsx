import { createElement, type ReactElement } from 'react'

// 站点设置中的「自定义 Head HTML」（统计脚本、站点验证 meta、额外样式等）在服务端渲染时转为 <head> 中的元素：
// React 不能把一段原始 HTML 直接放进 <head>，这里把常见的头部标签逐个转换；不支持的标签忽略。
// 内容由管理员填写（站点设置需要管理员权限，修改记入审计日志）。

const VOID_TAGS = new Set(['meta', 'link', 'base'])
const CONTENT_TAGS = new Set(['script', 'style', 'noscript', 'title'])

// HTML 属性名 → React 属性名
const ATTR_NAMES: Record<string, string> = {
  class: 'className', charset: 'charSet', 'http-equiv': 'httpEquiv', crossorigin: 'crossOrigin',
  referrerpolicy: 'referrerPolicy', nomodule: 'noModule', fetchpriority: 'fetchPriority',
  hreflang: 'hrefLang', imagesizes: 'imageSizes', imagesrcset: 'imageSrcSet',
}
const BOOLEAN_ATTRS = new Set(['async', 'defer', 'nomodule'])

function parseAttrs(raw: string): Record<string, string | boolean> {
  const out: Record<string, string | boolean> = {}
  const re = /([^\s"'<>/=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+)))?/g
  let m: RegExpExecArray | null
  while ((m = re.exec(raw))) {
    const name = m[1].toLowerCase()
    if (name.startsWith('on')) continue // 不支持内联事件处理（如 onload），请改用 <script>
    const value = m[2] ?? m[3] ?? m[4]
    const key = ATTR_NAMES[name] || name
    out[key] = value === undefined ? (BOOLEAN_ATTRS.has(name) ? true : '') : value
  }
  return out
}

// headHtmlElements 把一段头部 HTML 转为 React 元素（按出现顺序；注释与不支持的标签忽略）。
export function headHtmlElements(html: string | null | undefined, keyPrefix = 'custom-head'): ReactElement[] {
  const src = (html || '').replace(/<!--[\s\S]*?-->/g, '')
  const out: ReactElement[] = []
  const re = /<([a-zA-Z][a-zA-Z0-9-]*)(\s[^>]*?)?\s*(\/?)>/g
  let m: RegExpExecArray | null
  while ((m = re.exec(src))) {
    const tag = m[1].toLowerCase()
    const attrs = parseAttrs(m[2] || '')
    const key = `${keyPrefix}-${out.length}`
    if (VOID_TAGS.has(tag)) {
      out.push(createElement(tag, { key, ...attrs }))
      continue
    }
    if (!CONTENT_TAGS.has(tag)) continue
    const close = src.toLowerCase().indexOf(`</${tag}`, re.lastIndex)
    const inner = close === -1 ? '' : src.slice(re.lastIndex, close)
    if (close !== -1) re.lastIndex = src.indexOf('>', close) + 1 || src.length
    if (tag === 'title') {
      out.push(createElement('title', { key }, inner.trim()))
    } else {
      out.push(createElement(tag, { key, ...attrs, dangerouslySetInnerHTML: { __html: inner } }))
    }
  }
  return out
}
