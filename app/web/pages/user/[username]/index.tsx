import { useEffect, useRef, useState } from 'react'
import type { GetServerSideProps, InferGetServerSidePropsType } from 'next'
import { useRouter } from 'next/router'
import Container from '@/components/Container'
import { authHeaderFrom, getSSRUser, serverApi, getSiteConfig, siteUrlFrom, isInstalled } from '@/lib/server-api'
import { api, formatNumber } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { resolveMediaUrl } from '@/lib/media'
import { Pagination, SegmentedTabs, Select, Loading, Tooltip, useFeedback } from '@/components/ui'
import Seo from '@/components/Seo'
import UserAvatar from '@/components/UserAvatar'
import BookCard from '@/components/BookCard'
import VersionGroupToggle from '@/components/VersionGroupToggle'
import { useGridPageSize } from '@/lib/useGridPageSize'
import AchievementIcon from '@/components/AchievementIcon'
import { ArrowRightIcon, BookIcon, CalendarIcon, EyeIcon, GitHubIcon, GridIcon, ListIcon, ShareIcon } from '@/components/icons'
import TagChips from '@/components/TagChips'
import type { AchievementGrant, Book, PageResult, User } from '@/lib/types'
import { useTranslation } from '@/lib/i18n'
import FeedButton from '@/components/FeedButton'
import { feedsEnabled, userFeedPath } from '@/lib/feeds'

interface UserProfile {
  id: number
  username: string
  avatar: string
  bio: string
  github_url: string
  nickname?: string
  website?: string
  location?: string
  company?: string
  role: string
  created_at: string
  public_book_count: number
}

interface UserHomeProps {
  installed: boolean
  user: User | null
  site: Record<string, string>
  siteUrl: string
  profile: UserProfile
  books: PageResult<Book>
  sort: string
  achievements: { enabled: boolean; items: AchievementGrant[] }
}

export const getServerSideProps: GetServerSideProps<UserHomeProps> = async ({ req, query, params }) => {
  if (!(await isInstalled())) {
    return { redirect: { destination: '/install', permanent: false } }
  }
  const auth = authHeaderFrom(req)
  const user = await getSSRUser(req)

  const username = (typeof params?.username === 'string' ? params.username : '') || (typeof query.username === 'string' ? query.username : '')
  if (!username) return { notFound: true }
  const page = Math.max(1, parseInt(String(query.page || '1'), 10) || 1)
  const sort = typeof query.sort === 'string' ? query.sort : 'updated'
  const grouped = query.versions === 'grouped'

  const [site, profile] = await Promise.all([
    getSiteConfig(),
    serverApi<UserProfile>(`/users/${encodeURIComponent(username)}`).catch(() => null),
  ])
  if (!profile) return { notFound: true }

  const [books, achievements] = await Promise.all([
    serverApi<PageResult<Book>>(`/users/${encodeURIComponent(username)}/books`, { params: { page, page_size: 9, sort, group_versions: grouped ? 'true' : undefined } })
      .catch(() => ({ items: [], total: 0, page: 1, page_size: 9 }) as PageResult<Book>),
    serverApi<{ enabled: boolean; items: AchievementGrant[] }>(`/users/${encodeURIComponent(username)}/achievements`, { headers: auth })
      .catch(() => ({ enabled: false, items: [] as AchievementGrant[] })),
  ])

  return { props: { installed: true, user, site, siteUrl: siteUrlFrom(req), profile, books, sort, achievements } }
}

function joinYear(input: string | null | undefined, t: (key: string, vars?: Record<string, string>) => string): string {
  if (!input) return ''
  const d = new Date(input)
  if (Number.isNaN(d.getTime())) return ''
  return t('user.home.joinedAt', { year: String(d.getFullYear()), month: String(d.getMonth() + 1) })
}

