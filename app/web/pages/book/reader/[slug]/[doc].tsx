import { useEffect, useMemo, useRef, useState, ReactNode } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/router'
import type { GetServerSideProps, InferGetServerSidePropsType } from 'next'
import { serverApi, getSiteConfig, siteUrlFrom, authHeaderFrom, excerptFrom, isInstalled, getSSRUser } from '@/lib/server-api'
import { renderMarkdown, extractHeadings, bindMarkdownInteractivity, fillChildrenToc, wikiDocsFromTree } from '@/lib/markdown'
import { API_BASE, formatDate, formatNumber, api } from '@/lib/api'
import { resolveMediaUrl } from '@/lib/media'
import Seo from '@/components/Seo'
import UserAvatar from '@/components/UserAvatar'
import { Button, ButtonLink, Input, Tooltip } from '@/components/ui'
import DocTreeIcon from '@/components/DocTreeIcon'
import { CheckCircleSmallIcon, ChevronDownIcon, ChevronRightIcon, PencilIcon } from '@/components/icons'
import { saveReadingProgress, getReadingProgress } from '@/lib/reading-progress'
import { useTranslation } from '@/lib/i18n'
import Comments from '@/components/Comments'
import ReaderAnnotations from '@/components/ReaderAnnotations'
import type { PaidBookInfo } from '@/lib/paid'
import PaywallCard from '@/components/PaywallCard'
import QADrawer, { type QATab } from '@/components/qa/QADrawer'
import ChapterGuideCard from '@/components/chapter-guide/ChapterGuideCard'
import RelatedChapters from '@/components/RelatedChapters'
import Backlinks from '@/components/Backlinks'
import { backlinksEnabled } from '@/lib/backlinks'
import { chapterGuideEnabled } from '@/lib/chapter-guide'
import { qaEnabled, citationHref, type QACitation } from '@/lib/qa'
import ReportButton from '@/components/ReportButton'
import BookTranslations from '@/components/BookTranslations'
import BookVersions from '@/components/BookVersions'
import type { Book, BookAccess, Document, User } from '@/lib/types'
import { displayName } from '@/lib/users'

interface ReaderProps {
  installed: boolean
  user: User | null
  site: Record<string, string>
  siteUrl: string
  book: Book
  doc: Document
  html: string
  tree: Document[]
  access: BookAccess | null
  readDocIds: number[]
}

const FONT_SIZES = [15, 16, 18, 20, 22]

function WatermarkLayer({ text }: { text: string }) {
  // 用可平铺的 SVG 背景铺满整篇正文（无论章节多长都均匀重复）。
  // 原先的固定网格在长章节里会把水印拉得很稀疏，导致大片区域看不到水印。
  const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;')
  const tile = `<svg xmlns='http://www.w3.org/2000/svg' width='260' height='170'><text x='130' y='95' fill='rgba(100,116,139,0.10)' font-family='sans-serif' font-size='13' letter-spacing='2' text-anchor='middle' transform='rotate(-28 130 95)'>${esc(text)}</text></svg>`
  const url = `data:image/svg+xml,${encodeURIComponent(tile)}`
  return (
    <div aria-hidden="true" className="pointer-events-none absolute inset-0 z-10 select-none"
      style={{ backgroundImage: `url("${url}")`, backgroundRepeat: 'repeat' }} />
  )
}

// AuthorAvatars 创作者头像列表：hover 提示用户名，点击跳转用户主页
function AuthorAvatars({ users }: { users: { username: string; avatar?: string }[] }) {
  if (users.length === 0) return null
  return (
    <span className="flex items-center gap-1">
      {users.map((u) => (
        <UserAvatar key={u.username} user={u} size="h-6 w-6" className="ring-1 ring-white transition-transform hover:scale-110" />
      ))}
    </span>
  )
}

function flatten(docs: Document[] | null | undefined): Document[] {
  return (docs || []).flatMap((d) => [d, ...flatten(d.children)])
}

export const getServerSideProps: GetServerSideProps<ReaderProps> = async ({ req, params }) => {
  if (!(await isInstalled())) {
    return { redirect: { destination: '/install', permanent: false } }
  }
  const slug = typeof params?.slug === 'string' ? params.slug : ''
  const docSlug = typeof params?.doc === 'string' ? params.doc : ''
  if (!slug) return { notFound: true }
  const user = await getSSRUser(req)
  const auth = authHeaderFrom(req)
  const [site] = await Promise.all([getSiteConfig()])

  try {
    const book = await serverApi<Book>(`/books/slug/${encodeURIComponent(slug)}`, { headers: auth })
    const [tree, doc] = await Promise.all([
      serverApi<Document[]>(`/books/${book.id}/documents`, { headers: auth }).catch(() => []),
      docSlug
        ? serverApi<Document>(`/books/${book.id}/documents/slug/${encodeURIComponent(docSlug)}`, { headers: auth })
        : Promise.resolve(null),
    ])
    if (!doc) return { notFound: true }
    const [access, readChapters]: [BookAccess | null, { doc_ids: number[] }] = user
      ? await Promise.all([
          serverApi<BookAccess>(`/books/slug/${encodeURIComponent(slug)}/access`, { headers: auth }).catch(() => null),
          serverApi<{ doc_ids: number[] }>(`/books/${book.id}/read-chapters`, { headers: auth }).catch(() => ({ doc_ids: [] })),
        ])
      : [null, { doc_ids: [] }]
    // [children] 宏：用当前章节的直接子章节填充子章节目录
    const findNode = (nodes: Document[]): Document | null => {
      for (const n of nodes) {
        if (n.id === doc.id) return n
        const found = n.children ? findNode(n.children) : null
        if (found) return found
      }
      return null
    }
    const childDocs = (findNode(tree)?.children || []).map((c) => ({ slug: c.slug, title: c.title, external_url: c.external_url, external_new_tab: c.external_new_tab }))
    const html = fillChildrenToc(renderMarkdown(doc.content, { bookSlug: book.slug, docs: wikiDocsFromTree(tree) }), childDocs, book.slug, book.chapter_prefix || '')
    return { props: {
      installed: true, user, site, siteUrl: siteUrlFrom(req), book, doc,
      html, tree, access, readDocIds: readChapters.doc_ids || [],
    } }
  } catch (e) {
    // 404/403 一律按不存在处理：不向未授权访客泄露私有章节的存在
    return { notFound: true }
  }
}

