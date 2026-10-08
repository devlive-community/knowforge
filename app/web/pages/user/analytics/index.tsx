import { useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import Container from '@/components/Container'
import { api, formatNumber } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Card, EmptyState, Loading, Pagination, SegmentedTabs, Tooltip } from '@/components/ui'
import Seo from '@/components/Seo'
import { BookIcon, EyeIcon, UsersIcon, CheckCircleSmallIcon } from '@/components/icons'

interface AuthorBookRow {
  id: number
  title: string
  slug: string
  status: string
  is_public: boolean
  lifetime_views: number
  period_views: number
  previous_views: number
  growth_percent: number | null
  chapters: number
  registered_readers: number
  completed_readers: number
  completion_rate: number
  updated_at: string
}

interface AuthorAnalytics {
  days: number
  total_books: number
  published_books: number
  total_lifetime_views: number
  total_period_views: number
  total_previous_views: number
  total_growth_percent: number | null
  total_readers: number
  books: AuthorBookRow[]
}

interface RetentionCohort {
  week: string
  size: number
  retention: number[]
}

interface ReaderRetention {
  weeks: number
  cohorts: RetentionCohort[]
  curve: (number | null)[]
}

type SortKey = 'lifetime_views' | 'period_views' | 'registered_readers' | 'completion_rate' | 'chapters'

const BOOKS_PER_PAGE = 10

