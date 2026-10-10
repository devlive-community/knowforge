import Link from 'next/link'
import { useRouter } from 'next/router'
import type { GetServerSideProps, InferGetServerSidePropsType } from 'next'
import { useEffect, useState } from 'react'
import { authHeaderFrom, getSSRUser, getSiteConfig, isInstalled, serverApi, siteUrlFrom } from '@/lib/server-api'
import { formatNumber } from '@/lib/api'
import { resolveMediaUrl } from '@/lib/media'
import Container from '@/components/Container'
import { Button, Input, Loading, Pagination, SegmentedTabs, Select } from '@/components/ui'
import Seo from '@/components/Seo'
import TagChips from '@/components/TagChips'
import BookCard from '@/components/BookCard'
import BookListCard from '@/components/booklists/BookListCard'
import { bookListsEnabled, type BookList } from '@/lib/booklists'
import VersionGroupToggle from '@/components/VersionGroupToggle'
import { ArrowRightIcon, BookIcon, ClockIcon, EyeIcon, GlobeIcon, GridIcon, ListIcon, SearchIcon } from '@/components/icons'
import { useTranslation } from '@/lib/i18n'
import type { Book, PageResult, Tag, User } from '@/lib/types'
import { categoriesEnabled, categoryHref, type CategoryNode } from '@/lib/categories'

interface ExploreProps {
  installed: boolean
  user: User | null
  site: Record<string, string>
  siteUrl: string
  keyword: string
  tag: string
  tagName: string
  /** 书籍分类（分类插件）：当前分类 slug、分类详情（路径与子分类）与分类树 */
  category: string
  categoryInfo: CategoryInfo | null
  categoryTree: CategoryNode[]
  sort: 'latest' | 'hot'
  visibility: string
  /** 版本聚合（URL: versions=grouped，服务端聚合） */
  grouped: boolean
  page: number
  data: PageResult<Book>
  hotTags: Tag[]
  featuredLists: BookList[]
}

interface CategoryInfo {
  category: CategoryNode
  path: CategoryNode[]
  children: CategoryNode[]
}

export const getServerSideProps: GetServerSideProps<ExploreProps> = async ({ req, query }) => {
  if (!(await isInstalled())) {
    return { redirect: { destination: '/install', permanent: false } }
  }
  const auth = authHeaderFrom(req)
  const user = await getSSRUser(req)

  const keyword = typeof query.title === 'string' ? query.title.slice(0, 100) : ''
  const tag = typeof query.tag === 'string' ? query.tag.slice(0, 50) : ''
  const sort = (query.sort === 'hot' ? 'hot' : 'latest') as 'latest' | 'hot'
  const visibility = query.visibility === 'login' && user ? 'login' : ''
  const page = Math.max(1, parseInt(String(query.page || '1'), 10) || 1)
  const grouped = query.versions === 'grouped'
  const category = typeof query.category === 'string' ? query.category.slice(0, 80) : ''

  const [site, data, tags, categoryTree, categoryInfo] = await Promise.all([
    getSiteConfig(),
    // 版本聚合走 /books（支持 tag 过滤 + group_versions 服务端聚合）
    tag && !grouped && !category
      ? serverApi<PageResult<Book>>(`/tags/${encodeURIComponent(tag)}/books`, { params: { page, page_size: 12 } })
          .catch(() => ({ items: [], total: 0, page: 1, page_size: 12 }) as PageResult<Book>)
      : serverApi<PageResult<Book>>('/books', { headers: auth, params: { page, page_size: 12, title: keyword || undefined, visibility: visibility || undefined, tag: tag || undefined, category: category || undefined, group_versions: grouped ? 'true' : undefined } })
          .catch(() => ({ items: [], total: 0, page: 1, page_size: 12 }) as PageResult<Book>),
    serverApi<Tag[]>('/tags', { params: { limit: 200 } }).catch(() => [] as Tag[]),
    // 分类插件未启用时接口不可用，按空处理；透传语言信号，分类名称按访问者语言显示
    serverApi<{ items: CategoryNode[] }>('/categories', { headers: auth }).then((r) => r.items || []).catch(() => [] as CategoryNode[]),
    category ? serverApi<CategoryInfo>(`/categories/${encodeURIComponent(category)}`, { headers: auth }).catch(() => null) : Promise.resolve(null),
  ])
  const selectedBookTag = data.items
    .flatMap((book) => book.tags || [])
    .find((item) => item.slug === tag)
  const tagName = tag ? selectedBookTag?.name || tags.find((item) => item.slug === tag)?.name || tag : ''
  // 精选书单（书单插件启用时）：只在不带搜索/标签筛选的第一页展示；没有精选时用收藏最多的书单
  let featuredLists: BookList[] = []
  if (bookListsEnabled(site) && page === 1 && !keyword && !tag && !category) {
    const listsOf = (params: Record<string, string | number>) => serverApi<PageResult<BookList>>('/book-lists', { params: { ...params, page_size: 4 } })
      .then((r) => r.items || []).catch(() => [] as BookList[])
    featuredLists = await listsOf({ featured: 1 })
    if (featuredLists.length === 0) featuredLists = await listsOf({ sort: 'popular' })
  }
  return { props: { installed: true, user, site, siteUrl: siteUrlFrom(req), keyword, tag, tagName, category: categoryInfo ? category : '', categoryInfo: categoriesEnabled(site) ? categoryInfo : null, categoryTree: categoriesEnabled(site) ? categoryTree : [], sort, visibility, grouped, page, data, hotTags: tags.slice(0, 6), featuredLists } }
}

