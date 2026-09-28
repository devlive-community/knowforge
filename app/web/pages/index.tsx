import Link from 'next/link'
import { useEffect, useState } from 'react'
import type { GetServerSideProps, InferGetServerSidePropsType } from 'next'
import { getSSRUser, authHeaderFrom, serverApi, getSiteConfig, siteUrlFrom, isInstalled } from '@/lib/server-api'
import { ButtonLink, EmptyState } from '@/components/ui'
import HeroIllustration from '@/components/HeroIllustration'
import Seo from '@/components/Seo'
import Container from '@/components/Container'
import BookCard from '@/components/BookCard'
import CoverImage from '@/components/CoverImage'
import UserAvatar from '@/components/UserAvatar'
import ResourceIcon from '@/components/ResourceIcon'
import { useGridPageSize } from '@/lib/useGridPageSize'
import { formatNumber } from '@/lib/api'
import { BookIcon, ChevronRightIcon, CloudIcon, CodeIcon, EyeIcon, FileTextIcon, ShieldIcon, UsersIcon } from '@/components/icons'
import { useTranslation } from '@/lib/i18n'
import type { Book, PageResult, SiteStats, Tag, User } from '@/lib/types'
import { displayName } from '@/lib/users'

interface ActiveAuthor {
  id: number
  username: string
  avatar: string
  book_count: number
  total_views: number
}

interface HomeProps {
  installed: boolean
  user: User | null
  site: Record<string, string>
  siteUrl: string
  stats: SiteStats
  latest: Book[]
  hot: Book[]
  trending: Book[]
  tags: Tag[]
  authors: ActiveAuthor[]
}

export const getServerSideProps: GetServerSideProps<HomeProps> = async ({ req }) => {
  // 未安装时强制进入安装向导（服务端重定向，不渲染任何内容）
  if (!(await isInstalled())) {
    return { redirect: { destination: '/install', permanent: false } }
  }
  const auth = authHeaderFrom(req)
  const user = await getSSRUser(req)

  const [site, stats, latest, hot, trending, tags, authors] = await Promise.all([
    getSiteConfig(),
    serverApi<SiteStats>('/stats').catch(() => null),
    serverApi<Book[]>('/explore/latest').catch(() => []),
    serverApi<Book[]>('/explore/hot').catch(() => []),
    serverApi<PageResult<Book>>('/books', { params: { page_size: 10, sort: 'hot' } }).then((r) => r.items || []).catch(() => [] as Book[]),
    serverApi<Tag[]>('/tags', { params: { limit: 18 } }).catch(() => [] as Tag[]),
    serverApi<{ items: ActiveAuthor[] }>('/explore/active-authors').then((r) => r.items || []).catch(() => [] as ActiveAuthor[]),
  ])
  return {
    props: {
      installed: true,
      user,
      site,
      siteUrl: siteUrlFrom(req),
      stats: stats ?? { user_count: 0, book_count: 0, document_count: 0, total_views: 0 },
      latest,
      hot,
      trending,
      tags,
      authors,
    },
  }
}

// 段落标题：标题 + 副标题 + 「查看全部」入口
function SectionHead({ title, subtitle, href }: { title: string; subtitle?: string; href: string }) {
  const { t } = useTranslation()
  return (
    <div className="mb-5 flex items-end justify-between gap-4">
      <div className="min-w-0">
        <h2 className="text-xl font-bold text-slate-900">{title}</h2>
        {subtitle && <p className="mt-1 truncate text-sm text-slate-400">{subtitle}</p>}
      </div>
      <Link href={href} className="flex shrink-0 items-center gap-0.5 text-sm text-slate-500 transition-colors hover:text-primary-600">
        {t('home.section.viewAll')} <ChevronRightIcon className="h-4 w-4" />
      </Link>
    </div>
  )
}

// GitHub Star：客户端尽力获取 star 数，失败则只显示图标与文案（不阻塞 SSR）
function GitHubStarStat({ repo }: { repo: string }) {
  const [stars, setStars] = useState<number | null>(null)
  useEffect(() => {
    let alive = true
    fetch(`https://api.github.com/repos/${repo}`, { headers: { Accept: 'application/vnd.github+json' } })
      .then((r) => (r.ok ? r.json() : null))
      .then((d) => { if (alive && d && typeof d.stargazers_count === 'number') setStars(d.stargazers_count) })
      .catch(() => { /* 忽略：网络/限流失败保持占位 */ })
    return () => { alive = false }
  }, [repo])
  return (
    <a href={`https://github.com/${repo}`} target="_blank" rel="noopener noreferrer" className="flex items-center gap-4 p-6 transition-colors hover:bg-slate-50">
      <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-slate-100 text-slate-700"><i className="fa-brands fa-github text-lg" aria-hidden="true" /></span>
      <span>
        <span className="block text-xl font-bold tabular-nums text-slate-900">{stars === null ? '—' : formatNumber(stars)}</span>
        <span className="text-xs text-slate-400">GitHub Star</span>
      </span>
    </a>
  )
}

