import Link from 'next/link'
import { useRouter } from 'next/router'
import { useEffect, useState } from 'react'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import ResourceIcon from '@/components/ResourceIcon'
import Seo from '@/components/Seo'
import UserAvatar from '@/components/UserAvatar'
import CoverImage from '@/components/CoverImage'
import { api, formatNumber } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { resolveMediaUrl } from '@/lib/media'
import { Card, EmptyState, Loading, Pagination, SegmentedTabs, useFeedback } from '@/components/ui'
import { displayName } from '@/lib/users'
import { useChangeEffect, useUrlPage } from '@/lib/use-url-page'
import { boardText, enabledLeaderboards, type LeaderboardEntry, type LeaderboardLevel, type LeaderboardMeta, type LeaderboardPage, type LeaderboardPeriod } from '@/lib/leaderboards'

const PAGE_SIZE = 20

export default function LeaderboardsPage() {
  return <FeatureGate feature="leaderboards"><LeaderboardsInner /></FeatureGate>
}

function LevelTag({ level }: { level?: LeaderboardLevel }) {
  if (!level) return null
  return (
    <span className="inline-flex shrink-0 items-center gap-1 rounded-full border border-primary-100 bg-primary-50 px-2 py-0.5 text-xs text-primary-700">
      <ResourceIcon iconType={level.icon_type} iconValue={level.icon_value} fallback="fa-star" className="flex h-3.5 w-3.5 items-center justify-center" />
      {level.name || `Lv.${level.level}`}
    </span>
  )
}

const medalTones = ['bg-amber-400 text-white', 'bg-slate-400 text-white', 'bg-orange-400 text-white']