export default function Explore({ user, site, siteUrl, keyword, tag, tagName, category, categoryInfo, categoryTree, sort, visibility, grouped, page, data, hotTags, featuredLists }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  // 标签插件禁用时，隐藏所有标签入口（热门搜索、标签侧栏、全部标签链接）
  const featurePlugins = (site as Record<string, unknown>).feature_plugins
  const tagsEnabled = Array.isArray(featurePlugins) && (featurePlugins as string[]).includes('tags')
  const versionsEnabled = Array.isArray(featurePlugins) && (featurePlugins as string[]).includes('book-versions')
  const [view, setView] = useState<'grid' | 'list'>('grid')
  const [loading, setLoading] = useState(false)
  const router = useRouter()
  // 数据/筛选变化即视为加载完成，复位 loading（含 SSR 软导航返回新 props）
  useEffect(() => { setLoading(false) }, [data, tag, category, page, sort, keyword, grouped])

  // 目标地址与当前不同才进入加载态，避免点击当前项后 loading 卡住
  function navLoad(href: string) {
    if (href !== router.asPath) setLoading(true)
  }

  // 当前高亮的浏览项：按 URL 中的 sort 显式区分「全部公开书籍」(无 sort) 与「最新发布」(sort=latest)，
  // 保证同一时刻只有一个高亮；含标签筛选时三项均不高亮
  const rawSort = typeof router.query.sort === 'string' ? router.query.sort : ''
  const loginOnly = visibility === 'login'
  const activeMode = tag || category ? '' : loginOnly ? 'login' : rawSort === 'hot' ? 'hot' : rawSort === 'latest' ? 'latest' : 'all'

  const browseItems = [
    { mode: 'all', label: t('explore.browse.all'), shortLabel: t('explore.browse.allShort'), icon: <BookIcon className="h-4 w-4" />, href: '/explore' },
    { mode: 'latest', label: t('explore.browse.latest'), shortLabel: t('explore.browse.latestShort'), icon: <ClockIcon className="h-4 w-4" />, href: '/explore?sort=latest' },
    { mode: 'hot', label: t('explore.browse.hot'), shortLabel: t('explore.browse.hotShort'), icon: <i className="fa-solid fa-fire text-sm" aria-hidden="true" />, href: '/explore?sort=hot' },
    ...(user ? [{ mode: 'login', label: t('explore.browse.loginOnly'), shortLabel: t('explore.browse.loginOnlyShort'), icon: <i className="fa-solid fa-user-lock text-sm" aria-hidden="true" />, href: '/explore?visibility=login' }] : []),
  ]

  const items = [...(data.items || [])].sort((a, b) => {
    if (!tag && sort === 'hot') return b.view_count - a.view_count
    if (!tag && sort === 'latest') return a.updated_at < b.updated_at ? 1 : -1
    return 0
  })

  const sectionTitle = categoryInfo ? categoryInfo.category.name : tag ? t('explore.section.byTag', { tag: tagName }) : keyword ? t('explore.section.searchResult', { keyword }) : loginOnly ? t('explore.browse.loginOnly') : t('explore.section.allPublic')

  const jsonLd = items.length > 0 ? {
    '@context': 'https://schema.org',
    '@type': 'ItemList',
    itemListElement: items.slice(0, 10).map((book, i) => ({
      '@type': 'ListItem',
      position: i + 1,
      url: `${siteUrl}/book/detail/${encodeURIComponent(book.slug)}`,
      name: book.title,
    })),
  } : undefined

  // 列表地址：筛选/排序/版本聚合都放在 URL 查询参数里（可分享、刷新保持、SSR 按参数取数）
  function listUrl({ p = page, sortValue = sort, isGrouped = grouped }: { p?: number; sortValue?: 'latest' | 'hot'; isGrouped?: boolean } = {}): string {
    const params = new URLSearchParams()
    if (keyword) params.set('title', keyword)
    if (tag) params.set('tag', tag)
    if (category) params.set('category', category)
    if (visibility) params.set('visibility', visibility)
    if (sortValue !== 'latest') params.set('sort', sortValue)
    if (isGrouped) params.set('versions', 'grouped')
    if (p > 1) params.set('page', String(p))
    const qs = params.toString()
    return `/explore${qs ? `?${qs}` : ''}`
  }

  function toggleGrouped(next: boolean) {
    const href = listUrl({ p: 1, isGrouped: next })
    navLoad(href)
    void router.push(href)
  }

  return (
    <div className="bg-warm">
      <Seo
        siteName={siteName}
        title={categoryInfo ? t('explore.seo.categoryTitle', { category: categoryInfo.category.name }) : keyword ? t('explore.section.searchResult', { keyword }) : t('explore.seo.title')}
        description={categoryInfo ? (categoryInfo.category.description || t('explore.seo.categoryDescription', { site: siteName, category: categoryInfo.category.name })) : keyword ? t('explore.seo.searchDescription', { site: siteName, keyword }) : t('explore.seo.description', { site: siteName })}
        url={categoryInfo ? `${siteUrl}${categoryHref(categoryInfo.category.slug)}` : `${siteUrl}/explore`}
        jsonLd={jsonLd}
      />

      {/* Hero：居中标题 + 大搜索框 + 热门搜索 */}
      <section className="border-b border-slate-200 bg-gradient-to-b from-primary-50/60 to-warm">
        <Container className="py-8 text-center sm:py-10">
          <p className="text-sm font-medium tracking-wide text-primary-600">{t('explore.hero.eyebrow')}</p>
          <h1 className="mt-2 text-2xl font-bold text-ink sm:text-3xl md:text-4xl">{t('explore.hero.title')}</h1>
          <p className="mt-3 text-[15px] text-slate-500">{t('explore.hero.subtitle')}</p>

          <form action="/explore" method="get" className="mx-auto mt-6 flex max-w-2xl flex-col gap-2 sm:flex-row">
            <div className="relative flex-1">
              <SearchIcon className="pointer-events-none absolute left-4 top-1/2 h-5 w-5 -translate-y-1/2 text-slate-400" />
              <Input size="lg" className="pl-11 text-base" name="title" placeholder={t('explore.search.placeholder')} defaultValue={keyword} />
            </div>
            <Button type="submit" size="lg" className="w-full px-7 sm:w-auto">{t('explore.search.submit')}</Button>
          </form>

          {tagsEnabled && (hotTags || []).length > 0 && (
          <div className="mt-4 flex flex-wrap items-center justify-center gap-2 text-sm">
            <span className="text-slate-400">{t('explore.search.hotSearch')}</span>
            {(hotTags || []).slice(0, 4).map((t) => (
              <Link key={t.id} href={`/explore?tag=${encodeURIComponent(t.slug)}`} onClick={() => navLoad(`/explore?tag=${encodeURIComponent(t.slug)}`)}
                className="rounded-full px-2.5 py-1 text-primary-600 transition-colors hover:bg-primary-50">{t.name}</Link>
            ))}
          </div>
          )}
        </Container>
      </section>

      {/* 主体：左栏浏览 + 右内容 */}
      <Container className="grid gap-5 py-6 sm:py-8 lg:grid-cols-[240px_1fr] lg:gap-8">
        <div className="min-w-0 space-y-3 lg:hidden">
          <SegmentedTabs fullWidth value={activeMode} ariaLabel={t('explore.browse.aria')}
            items={browseItems.map((item) => ({ value: item.mode, label: item.shortLabel, icon: item.icon }))}
            onChange={(mode) => {
              const item = browseItems.find((entry) => entry.mode === mode)
              if (item) { navLoad(item.href); void router.push(item.href) }
            }} />
          {categoryTree.length > 0 && <div className="flex gap-2 overflow-x-auto pb-1 [scrollbar-width:none]" data-testid="explore-category-chips">
            {(categoryInfo && categoryInfo.children.length > 0 ? categoryInfo.children : categoryTree).map((item) => <Link key={item.id} href={categoryHref(item.slug)}
              onClick={() => navLoad(categoryHref(item.slug))}
              className={`shrink-0 rounded-full border px-3 py-1.5 text-sm ${category === item.slug ? 'border-primary-300 bg-primary-50 text-primary-700' : 'border-slate-200 bg-white text-slate-600'}`}>
              {item.name} <span className="text-xs text-slate-400">{item.book_count}</span>
            </Link>)}
          </div>}
          {tagsEnabled && hotTags.length > 0 && <div className="flex gap-2 overflow-x-auto pb-1 [scrollbar-width:none]">
            {hotTags.map((item) => <Link key={item.id} href={`/explore?tag=${encodeURIComponent(item.slug)}`}
              onClick={() => navLoad(`/explore?tag=${encodeURIComponent(item.slug)}`)}
              className={`shrink-0 rounded-full border px-3 py-1.5 text-sm ${tag === item.slug ? 'border-primary-300 bg-primary-50 text-primary-700' : 'border-slate-200 bg-white text-slate-600'}`}>
              {item.name} <span className="text-xs text-slate-400">{item.book_count}</span>
            </Link>)}
          </div>}
        </div>
        <aside className="hidden h-fit rounded-2xl border border-slate-200 bg-white p-4 shadow-sm lg:sticky lg:top-20 lg:block">
          <h2 className="mb-2 px-2 text-sm font-semibold text-slate-900">{t('explore.browse.heading')}</h2>
          <ul className="space-y-0.5">
            {browseItems.map((item) => {
              const active = item.mode === activeMode
              return (
                <li key={item.mode}>
                  <Link href={item.href} onClick={() => navLoad(item.href)}
                    className={`flex items-center gap-2.5 rounded-lg px-3 py-2.5 text-sm transition-colors ${
                      active ? 'bg-primary-50 font-medium text-primary-700 ring-1 ring-inset ring-primary-100' : 'text-slate-600 hover:bg-slate-50'
                    }`}>
                    {item.icon} {item.label}
                  </Link>
                </li>
              )
            })}
          </ul>

          {categoryTree.length > 0 && <div className="mt-4 border-t border-slate-100 pt-4" data-testid="explore-categories">
            <h2 className="mb-2 px-2 text-sm font-semibold text-slate-900">{t('explore.categories.heading')}</h2>
            <CategoryNav nodes={categoryTree} active={category} activePath={(categoryInfo?.path || []).map((c) => c.id)} onNavigate={navLoad} />
          </div>}

          {tagsEnabled && <div className="mt-4 border-t border-slate-100 pt-4">
            <h2 className="mb-2 px-2 text-sm font-semibold text-slate-900">{t('explore.tags.heading')}</h2>
            <ul className="space-y-0.5">
              {(hotTags || []).map((t) => (
                <li key={t.id}>
                  <Link href={`/explore?tag=${encodeURIComponent(t.slug)}`} onClick={() => navLoad(`/explore?tag=${encodeURIComponent(t.slug)}`)}
                    className={`flex items-center justify-between rounded-lg px-3 py-2 text-sm transition-colors ${
                      tag === t.slug ? 'bg-primary-50 font-medium text-primary-700' : 'text-slate-600 hover:bg-slate-50'
                    }`}>
                    {t.name}
                    <span className="text-xs text-slate-400">{t.book_count}</span>
                  </Link>
                </li>
              ))}
              {(hotTags || []).length === 0 && <li className="px-3 py-2 text-xs text-slate-400">{t('explore.tags.empty')}</li>}
            </ul>
            <Link href="/tags" className="mt-1.5 flex items-center gap-1 px-3 py-2 text-sm font-medium text-primary-600 hover:underline">
              {t('explore.tags.viewAll')} <ArrowRightIcon className="h-3.5 w-3.5" />
            </Link>
          </div>}

          <p className="mt-4 flex items-start gap-1.5 px-2 text-xs leading-5 text-slate-400">
            <GlobeIcon className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            {t('explore.notice')}
          </p>
        </aside>

        <section className="min-w-0">
          {featuredLists.length > 0 && (
            <div className="mb-8" data-testid="featured-lists">
              <div className="mb-4 flex items-baseline justify-between gap-3">
                <h2 className="text-xl font-bold text-ink sm:text-2xl">{t('explore.featuredLists.title')}</h2>
                <Link href="/lists" className="flex shrink-0 items-center gap-1 text-sm font-medium text-primary-600 hover:underline">
                  {t('explore.featuredLists.viewAll')} <ArrowRightIcon className="h-3.5 w-3.5" />
                </Link>
              </div>
              <div className="grid gap-5 grid-cols-[repeat(auto-fill,minmax(15rem,1fr))]">
                {featuredLists.map((l) => <BookListCard key={l.id} list={l} />)}
              </div>
            </div>
          )}
          {categoryInfo && (
            <div className="mb-4" data-testid="explore-category-header">
              <nav className="flex flex-wrap items-center gap-1.5 text-sm text-slate-500">
                <Link href="/explore" onClick={() => navLoad('/explore')} className="hover:text-primary-600">{t('explore.categories.all')}</Link>
                {categoryInfo.path.slice(0, -1).map((c) => (
                  <span key={c.id} className="flex items-center gap-1.5">
                    <span className="text-slate-300">/</span>
                    <Link href={categoryHref(c.slug)} onClick={() => navLoad(categoryHref(c.slug))} className="hover:text-primary-600">{c.name}</Link>
                  </span>
                ))}
              </nav>
              {categoryInfo.category.description && <p className="mt-2 text-sm text-slate-500">{categoryInfo.category.description}</p>}
              {categoryInfo.children.length > 0 && (
                <div className="mt-3 flex flex-wrap gap-2">
                  {categoryInfo.children.map((c) => (
                    <Link key={c.id} href={categoryHref(c.slug)} onClick={() => navLoad(categoryHref(c.slug))}
                      className="rounded-full border border-slate-200 bg-white px-3 py-1 text-sm text-slate-600 transition-colors hover:border-primary-300 hover:text-primary-700">
                      {c.name} <span className="text-xs text-slate-400">{c.book_count}</span>
                    </Link>
                  ))}
                </div>
              )}
            </div>
          )}
          <div className="mb-5 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex min-w-0 items-baseline gap-3">
              <h2 className="min-w-0 text-xl font-bold text-ink sm:text-2xl">{sectionTitle}</h2>
              <span className="text-sm text-slate-400">{t('explore.section.count', { total: data.total })}</span>
            </div>
            <div className="flex w-full items-center gap-2 sm:w-auto">
              {versionsEnabled && <VersionGroupToggle checked={grouped} onChange={toggleGrouped} disabled={loading} />}
              {!tag && !loginOnly && (
                <Select className="min-w-0 flex-1 sm:w-36 sm:flex-none" value={sort} onChange={(v) => { setLoading(true); window.location.href = listUrl({ p: 1, sortValue: v === 'hot' ? 'hot' : 'latest' }) }}
                  options={[{ value: 'latest', label: t('explore.browse.latest') }, { value: 'hot', label: t('explore.browse.hot') }]} />
              )}
              <SegmentedTabs iconOnly value={view} ariaLabel={t('explore.view.aria')}
                onChange={(value) => setView(value as 'grid' | 'list')} items={[
                  { value: 'grid', label: t('explore.view.grid'), icon: <GridIcon className="h-4 w-4" /> },
                  { value: 'list', label: t('explore.view.list'), icon: <ListIcon className="h-4 w-4" /> },
                ]} />
            </div>
          </div>

          {loading ? (
            <Loading />
          ) : items.length === 0 ? (
            <div className="rounded-xl border border-dashed border-slate-300 bg-white/60 py-20 text-center text-sm text-slate-400">
              {keyword ? t('explore.empty.search') : category ? t('explore.empty.category') : tag ? t('explore.empty.tag') : t('explore.empty.none')}
            </div>
          ) : (
            <div className={view === 'grid' ? 'grid gap-5 grid-cols-[repeat(auto-fill,minmax(15rem,1fr))]' : 'space-y-4'}>
              {items.map((b) => <BookCard key={b.id} book={b} view={view} />)}
            </div>
          )}

          {!loading && items.length > 0 && (
            <Pagination page={data.page} pageSize={data.page_size} total={data.total}
              onChange={(p) => { setLoading(true); window.location.href = listUrl({ p }) }} />
          )}
        </section>
      </Container>
    </div>
  )
}

