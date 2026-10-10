import Link from 'next/link'
import Container from '@/components/Container'
import type { GetServerSideProps, InferGetServerSidePropsType } from 'next'
import { serverApi, getSiteConfig, siteUrlFrom, authHeaderFrom, excerptFrom, isInstalled, getSSRUser } from '@/lib/server-api'
import { API_BASE, formatNumber } from '@/lib/api'
import { resolveMediaUrl } from '@/lib/media'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { api } from '@/lib/api'
import { getReadingProgress } from '@/lib/reading-progress'
import { useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/router'
import { Button, ButtonLink, Tooltip, Loading, SegmentedTabs, useFeedback } from '@/components/ui'
import UserAvatar from '@/components/UserAvatar'
import TagChips from '@/components/TagChips'
import BookCard from '@/components/BookCard'
import BookExportButton from '@/components/BookExportButton'
import BookSearch from '@/components/BookSearch'
import BookTranslations from '@/components/BookTranslations'
import BookVersions from '@/components/BookVersions'
import BookCopyDialog from '@/components/BookCopyDialog'
import BookReviews from '@/components/BookReviews'
import PaidBookCard from '@/components/PaidBookCard'
import QACommunity from '@/components/qa/QACommunity'
import BookOverviewCard from '@/components/chapter-guide/BookOverviewCard'
import { chapterGuideEnabled } from '@/lib/chapter-guide'
import { qaEnabled } from '@/lib/qa'
import BookExtraInfo from '@/components/BookExtraInfo'
import ReportButton from '@/components/ReportButton'
import CoverImage from '@/components/CoverImage'
import Seo from '@/components/Seo'
import {
  BookIcon, CalendarIcon, CheckCircleSmallIcon, ChevronRightIcon, EyeIcon,
  GlobeIcon, HeartIcon, HelpCircleIcon, LinkIcon, ShareIcon, BookmarkIcon, ClockIcon, GearIcon,
} from '@/components/icons'
import type { Book, BookAccess, Document, User } from '@/lib/types'
import FeedButton from '@/components/FeedButton'
import { bookFeedPath, feedsEnabled } from '@/lib/feeds'
import EmbedModal from '@/components/EmbedModal'
import { embedEnabled } from '@/lib/embed'
import AddToListModal from '@/components/booklists/AddToListModal'
import OfflineBookButton from '@/components/OfflineBookButton'
import BookListsOnBook from '@/components/booklists/BookListsOnBook'
import { bookListsEnabled } from '@/lib/booklists'
import { displayName } from '@/lib/users'
import { categoriesEnabled, categoryHref } from '@/lib/categories'

interface BookDetailProps {
  installed: boolean
  user: User | null
  site: Record<string, string>
  siteUrl: string
  book: Book
  tree: Document[]
  /** true 表示 SSR 阶段无法公开访问（草稿/私有），交给客户端携带令牌重试 */
  needsAuth: boolean
  related: Book[]
  access: BookAccess | null
}

interface BookData {
  book: Book | null
  tree: Document[]
  status: number
}

async function fetchBook(slug: string, auth: Record<string, string>): Promise<BookData> {
  try {
    const book = await serverApi<Book>(`/books/slug/${encodeURIComponent(slug)}`, { headers: auth })
    const tree = await serverApi<Document[]>(`/books/${book.id}/documents`, { headers: auth }).catch(() => [])
    return { book, tree, status: 200 }
  } catch (e) {
    return { book: null, tree: [], status: (e as { status?: number }).status ?? 500 }
  }
}

export const getServerSideProps: GetServerSideProps<BookDetailProps> = async ({ req, params }) => {
  // 未安装时强制进入安装向导（服务端重定向，不渲染任何内容）
  if (!(await isInstalled())) {
    return { redirect: { destination: '/install', permanent: false } }
  }

  const slug = typeof params?.slug === 'string' ? params.slug : ''
  if (!slug) return { notFound: true }
  const user = await getSSRUser(req)

  const auth = authHeaderFrom(req)
  const [site, first] = await Promise.all([getSiteConfig(), fetchBook(slug, auth)])

  // 公开访问失败且用户带了令牌（草稿/私有书），交给客户端重试
  if (!first.book) {
    if (auth.Authorization) {
      return { props: { installed: true, user, site, siteUrl: siteUrlFrom(req), book: null as unknown as Book, tree: [], needsAuth: true, related: [], access: null } }
    }
    return { notFound: true }
  }

  // 你可能也喜欢：优先内容相近的书（站点开启语义检索时），否则同标签的其它公开书籍
  const semantic = await serverApi<{ items: Book[] }>(`/books/${first.book.id}/related`, { headers: auth, params: { limit: 3 } })
    .then((d) => d.items || []).catch(() => [] as Book[])
  const tagSlug = first.book.tags?.[0]?.slug
  const related = semantic.length > 0 ? semantic : tagSlug
    ? await serverApi<Book[]>(`/tags/${encodeURIComponent(tagSlug)}/books`, { params: { page_size: 6 } })
        .then((d) => (d as unknown as { items?: Book[] }).items || []).catch(() => [] as Book[])
    : []

  const access = user
    ? await serverApi<BookAccess>(`/books/slug/${encodeURIComponent(slug)}/access`, { headers: auth }).catch(() => null)
    : null

  return { props: { installed: true, user, site, siteUrl: siteUrlFrom(req), book: first.book, tree: first.tree, needsAuth: false, related: related.filter((b) => b.id !== first.book!.id).slice(0, 3), access } }
}

// —— 计算辅助 ——

function countChapters(docs: Document[]): { chapters: number; sections: number } {
  let chapters = 0
  let sections = 0
  for (const d of docs) {
    if (d.children?.length) { chapters += 1; sections += d.children.length }
    else if (d.parent_id === null) chapters += 1
  }
  return { chapters, sections }
}

export default function BookDetail({ site, siteUrl, book: ssrBook, tree: ssrTree, related, needsAuth, access }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { requestInput, showToast, confirmAction } = useFeedback()
  const { user, authReady } = useApp()
  const { t } = useTranslation()
  const router = useRouter()
  // 标签插件禁用时隐藏详情页的标签入口（面包屑标签、标签 chip）
  const tagsEnabled = Array.isArray((site as Record<string, unknown>).feature_plugins) && ((site as Record<string, unknown>).feature_plugins as string[]).includes('tags')
  const followEnabled = Array.isArray((site as Record<string, unknown>).feature_plugins) && ((site as Record<string, unknown>).feature_plugins as string[]).includes('book-follow')
  const slug = typeof router.query.slug === 'string' ? router.query.slug : ''
  // 目录 / 评价 / 问答 横向 Tab：由 URL 承载（?tab=reviews|qa，问答详情 &question=ID），浅路由切换，可分享可回退
  const qaOn = qaEnabled(site)
  const activeTab = router.query.tab === 'reviews' ? 'reviews' : router.query.tab === 'qa' && qaOn ? 'qa' : 'toc'
  const qaQuestion = Number(router.query.question) || null
  const goTab = (value: string, question?: number | null) => router.push({ pathname: '/book/detail/[slug]', query: {
    slug, ...(value !== 'toc' ? { tab: value } : {}), ...(value === 'qa' && question ? { question: String(question) } : {}),
  } }, undefined, { shallow: true, scroll: false })
  // 私有/草稿书 SSR 无令牌取不到，挂载后携带本地令牌客户端重试（避免默认空白）
  const [book, setBook] = useState<Book | null>(ssrBook ?? null)
  const [copyOpen, setCopyOpen] = useState(false)
  const [embedOpen, setEmbedOpen] = useState(false)
  const [addToListOpen, setAddToListOpen] = useState(false)
  const [bookViews, setBookViews] = useState(ssrBook?.view_count || 0)
  const countedBook = useRef<number | null>(null)
  const [tree, setTree] = useState<Document[]>(ssrTree || [])
  const [fetching, setFetching] = useState<boolean>(!!needsAuth && !ssrBook)
  // 切换语言/版本会 Link 跳到另一本 slug：同一动态路由复用组件实例，需在 slug 变化时用新 SSR props 覆盖本地状态，否则不刷新不生效
  useEffect(() => {
    if (ssrBook) { setBook(ssrBook); setTree(ssrTree || []) }
  }, [slug]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!needsAuth || ssrBook || !slug) return
    let cancelled = false
    ;(async () => {
      try {
        const b = await api<Book>(`/books/slug/${encodeURIComponent(slug)}`)
        const docs = await api<Document[]>(`/books/${b.id}/documents`).catch(() => [] as Document[])
        if (!cancelled) { setBook(b); setTree(docs) }
      } catch { /* 无权限：保持 null，交给 ClientFallback 提示 */ }
      finally { if (!cancelled) setFetching(false) }
    })()
    return () => { cancelled = true }
  }, [needsAuth, ssrBook, slug])
  useEffect(() => {
    if (!book || countedBook.current === book.id) return
    countedBook.current = book.id
    setBookViews(book.view_count)
    api<{ view_count: number }>(`/books/${book.id}/view`, {
      method: 'POST', body: { referrer: document.referrer },
    }).then((result) => setBookViews(result.view_count)).catch(() => { /* 计数失败不影响详情页 */ })
  }, [book])
  // 阅读进度仅存在于本地，客户端挂载后读取（避免水合不一致）
  const [progress, setProgress] = useState<{ docSlug: string; docTitle: string; chapterPrefix?: string; readSeconds?: number } | null>(null)
  const [progressBusy, setProgressBusy] = useState<'reset' | 'complete' | null>(null)
  const [liked, setLiked] = useState(false)
  const [likeCount, setLikeCount] = useState<number | null>(null)
  const [favorited, setFavorited] = useState(false)
  const [favoriteCount, setFavoriteCount] = useState<number | null>(null)
  const [reactionsReady, setReactionsReady] = useState(false)
  const [reactionError, setReactionError] = useState('')
  const [reactBusy, setReactBusy] = useState<'like' | 'favorite' | null>(null)
  const [following, setFollowing] = useState(false)
  const [followBusy, setFollowBusy] = useState(false)
  const bookSlugSafe = book?.slug || ''
  const username = user?.username || ''
  useEffect(() => {
    if (username && book) {
      getReadingProgress(username, book.id).then(setProgress)
    }
  }, [username, book])

  useEffect(() => {
    if (!authReady || !book) {
      setReactionsReady(false)
      return
    }
    if (!user) {
      setLiked(false)
      setLikeCount(null)
      setFavorited(false)
      setFavoriteCount(null)
      setReactionError('')
      setReactionsReady(true)
      return
    }

    let cancelled = false
    setReactionsReady(false)
    setReactionError('')
    api<{ types: string[]; like_count: number; favorite_count: number }>(`/books/${book.id}/reactions/me`)
      .then((result) => {
        if (cancelled) return
        const types = result.types || []
        setLiked(types.includes('like'))
        setLikeCount(result.like_count || 0)
        setFavorited(types.includes('favorite'))
        setFavoriteCount(result.favorite_count || 0)
      })
      .catch((error) => {
        if (cancelled) return
        setLiked(false)
        setLikeCount(null)
        setFavorited(false)
        setFavoriteCount(null)
        setReactionError((error as Error).message || t('detail.reactionLoadFailed'))
      })
      .finally(() => {
        if (!cancelled) setReactionsReady(true)
      })

    if (followEnabled) {
      api<{ following: boolean }>(`/books/${book.id}/follow/me`)
        .then((r) => { if (!cancelled) setFollowing(!!r.following) })
        .catch(() => { /* 忽略 */ })
    }

    return () => { cancelled = true }
  }, [authReady, user, book, followEnabled]) // eslint-disable-line react-hooks/exhaustive-deps

  // 阅读进度：登录用户读过的章节 ID 集合，用于目录标记与整体进度
  const [readSet, setReadSet] = useState<Set<number>>(new Set())
  useEffect(() => {
    if (!user || !book) { setReadSet(new Set()); return }
    api<{ doc_ids: number[] }>(`/books/${book.id}/read-chapters`)
      .then((r) => setReadSet(new Set(r.doc_ids || [])))
      .catch(() => { /* 未登录或无进度：空集合 */ })
  }, [user?.id, book?.id]) // eslint-disable-line react-hooks/exhaustive-deps
  const siteName = site.site_name || 'KnowForge'
  const chapterPrefix = book?.chapter_prefix || ''

  const canManage = access?.can_manage === true || (!!user && !!book && (user.id === book.user_id || user.role === 'admin'))
  const canEdit = access?.can_edit_content === true || canManage

  if (fetching) {
    return <Container><Loading className="min-h-[60vh]" label={t('detail.loading')} /></Container>
  }
  if (!book) {
    return <ClientFallback slug={slug} />
  }

  const cover = resolveMediaUrl(book.cover_image)
  const { chapters } = countChapters(tree)
  const firstDoc = flatFirst(tree)
  const readDocSlug = firstDoc?.slug || ''
  const readUrl = readDocSlug ? `/book/reader/${encodeURIComponent(book.slug)}/${readDocSlug}` : ''
  const bookUrl = `${siteUrl}/book/detail/${encodeURIComponent(book.slug)}`
  const words = flatWords(tree)
  const readingMin = Math.max(1, Math.round(words / 400))
  const author = book.user

  // 整体阅读进度：已读章节数 / 全部章节数
  const allDocIds = flatDocIds(tree)
  const totalChapters = allDocIds.length
  const readCount = allDocIds.filter((id) => readSet.has(id)).length
  const progressPct = totalChapters > 0 ? Math.round((readCount / totalChapters) * 100) : 0

  // 重新拉取已读章节与进度（重置/{t('detail.markRead')}后刷新展示）
  const refreshReading = async () => {
    try {
      const r = await api<{ doc_ids: number[] }>(`/books/${book.id}/read-chapters`)
      setReadSet(new Set(r.doc_ids || []))
    } catch { /* 忽略 */ }
    try { setProgress(await getReadingProgress(username, book.id)) } catch { /* 忽略 */ }
  }
  const handleMarkRead = async () => {
    setProgressBusy('complete')
    try {
      await api(`/reading-progress/${book.id}/complete`, { method: 'POST' })
      await refreshReading()
      showToast({ message: t('detail.markReadDone'), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error)?.message || t('detail.opFailed'), tone: 'error' })
    } finally {
      setProgressBusy(null)
    }
  }
  const handleResetProgress = async () => {
    const confirmed = await confirmAction({
      title: t('detail.resetTitle'),
      message: t('detail.resetConfirm'),
      confirmLabel: t('detail.reset'),
      danger: true,
    })
    if (!confirmed) return
    setProgressBusy('reset')
    try {
      await api(`/reading-progress/${book.id}`, { method: 'DELETE' })
      setReadSet(new Set())
      setProgress(null)
      showToast({ message: t('detail.resetDone'), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error)?.message || t('detail.opFailed'), tone: 'error' })
    } finally {
      setProgressBusy(null)
    }
  }

  // 分类插件：面包屑与结构化数据使用分类路径（顶级 → 书籍所在分类），未分类时沿用首个标签
  const categoryPath = categoriesEnabled(site) && book.category ? (book.category.path?.length ? book.category.path : [book.category]) : []
  const jsonLd = [
    {
      '@context': 'https://schema.org', '@type': 'Book', name: book.title,
      description: book.description || undefined,
      author: { '@type': 'Person', name: displayName(author) || t('detail.anonymous') },
      url: bookUrl, inLanguage: 'zh-CN',
    },
    { '@context': 'https://schema.org', '@type': 'BreadcrumbList', itemListElement: [
      { '@type': 'ListItem', position: 1, name: siteName, item: siteUrl },
      { '@type': 'ListItem', position: 2, name: t('detail.breadcrumbExplore'), item: `${siteUrl}/explore` },
      ...categoryPath.map((c, i) => ({ '@type': 'ListItem', position: 3 + i, name: c.name, item: `${siteUrl}${categoryHref(c.slug)}` })),
      { '@type': 'ListItem', position: 3 + categoryPath.length, name: book.title, item: bookUrl },
    ] },
  ]

  async function share() {
    try {
      await navigator.clipboard.writeText(bookUrl)
      showToast({ message: t('detail.linkCopied'), tone: 'success' })
    } catch {
      await requestInput({ title: t('detail.shareTitle'), label: t('detail.shareLabel'), defaultValue: bookUrl, confirmLabel: t('detail.close') })
    }
  }

  async function toggleReaction(type: 'like' | 'favorite') {
    const currentBook = book
    if (!currentBook) return
    if (!user) {
      const next = router.asPath.startsWith('/') && !router.asPath.startsWith('//') ? router.asPath : `/book/detail/${encodeURIComponent(currentBook.slug)}`
      await router.push(`/login?next=${encodeURIComponent(next)}`)
      return
    }
    if (!reactionsReady || reactBusy) return

    const wasActive = type === 'like' ? liked : favorited
    const previousCount = type === 'like' ? likeCount : favoriteCount
    setReactBusy(type)
    setReactionError('')
    if (type === 'like') {
      setLiked(!wasActive)
      setLikeCount((count) => count === null ? count : Math.max(0, count + (wasActive ? -1 : 1)))
    } else {
      setFavorited(!wasActive)
      setFavoriteCount((count) => count === null ? count : Math.max(0, count + (wasActive ? -1 : 1)))
    }
    try {
      if (wasActive) {
        await api(`/books/${currentBook.id}/reactions`, { method: 'DELETE', params: { type } })
      } else {
        await api(`/books/${currentBook.id}/reactions`, { method: 'POST', body: { type } })
      }
    } catch (error) {
      if (type === 'like') {
        setLiked(wasActive)
        setLikeCount(previousCount)
      } else {
        setFavorited(wasActive)
        setFavoriteCount(previousCount)
      }
      setReactionError((error as Error).message || t(type === 'like' ? 'detail.likeFailed' : 'detail.favoriteFailed'))
    } finally {
      setReactBusy(null)
    }
  }

  async function toggleFollow() {
    if (!book) return
    if (!user) { router.push(`/login?redirect=${encodeURIComponent(router.asPath)}`); return }
    if (followBusy) return
    const next = !following
    setFollowBusy(true)
    setFollowing(next)
    try {
      await api(`/books/${book.id}/follow`, { method: next ? 'POST' : 'DELETE' })
    } catch (e) {
      setFollowing(!next)
      showToast({ title: t('detail.follow.failed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setFollowBusy(false)
    }
  }

  return (
    <div className="min-w-0 overflow-x-hidden bg-warm">
      <Seo
        siteName={siteName}
        title={book.title}
        description={excerptFrom(book.description || t('detail.seoBookOf', { title: book.title, author: author?.username || '' }), 160)}
        url={bookUrl}
        image={book.cover_image || undefined}
        jsonLd={jsonLd}
        rss={feedsEnabled(site) ? { title: book.title, href: bookFeedPath(book.slug) } : undefined}
      />

      <Container className="!py-0">
        {/* 面包屑 */}
        <nav className="flex min-w-0 items-center gap-1.5 overflow-hidden py-3 text-sm text-slate-500">
          <Link href="/explore" className="shrink-0 hover:text-primary-600">{t('detail.breadcrumbExplore')}</Link>
          {categoryPath.map((c) => (
            <span key={c.id} className="flex min-w-0 items-center gap-1.5" data-testid="book-category-crumb">
              <span className="text-slate-300">/</span>
              <Link href={categoryHref(c.slug)} className="truncate hover:text-primary-600">{c.name}</Link>
            </span>
          ))}
          {categoryPath.length === 0 && tagsEnabled && (book.tags || []).slice(0, 1).map((t) => (
            <span key={t.id} className="flex min-w-0 items-center gap-1.5">
              <span className="text-slate-300">/</span>
              <Link href={`/explore?tag=${encodeURIComponent(t.slug)}`} className="truncate hover:text-primary-600">{t.name}</Link>
            </span>
          ))}
          <span className="text-slate-300">/</span>
          <span className="truncate text-slate-900">{book.title}</span>
        </nav>
      </Container>

      {/* Hero */}
      <Container className="!py-5 sm:!py-8">
        <section className="grid min-w-0 grid-cols-1 items-start gap-x-10 gap-y-6 pb-4 sm:gap-y-8 lg:grid-cols-[300px_minmax(0,1fr)_300px] lg:pb-8">
          {/* 左：大封面 */}
          <div className="mx-auto w-36 sm:w-52 lg:mx-0 lg:w-full">
            <div className="relative aspect-[3/4] w-full overflow-hidden rounded-xl border border-slate-200 bg-gradient-to-br from-primary-200 to-[#8B8DFF] shadow-md">
              {cover && <CoverImage src={cover} alt={book.title} />}
            </div>
          </div>

          {/* 中：标题区 + 操作 */}
          <div className="min-w-0">
            <h1 className="break-words text-2xl font-bold leading-tight text-ink sm:text-3xl md:text-4xl">{book.title}</h1>
            {book.description && <p className="mt-3 max-w-full whitespace-normal text-[15px] leading-7 text-slate-500 [overflow-wrap:anywhere]">{book.description}</p>}

            {tagsEnabled && (book.tags || []).length > 0 && (
            <div className="mt-4 flex min-w-0 max-w-full flex-wrap gap-2 overflow-hidden">
              {(book.tags || []).map((t) => (
                <span key={t.id} className="inline-flex max-w-full items-center truncate rounded-md bg-emerald-50 px-2.5 py-1 text-xs font-medium text-emerald-700 ring-1 ring-inset ring-emerald-200">{t.name}</span>
              ))}
            </div>
            )}

            <div className="mt-4 flex flex-col gap-2"><BookTranslations bookId={book.id} /><BookVersions bookId={book.id} /></div>

            {author && (
              <div className="mt-6 flex min-w-0 flex-wrap items-center gap-3 sm:gap-4">
                <Link href={`/user/${encodeURIComponent(author.username)}`} className="flex min-w-0 items-center gap-3">
                  <UserAvatar user={author} size="h-11 w-11" link={false} />
                  <span className="min-w-0">
                    <span className="block truncate font-semibold text-slate-900">{displayName(author)}</span>
                    {author.bio && <span className="block truncate text-xs text-slate-400">{author.bio}</span>}
                  </span>
                </Link>
                <Link href={`/user/${encodeURIComponent(author.username)}`}
                  className="rounded-lg border border-slate-300 px-3.5 py-2 text-sm text-slate-700 transition-colors hover:border-primary-400 hover:text-primary-600">{t('detail.viewAuthor')}</Link>
              </div>
            )}

            {book.crawling && (
              <div className="mt-4 flex items-center gap-2 rounded-lg border border-primary-200 bg-primary-50/60 px-3 py-2 text-sm text-primary-700">
                <i className="fa-solid fa-spinner fa-spin" aria-hidden="true" /> {t('collect.crawlingHint')}
              </div>
            )}

            {/* 统计条 */}
            <div className="mt-6 grid grid-cols-2 gap-x-4 gap-y-3 text-sm text-slate-500 sm:flex sm:flex-wrap sm:items-center sm:gap-x-6 sm:gap-y-2">
              <span className="flex items-center gap-1.5"><BookIcon className="h-4 w-4" /> {t('detail.chaptersCount', { n: totalChapters })}</span>
              <span className="flex items-center gap-1.5"><ClockIcon className="h-4 w-4" /> {t('detail.readingMin', { n: readingMin })}</span>
              <span className="flex items-center gap-1.5"><EyeIcon className="h-4 w-4" /> {t('detail.readCount', { n: formatNumber(bookViews) })}</span>
              <span className="flex items-center gap-1.5"><CalendarIcon className="h-4 w-4" /> {t('detail.updatedAt', { date: fmtDate(book.updated_at) })}</span>
            </div>

            {/* 操作 */}
            {/* 上次阅读（有进度且不是第一章时显示） */}
            {progress && progress.docSlug !== readDocSlug && readDocSlug && (
              <p className="mt-5 text-sm text-slate-500">
                {t('detail.lastRead')}{progress.chapterPrefix}{progress.docTitle}
                <Link href={`/book/reader/${encodeURIComponent(book.slug)}/${progress.docSlug}`}
                  className="ml-3 font-medium text-primary-600 hover:underline">{t('detail.continue')}</Link>
              </p>
            )}

            <div className="mt-6 min-w-0 space-y-2 sm:flex sm:flex-wrap sm:items-center sm:gap-3 sm:space-y-0">
              {readUrl ? (
                <ButtonLink href={progress && progress.docSlug !== readDocSlug ? `/book/reader/${encodeURIComponent(book.slug)}/${progress.docSlug}` : readUrl} className="w-full px-7 text-base sm:w-auto">
                  <BookIcon className="h-5 w-5" /> {progress && progress.docSlug !== readDocSlug ? t('detail.continue') : t('detail.startRead')}
                </ButtonLink>
              ) : (
                <span className="text-sm text-slate-400">{t('detail.noPublished')}</span>
              )}
              <div className="grid min-w-0 grid-cols-2 gap-2 sm:contents">
                <Button type="button" variant="outline" onClick={() => toggleReaction('like')}
                  disabled={!authReady || (!!user && !reactionsReady) || reactBusy !== null}
                  aria-pressed={liked}
                  className={`w-full px-3 sm:w-auto sm:px-5 ${liked
                    ? 'border-rose-200 bg-rose-50 text-rose-700 hover:border-rose-300 hover:bg-rose-100'
                    : 'border-slate-300 bg-white text-slate-700 hover:border-slate-400 hover:bg-slate-50'}`}>
                  <HeartIcon className="h-4 w-4" />
                  {!authReady || (!!user && !reactionsReady) || reactBusy === 'like'
                    ? t('detail.processing')
                    : `${liked ? t('detail.liked') : t('detail.like')}${likeCount !== null ? ` ${formatNumber(likeCount)}` : ''}`}
                </Button>
                <Button type="button" variant="outline" onClick={() => toggleReaction('favorite')}
                  disabled={!authReady || (!!user && !reactionsReady) || reactBusy !== null}
                  aria-pressed={favorited}
                  className={`w-full px-3 sm:w-auto sm:px-5 ${favorited
                    ? 'border-primary-200 bg-primary-50 text-primary-700 hover:border-primary-300 hover:bg-primary-100'
                    : 'border-slate-300 bg-white text-slate-700 hover:border-slate-400 hover:bg-slate-50'}`}>
                  <BookmarkIcon className="h-4 w-4" />
                  {!authReady || (!!user && !reactionsReady) || reactBusy === 'favorite'
                    ? t('detail.processing')
                    : `${favorited ? t('detail.favorited') : t('detail.favorite')}${favoriteCount !== null ? ` ${formatNumber(favoriteCount)}` : ''}`}
                </Button>
              </div>
              {followEnabled && (
              <div className="grid min-w-0 grid-cols-1 sm:contents">
                <Button type="button" variant="outline" onClick={toggleFollow} disabled={followBusy} aria-pressed={following}
                  className={`w-full px-3 sm:w-auto sm:px-5 ${following
                    ? 'border-primary-200 bg-primary-50 text-primary-700 hover:border-primary-300 hover:bg-primary-100'
                    : 'border-slate-300 bg-white text-slate-700 hover:border-slate-400 hover:bg-slate-50'}`}>
                  <i className="fa-solid fa-bell" aria-hidden="true" />
                  {followBusy ? t('detail.processing') : (following ? t('detail.follow.following') : t('detail.follow.follow'))}
                </Button>
              </div>
              )}
              <div className="grid min-w-0 grid-cols-2 gap-2 sm:contents">
                <Tooltip content={t('detail.share')} className="w-full sm:w-auto"><Button type="button" variant="outline" onClick={share}
                  className="w-full !px-0 text-slate-500 sm:w-[var(--control-height)]">
                    <ShareIcon className="h-4 w-4" />
                  </Button></Tooltip>
                {feedsEnabled(site) && book.is_public && <FeedButton path={bookFeedPath(book.slug)} className="w-full sm:w-auto" />}
                {bookListsEnabled(site) && user && (
                  <Tooltip content={t('booklists.add.button')} className="w-full sm:w-auto"><Button type="button" variant="outline" onClick={() => setAddToListOpen(true)} aria-label={t('booklists.add.button')}
                    className="w-full !px-0 text-slate-500 sm:w-[var(--control-height)]">
                    <i className="fa-solid fa-layer-group" aria-hidden="true" />
                  </Button></Tooltip>
                )}
                {embedEnabled(site) && book.is_public && (
                  <Tooltip content={t('embed.button')} className="w-full sm:w-auto"><Button type="button" variant="outline" onClick={() => setEmbedOpen(true)} aria-label={t('embed.button')}
                    className="w-full !px-0 text-slate-500 sm:w-[var(--control-height)]">
                    <i className="fa-solid fa-code" aria-hidden="true" />
                  </Button></Tooltip>
                )}
                <OfflineBookButton bookSlug={book.slug} tree={tree} className="w-full sm:w-auto" />
                <BookExportButton book={book} className="w-full sm:w-auto" />
              </div>
              {user && (
                <Button type="button" variant="outline" onClick={() => setCopyOpen(true)} className="w-full sm:w-auto">{t('detail.copy')}</Button>
              )}
              {(canManage || canEdit) && (
                <div className="grid min-w-0 grid-cols-2 gap-2 sm:contents">
                  {canEdit && <ButtonLink href={`/book/writer/${encodeURIComponent(book.slug)}`} variant="outline" className="w-full sm:w-auto">{t('detail.write')}</ButtonLink>}
                  {canManage && (
                    <ButtonLink href={`/book/settings/${encodeURIComponent(book.slug)}`} variant="outline" className="w-full sm:w-auto">
                      <GearIcon className="h-4 w-4" /> {t('detail.settings')}
                    </ButtonLink>
                  )}
                </div>
              )}
            </div>
            {reactionError && <p role="alert" className="mt-2 text-sm text-rose-600">{reactionError}</p>}
          </div>

          {/* 右：书籍信息卡 */}
          <aside className="min-w-0 space-y-5">
            <div className="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
              <h2 className="mb-4 font-bold text-slate-900">{t('detail.infoHeading')}</h2>
              <dl className="space-y-3 text-sm">
                <InfoRow icon={<CheckCircleSmallIcon className="h-4 w-4" />} label={t('detail.infoStatus')} value={t(`book.status.${book.status}`)} />
                <InfoRow icon={<GlobeIcon className="h-4 w-4" />} label={t('detail.infoVisibility')} value={book.is_public ? t('detail.infoPublic') : t('detail.infoPrivate')} />
                <InfoRow icon={<CalendarIcon className="h-4 w-4" />} label={t('detail.infoCreatedAt')} value={fmtDate(book.created_at)} />
                <InfoRow icon={<CalendarIcon className="h-4 w-4" />} label={t('detail.infoUpdatedAt')} value={fmtDate(book.updated_at)} />
                <InfoRow icon={<LinkIcon className="h-4 w-4" />} label={t('detail.infoPath')} value={`/${book.slug}`} mono />
              </dl>
            </div>

            <PaidBookCard bookId={book.id} />
            <BookExtraInfo items={book.extra_info} />

            <ReportButton targetType="book" targetId={book.id} size="md"
              className="w-full border border-rose-300 text-rose-600 hover:bg-rose-50" />

            <div className="flex min-w-0 items-start gap-1.5 text-sm text-slate-500">
              <HelpCircleIcon className="mt-0.5 h-4 w-4 shrink-0" /> <span className="min-w-0 [overflow-wrap:anywhere]">{t('detail.reportHint')}</span>
            </div>
          </aside>
        </section>
      </Container>

      {/* 关于这本书（始终显示在每个 Tab 上方） + 目录 / 评价 横向 Tab */}
      <Container>
        <section className="border-t border-slate-200 py-6">
          <div className="max-w-3xl">
            <h2 className="text-xl font-bold text-slate-900">{t('detail.about')}</h2>
            <div className="mt-4 min-w-0 space-y-3 text-[15px] leading-7 text-slate-600">
              {(book.description || t('detail.noDescription')).split('\n').filter(Boolean).map((para, i) => <p key={i} className="max-w-full [overflow-wrap:anywhere]">{para}</p>)}
            </div>
          </div>
          {chapterGuideEnabled(site) && <BookOverviewCard bookId={book.id} bookSlug={book.slug} />}

          <SegmentedTabs className="mb-6 mt-8 max-w-md" value={activeTab} ariaLabel={t('detail.toc')} onChange={(v) => goTab(v)}
            items={[{ value: 'toc', label: t('detail.toc') }, { value: 'reviews', label: t('review.title') },
              ...(qaOn ? [{ value: 'qa', label: t('qa.detail.tab') }] : [])]} />
          {activeTab === 'toc' ? (
          <div className="max-w-3xl">
            <div className="mb-4 flex items-baseline gap-3">
              <h2 className="text-xl font-bold text-slate-900">{t('detail.toc')}</h2>
              <span className="text-sm text-slate-400">
                {t('detail.tocCount', { n: chapters })}
                {totalChapters > chapters && <span className="ml-1">{t('detail.tocCountTotal', { n: totalChapters })}</span>}
              </span>
            </div>

            {user && totalChapters > 0 && (
              <div className="mb-4 rounded-xl border border-slate-200 bg-white p-4">
                <div className="flex flex-col gap-1 text-sm sm:flex-row sm:items-center sm:justify-between">
                  <span className="font-medium text-slate-700">{t('detail.myProgress')}</span>
                  <span className="text-slate-500">
                    {t('detail.readProgress', { read: readCount, total: totalChapters, pct: progressPct })}
                    {progress?.readSeconds ? <span className="text-slate-400"> · {t('detail.readTimeLabel', { time: fmtReadTime(progress.readSeconds, t) })}</span> : null}
                  </span>
                </div>
                <div className="mt-2.5 h-2 w-full overflow-hidden rounded-full bg-slate-100">
                  <span className="block h-full rounded-full bg-primary-500 transition-all duration-300" style={{ width: `${progressPct}%` }} />
                </div>
                <div className="mt-3 flex flex-wrap gap-2">
                  {progressPct < 100 && (
                    <Button variant="ghost" size="sm" loading={progressBusy === 'complete'} disabled={progressBusy !== null} onClick={handleMarkRead}>
                      {t('detail.markRead')}
                    </Button>
                  )}
                  {readCount > 0 && (
                    <Button variant="ghost" size="sm" loading={progressBusy === 'reset'} disabled={progressBusy !== null} onClick={handleResetProgress}>
                      {t('detail.resetProgress')}
                    </Button>
                  )}
                </div>
              </div>
            )}

            {tree.length > 0 && <BookSearch bookSlug={book.slug} />}

            <div className="overflow-hidden rounded-xl border border-slate-200 bg-white">
              {tree.length === 0 ? (
                <p className="py-10 text-center text-sm text-slate-400">{t('detail.noChapters')}</p>
              ) : (
                <ul className="divide-y divide-slate-100">
                  {tree.map((doc, i) => {
                    const external = (doc.external_url || '').trim()
                    const rowClass = "group flex items-center gap-3 border-l-2 border-transparent px-3 py-3.5 transition-colors hover:bg-primary-50/40 sm:gap-5 sm:px-6 sm:py-4"
                    const inner = (
                      <>
                        <span className={`w-8 shrink-0 text-center text-lg font-bold transition-colors group-hover:text-primary-500 sm:w-10 sm:text-2xl ${readSet.has(doc.id) ? 'text-emerald-400' : 'text-slate-300'}`}>{String(i + 1).padStart(2, '0')}</span>
                        <span className="min-w-0 flex-1">
                          <span className="flex items-center gap-1.5">
                            <span className="truncate font-semibold text-slate-900">{chapterPrefix}{doc.title}</span>
                            {external && <i className="fa-solid fa-arrow-up-right-from-square shrink-0 text-xs text-slate-400" aria-hidden="true" />}
                            {readSet.has(doc.id) && <CheckCircleSmallIcon className="h-4 w-4 shrink-0 text-emerald-500" />}
                          </span>
                          {(doc.children?.length || 0) > 0 && (
                            <span className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1">
                              {doc.children!.slice(0, 3).map((c) => (
                                <span key={c.id} className={`flex items-center gap-1 text-xs ${readSet.has(c.id) ? 'text-emerald-600' : 'text-slate-400'}`}>
                                  <span className={`h-1 w-1 rounded-full ${readSet.has(c.id) ? 'bg-emerald-500' : 'bg-slate-300'}`} /> {c.title}
                                </span>
                              ))}
                              {doc.children!.length > 3 && <span className="text-xs text-slate-300">…</span>}
                            </span>
                          )}
                        </span>
                        {(doc.children?.length || 0) > 0 && (
                          <span className="hidden shrink-0 rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500 sm:inline-flex">{t('detail.sectionCount', { n: doc.children!.length })}</span>
                        )}
                        <ChevronRightIcon className="h-4 w-4 shrink-0 text-slate-300 transition-colors group-hover:text-primary-500" />
                      </>
                    )
                    return (
                      <li key={doc.id}>
                        {external ? (
                          <a href={external} target={doc.external_new_tab === false ? '_self' : '_blank'} rel={doc.external_new_tab === false ? undefined : 'noopener noreferrer'} className={rowClass}>{inner}</a>
                        ) : (
                          <Link href={`/book/reader/${encodeURIComponent(book.slug)}/${doc.slug}`} className={rowClass}>{inner}</Link>
                        )}
                      </li>
                    )
                  })}
                </ul>
              )}
            </div>
          </div>
          ) : activeTab === 'qa' ? (
            <div className="max-w-3xl space-y-4">
              {readDocSlug && !qaQuestion && (
                <div className="flex flex-col gap-3 rounded-xl border border-primary-100 bg-primary-50/60 p-4 sm:flex-row sm:items-center">
                  <i className="fa-solid fa-wand-magic-sparkles text-xl text-primary-500" aria-hidden="true" />
                  <div className="min-w-0 flex-1 text-sm">
                    <div className="font-medium text-slate-900">{t('qa.detail.aiTitle')}</div>
                    <div className="mt-0.5 text-slate-500">{t('qa.detail.aiHint')}</div>
                  </div>
                  <ButtonLink size="sm" href={`/book/reader/${encodeURIComponent(book.slug)}/${encodeURIComponent(readDocSlug)}?qa=ai`}>{t('qa.detail.aiOpen')}</ButtonLink>
                </div>
              )}
              <QACommunity user={user} book={{ id: book.id, slug: book.slug }} questionId={qaQuestion}
                onOpenQuestion={(id) => goTab('qa', id)}
                loginHref={`/login?next=${encodeURIComponent(`/book/detail/${book.slug}?tab=qa`)}`} />
            </div>
          ) : (
            <BookReviews bookId={book.id} authorId={book.user_id} />
          )}
        </section>
      </Container>

      {bookListsEnabled(site) && <Container><BookListsOnBook bookId={book.id} /></Container>}

      {/* 你可能也喜欢 */}
      {related.length > 0 && (
        <section className="border-t border-slate-200 bg-white py-10">
          <Container>
            <h2 className="mb-6 text-xl font-bold text-slate-900">{t('detail.related')}</h2>
            <div className="grid gap-4 grid-cols-[repeat(auto-fill,minmax(15rem,1fr))]">
              {related.map((b) => <BookCard key={b.id} book={b} showStatus tagsMax={2} tagsLink={false} dateField="created" />)}
            </div>
          </Container>
        </section>
      )}

      <BookCopyDialog book={book} tree={tree} open={copyOpen} onClose={() => setCopyOpen(false)} />
      {addToListOpen && <AddToListModal bookId={book.id} onClose={() => setAddToListOpen(false)} />}
      {embedOpen && <EmbedModal book={book} tree={tree} onClose={() => setEmbedOpen(false)} />}
    </div>
  )
}

/* ── 辅助 ── */

function InfoRow({ icon, label, value, mono }: { icon: React.ReactNode; label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <dt className="flex shrink-0 items-center gap-2 whitespace-nowrap text-slate-500"><span className="text-slate-400">{icon}</span>{label}</dt>
      <dd className={`min-w-0 text-right ${mono ? 'font-mono text-xs text-primary-600' : 'font-medium text-slate-900'}`}>
        <Tooltip content={value} className="max-w-full"><span className="block truncate">{value}</span></Tooltip>
      </dd>
    </div>
  )
}

function fmtDate(input: string | null | undefined): string {
  if (!input) return '-'
  return input.slice(0, 10)
}

// fmtReadTime 累计阅读秒数格式化为「N 分钟 / N.N 小时」
function fmtReadTime(seconds: number | undefined, t: (k: string, v?: Record<string, string | number>) => string): string {
  if (!seconds || seconds < 60) return t('detail.readTimeLessMinute')
  const minutes = Math.round(seconds / 60)
  return minutes < 60 ? t('detail.readTimeMinutes', { n: minutes }) : t('detail.readTimeHours', { n: (minutes / 60).toFixed(1) })
}

function flatFirst(docs: Document[]): Document | null {
  for (const d of docs) {
    if (d.status === 'published') return d
    const child = d.children ? flatFirst(d.children) : null
    if (child) return child
  }
  return null
}

// flatDocIds 递归收集全部章节 ID（含子章节），用于阅读进度统计
function flatDocIds(docs: Document[]): number[] {
  return docs.flatMap((d) => [d.id, ...flatDocIds(d.children || [])])
}

function flatWords(docs: Document[]): number {
  return docs.reduce((sum, d) => sum + (d.content || '').replace(/\s/g, '').length + flatWords(d.children || []), 0)
}

function ClientFallback({ slug }: { slug: string }) {
  const { user } = useApp()
  const { t } = useTranslation()
  if (!user) {
    return (
      <div className="rounded-xl border border-slate-200 bg-white shadow-sm p-6 text-center text-sm text-slate-500">
        {t('detail.fallbackLoginPrefix')}<Link href="/login" className="mx-1 text-primary-600">{t('detail.fallbackLoginLink')}</Link>{t('detail.fallbackLoginSuffix')}
      </div>
    )
  }
  return (
    <div className="rounded-xl border border-slate-200 bg-white shadow-sm p-6 text-center text-sm text-slate-500">
      {t('detail.fallbackAuthorOnly')}
      <a href={`/book/detail/${encodeURIComponent(slug)}`} className="ml-1 text-primary-600">{t('detail.fallbackRefresh')}</a>
    </div>
  )
}