function RankBadge({ rank }: { rank: number }) {
  return (
    <span className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-sm font-bold tabular-nums ${rank <= 3 ? medalTones[rank - 1] : 'bg-slate-100 text-slate-500'}`}>
      {rank}
    </span>
  )
}

function Value({ value, unit, strong }: { value: number; unit: string; strong?: boolean }) {
  return (
    <span className={`ml-auto shrink-0 text-sm font-semibold tabular-nums ${strong ? 'text-primary-600' : 'text-slate-700'}`}>
      {formatNumber(value)}{unit && <span className="ml-1 text-xs font-normal text-slate-400">{unit}</span>}
    </span>
  )
}

function UserRow({ entry, unit, highlight }: { entry: LeaderboardEntry; unit: string; highlight: boolean }) {
  const user = entry.user!
  return (
    <li className={`flex items-center gap-3 px-4 py-3 ${highlight ? 'bg-primary-50/60' : ''}`}>
      <RankBadge rank={entry.rank} />
      <Link href={`/user/${encodeURIComponent(user.username)}`} className="flex min-w-0 items-center gap-2.5 hover:text-primary-600">
        <UserAvatar user={user} size="h-9 w-9" link={false} tooltip={false} />
        <span className="min-w-0">
          <span className="block truncate text-sm font-medium text-slate-800">{displayName(user)}</span>
          {displayName(user) !== user.username && <span className="block truncate text-xs text-slate-400">@{user.username}</span>}
        </span>
      </Link>
      <span className="hidden sm:inline-flex"><LevelTag level={entry.extra?.level} /></span>
      <Value value={entry.value} unit={unit} />
    </li>
  )
}

function BookRow({ entry, unit }: { entry: LeaderboardEntry; unit: string }) {
  const book = entry.book!
  const cover = resolveMediaUrl(book.cover_image)
  const href = `/book/detail/${encodeURIComponent(book.slug)}`
  return (
    <li className="flex items-center gap-3 px-4 py-3">
      <RankBadge rank={entry.rank} />
      <Link href={href} aria-label={book.title} className="relative block h-12 w-10 shrink-0 overflow-hidden rounded bg-gradient-to-br from-primary-300 to-[#8B8DFF]">
        {cover ? <CoverImage src={cover} alt={book.title} /> : <span className="flex h-full w-full items-center justify-center text-sm font-bold text-white/80">{book.title.slice(0, 1)}</span>}
      </Link>
      <span className="min-w-0">
        <Link href={href} className="block truncate text-sm font-medium text-slate-800 hover:text-primary-600">{book.title}</Link>
        {book.user && (
          <Link href={`/user/${encodeURIComponent(book.user.username)}`} className="block truncate text-xs text-slate-400 hover:text-primary-600">{displayName(book.user)}</Link>
        )}
      </span>
      <Value value={entry.value} unit={unit} />
    </li>
  )
}

// LeaderboardsInner 排行榜：榜单（?board=）与周期（?period=）由 URL 驱动；用户榜登录时显示本人名次。
function LeaderboardsInner() {
  const { t } = useTranslation()
  const { site } = useApp()
  const { showToast } = useFeedback()
  const router = useRouter()
  const siteName = site.site_name || 'KnowForge'
  const keys = enabledLeaderboards(site)
  const requested = typeof router.query.board === 'string' ? router.query.board : ''
  const boardKey = keys.includes(requested) ? requested : keys[0] || ''
  const [boards, setBoards] = useState<LeaderboardMeta[] | null>(null)
  const meta = boards?.find((b) => b.key === boardKey)
  const periods: LeaderboardPeriod[] = meta?.periods || ['all']
  const period: LeaderboardPeriod = periods.includes(router.query.period as LeaderboardPeriod) ? (router.query.period as LeaderboardPeriod) : periods[0]
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<LeaderboardPage | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    api<{ items: LeaderboardMeta[] }>('/leaderboards').then((r) => setBoards(r.items || [])).catch(() => setBoards([]))
  }, [])
  useChangeEffect(() => setPage(1), [boardKey, period])
  useEffect(() => {
    if (!router.isReady || !boardKey || boards === null) return // 等榜单列表确定可用周期后再读取
    let alive = true
    setLoading(true)
    api<LeaderboardPage>(`/leaderboards/${encodeURIComponent(boardKey)}`, { params: { period, page, page_size: PAGE_SIZE } })
      .then((r) => { if (alive) setData(r) })
      .catch((e) => { if (alive) showToast({ title: t('leaderboards.loadFailed'), message: (e as Error).message, tone: 'error' }) })
      .finally(() => { if (alive) setLoading(false) })
    return () => { alive = false }
  }, [router.isReady, boardKey, period, page, boards === null]) // eslint-disable-line react-hooks/exhaustive-deps

  const href = (board: string, p?: LeaderboardPeriod) => {
    const q = new URLSearchParams()
    if (board !== keys[0]) q.set('board', board)
    if (p && p !== 'all') q.set('period', p)
    const s = q.toString()
    return s ? `/leaderboards?${s}` : '/leaderboards'
  }
  const current = data && data.board.key === boardKey ? data : null
  const unit = boardText(t, 'unit', boardKey)
  const me = current?.me

  return (
    <>
      <Seo siteName={siteName} title={boardKey ? `${boardText(t, 'name', boardKey)} · ${t('leaderboards.title')}` : t('leaderboards.title')} description={t('leaderboards.subtitle')} />
      <Container>
        <div className="py-8">
          <h1 className="text-2xl font-bold text-ink">{t('leaderboards.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('leaderboards.subtitle')}</p>

          {keys.length === 0 ? (
            <div className="mt-6"><EmptyState>{t('leaderboards.closed')}</EmptyState></div>
          ) : (<>
            <SegmentedTabs className="mt-6" value={boardKey} ariaLabel={t('leaderboards.title')}
              items={keys.map((k) => {
                const icon = boards?.find((b) => b.key === k)?.icon
                return { value: k, href: href(k), label: boardText(t, 'name', k), icon: icon ? <i className={`fa-solid ${icon} text-xs opacity-70`} aria-hidden="true" /> : undefined }
              })} />
            <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
              <p className="text-sm text-slate-500">{boardText(t, 'desc', boardKey)}</p>
              {periods.length > 1 && (
                <SegmentedTabs size="sm" value={period} ariaLabel={t('leaderboards.periodLabel')}
                  items={periods.map((p) => ({ value: p, label: t(`leaderboards.period.${p}`), href: href(boardKey, p) }))} />
              )}
            </div>

            {me && (
              <Card className="mt-5 flex flex-wrap items-center gap-3 p-4" data-testid="leaderboard-me">
                {me.rank ? <RankBadge rank={me.rank} /> : <span className="flex h-8 w-8 items-center justify-center rounded-full bg-slate-100 text-slate-400">—</span>}
                <UserAvatar user={me.user} size="h-9 w-9" link={false} tooltip={false} />
                <span className="min-w-0">
                  <span className="block text-sm font-medium text-slate-800">{t('leaderboards.me')}</span>
                  <span className="text-xs text-slate-400">
                    {me.rank ? t('leaderboards.myRank', { rank: me.rank }) : t('leaderboards.noRank')}
                    {me.public === false && ` · ${t('leaderboards.hiddenHint')}`}
                  </span>
                </span>
                <LevelTag level={me.level} />
                <Value value={me.value} unit={unit} strong />
              </Card>
            )}

            {!current ? <Loading className="py-16" /> : current.total === 0 ? (
              <div className="mt-5"><EmptyState>{t('leaderboards.empty')}</EmptyState></div>
            ) : (
              <Card className={`mt-5 overflow-hidden ${loading ? 'pointer-events-none opacity-60' : ''}`} aria-busy={loading}>
                <ul className="divide-y divide-slate-100" data-testid="leaderboard-list">
                  {current.items.map((e) => e.book
                    ? <BookRow key={`b${e.book.id}`} entry={e} unit={unit} />
                    : <UserRow key={`u${e.user?.id}`} entry={e} unit={unit} highlight={!!me && me.user.id === e.user?.id} />)}
                </ul>
              </Card>
            )}
            {current && <Pagination page={current.page} pageSize={current.page_size} total={current.total} onChange={setPage} />}
          </>)}
        </div>
      </Container>
    </>
  )
}