// ProfileLevelBadge 公开主页头部的等级徽标（成长插件启用且用户未隐藏时显示）。
function ProfileLevelBadge({ username }: { username: string }) {
  const { site } = useApp()
  const enabled = (site.feature_plugins || []).includes('growth')
  const [level, setLevel] = useState<{ name: string; icon_type?: string; icon_value?: string } | null>(null)
  useEffect(() => {
    if (!enabled) return
    api<{ public?: boolean; level?: { name: string; icon_type?: string; icon_value?: string } }>(`/users/${encodeURIComponent(username)}/growth`)
      .then((r) => { if (r.public !== false && r.level) setLevel(r.level) })
      .catch(() => { /* 忽略 */ })
  }, [username, enabled])
  if (!enabled || !level) return null
  return (
    <span className="inline-flex items-center gap-1.5 rounded-md bg-amber-50 px-2.5 py-1 text-sm font-medium text-amber-700 ring-1 ring-inset ring-amber-200">
      {level.icon_type !== 'image' && level.icon_type !== 'svg' && <i className={`fa-solid ${level.icon_value || 'fa-star'}`} aria-hidden="true" />}{level.name}
    </span>
  )
}

function AuthorProfileCard({ profile, siteUrl, share, t }: { profile: UserProfile; siteUrl: string; share: () => void; t: (key: string, vars?: Record<string, string>) => string }) {
  const { site } = useApp()
  const feeds = feedsEnabled(site)
  return (
    <div className="relative overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
      <div className="grid items-center gap-5 px-5 py-6 sm:px-8 lg:grid-cols-[160px_1fr_300px] lg:gap-8">
        <div className="mx-auto lg:mx-0">
          <UserAvatar user={profile} size="h-28 w-28 lg:h-32 lg:w-32 text-4xl" link={false} />
        </div>

        <div className="min-w-0">
          <p className="text-sm text-slate-400">{t('user.home.knowledgeCreator')}</p>
          <h1 className="mt-1 flex min-w-0 flex-wrap items-center gap-2 break-words text-2xl font-bold text-ink sm:gap-3 sm:text-4xl">
            {profile.nickname || profile.username}
            {profile.role === 'admin' && (
              <span className="inline-flex items-center rounded-md bg-primary-50 px-2.5 py-1 text-sm font-medium text-primary-700 ring-1 ring-inset ring-primary-200">{t('user.home.admin')}</span>
            )}
            <ProfileLevelBadge username={profile.username} />
          </h1>
          {profile.nickname && <p className="mt-1 text-sm text-slate-400">@{profile.username}</p>}
          {profile.bio && <p className="mt-3 max-w-lg text-[15px] leading-7 text-slate-500">{profile.bio}</p>}
          <div className="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 text-sm text-slate-500">
            {profile.created_at && (
              <span className="flex items-center gap-1.5"><CalendarIcon className="h-4 w-4" /> {joinYear(profile.created_at, t)}</span>
            )}
            <span className="flex items-center gap-1.5"><BookIcon className="h-4 w-4" /> {t('user.home.publicBooks', { count: String(profile.public_book_count) })}</span>
            {profile.location && <span className="flex items-center gap-1.5"><i className="fa-solid fa-location-dot text-slate-400" aria-hidden="true" /> {profile.location}</span>}
            {profile.company && <span className="flex items-center gap-1.5"><i className="fa-solid fa-building text-slate-400" aria-hidden="true" /> {profile.company}</span>}
          </div>
          <div className="mt-5 flex flex-wrap items-center gap-3">
            {profile.website && (
              <a href={profile.website} target="_blank" rel="noopener noreferrer nofollow"
                className="flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 text-sm font-medium text-slate-700 transition-colors hover:border-slate-400"
                style={{ height: 'var(--control-height)' }}>
                <i className="fa-solid fa-globe text-slate-500" aria-hidden="true" /> {t('user.home.personalWebsite')}
              </a>
            )}
            {profile.github_url && (
              <a href={profile.github_url} target="_blank" rel="noopener noreferrer"
                className="flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 text-sm font-medium text-slate-700 transition-colors hover:border-slate-400"
                style={{ height: 'var(--control-height)' }}>
                <GitHubIcon className="h-4 w-4" /> {t('user.home.visitGithub')}
              </a>
            )}
            {feeds && <FeedButton path={userFeedPath(profile.username)} />}
            <Tooltip content={t('user.home.shareProfile')}><button onClick={share}
              className="flex items-center justify-center rounded-lg border border-slate-300 bg-white text-slate-500 transition-colors hover:border-slate-400 hover:text-slate-700"
              style={{ width: 'var(--control-height)', height: 'var(--control-height)' }}>
                <ShareIcon className="h-4 w-4" />
              </button></Tooltip>
          </div>
        </div>

        <KnowledgeNetwork />
      </div>
    </div>
  )
}

