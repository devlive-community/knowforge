import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { createElement, Fragment } from 'react'
import { headHtmlElements } from '../head-html'

const render = (html: string) => renderToStaticMarkup(createElement(Fragment, null, ...headHtmlElements(html)))

describe('自定义 Head HTML', () => {
  it('转换常见头部标签并保持顺序', () => {
    const out = render(`<!-- 统计 -->
<meta name="google-site-verification" content="abc123" />
<meta http-equiv="X-UA-Compatible" content="IE=edge">
<link rel="preconnect" href="https://fonts.example.com" crossorigin>
<script async src="https://www.googletagmanager.com/gtag/js?id=G-1"></script>
<script>window.dataLayer = window.dataLayer || []; if (1 < 2) { gtag('js', new Date()) }</script>
<style>.x > .y { color: red }</style>`)
    expect(out).toContain('<meta name="google-site-verification" content="abc123"/>')
    expect(out).toContain('<meta http-equiv="X-UA-Compatible" content="IE=edge"/>')
    expect(out).toContain('<link rel="preconnect" href="https://fonts.example.com" crossorigin=""/>')
    expect(out).toContain('<script async="" src="https://www.googletagmanager.com/gtag/js?id=G-1"></script>')
    expect(out).toContain("<script>window.dataLayer = window.dataLayer || []; if (1 < 2) { gtag('js', new Date()) }</script>")
    expect(out).toContain('<style>.x > .y { color: red }</style>')
    expect(out).not.toContain('统计')
    expect(out.indexOf('google-site-verification')).toBeLessThan(out.indexOf('<style>'))
  })

  it('忽略不支持的标签与内联事件', () => {
    const out = render('<div>不该出现</div><img src=x onerror="alert(1)"><link rel="stylesheet" href="/a.css" onload="alert(2)">')
    expect(out).toBe('<link rel="stylesheet" href="/a.css"/>')
  })
})