export default function Reader({ site, siteUrl, user, book, doc, html, tree, access, readDocIds }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const chapterPrefix = book?.chapter_prefix || ''

  const router = useRouter()
  const [fontIdx, setFontIdx] = useState(1)
  const [focus, setFocus] = useState(false)
  const [mobileNav, setMobileNav] = useState(false) // 移动端目录/版本/语言抽屉
  const [activeHeading, setActiveHeading] = useState('')
  const [expanded, setExpanded] = useState<Set<number>>(new Set())
  const [readSet, setReadSet] = useState<Set<number>>(new Set(readDocIds))
  // 付费内容：当前读者未解锁的章节（目录中显示锁标记；插件未启用时不请求）
  const [lockedSet, setLockedSet] = useState<Set<number>>(new Set())
  const paidEnabled = ((site as { feature_plugins?: string[] }).feature_plugins || []).includes('paid-content')
  useEffect(() => {
    if (!paidEnabled) return
    api<PaidBookInfo>(`/paid/books/${book.id}`).then((r) => setLockedSet(new Set(r.enabled ? r.locked_doc_ids : []))).catch(() => {})
  }, [paidEnabled, book.id, user?.id])
  // 书籍问答：抽屉状态由 URL 承载（?qa=ai|community&qaq=问题ID），可分享可回退；划词内容带入 AI 问答
  const qaOn = qaEnabled(site)
  const qaTab: QATab | null = !qaOn ? null : router.query.qa === 'community' ? 'community' : router.query.qa === 'ai' ? 'ai' : null
  const qaQuestion = Number(router.query.qaq) || null
  const [qaSelection, setQaSelection] = useState('')
  const navigateQa = (tab: QATab | null, questionId?: number | null) => {
    const { qa: _qa, qaq: _qaq, ...rest } = router.query
    const query = { ...rest, ...(tab ? { qa: tab } : {}), ...(tab === 'community' && questionId ? { qaq: String(questionId) } : {}) }
    router.push({ pathname: router.pathname, query }, undefined, { shallow: true, scroll: false })
  }
  const citeQa = (c: QACitation) => {
    if (c.doc_id === doc?.id) {
      const el = c.anchor ? document.getElementById(c.anchor) : null
      if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
      else contentScrollRef.current?.scrollTo({ top: 0, behavior: 'smooth' })
      if (window.innerWidth < 1024) navigateQa(null)
      return
    }
    // 跳到其他章节：保留抽屉（桌面端），落地后按锚点定位
    const keep = window.innerWidth >= 1024 && qaTab ? `?qa=${qaTab}` : ''
    router.push(citationHref(book.slug, c).replace('#', `${keep}#`))
  }
  const contentRef = useRef<HTMLDivElement>(null)
  // M17 扩展交互：tabs 切换 / mermaid 渲染 / lucide 图标（html 变化后重挂）
  useEffect(() => {
    if (contentRef.current && typeof window !== 'undefined') {
      bindMarkdownInteractivity(contentRef.current)
    }
  })

  // 阅读字号本地记忆（读一次 + 变化写入；try/catch 防隐私模式抛错）
  useEffect(() => {
    try {
      const saved = parseInt(localStorage.getItem('reader:font-idx') || '', 10)
      if (saved >= 0 && saved < FONT_SIZES.length) setFontIdx(saved)
    } catch { /* 忽略 */ }
  }, [])
  useEffect(() => {
    try { localStorage.setItem('reader:font-idx', String(fontIdx)) } catch { /* 忽略 */ }
  }, [fontIdx])

  // 移动端目录抽屉：切换章节/版本/语言（路由变化）后自动关闭
  useEffect(() => {
    const close = () => setMobileNav(false)
    router.events.on('routeChangeComplete', close)
    return () => router.events.off('routeChangeComplete', close)
  }, [router.events])

  const flat = useMemo(() => flatten(tree), [tree])
  const headings = useMemo(() => extractHeadings(doc?.content), [doc])

  // 目录搜索：按标题过滤（命中节点或其任意子孙命中即保留），搜索时全部展开命中分支
  const [tocSearch, setTocSearch] = useState('')
  const filteredTree = useMemo(() => {
    const q = tocSearch.trim().toLowerCase()
    if (!q) return tree
    const filter = (docs: Document[]): Document[] => docs.reduce<Document[]>((acc, d) => {
      const kids = d.children ? filter(d.children) : []
      if (d.title.toLowerCase().includes(q) || kids.length) acc.push({ ...d, children: kids })
      return acc
    }, [])
    return filter(tree)
  }, [tree, tocSearch])
  const searchExpanded = useMemo(() => {
    if (!tocSearch.trim()) return null
    const ids = new Set<number>()
    const walk = (docs: Document[]) => docs.forEach((d) => { if (d.children?.length) { ids.add(d.id); walk(d.children) } })
    walk(filteredTree)
    return ids
  }, [filteredTree, tocSearch])

  // 默认展开所有含子章节的节点
  // 默认展开所有含子章节的节点；切换语言/版本（book 变化）时同一组件实例会复用，需按 book 重新展开，
  // 否则会残留上一本书的展开集合，让新书目录看起来是收缩的。
  const allParentIds = useMemo(() => {
    const ids = new Set<number>()
    const walk = (docs: Document[]) => docs.forEach((d) => { if (d.children?.length) { ids.add(d.id); walk(d.children) } })
    walk(tree)
    return ids
  }, [tree])
  useEffect(() => {
    setExpanded(new Set(allParentIds))
  }, [book?.id]) // eslint-disable-line react-hooks/exhaustive-deps
  const allCollapsed = expanded.size === 0
  const toggleAll = () => setExpanded(allCollapsed ? new Set(allParentIds) : new Set())

  // 目录自动定位：切换章节后，把当前章节滚动到左侧目录可视区（若它在非展开/滚动区外）
  const tocScrollRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = tocScrollRef.current?.querySelector('[data-toc-active="1"]')
    el?.scrollIntoView({ block: 'nearest' })
  }, [doc?.id, expanded])

  // 记录阅读进度（登录用户按用户名隔离）：打开即标记该章已读并置为最近章节
  useEffect(() => {
    if (user && book && doc) {
      saveReadingProgress(user.username, book.id, { docId: doc.id, docSlug: doc.slug, docTitle: doc.title, chapterPrefix: book.chapter_prefix || '' } as any)
    }
  }, [user, book, doc])

  // 正文滚动容器（阅读区是 h-screen 内部滚动，window 不滚动，续读/上报/回顶都以此容器为准）
  const contentScrollRef = useRef<HTMLDivElement>(null)
  // URL 带锚点（如问答出处 #h-3）时滚到对应标题；返回是否已定位
  const scrollToHash = () => {
    const id = typeof window === 'undefined' ? '' : decodeURIComponent(window.location.hash.slice(1))
    const el = id ? document.getElementById(id) : null
    if (el) requestAnimationFrame(() => el.scrollIntoView({ block: 'start' }))
    return Boolean(el)
  }

  // 精确续读：首次进入时，若上次进度停留在当前章节则恢复滚动位置
  const restoredRef = useRef(false)
  useEffect(() => {
    if (restoredRef.current || !book || !doc) return
    restoredRef.current = true
    if (scrollToHash()) return
    getReadingProgress(user?.username || '', book.id).then((p) => {
      if (p && p.docId === doc.id && (p.scrollPercent ?? 0) > 0) {
        requestAnimationFrame(() => {
          const el = contentScrollRef.current
          const max = el ? el.scrollHeight - el.clientHeight : 0
          if (el && max > 0) el.scrollTo({ top: (max * (p.scrollPercent as number)) / 100 })
        })
      }
    })
  }, [book?.id, doc?.id, user?.username]) // eslint-disable-line react-hooks/exhaustive-deps

  // 切换章节后正文回到首行（首个章节交给续读逻辑，避免覆盖恢复位置）
  const firstDocRef = useRef(true)
  useEffect(() => {
    if (firstDocRef.current) { firstDocRef.current = false; return }
    if (!scrollToHash()) contentScrollRef.current?.scrollTo({ top: 0 })
  }, [doc?.id])

  // 阅读时长与滚动位置上报（登录用户）：活跃计时（隐藏暂停），节流上报滚动百分比，
  // 每 15s / 页面隐藏 / 切章 / 关闭时 flush 一次增量。
  const activeSecondsRef = useRef(0)
  const scrollPctRef = useRef(0)
  useEffect(() => {
    if (!user || !book || !doc) return
    let lastTick = Date.now()
    const scroller = contentScrollRef.current
    const computeScroll = () => {
      const el = contentScrollRef.current
      const max = el ? el.scrollHeight - el.clientHeight : 0
      scrollPctRef.current = el && max > 0 ? Math.min(100, Math.max(0, Math.round((el.scrollTop / max) * 100))) : 0
    }
    const onScroll = () => computeScroll()
    scroller?.addEventListener('scroll', onScroll, { passive: true })
    computeScroll()

    const tick = setInterval(() => {
      const now = Date.now()
      if (document.visibilityState === 'visible') activeSecondsRef.current += Math.round((now - lastTick) / 1000)
      lastTick = now
    }, 1000)

    const flush = () => {
      const secs = activeSecondsRef.current
      activeSecondsRef.current = 0
      saveReadingProgress(user.username, book.id, {
        docId: doc.id, docSlug: doc.slug, docTitle: doc.title, chapterPrefix: book.chapter_prefix || '',
        scrollPercent: scrollPctRef.current, secondsDelta: secs,
      })
    }
    const heartbeat = setInterval(flush, 15000)
    const onVisibility = () => { if (document.visibilityState === 'hidden') flush() }
    document.addEventListener('visibilitychange', onVisibility)
    // pagehide 是 beforeunload 的现代替代（兼容 bfcache），用于关闭/离开前 flush
    window.addEventListener('pagehide', flush)

    return () => {
      clearInterval(tick)
      clearInterval(heartbeat)
      scroller?.removeEventListener('scroll', onScroll)
      document.removeEventListener('visibilitychange', onVisibility)
      window.removeEventListener('pagehide', flush)
      flush()
    }
  }, [user?.id, book?.id, doc?.id]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const next = new Set(readDocIds)
    if (user && doc) next.add(doc.id)
    setReadSet(next)
  }, [readDocIds, user, doc])

  // 本章浏览量：打开即 +1，服务端同步累加到书籍总浏览数
  const [docViews, setDocViews] = useState(0)
  useEffect(() => {
    if (!doc) return
    setDocViews(doc.view_count ?? 0)
    api<{ view_count: number }>(`/documents/${doc.id}/view`, {
      method: 'POST', body: { referrer: document.referrer },
    })
      .then((r) => setDocViews(r.view_count))
      .catch(() => { /* 计数失败不影响阅读 */ })
  }, [doc?.id]) // eslint-disable-line react-hooks/exhaustive-deps

  // 本章目录：滚动高亮
  useEffect(() => {
    if (!headings.length) return
    setActiveHeading(headings[0].id)
    const els = headings.map((h) => document.getElementById(h.id)).filter((el): el is HTMLElement => !!el)
    if (!els.length) return
    const obs = new IntersectionObserver((entries) => {
      const visible = entries.filter((e) => e.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
      if (visible[0]) setActiveHeading((visible[0].target as HTMLElement).id)
    }, { rootMargin: '-80px 0px -70% 0px', threshold: 0 })
    els.forEach((el) => obs.observe(el))
    return () => obs.disconnect()
  }, [headings, doc?.id])

  // 键盘 ← / → 翻章（忽略输入框内与组合键）
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey) return
      if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
      const t = e.target as HTMLElement | null
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return
      if (!book || !doc) return
      const i = flat.findIndex((d) => d.id === doc.id)
      if (i < 0) return
      if (e.key === 'ArrowLeft' && i > 0) router.push(`/book/reader/${encodeURIComponent(book.slug)}/${flat[i - 1].slug}`)
      else if (e.key === 'ArrowRight' && i < flat.length - 1) router.push(`/book/reader/${encodeURIComponent(book.slug)}/${flat[i + 1].slug}`)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [flat, doc, book, router])

  if (!book || !doc) {
    return (
      <div className="mx-auto max-w-md rounded-xl border border-slate-200 bg-white p-8 text-center text-sm text-slate-500 shadow-sm" style={{ marginTop: '4rem' }}>
        {t('reader.authorOnlyPrefix')}<Link href="/login" className="mx-1 text-primary-600">{t('reader.authorOnlyLogin')}</Link>{t('reader.authorOnlySuffix')}。
      </div>
    )
  }

  const canEdit = access?.can_edit_content === true

  const index = doc ? flat.findIndex((d) => d.id === doc.id) : -1
  const prev = index > 0 ? flat[index - 1] : null
  const next = index >= 0 && index < flat.length - 1 ? flat[index + 1] : null
  // 上/下篇若是外链章节则跳转到外部地址（仅 http(s)），否则走内部阅读页。
  const neighborExtURL = (n: { external_url?: string }) => {
    const ext = (n.external_url || '').trim()
    return /^https?:\/\//i.test(ext) ? ext : ''
  }
  const readingMin = doc ? Math.max(1, Math.round((doc.content || '').replace(/\s/g, '').length / 400)) : 0
  const parentDoc = doc?.parent_id ? flat.find((d) => d.id === doc.parent_id) : null
  const author = book.user
  const authorAvatar = author?.avatar ? (/^https?:\/\//.test(author.avatar) ? author.avatar : API_BASE + author.avatar) : ''
  const cover = resolveMediaUrl(book.cover_image)

  const docUrl = doc ? `${siteUrl}/book/reader?slug=${encodeURIComponent(book.slug)}&doc=${encodeURIComponent(doc.slug)}` : siteUrl
  const jsonLd = doc ? [
    { '@context': 'https://schema.org', '@type': 'Chapter', name: `${chapterPrefix}${doc.title}`, url: docUrl, datePublished: doc.created_at, dateModified: doc.updated_at, isPartOf: { '@type': 'Book', name: book.title }, author: { '@type': 'Person', name: author?.username || '佚名' } },
    { '@context': 'https://schema.org', '@type': 'BreadcrumbList', itemListElement: [
      { '@type': 'ListItem', position: 1, name: siteName, item: siteUrl },
      { '@type': 'ListItem', position: 2, name: book.title, item: `${siteUrl}/book/detail?slug=${encodeURIComponent(book.slug)}` },
      { '@type': 'ListItem', position: 3, name: doc.title, item: docUrl },
    ] },
  ] : undefined

  function jumpTo(id: string) {
    document.getElementById(id)?.scrollIntoView({ behavior: 'smooth' })
    setActiveHeading(id)
  }

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-white">
      <Seo
        siteName={siteName}
        title={doc ? `${chapterPrefix}${doc.title} · ${book.title}` : book.title}
        description={doc ? excerptFrom(doc.content || book.description, 160) : book.description}
        url={docUrl}
        image={book.cover_image || undefined}
        jsonLd={jsonLd}
      />
      {/* 顶栏 */}
      <header className="flex shrink-0 items-center justify-between gap-3 border-b border-slate-200 bg-white px-4" style={{ height: 'var(--nav-height)' }}>
        <div className="flex min-w-0 items-center gap-2 text-sm">
          {/* 移动端目录/版本/语言入口（桌面端在左侧栏） */}
          <button type="button" onClick={() => setMobileNav(true)} aria-label={t('reader.toc')}
            className="-ml-1 shrink-0 rounded-lg p-2 text-slate-500 hover:bg-slate-100 lg:hidden">
            <i className="fa-solid fa-list-ul" aria-hidden="true" />
          </button>
          <Link href="/" className="flex shrink-0 items-center gap-2 font-bold text-slate-900">
            <img src="/logo.png" alt="" className="h-8 w-8 object-contain" />
            {siteName}
          </Link>
          <span className="text-slate-300">/</span>
          {/* 点击书名直接回到书籍详情页（替代原「返回书籍」按钮） */}
          <Link href={`/book/detail/${encodeURIComponent(book.slug)}`}
            className="max-w-[220px] truncate font-medium text-slate-900 hover:text-primary-600">{book.title}</Link>
        </div>
        <div className="hidden min-w-0 truncate text-sm font-medium text-slate-900 md:block">
          {doc ? `${chapterPrefix}${doc.title}` : book.title}
        </div>
        {qaOn && doc && (
          <Button size="sm" variant={qaTab ? 'primary' : 'outline'} className="shrink-0" onClick={() => navigateQa(qaTab ? null : 'ai')}>
            <i className="fa-solid fa-comments" aria-hidden="true" />{t('qa.reader.open')}
          </Button>
        )}
      </header>
      <div className="flex min-h-0 flex-1 items-stretch">
        {/* 左：书籍信息 + 目录 */}
        {!focus && (
          <aside className="hidden shrink-0 flex-col border-r border-slate-200 pt-5 lg:flex" style={{ width: 'var(--sidebar-width)' }}>
            <div className="shrink-0 px-4">
            <div className="mb-3 flex flex-col text-left">
              <div className="aspect-[16/10] w-full overflow-hidden rounded-lg border border-slate-200 bg-gradient-to-br from-primary-200 to-primary-600">
                {cover && <img src={cover} alt="" className="h-full w-full object-cover" onError={(e) => { e.currentTarget.style.display = 'none' }} />}
              </div>
              <div className="mt-3 flex flex-wrap gap-1.5">
                {(book.tags || []).map((t) => (
                  <span key={t.id} className="inline-flex shrink-0 items-center whitespace-nowrap rounded-full bg-emerald-50 px-2 py-0.5 text-xs font-medium text-emerald-700 ring-1 ring-inset ring-emerald-200">{t.name}</span>
                ))}
              </div>
              <div className="mt-1.5 flex items-center justify-between text-sm text-slate-500">
                <span className="flex items-center gap-1.5">
                  <UserAvatar user={author} size="h-5 w-5" />
                  {displayName(author) || t('reader.anonymous')}
                </span>
                <span className="text-xs text-slate-400">{t('reader.chaptersCount', { n: flat.length })}</span>
              </div>
              <div className="mt-3 flex flex-col gap-2"><BookTranslations bookId={book.id} linkTo="reader" /><BookVersions bookId={book.id} linkTo="reader" /></div>
              {canEdit && (
                <ButtonLink href={`/book/writer/${encodeURIComponent(book.slug)}/${doc ? encodeURIComponent(doc.slug) : ''}`}
                  className="mt-3 w-full">
                  <PencilIcon className="h-4 w-4" /> {t('reader.write')}
                </ButtonLink>
              )}
            </div>
            <div className="mb-2 mt-2 flex items-center justify-between">
              <div className="text-sm font-semibold text-slate-900">{t('reader.toc')}</div>
              {allParentIds.size > 0 && (
                <Tooltip content={allCollapsed ? t('reader.expandAll') : t('reader.collapseAll')}>
                  <button type="button" onClick={toggleAll}
                    aria-label={allCollapsed ? t('reader.expandAll') : t('reader.collapseAll')}
                    className="rounded p-1 text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-600">
                    {allCollapsed ? <ChevronRightIcon className="h-4 w-4" /> : <ChevronDownIcon className="h-4 w-4" />}
                  </button>
                </Tooltip>
              )}
            </div>
            <Input size="sm" value={tocSearch} onChange={(e) => setTocSearch(e.target.value)}
              placeholder={t('reader.tocSearchPlaceholder')} aria-label={t('reader.tocSearchPlaceholder')} className="mb-2" />
            </div>
            <div ref={tocScrollRef} className="min-h-0 flex-1 overflow-y-auto">
              <div className="min-w-max pb-2 pl-4">
                {filteredTree.length === 0 ? (
                  <p className="py-6 pr-4 text-center text-xs text-slate-400">{t('reader.tocNoMatch')}</p>
                ) : (
                  <ReaderTree items={filteredTree} bookSlug={book.slug} chapterPrefix={chapterPrefix} activeId={doc?.id}
                    expanded={searchExpanded ?? expanded} setExpanded={setExpanded} readSet={readSet} lockedSet={lockedSet} />
                )}
              </div>
            </div>
            <div className="shrink-0 border-t border-slate-100 px-4 py-2 text-center text-xs text-slate-400">
              Powered by KnowForge
            </div>
          </aside>
        )}

        {/* 移动端：目录/版本/语言抽屉（桌面端见左侧栏；点章节/切版本后由路由变化自动关闭） */}
        {mobileNav && (
          <div className="fixed inset-0 z-[70] lg:hidden">
            <div className="absolute inset-0 bg-black/40" onClick={() => setMobileNav(false)} />
            <div className="absolute inset-y-0 left-0 flex w-[86%] max-w-sm flex-col bg-white shadow-xl">
              <div className="flex shrink-0 items-center justify-between border-b border-slate-200 px-4 py-3">
                <span className="font-semibold text-slate-900">{t('reader.toc')}</span>
                <button type="button" onClick={() => setMobileNav(false)} aria-label={t('common.actions.cancel')}
                  className="rounded-lg p-1.5 text-slate-500 hover:bg-slate-100"><i className="fa-solid fa-xmark" aria-hidden="true" /></button>
              </div>
              <div className="shrink-0 space-y-2 border-b border-slate-100 px-4 py-3"><BookTranslations bookId={book.id} linkTo="reader" /><BookVersions bookId={book.id} linkTo="reader" /></div>
              <div className="shrink-0 px-4 py-2">
                <Input size="sm" value={tocSearch} onChange={(e) => setTocSearch(e.target.value)}
                  placeholder={t('reader.tocSearchPlaceholder')} aria-label={t('reader.tocSearchPlaceholder')} />
              </div>
              <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
                {filteredTree.length === 0 ? (
                  <p className="py-6 text-center text-xs text-slate-400">{t('reader.tocNoMatch')}</p>
                ) : (
                  <ReaderTree items={filteredTree} bookSlug={book.slug} chapterPrefix={chapterPrefix} activeId={doc?.id}
                    expanded={searchExpanded ?? expanded} setExpanded={setExpanded} readSet={readSet} lockedSet={lockedSet} />
                )}
              </div>
            </div>
          </div>
        )}

        {/* 中：正文（内部滚动）+ 底部固定的上一篇/下一篇 */}
        <main className="flex min-w-0 flex-1 flex-col overflow-hidden">
          <div ref={contentScrollRef} className="min-w-0 flex-1 overflow-y-auto">
            <div className="mx-auto w-full px-8 py-10 lg:px-14" style={{ maxWidth: 'var(--content-max-width)' }}>
              {doc ? (
                <article className="relative isolate">
                  {book.watermark_enabled && book.watermark_text && ((site as { feature_plugins?: string[] }).feature_plugins || []).includes('watermark') && <WatermarkLayer text={book.watermark_text} />}
                  {parentDoc && <div className="mb-1 text-sm font-medium text-primary-600">{chapterPrefix}{parentDoc.title}</div>}
                  <h1 className="text-3xl font-bold leading-tight text-ink sm:text-4xl">{doc.title}</h1>
                  <div className="mt-4 flex flex-wrap items-center gap-2 text-sm text-slate-400">
                    <UserAvatar user={author} size="h-6 w-6" />
                    <span className="text-slate-600">{displayName(author) || t('reader.anonymous')}</span>
                    <span>· {t('reader.updatedAt', { date: formatDate(doc.updated_at).slice(0, 10) })}</span>
                    <span>· {t('reader.readingMin', { n: readingMin })}</span>
                    <span>· {t('reader.readCount', { n: formatNumber(docViews) })}</span>
                    {canEdit && (
                      <Link href={`/book/writer/${encodeURIComponent(book.slug)}/${encodeURIComponent(doc.slug)}`}
                        className="flex items-center gap-1 text-primary-600 transition-colors hover:text-primary-700">
                        <i className="fa-solid fa-pen-to-square text-xs" aria-hidden="true" /> {t('reader.edit')}
                      </Link>
                    )}
                    <ReportButton targetType="document" targetId={doc.id} />
                  </div>
                  <hr className="my-6 border-slate-100" />
                  <ReaderAnnotations user={user} book={book} doc={doc} contentRef={contentRef} selectionActions={qaOn ? [{
                    key: 'qa', icon: 'fa-wand-magic-sparkles', label: t('qa.reader.askSelection'),
                    onSelect: (quote) => { setQaSelection(quote.slice(0, 2000)); navigateQa('ai') },
                  }] : []} />
                  {chapterGuideEnabled(site) && <ChapterGuideCard docId={doc.id} />}
                  <div ref={contentRef} className="markdown-body" style={{ fontSize: FONT_SIZES[fontIdx] }} dangerouslySetInnerHTML={{ __html: html }} />
                  {doc.paywall && <PaywallCard paywall={doc.paywall} />}
                  {backlinksEnabled(site) && <Backlinks docId={doc.id} bookId={book.id} />}
                  <RelatedChapters docId={doc.id} bookId={book.id} />

                  {/* 章节关闭评论时整个评论模块都不出现（不渲染标题/评论框/列表） */}
                  {doc.allow_comments !== false && <Comments docId={doc.id} allowComments />}

                {canEdit && (
                  <Link href={`/book/writer/${encodeURIComponent(book.slug)}/${doc.slug}`}
                    className="mt-6 inline-flex items-center gap-1.5 text-sm text-primary-600 hover:underline">
                    <PencilIcon className="h-3.5 w-3.5" /> {t('reader.editChapter')}
                  </Link>
                )}
                </article>
              ) : (
                <div className="py-24 text-center text-slate-400">
                  <p>{t('reader.pickChapter')}</p>
                  {flat.length === 0 && <p className="mt-2 text-xs">{t('reader.noPublished')}</p>}
                </div>
              )}
            </div>
          </div>

          {/* 上一篇/下一篇：固定在内容区底部（标题 + 创作者头像列表） */}
          {doc && (
            <nav className="flex shrink-0 items-start justify-between gap-3 border-t border-slate-200 bg-white px-8 py-3 lg:px-14">
              {prev ? (
                <div className="group flex min-w-0 flex-col gap-1.5 text-sm">
                  <span className="text-xs text-slate-400">{t('reader.prev')}</span>
                  {neighborExtURL(prev) ? (
                    <a href={neighborExtURL(prev)} target={prev.external_new_tab === false ? '_self' : '_blank'} rel="noopener noreferrer nofollow"
                      className="block truncate font-medium text-slate-800 group-hover:text-primary-600">{chapterPrefix}{prev.title} ↗</a>
                  ) : (
                    <Link href={`/book/reader/${encodeURIComponent(book.slug)}/${prev.slug}`}
                      className="block truncate font-medium text-slate-800 group-hover:text-primary-600">{chapterPrefix}{prev.title}</Link>
                  )}
                  <AuthorAvatars users={book.user ? [book.user] : []} />
                </div>
              ) : <span className="text-xs text-slate-300">{t('reader.firstChapter')}</span>}
              {next ? (
                <div className="group flex min-w-0 flex-col items-end gap-1.5 text-right text-sm">
                  <span className="text-xs text-slate-400">{t('reader.next')}</span>
                  {neighborExtURL(next) ? (
                    <a href={neighborExtURL(next)} target={next.external_new_tab === false ? '_self' : '_blank'} rel="noopener noreferrer nofollow"
                      className="block truncate font-medium text-slate-800 group-hover:text-primary-600">{chapterPrefix}{next.title} ↗</a>
                  ) : (
                    <Link href={`/book/reader/${encodeURIComponent(book.slug)}/${next.slug}`}
                      className="block truncate font-medium text-slate-800 group-hover:text-primary-600">{chapterPrefix}{next.title}</Link>
                  )}
                  <AuthorAvatars users={book.user ? [book.user] : []} />
                </div>
              ) : <span className="text-xs text-slate-300">{t('reader.lastChapter')}</span>}
            </nav>
          )}
        </main>

        {/* 右：本章目录（内滚）+ 阅读设置 + 作者（固定底部） */}
        {!focus && (
          <aside className="hidden shrink-0 flex-col border-l border-slate-200 px-5 py-6 xl:flex" style={{ width: 'var(--sidebar-width)' }}>
            {/* 本章目录：标题固定不滚动，列表过长时独立内部滚动 */}
            <div className="flex min-h-0 flex-1 flex-col">
              <h2 className="mb-3 shrink-0 text-sm font-semibold text-slate-900">{t('reader.chapterToc')}</h2>
              {headings.length > 0 ? (
                <div className="min-h-0 flex-1 overflow-y-auto">
                  <ul className="space-y-1 border-l border-slate-100">
                    {headings.map((h) => (
                      <li key={h.id}>
                        <a href={`#${h.id}`} onClick={(e) => { e.preventDefault(); jumpTo(h.id) }}
                          className={`-ml-px block border-l-2 py-1 text-sm transition-colors ${h.level === 3 ? 'pl-6' : 'pl-3'} ${
                            activeHeading === h.id ? 'border-primary-500 font-medium text-primary-600' : 'border-transparent text-slate-500 hover:text-slate-800'
                          }`}>
                          {h.text}
                        </a>
                      </li>
                    ))}
                  </ul>
                </div>
              ) : (
                <p className="px-1 py-2 text-xs text-slate-400">{t('reader.noChapterToc')}</p>
              )}
            </div>

            {/* 阅读设置：固定 */}
            <div className="shrink-0 border-t border-slate-100 pt-5">
              <h2 className="mb-3 text-sm font-semibold text-slate-900">{t('reader.settings')}</h2>
              <div className="grid grid-cols-3 gap-2">
                <SettingButton label={t('reader.fontDown')} onClick={() => setFontIdx((i) => Math.max(0, i - 1))} disabled={fontIdx === 0}>
                  <span className="text-base font-semibold">A-</span>
                </SettingButton>
                <SettingButton label={t('reader.fontUp')} onClick={() => setFontIdx((i) => Math.min(FONT_SIZES.length - 1, i + 1))} disabled={fontIdx === FONT_SIZES.length - 1}>
                  <span className="text-lg font-semibold">A+</span>
                </SettingButton>
                <SettingButton label={t('reader.focusMode')} active={focus} onClick={() => setFocus((f) => !f)}>
                  <FocusIcon className="h-5 w-5" />
                </SettingButton>
              </div>
            </div>

            {/* 作者：固定在最底部 */}
            {author && (
              <div className="mt-5 shrink-0 rounded-xl border border-slate-200 p-4">
                <div className="flex items-center gap-3">
                  {authorAvatar
                    ? <img src={authorAvatar} alt="" className="h-11 w-11 rounded-lg object-cover" />
                    : <span className="flex h-11 w-11 items-center justify-center rounded-lg bg-gradient-to-br from-primary-200 to-[#8B8DFF] font-semibold text-white">{(author.username || '?').slice(0, 1)}</span>}
                  <div className="min-w-0">
                    <div className="truncate font-semibold text-slate-900">{displayName(author)}</div>
                    {author.bio && <div className="line-clamp-2 text-xs text-slate-500">{author.bio}</div>}
                  </div>
                </div>
                <Link href={`/user/${encodeURIComponent(author.username)}`}
                  className="mt-3 flex w-full items-center justify-center rounded-lg border border-primary-500 text-sm font-medium text-primary-600 transition-colors hover:bg-primary-50"
                  style={{ height: 'var(--control-height)' }}>
                  {t('reader.viewAuthor')}
                </Link>
              </div>
            )}
          </aside>
        )}

        {/* 专注模式下的退出按钮 */}
        {focus && (
          <button onClick={() => setFocus(false)}
            className="fixed right-6 top-20 z-20 flex items-center gap-1.5 rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-sm text-slate-600 shadow-sm hover:text-primary-600">
            <FocusIcon className="h-4 w-4" /> {t('reader.exitFocus')}
          </button>
        )}
      </div>
      {qaTab && doc && (
        <QADrawer user={user} book={book} docId={doc.id} tab={qaTab} questionId={qaQuestion}
          selection={qaSelection} onClearSelection={() => setQaSelection('')}
          onNavigate={navigateQa} onCite={citeQa}
          loginHref={`/login?next=${encodeURIComponent(`/book/reader/${book.slug}/${doc.slug}?qa=${qaTab}`)}`} />
      )}
    </div>
  )
}

