import Link from 'next/link'
import { useRouter } from 'next/router'
import { useEffect, useState } from 'react'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import ResourceIcon from '@/components/ResourceIcon'
import Seo from '@/components/Seo'
import UserAvatar from '@/components/UserAvatar'
import { api, formatNumber } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Card, EmptyState, Loading, Pagination, SegmentedTabs, useFeedback } from '@/components/ui'
import { displayName } from '@/lib/users'

type Period = 'all' | 'month' | 'week'
interface Level { level: number; name: string; icon_type?: string; icon_value?: string; color?: string }
interface LbUser { id: number; username: string; nickname?: string; display_name?: string; avatar?: string }
interface Entry { rank: number; xp: number; user: LbUser; level?: Level }
interface Me { rank?: number; xp: number; public: boolean; user: LbUser; level?: Level }
interface Board { items: Entry[]; total: number; page: number; page_size: number; period: Period; min_xp: number; me?: Me }

const PAGE_SIZE = 20
const PERIODS: Period[] = ['all', 'month', 'week']

export default function LeaderboardPage() {
  return <FeatureGate feature="growth"><LeaderboardInner /></FeatureGate>
}

function LevelTag({ level }: { level?: Level }) {
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

// LeaderboardInner 公开经验排行榜：周期（累计/近 30 天/近 7 天）由 URL ?period= 驱动；登录时显示本人名次。
function LeaderboardInner() {
  const { t } = useTranslation()
  const { site } = useApp()
  const { showToast } = useFeedback()
  const router = useRouter()
  const siteName = site.site_name || 'KnowForge'
  const period: Period = PERIODS.includes(router.query.period as Period) ? (router.query.period as Period) : 'all'
  const [page, setPage] = useState(1)
  const [board, setBoard] = useState<Board | null>(null)
  const [loading, setLoading] = useState(false)

  const open = site.growth_leaderboard_enabled !== false // 管理员关闭排行榜时显示未开放
  useEffect(() => { setPage(1) }, [period])
  useEffect(() => {
    if (!router.isReady || !open) return
    let alive = true
    setLoading(true)
    api<Board>('/growth/leaderboard', { params: { period, page, page_size: PAGE_SIZE } })
      .then((r) => { if (alive) setBoard(r) })
      .catch((e) => { if (alive) showToast({ title: t('growth.leaderboard.loadFailed'), message: (e as Error).message, tone: 'error' }) })
      .finally(() => { if (alive) setLoading(false) })
    return () => { alive = false }
  }, [router.isReady, open, period, page]) // eslint-disable-line react-hooks/exhaustive-deps

  const me = board?.me

  return (
    <>
      <Seo siteName={siteName} title={t('growth.leaderboard.title')} description={t('growth.leaderboard.subtitle')} />
      <Container>
        <div className="py-8">
          <h1 className="text-2xl font-bold text-ink">{t('growth.leaderboard.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('growth.leaderboard.subtitle')}</p>

          {!open ? (
            <div className="mt-6"><EmptyState>{t('growth.leaderboard.closed')}</EmptyState></div>
          ) : (<>
          <SegmentedTabs className="mt-6" value={period} ariaLabel={t('growth.leaderboard.title')}
            items={PERIODS.map((p) => ({ value: p, label: t(`growth.leaderboard.period.${p}`), href: p === 'all' ? '/growth/leaderboard' : `/growth/leaderboard?period=${p}` }))} />

          {me && (
            <Card className="mt-5 flex flex-wrap items-center gap-3 p-4">
              {me.rank ? <RankBadge rank={me.rank} /> : <span className="flex h-8 w-8 items-center justify-center rounded-full bg-slate-100 text-slate-400">—</span>}
              <UserAvatar user={me.user} size="h-9 w-9" link={false} tooltip={false} />
              <span className="min-w-0">
                <span className="block text-sm font-medium text-slate-800">{t('growth.leaderboard.me')}</span>
                <span className="text-xs text-slate-400">
                  {me.rank ? t('growth.leaderboard.myRank', { rank: me.rank }) : t('growth.leaderboard.noRank')}
                  {!me.public && ` · ${t('growth.leaderboard.hiddenHint')}`}
                </span>
              </span>
              <LevelTag level={me.level} />
              <span className="ml-auto text-sm font-semibold tabular-nums text-primary-600">{formatNumber(me.xp)} XP</span>
            </Card>
          )}

          {!board ? <Loading className="py-16" /> : board.total === 0 ? (
            <div className="mt-5"><EmptyState>{t('growth.leaderboard.empty')}</EmptyState></div>
          ) : (
            <Card className={`mt-5 overflow-hidden ${loading ? 'pointer-events-none opacity-60' : ''}`} aria-busy={loading}>
              <ul className="divide-y divide-slate-100">
                {board.items.map((e) => (
                  <li key={e.user.id} className={`flex items-center gap-3 px-4 py-3 ${me && me.user.id === e.user.id ? 'bg-primary-50/60' : ''}`}>
                    <RankBadge rank={e.rank} />
                    <Link href={`/user/${encodeURIComponent(e.user.username)}`} className="flex min-w-0 items-center gap-2.5 hover:text-primary-600">
                      <UserAvatar user={e.user} size="h-9 w-9" link={false} tooltip={false} />
                      <span className="min-w-0">
                        <span className="block truncate text-sm font-medium text-slate-800">{displayName(e.user)}</span>
                        {displayName(e.user) !== e.user.username && <span className="block truncate text-xs text-slate-400">@{e.user.username}</span>}
                      </span>
                    </Link>
                    <span className="hidden sm:inline-flex"><LevelTag level={e.level} /></span>
                    <span className="ml-auto shrink-0 text-sm font-semibold tabular-nums text-slate-700">{formatNumber(e.xp)} XP</span>
                  </li>
                ))}
              </ul>
            </Card>
          )}
          {board && <Pagination page={board.page} pageSize={board.page_size} total={board.total} onChange={setPage} />}
          {board && board.min_xp > 1 && <p className="mt-3 text-xs text-slate-400">{t('growth.leaderboard.minXpNote', { xp: formatNumber(board.min_xp) })}</p>}
          </>)}
        </div>
      </Container>
    </>
  )
}