// 主题卡片使用的一组轮换图标（避免为每个标签写死映射）
const TOPIC_ICONS = ['fa-layer-group', 'fa-code', 'fa-database', 'fa-screwdriver-wrench', 'fa-cube', 'fa-diagram-project', 'fa-terminal', 'fa-robot', 'fa-book', 'fa-cloud']

export default function Home({ site, siteUrl, stats, latest, hot, trending, tags, authors }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  // 精选书籍：按屏宽自适应单行展示数量（列数 × 1 行）
  const { ref: featuredRef, pageSize: featuredCount } = useGridPageSize({ minItemRem: 15, rows: 1, fallback: 4 })

  const jsonLd = {
    '@context': 'https://schema.org',
    '@type': 'WebSite',
    name: siteName,
    description: site.site_description || t('home.seo.jsonldDescription'),
    url: siteUrl,
    potentialAction: {
      '@type': 'SearchAction',
      target: `${siteUrl}/explore?title={search_term_string}`,
      'query-input': 'required name=search_term_string',
    },
  }

  const statItems = [
    { label: t('home.stats.users'), value: stats.user_count, icon: UsersIcon, tone: 'bg-primary-50 text-primary-500' },
    { label: t('home.stats.books'), value: stats.book_count, icon: BookIcon, tone: 'bg-sky-50 text-sky-500' },
    { label: t('home.stats.chapters'), value: stats.document_count, icon: FileTextIcon, tone: 'bg-emerald-50 text-emerald-500' },
    { label: t('home.stats.views'), value: stats.total_views, icon: EyeIcon, tone: 'bg-amber-50 text-amber-500' },
  ]

  return (
    <div>
      <Seo
        siteName={siteName}
        description={site.site_description || t('home.seo.description')}
        url={siteUrl}
        jsonLd={jsonLd}
      />

      <Container>
      {/* Hero */}
      <section className="grid items-center gap-10 py-8 lg:grid-cols-2 lg:py-12">
        <div>
          <span className="mb-6 block h-1 w-12 rounded-full bg-primary-500" aria-hidden="true" />
          <h1 className="text-4xl font-bold leading-[1.15] text-slate-900 md:text-[44px] md:leading-[1.15]">
            {t('home.hero.titleLine1')}<br />{t('home.hero.titleLine2')}
          </h1>
          <p className="mt-5 max-w-md text-[15px] leading-7 text-slate-500">
            {t('home.hero.subtitle')}
          </p>
          <div className="mt-8 flex flex-wrap gap-3">
            <ButtonLink href="/explore">{t('home.hero.explore')}</ButtonLink>
            <ButtonLink href="/books/create" variant="outline"
              className="border-primary-500 text-primary-600 hover:border-primary-600 hover:bg-primary-50">
              {t('home.hero.createFirst')}
            </ButtonLink>
          </div>
          <div className="mt-9 flex flex-wrap items-center gap-x-3 gap-y-2 text-sm text-slate-500">
            <span className="flex items-center gap-1.5"><CodeIcon className="h-4 w-4 text-primary-500" /> {t('home.hero.tagOpenSource')}</span>
            <span className="text-slate-300">·</span>
            <span className="flex items-center gap-1.5"><ShieldIcon className="h-4 w-4 text-primary-500" /> {t('home.hero.tagSelfHosted')}</span>
            <span className="text-slate-300">·</span>
            <span className="flex items-center gap-1.5"><CloudIcon className="h-4 w-4 text-primary-500" /> {t('home.hero.tagMultiDevice')}</span>
          </div>
        </div>
        <HeroIllustration books={latest} />
      </section>

      {/* 统计条 */}
      <section className="rounded-2xl border border-slate-200 bg-white shadow-sm">
        <div className="grid grid-cols-2 divide-y divide-slate-100 sm:grid-cols-3 sm:divide-y-0 lg:grid-cols-5 sm:divide-x">
          {statItems.map((s) => (
            <div key={s.label} className="flex items-center gap-4 p-6">
              <span className={`flex h-11 w-11 shrink-0 items-center justify-center rounded-full ${s.tone}`}>
                <s.icon className="h-5 w-5" />
              </span>
              <span>
                <span className="block text-xl font-bold tabular-nums text-slate-900">{formatNumber(s.value)}</span>
                <span className="text-xs text-slate-400">{s.label}</span>
              </span>
            </div>
          ))}
          <GitHubStarStat repo="devlive-community/knowforge" />
        </div>
      </section>

      {/* 精选书籍 */}
      {hot.length > 0 && (
        <section className="mt-12">
          <SectionHead title={t('home.featured.title')} subtitle={t('home.featured.subtitle')} href="/explore?sort=hot" />
          <div ref={featuredRef} className="grid gap-4 grid-cols-[repeat(auto-fill,minmax(15rem,1fr))]">
            {(hot || []).slice(0, featuredCount).map((b) => <BookCard key={b.id} book={b} tagsMax={2} tagsLink={false} authorLink={false} />)}
          </div>
        </section>
      )}

      {/* 按主题探索 */}
      {tags.length > 0 && (
        <section className="mt-12">
          <SectionHead title={t('home.topics.title')} subtitle={t('home.topics.subtitle')} href="/tags" />
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-8">
            {(tags || []).slice(0, 8).map((tag, i) => (
              <Link key={tag.id} href={`/explore?tag=${encodeURIComponent(tag.slug)}`}
                className="flex flex-col items-center gap-2 rounded-xl border border-slate-200 bg-white p-4 text-center transition-colors hover:border-primary-200 hover:bg-primary-50/40">
                <ResourceIcon iconType={tag.icon_type} iconValue={tag.icon_value} name={tag.name} fallback={TOPIC_ICONS[i % TOPIC_ICONS.length]} className="flex h-11 w-11 items-center justify-center overflow-hidden rounded-xl bg-primary-50 text-lg text-primary-600" />
                <span className="w-full truncate text-sm font-medium text-slate-800">{tag.name}</span>
                <span className="text-xs text-slate-400">{t('home.topics.count', { n: tag.book_count || 0 })}</span>
              </Link>
            ))}
          </div>
        </section>
      )}

      {/* 最新发布 · 本周热门 · 热门标签/活跃作者 */}
      <section className="mt-12 grid gap-8 lg:grid-cols-[1.25fr_1fr_1fr]">
        {/* 最新发布 */}
        <div className="min-w-0">
          <SectionHead title={t('home.section.latest')} subtitle={t('home.latest.subtitle')} href="/explore?sort=latest" />
          {latest.length === 0 ? (
            <EmptyState>
              {t('home.empty.prefix')}<Link href="/books/create" className="text-primary-600 hover:underline">{t('home.empty.createLink')}</Link>
            </EmptyState>
          ) : (
            <ul className="space-y-4">
              {(latest || []).slice(0, 6).map((b) => (
                <li key={b.id}>
                  <Link href={`/book/detail/${encodeURIComponent(b.slug)}`} className="group flex gap-3">
                    <span className="h-16 w-12 shrink-0 overflow-hidden rounded-lg bg-gradient-to-br from-primary-400 to-primary-600">
                      <CoverImage src={b.cover_image} alt={b.title} />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-semibold text-slate-900 group-hover:text-primary-600">{b.title}</span>
                      {b.description && <span className="mt-0.5 block line-clamp-1 text-xs text-slate-400">{b.description}</span>}
                      <span className="mt-1 block truncate text-xs text-slate-400">{displayName(b.user)} · {t('home.latest.meta', { chapters: b.chapter_count || 0, views: formatNumber(b.view_count) })}</span>
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>

        {/* 本周热门 */}
        <div className="min-w-0">
          <SectionHead title={t('home.trending.title')} subtitle={t('home.trending.subtitle')} href="/explore?sort=hot" />
          {trending.length === 0 ? <EmptyState>{t('home.trending.empty')}</EmptyState> : (
            <ol className="space-y-2.5">
              {(trending || []).slice(0, 10).map((b, i) => (
                <li key={b.id}>
                  <Link href={`/book/detail/${encodeURIComponent(b.slug)}`} className="group flex items-center gap-3">
                    <span className={`w-6 shrink-0 text-center text-sm font-bold ${i < 3 ? 'text-primary-500' : 'text-slate-300'}`}>{i + 1}</span>
                    <span className="min-w-0 flex-1 truncate text-sm text-slate-700 group-hover:text-primary-600">{b.title}</span>
                    <span className="shrink-0 text-xs text-slate-400">{t('home.trending.reads', { views: formatNumber(b.view_count) })}</span>
                  </Link>
                </li>
              ))}
            </ol>
          )}
        </div>

        {/* 热门标签 + 活跃作者 */}
        <div className="min-w-0 space-y-8">
          {tags.length > 0 && (
            <div>
              <SectionHead title={t('home.hotTags.title')} subtitle={t('home.hotTags.subtitle')} href="/tags" />
              <div className="flex flex-wrap gap-2">
                {(tags || []).map((tag) => (
                  <Link key={tag.id} href={`/explore?tag=${encodeURIComponent(tag.slug)}`}
                    className="inline-flex items-center gap-1.5 rounded-lg bg-slate-100 px-2.5 py-1.5 text-xs text-slate-700 transition-colors hover:bg-primary-50 hover:text-primary-700">
                    {tag.name}<span className="text-slate-400">{tag.book_count || 0}</span>
                  </Link>
                ))}
              </div>
            </div>
          )}
          {authors.length > 0 && (
            <div>
              <SectionHead title={t('home.authors.title')} subtitle={t('home.authors.subtitle')} href="/explore" />
              <ul className="space-y-3">
                {(authors || []).slice(0, 5).map((author) => (
                  <li key={author.id}>
                    <Link href={`/user/${encodeURIComponent(author.username)}`} className="group flex items-center gap-3">
                      <UserAvatar user={{ username: author.username, avatar: author.avatar }} size="h-9 w-9" link={false} />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm font-medium text-slate-800 group-hover:text-primary-600">{displayName(author)}</span>
                        <span className="block truncate text-xs text-slate-400">{t('home.authors.meta', { books: author.book_count, views: formatNumber(author.total_views) })}</span>
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      </section>
      </Container>
    </div>
  )
}