function StatTile({ icon, label, value, hint, tone }: { icon: string; label: string; value: string; hint?: string; tone: string }) {
  return (
    <div className="flex items-center gap-3 rounded-xl border border-slate-200 bg-white p-4">
      <span className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-full ${tone}`}>
        <i className={`fa-solid ${icon}`} aria-hidden="true" />
      </span>
      <span className="min-w-0">
        <span className="block text-lg font-bold tabular-nums text-slate-900">{value}</span>
        <span className="text-xs text-slate-400">{label}{hint ? ` · ${hint}` : ''}</span>
      </span>
    </div>
  )
}

function GrowthTag({ value }: { value: number | null }) {
  if (value === null) return <span className="text-xs text-slate-300">—</span>
  const up = value >= 0
  return (
    <span className={`inline-flex items-center gap-0.5 text-xs font-medium ${up ? 'text-emerald-600' : 'text-rose-500'}`}>
      <i className={`fa-solid ${up ? 'fa-arrow-trend-up' : 'fa-arrow-trend-down'}`} aria-hidden="true" />
      {up ? '+' : ''}{value}%
    </span>
  )
}

function retentionCellClass(ratio: number): string {
  if (ratio <= 0) return 'bg-slate-50 text-slate-300'
  if (ratio < 0.25) return 'bg-primary-100 text-primary-700'
  if (ratio < 0.5) return 'bg-primary-200 text-primary-800'
  if (ratio < 0.75) return 'bg-primary-400 text-white'
  return 'bg-primary-600 text-white'
}

function RetentionSection() {
  const { t } = useTranslation()
  const [data, setData] = useState<ReaderRetention | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api<ReaderRetention>('/users/me/reader-retention')
      .then(setData)
      .catch(() => setData(null))
      .finally(() => setLoading(false))
  }, [])

  const rows = useMemo(() => (data?.cohorts || []).filter((c) => c.size > 0), [data])
  const curvePoints = useMemo(() => (data?.curve || []).map((v, i) => ({ offset: i, value: v })).filter((p) => p.value !== null), [data])

  return (
    <Card className="mt-5 overflow-hidden">
      <div className="border-b border-slate-100 px-6 py-5">
        <h2 className="font-bold text-slate-900">{t('user.analytics.retention')}</h2>
        <p className="mt-1 text-xs text-slate-400">{t('user.analytics.retentionHint', { weeks: data?.weeks || 12 })}</p>
      </div>

      {loading ? (
        <Loading label={t('user.analytics.calculatingRetention')} />
      ) : !data || rows.length === 0 ? (
        <EmptyState>{t('user.analytics.noRetentionData', { weeks: data?.weeks || 12 })}</EmptyState>
      ) : (
        <div className="space-y-6 p-6">
          {/* 加权聚合曲线 */}
          <div>
            <h3 className="mb-3 text-sm font-semibold text-slate-700">{t('user.analytics.overallCurve')}</h3>
            <div className="flex h-32 items-end gap-1.5 border-b border-slate-200" role="img" aria-label={t('user.analytics.overallCurve')}>
              {curvePoints.map((p) => (
                <div key={p.offset} className="flex min-w-0 flex-1 flex-col items-center justify-end" style={{ height: '100%' }}>
                  <span className="mb-1 text-[10px] tabular-nums text-slate-400">{p.value}%</span>
                  <span className="w-full max-w-[40px] rounded-t bg-primary-400" style={{ height: `${Math.max(2, p.value as number)}%` }}
                    aria-label={t('user.analytics.weekRetention', { week: p.offset, value: p.value ?? 0 })} />
                </div>
              ))}
            </div>
            <div className="mt-1.5 flex gap-1.5">
              {curvePoints.map((p) => (
                <span key={p.offset} className="min-w-0 flex-1 text-center text-[10px] text-slate-400">{p.offset}{t('user.analytics.week')}</span>
              ))}
            </div>
          </div>

          {/* 队列三角矩阵 */}
          <div>
            <h3 className="mb-3 text-sm font-semibold text-slate-700">{t('user.analytics.cohortDetails')}</h3>
            <div className="overflow-x-auto">
              <table className="min-w-[560px] border-separate" style={{ borderSpacing: '3px' }}>
                <thead>
                  <tr className="text-[10px] text-slate-400">
                    <th className="px-2 text-left font-medium">{t('user.analytics.firstReadWeek')}</th>
                    <th className="px-2 text-right font-medium">{t('user.analytics.newReaders')}</th>
                    {Array.from({ length: data.weeks }, (_, i) => (
                      <th key={i} className="w-10 text-center font-medium">{i}{t('user.analytics.week')}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((cohort) => (
                    <tr key={cohort.week}>
                      <td className="whitespace-nowrap px-2 text-xs tabular-nums text-slate-500">{cohort.week.slice(5)}</td>
                      <td className="px-2 text-right text-xs font-medium tabular-nums text-slate-700">{cohort.size}</td>
                      {Array.from({ length: data.weeks }, (_, offset) => {
                        const count = cohort.retention[offset]
                        if (count === undefined) return <td key={offset} className="h-9 w-10" />
                        const ratio = cohort.size > 0 ? count / cohort.size : 0
                        return (
                          <td key={offset} className="h-9 w-10">
                            <Tooltip content={t('user.analytics.retentionTooltip', { week: cohort.week, offset, count, size: cohort.size, percent: Math.round(ratio * 100) })} className="block h-full w-full">
                              <span className={`flex h-9 w-full items-center justify-center rounded text-[11px] font-medium tabular-nums ${retentionCellClass(ratio)}`}>
                                {Math.round(ratio * 100)}
                              </span>
                            </Tooltip>
                          </td>
                        )
                      })}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p className="mt-2 text-[10px] text-slate-400">{t('user.analytics.retentionNote')}</p>
          </div>
        </div>
      )}
    </Card>
  )
}

export default function AuthorAnalyticsPage() {
  const user = useRequireAuth()
  const { site } = useApp()
  const siteName = site.site_name || 'KnowForge'
  const { t } = useTranslation()
  const [days, setDays] = useState('30')
  const [data, setData] = useState<AuthorAnalytics | null>(null)
  const [loading, setLoading] = useState(true)
  const [sortKey, setSortKey] = useState<SortKey>('period_views')
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc')
  const [page, setPage] = useState(1)

  useEffect(() => {
    if (!user) return
    setLoading(true)
    api<AuthorAnalytics>('/users/me/author-analytics', { params: { days } })
      .then(setData)
      .catch(() => setData(null))
      .finally(() => setLoading(false))
  }, [user, days])

  const sorted = useMemo(() => {
    const rows = [...(data?.books || [])]
    rows.sort((a, b) => {
      const diff = (a[sortKey] as number) - (b[sortKey] as number)
      return sortDir === 'asc' ? diff : -diff
    })
    return rows
  }, [data, sortKey, sortDir])

  function toggleSort(key: SortKey) {
    setPage(1)
    if (key === sortKey) {
      setSortDir((dir) => (dir === 'desc' ? 'asc' : 'desc'))
    } else {
      setSortKey(key)
      setSortDir('desc')
    }
  }

  function changePeriod(value: string) {
    setDays(value)
    setPage(1)
  }

  const visibleRows = sorted.slice((page - 1) * BOOKS_PER_PAGE, page * BOOKS_PER_PAGE)

  const PERIODS = [
    { value: '7', label: t('user.analytics.period7') },
    { value: '30', label: t('user.analytics.period30') },
    { value: '90', label: t('user.analytics.period90') },
    { value: '180', label: t('user.analytics.period180') },
  ]

  const STATUS_LABELS: Record<string, string> = {
    draft: t('user.analytics.statusDraft'),
    in_progress: t('user.analytics.statusInProgress'),
    published: t('user.analytics.statusPublished'),
    completed: t('user.analytics.statusCompleted'),
    archived: t('user.analytics.statusArchived'),
  }

  const COLUMNS: { key: SortKey; label: string }[] = [
    { key: 'chapters', label: t('user.analytics.chapters') },
    { key: 'lifetime_views', label: t('user.analytics.lifetimeViews') },
    { key: 'period_views', label: t('user.analytics.periodViews') },
    { key: 'registered_readers', label: t('user.analytics.readers') },
    { key: 'completion_rate', label: t('user.analytics.completionRate') },
  ]

  if (!user) return <Loading className="min-h-[60vh]" label={t('user.analytics.verifyingAuth')} />

  return (
    <>
      <Seo siteName={siteName} title={t('user.analytics.title')} noindex />
      <Container>
        <div className="flex flex-wrap items-end justify-between gap-3 pb-6">
          <div>
            <h1 className="text-2xl font-bold text-ink">{t('user.analytics.title')}</h1>
            <p className="mt-1 text-sm text-slate-500">{t('user.analytics.description')}</p>
          </div>
          <SegmentedTabs size="sm" value={days} items={PERIODS} ariaLabel={t('user.analytics.period')} onChange={changePeriod} />
        </div>

        {loading || data === null ? (
          <Loading label={t('user.analytics.loading')} />
        ) : data.total_books === 0 ? (
          <EmptyState>
            <BookIcon className="mx-auto mb-3 h-10 w-10 text-slate-300" />
            {t('user.analytics.noBooks')}<Link href="/books" className="text-primary-600 hover:underline">{t('user.analytics.goToBooks')}</Link>{t('user.analytics.publishFirst')}
          </EmptyState>
        ) : (
          <>
            <div className="mb-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
              <StatTile icon="fa-book" label={t('user.analytics.books')} hint={`${data.published_books} ${t('user.analytics.publishedCount')}`} value={String(data.total_books)} tone="bg-primary-50 text-primary-500" />
              <StatTile icon="fa-eye" label={t('user.analytics.totalViews')} value={formatNumber(data.total_lifetime_views)} tone="bg-sky-50 text-sky-500" />
              <StatTile icon="fa-chart-line" label={`${data.days} ${t('user.analytics.daysViews')}`}
                hint={data.total_growth_percent === null ? t('user.analytics.noPreviousData') : `${t('user.analytics.growth')} ${data.total_growth_percent >= 0 ? '+' : ''}${data.total_growth_percent}%`}
                value={formatNumber(data.total_period_views)} tone="bg-violet-50 text-violet-500" />
              <StatTile icon="fa-users" label={t('user.analytics.loggedInReaders')} value={formatNumber(data.total_readers)} tone="bg-emerald-50 text-emerald-500" />
            </div>

            <Card className="overflow-hidden">
              <div className="border-b border-slate-100 px-6 py-5">
                <h2 className="font-bold text-slate-900">{t('user.analytics.bookComparison')}</h2>
                <p className="mt-1 text-xs text-slate-400">{t('user.analytics.bookComparisonHint')}</p>
              </div>
              <div className="overflow-x-auto">
                <table className="w-full min-w-[720px] text-sm">
                  <thead>
                    <tr className="border-b border-slate-100 text-left text-xs text-slate-400">
                      <th className="px-6 py-3 font-medium">{t('user.analytics.book')}</th>
                      {COLUMNS.map((col) => (
                        <th key={col.key} className="px-4 py-3 font-medium">
                          <button type="button" onClick={() => toggleSort(col.key)}
                            className={`inline-flex items-center gap-1 transition-colors hover:text-slate-700 ${sortKey === col.key ? 'text-slate-700' : ''}`}>
                            {col.label}
                            <i className={`fa-solid text-[10px] ${sortKey === col.key ? (sortDir === 'desc' ? 'fa-caret-down' : 'fa-caret-up') : 'fa-sort text-slate-300'}`} aria-hidden="true" />
                          </button>
                        </th>
                      ))}
                      <th className="px-4 py-3 font-medium">{t('user.analytics.growth')}</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-50">
                    {visibleRows.map((row) => (
                      <tr key={row.id} className="transition-colors hover:bg-slate-50/60">
                        <td className="px-6 py-3.5">
                          <Link href={`/book/settings/${encodeURIComponent(row.slug)}/analytics`}
                            className="block max-w-[220px] truncate font-medium text-slate-800 hover:text-primary-600">{row.title}</Link>
                          <span className="mt-0.5 flex items-center gap-1.5 text-xs text-slate-400">
                            <span className={`rounded px-1.5 py-0.5 ${row.status === 'published' || row.status === 'completed' ? 'bg-emerald-50 text-emerald-600' : 'bg-slate-100 text-slate-500'}`}>
                              {STATUS_LABELS[row.status] || row.status}
                            </span>
                            {!row.is_public && <span className="text-slate-400">{t('user.analytics.private')}</span>}
                          </span>
                        </td>
                        <td className="px-4 py-3.5 tabular-nums text-slate-600">{row.chapters}</td>
                        <td className="px-4 py-3.5 tabular-nums text-slate-600">{formatNumber(row.lifetime_views)}</td>
                        <td className="px-4 py-3.5 tabular-nums text-slate-600">{formatNumber(row.period_views)}</td>
                        <td className="px-4 py-3.5 tabular-nums text-slate-600">{formatNumber(row.registered_readers)}</td>
                        <td className="px-4 py-3.5 tabular-nums text-slate-600">{row.completion_rate}%</td>
                        <td className="px-4 py-3.5"><GrowthTag value={row.growth_percent} /></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="px-6 pb-5">
                <Pagination page={page} pageSize={BOOKS_PER_PAGE} total={sorted.length} onChange={setPage} />
              </div>
            </Card>

            <RetentionSection />

            <p className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-slate-400">
              <span className="inline-flex items-center gap-1"><EyeIcon className="h-3.5 w-3.5" /> {t('user.analytics.viewsIncludeDetails')}</span>
              <span className="inline-flex items-center gap-1"><UsersIcon className="h-3.5 w-3.5" /> {t('user.analytics.readersDefinition')}</span>
              <span className="inline-flex items-center gap-1"><CheckCircleSmallIcon className="h-3.5 w-3.5" /> {t('user.analytics.completionRateDefinition')}</span>
            </p>
          </>
        )}
      </Container>
    </>
  )
}