function KnowledgeNetwork() {
  return (
    <div className="relative hidden h-44 select-none lg:block" aria-hidden="true">
      <svg className="absolute inset-0 h-full w-full" viewBox="0 0 320 160" fill="none">
        <path d="M30 60 C 100 120, 200 30, 290 70" stroke="#c9d9f8" strokeWidth="1.5" strokeDasharray="1 6" strokeLinecap="round" />
        <path d="M20 160 C 120 200, 220 150, 300 180" stroke="#d8e3fb" strokeWidth="1.5" strokeDasharray="1 6" strokeLinecap="round" />
        <path d="M70 20 C 140 100, 190 190, 260 40" stroke="#d8e3fb" strokeWidth="1.5" strokeDasharray="1 6" strokeLinecap="round" />
        <path d="M40 110 L 200 90 M 200 90 L 280 150 M 200 90 L 150 200" stroke="#e2eafc" strokeWidth="1.5" />
        {[[30, 60], [290, 70], [20, 160], [300, 180], [70, 20], [40, 110], [200, 90], [280, 150], [150, 200], [260, 40]].map(([cx, cy], i) => (
          <circle key={i} cx={cx} cy={cy} r={i % 3 === 0 ? 4 : 3} fill={i % 3 === 0 ? '#8fb2f5' : '#c9d9f8'} />
        ))}
      </svg>
      {[[190, 5], [40, 40], [230, 105]].map(([x, y], i) => (
        <span key={i} className="absolute rounded-lg border border-slate-100 bg-white p-1.5 shadow-sm"
          style={{ left: `${(x / 320) * 100}%`, top: `${(y / 160) * 100}%` }}>
          <span className="block h-10 w-8 rounded bg-gradient-to-br from-primary-100 to-[#B9E4D0]/60" />
        </span>
      ))}
    </div>
  )
}