/* ── 子组件 ── */

function SettingButton({ children, label, onClick, active, disabled }: { children: ReactNode; label: string; onClick: () => void; active?: boolean; disabled?: boolean }) {
  return (
    <button type="button" onClick={onClick} disabled={disabled}
      className={`flex flex-col items-center justify-center gap-1 rounded-lg border py-2.5 text-xs transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${
        active ? 'border-primary-500 bg-primary-50 text-primary-600' : 'border-slate-200 text-slate-600 hover:border-slate-300 hover:bg-slate-50'
      }`}>
      {children}
      <span className="text-[11px] text-slate-400">{label}</span>
    </button>
  )
}

interface ReaderTreeProps {
  items: Document[]
  bookSlug: string
  chapterPrefix: string
  activeId?: number
  expanded: Set<number>
  setExpanded: (s: Set<number>) => void
  readSet: Set<number>
  lockedSet?: Set<number>
  depth?: number
}

function ReaderTree({ items, bookSlug, chapterPrefix, activeId, expanded, setExpanded, readSet, lockedSet, depth = 0 }: ReaderTreeProps) {
  const { t } = useTranslation()
  return (
    <ul className={depth === 0 ? 'min-w-max space-y-0.5' : 'ml-4 min-w-max space-y-0.5 border-l border-slate-100 pl-1'}>
      {items.map((item) => {
        const hasChildren = !!item.children?.length
        const isExpanded = expanded.has(item.id)
        const active = activeId === item.id
        const hasRead = readSet.has(item.id)
        return (
          <li key={item.id}>
            <div className={`group relative flex items-center rounded-lg text-sm ${active ? 'bg-primary-50' : 'hover:bg-slate-50'}`}>
              {active && <span className="absolute left-0 top-1.5 h-[calc(100%-12px)] w-0.5 rounded-full bg-primary-500" />}
              {hasChildren ? (
                <button type="button" aria-label={isExpanded ? t('reader.collapse') : t('reader.expand')}
                  onClick={() => { const n = new Set(expanded); n.has(item.id) ? n.delete(item.id) : n.add(item.id); setExpanded(n) }}
                  className="ml-1 flex h-6 w-5 shrink-0 items-center justify-center rounded text-slate-400 hover:text-slate-600">
                  {isExpanded ? <ChevronDownIcon className="h-3.5 w-3.5" /> : <ChevronRightIcon className="h-3.5 w-3.5" />}
                </button>
              ) : <span className="ml-1 w-5 shrink-0" />}
              {(item.external_url || '').trim() ? (
                <a href={(item.external_url || '').trim()} target={item.external_new_tab === false ? '_self' : '_blank'} rel={item.external_new_tab === false ? undefined : 'noopener noreferrer'}
                  className="flex flex-1 items-center gap-1.5 py-1.5 pl-1 pr-2 text-left">
                  <DocTreeIcon icon={item.icon} hasChildren={hasChildren} colorClass="text-slate-400" />
                  <span className="whitespace-nowrap text-slate-700">{chapterPrefix}{item.title}</span>
                  <i className="fa-solid fa-arrow-up-right-from-square ml-1 shrink-0 text-[10px] text-slate-400" aria-hidden="true" />
                </a>
              ) : (
              <Link href={`/book/reader?slug=${encodeURIComponent(bookSlug)}&doc=${item.slug}`}
                {...(active ? { 'data-toc-active': '1' } : {})}
                className="flex flex-1 items-center gap-1.5 py-1.5 pl-1 pr-2 text-left">
                <DocTreeIcon icon={item.icon} hasChildren={hasChildren} colorClass={active ? 'text-primary-500' : 'text-slate-400'} />
                <span className={`whitespace-nowrap ${active ? 'font-medium text-primary-700' : 'text-slate-700'}`}>{chapterPrefix}{item.title}</span>
                {lockedSet?.has(item.id) && <i className="fa-solid fa-lock ml-1 shrink-0 text-[10px] text-amber-500" aria-label={t('paid.locked')} />}
                {hasRead && (
                  <span className="ml-auto flex shrink-0 items-center gap-1 pl-2 text-[11px] font-medium text-emerald-600">
                    <CheckCircleSmallIcon className="h-3.5 w-3.5" /> {t('reader.readBadge')}
                  </span>
                )}
              </Link>
              )}
            </div>
            {hasChildren && isExpanded && (
              <ReaderTree items={item.children!} bookSlug={bookSlug} chapterPrefix={chapterPrefix} activeId={activeId}
                expanded={expanded} setExpanded={setExpanded} readSet={readSet} lockedSet={lockedSet} depth={depth + 1} />
            )}
          </li>
        )
      })}
    </ul>
  )
}

function FocusIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d="M4 8V5a1 1 0 0 1 1-1h3M16 4h3a1 1 0 0 1 1 1v3M20 16v3a1 1 0 0 1-1 1h-3M8 20H5a1 1 0 0 1-1-1v-3" />
    </svg>
  )
}