// CategoryNav 发现页侧栏的分类树：顶级分类常显，当前分类所在的分支展开（最多三层）。
function CategoryNav({ nodes, active, activePath, onNavigate, depth = 0 }: {
  nodes: CategoryNode[]; active: string; activePath: number[]; onNavigate: (href: string) => void; depth?: number
}) {
  return (
    <ul className="space-y-0.5">
      {nodes.map((c) => {
        const open = activePath.includes(c.id)
        const href = categoryHref(c.slug)
        return (
          <li key={c.id}>
            <Link href={href} onClick={() => onNavigate(href)} style={{ paddingLeft: `${0.75 + depth * 0.875}rem` }}
              className={`flex items-center justify-between gap-2 rounded-lg py-2 pr-3 text-sm transition-colors ${
                active === c.slug ? 'bg-primary-50 font-medium text-primary-700' : 'text-slate-600 hover:bg-slate-50'
              }`}>
              <span className="truncate">{c.name}</span>
              <span className="shrink-0 text-xs text-slate-400">{c.book_count}</span>
            </Link>
            {open && c.children.length > 0 && <CategoryNav nodes={c.children} active={active} activePath={activePath} onNavigate={onNavigate} depth={depth + 1} />}
          </li>
        )
      })}
    </ul>
  )
}