export default function UserHome({ site, siteUrl, profile, books, sort, achievements }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { requestInput, showToast } = useFeedback()
  const { t, locale } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const [view, setView] = useState<'grid' | 'list'>('grid')
  const sortOptions = [
    { value: 'updated', label: t('user.home.sortUpdated') },
    { value: 'views', label: t('user.home.sortViews') },
    { value: 'title', label: t('user.home.sortTitle') },
  ]
  // 公开书籍列表：客户端按屏宽自适应每页数量（首屏用 SSR 数据，SEO 保持）
  const { ref: gridRef, pageSize } = useGridPageSize({ minItemRem: 15, rows: 3, fallback: books.page_size || 9 })
  const [data, setData] = useState<PageResult<Book>>(books)
  const [page, setPage] = useState(books.page || 1)
  const [sortState, setSortState] = useState(sort)
  const [loading, setLoading] = useState(false)
  const didMount = useRef(false)
  const router = useRouter()
  const { site: appSite } = useApp()
  const versionsEnabled = (appSite.feature_plugins || []).includes('book-versions')
  // 版本聚合：参数放在 URL（versions=grouped），服务端聚合后重新加载分页数据
  const grouped = router.query.versions === 'grouped'

  useEffect(() => {
    // 首次渲染沿用 SSR 数据，不重复请求；此后（翻页/排序/列数变化）客户端拉取
    if (!didMount.current) { didMount.current = true; return }
    let cancelled = false
    setLoading(true)
    api<PageResult<Book>>(`/users/${encodeURIComponent(profile.username)}/books`, { params: { page, page_size: pageSize, sort: sortState, group_versions: grouped ? 'true' : undefined } })
      .then((r) => { if (!cancelled) setData(r) })
      .catch(() => { /* 保持原数据 */ })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [page, pageSize, sortState, grouped, profile.username])

  function changeSort(v: string) {
    setSortState(v)
    setPage(1)
  }
  function toggleGrouped(next: boolean) {
    const q = { ...router.query }
    delete q.page
    if (next) q.versions = 'grouped'
    else delete q.versions
    setPage(1)
    void router.replace({ pathname: router.pathname, query: q }, undefined, { shallow: true, scroll: false })
  }
  const profileUrl = `${siteUrl}/user/${encodeURIComponent(profile.username)}`

  async function share() {
    try {
      await navigator.clipboard.writeText(profileUrl)
      showToast({ message: t('user.home.profileLinkCopied'), tone: 'success' })
    } catch {
      await requestInput({ title: t('user.home.shareTitle'), label: t('user.home.shareLabel'), defaultValue: profileUrl, confirmLabel: t('user.home.shareClose') })
    }
  }

  const jsonLd = {
    '@context': 'https://schema.org',
    '@type': 'ProfilePage',
    mainEntity: {
      '@type': 'Person',
      name: profile.username,
      description: profile.bio || undefined,
      url: profileUrl,
      sameAs: profile.github_url || undefined,
    },
  }

  const items = data.items || [] // 由服务端按 sort 排序 + 分页

  return (
    <Container>
      <Seo
        siteName={siteName}
        title={`${profile.username}'s profile`}
        description={profile.bio || `${siteName} user ${profile.username}, ${profile.public_book_count} public books.`}
        url={profileUrl}
        jsonLd={jsonLd}
        rss={feedsEnabled(site) ? { title: profile.nickname || profile.username, href: userFeedPath(profile.username) } : undefined}
      />

      <div className="py-6">
        <AuthorProfileCard profile={profile} siteUrl={siteUrl} share={share} t={t} />

        {achievements.enabled && achievements.items.length > 0 && (
          <section className="mt-8 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm sm:p-6">
            <div className="flex flex-wrap items-end justify-between gap-3">
              <div><p className="text-xs font-medium uppercase tracking-wider text-primary-600">{t('user.home.growthRecord')}</p><h2 className="mt-1 text-xl font-bold text-ink">{t('user.home.publicAchievements')}</h2></div>
              <span className="text-sm text-slate-400">{t('user.home.publicAchievementsHint', { username: profile.username })}</span>
            </div>
            <div className="mt-5 grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
              {achievements.items.map((grant) => grant.achievement && (
                <div key={grant.id} className="flex min-w-0 flex-col items-center rounded-xl border border-slate-100 bg-slate-50/70 p-3 text-center">
                  <AchievementIcon achievement={grant.achievement} size="sm" />
                  <span className="mt-2 line-clamp-2 text-sm font-medium text-slate-800">{grant.achievement.name}</span>
                  {grant.achievement.series_key && <span className="mt-1 text-[11px] text-slate-400">{t('user.home.achievementTier', { tier: String(grant.achievement.tier) })}</span>}
                </div>
              ))}
            </div>
          </section>
        )}

        <section className="mt-10">
          <div className="mb-5 flex flex-col gap-4 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between">
            <div className="flex flex-col gap-1 sm:flex-row sm:items-baseline sm:gap-3">
              <h2 className="text-2xl font-bold text-ink">{t('user.home.publicBooksSection')}</h2>
              <span className="text-sm text-slate-400">{t('user.home.publicBooksCount', { username: profile.username, count: String(data.total) })}</span>
            </div>
            <div className="flex w-full items-center gap-2 sm:w-auto">
              {versionsEnabled && <VersionGroupToggle checked={grouped} onChange={toggleGrouped} disabled={loading} />}
              <Select className="min-w-0 flex-1 sm:w-36 sm:flex-none" value={sortState} onChange={changeSort} options={sortOptions} />
              <SegmentedTabs iconOnly value={view} ariaLabel={t('user.home.bookViewLabel')}
                onChange={(value) => setView(value as 'grid' | 'list')} items={[
                  { value: 'grid', label: t('user.home.gridView'), icon: <GridIcon className="h-4 w-4" /> },
                  { value: 'list', label: t('user.home.listView'), icon: <ListIcon className="h-4 w-4" /> },
                ]} />
            </div>
          </div>

          <div ref={gridRef}>
          {data.total === 0 ? (
            <p className="py-16 text-center text-slate-400">{t('user.home.noPublicBooks')}</p>
          ) : loading ? (
            <Loading />
          ) : (
            <div className={view === 'grid' ? 'grid gap-5 grid-cols-[repeat(auto-fill,minmax(15rem,1fr))]' : 'space-y-4'}>
              {items.map((b) => <BookCard key={b.id} book={b} view={view} showAuthor={false} />)}
            </div>
          )}
          </div>
        </section>
      </div>

      <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} />
    </Container>
  )
}
