import { useEffect, useState } from 'react'
import Container from '@/components/Container'
import { api } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useGridPageSize } from '@/lib/useGridPageSize'
import MyLibraryTabs from '@/components/MyLibraryTabs'
import { useTranslation } from '@/lib/i18n'
import { Button, ButtonLink, EmptyState, Input, Loading, Pagination, Tooltip, useFeedback } from '@/components/ui'
import BookCard from '@/components/BookCard'
import Seo from '@/components/Seo'
import { BookIcon } from '@/components/icons'
import type { Book } from '@/lib/types'
import { useUrlPage } from '@/lib/use-url-page'

interface ReadingItem {
  book: Book
  read_count: number
  total_chapters: number
  percentage: number
  last_doc_slug: string
  last_doc_title: string
  last_read_at: string
  read_seconds: number
}

interface ReadingPage {
  items: ReadingItem[]
  total: number
  page: number
  page_size: number
}

interface ReadingStats {
  reading_books: number
  completed_books: number
  chapters_read: number
  streak_days: number
}

interface ActivityDay {
  date: string
  count: number
  minutes: number
  met: boolean
}
interface ReadingGoalCfg {
  goal_type: 'chapters' | 'minutes'
  daily_chapters: number
  daily_minutes: number
}
interface ActivityData {
  goal: ReadingGoalCfg
  days: ActivityDay[]
  current_streak: number
  longest_streak: number
  today_count: number
  today_minutes: number
  today_met: boolean
}

function formatReadTime(seconds: number, t: (key: string, vars?: Record<string, string | number>) => string): string {
  if (!seconds || seconds < 60) return t('user.reading.lessThanMinute')
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return t('user.reading.minutes', { count: String(minutes) })
  return t('user.reading.hours', { count: (minutes / 60).toFixed(1) })
}

function CheckinCalendar() {
  const { showToast } = useFeedback()
  const { t } = useTranslation()
  const [data, setData] = useState<ActivityData | null>(null)
  const [goalType, setGoalType] = useState<'chapters' | 'minutes'>('chapters')
  const [chaptersInput, setChaptersInput] = useState(1)
  const [minutesInput, setMinutesInput] = useState(15)
  const [saving, setSaving] = useState(false)

  const load = () => {
    api<ActivityData>('/users/me/reading-activity', { params: { days: 364 } })
      .then((d) => {
        setData(d)
        setGoalType(d.goal.goal_type)
        setChaptersInput(d.goal.daily_chapters)
        setMinutesInput(d.goal.daily_minutes)
      })
      .catch(() => { /* 忽略 */ })
  }
  useEffect(() => { load() }, [])

  if (!data) return null
  const isMinutes = data.goal.goal_type === 'minutes'
  const target = isMinutes ? data.goal.daily_minutes : data.goal.daily_chapters
  const todayVal = isMinutes ? data.today_minutes : data.today_count
  const unit = isMinutes ? t('user.reading.unitMinutes') : t('user.reading.unitChapters')
  const dirty = goalType !== data.goal.goal_type || chaptersInput !== data.goal.daily_chapters || minutesInput !== data.goal.daily_minutes

  const saveGoal = async () => {
    setSaving(true)
    try {
      await api('/users/me/reading-goal', { method: 'PUT', body: { goal_type: goalType, daily_chapters: chaptersInput, daily_minutes: minutesInput } })
      load()
      showToast({ message: t('user.reading.goalUpdated'), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error)?.message || t('user.reading.saveFailed'), tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  const cellColor = (d: ActivityDay) => {
    const val = isMinutes ? d.minutes : d.count
    if (val === 0) return 'bg-slate-100'
    if (!d.met) return 'bg-primary-200'
    return val >= target * 2 ? 'bg-primary-600' : 'bg-primary-500'
  }
  const leadPad = data.days.length ? new Date(data.days[0].date + 'T00:00:00').getDay() : 0
  const padded: (ActivityDay | null)[] = [...Array(leadPad).fill(null), ...data.days]
  const weeks: (ActivityDay | null)[][] = []
  for (let i = 0; i < padded.length; i += 7) weeks.push(padded.slice(i, i + 7))
  const weekdayLabels = ['', t('user.reading.weekdayMon'), '', t('user.reading.weekdayWed'), '', t('user.reading.weekdayFri'), '']

  return (
    <div className="mb-6 rounded-xl border border-slate-200 bg-white p-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-sm font-semibold text-slate-700">{t('user.reading.checkinTitle')}</h2>
          <p className="mt-0.5 flex flex-wrap items-center gap-x-1 text-xs text-slate-400">
            {t('user.reading.streak')} <span className="font-semibold text-primary-600">{data.current_streak}</span> {t('user.reading.days')} · {t('user.reading.longest')} {data.longest_streak} {t('user.reading.days')} ·
            {t('user.reading.today')} {todayVal}/{target} {unit}
            {data.today_met && <i className="fa-solid fa-circle-check text-emerald-500" aria-label={t('user.reading.goalMet')} />}
          </p>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-2 text-xs text-slate-500">
          <span>{t('user.reading.dailyGoal')}</span>
          <span className="inline-flex overflow-hidden rounded-lg border border-slate-200">
            {(['chapters', 'minutes'] as const).map((g) => (
              <button key={g} type="button" onClick={() => setGoalType(g)}
                className={`px-2.5 py-1 transition-colors ${goalType === g ? 'bg-primary-500 text-white' : 'bg-white text-slate-500 hover:bg-slate-50'}`}>
                {g === 'chapters' ? t('user.reading.goalChapters') : t('user.reading.goalMinutes')}
              </button>
            ))}
          </span>
          {goalType === 'chapters' ? (
            <span className="inline-block w-14 shrink-0">
              <Input type="number" min={1} max={100} size="sm" value={chaptersInput}
                onChange={(e) => setChaptersInput(Math.max(1, Math.min(100, Number(e.target.value) || 1)))}
                className="text-center" aria-label={t('user.reading.dailyChaptersGoal')} />
            </span>
          ) : (
            <span className="inline-block w-16 shrink-0">
              <Input type="number" min={1} max={600} size="sm" value={minutesInput}
                onChange={(e) => setMinutesInput(Math.max(1, Math.min(600, Number(e.target.value) || 1)))}
                className="text-center" aria-label={t('user.reading.dailyMinutesGoal')} />
            </span>
          )}
          <span>{goalType === 'chapters' ? t('user.reading.unitChapters') : t('user.reading.unitMinutes')}</span>
          <Button variant="ghost" size="sm" loading={saving} disabled={!dirty} onClick={saveGoal}>{t('user.reading.save')}</Button>
        </div>
      </div>

      <div className="mt-3 overflow-x-auto pb-1">
        <div className="flex gap-1" style={{ minWidth: `${weeks.length * 10 + 24}px` }}>
          <div className="flex shrink-0 flex-col gap-1 pr-1 text-[9px] leading-none text-slate-300">
            {weekdayLabels.map((w, i) => <span key={i} className="flex flex-1 items-center">{w}</span>)}
          </div>
          {weeks.map((week, wi) => (
            <div key={wi} className="flex flex-1 flex-col gap-1">
              {week.map((d, di) => (
                <div key={di} className="aspect-square w-full">
                  {d && (
                    <Tooltip content={t('user.reading.activityTooltip', { date: d.date, count: String(d.count), minutes: String(d.minutes), met: d.met ? ' · ' + t('user.reading.goalMet') : '' })} className="h-full w-full">
                      <span className={`block h-full w-full rounded-sm ${cellColor(d)}`} />
                    </Tooltip>
                  )}
                </div>
              ))}
            </div>
          ))}
        </div>
      </div>
      <div className="mt-2 flex items-center justify-end gap-1 text-[10px] text-slate-400">
        <span>{t('user.reading.less')}</span>
        <span className="h-3 w-3 rounded-sm bg-slate-100" />
        <span className="h-3 w-3 rounded-sm bg-primary-200" />
        <span className="h-3 w-3 rounded-sm bg-primary-500" />
        <span className="h-3 w-3 rounded-sm bg-primary-600" />
        <span>{t('user.reading.more')}</span>
      </div>
    </div>
  )
}

function StatTile({ icon, label, value, tone }: { icon: string; label: string; value: number; tone: string }) {
  return (
    <div className="flex items-center gap-3 rounded-xl border border-slate-200 bg-white p-4">
      <span className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-full ${tone}`}>
        <i className={`fa-solid ${icon}`} aria-hidden="true" />
      </span>
      <span className="min-w-0">
        <span className="block text-lg font-bold tabular-nums text-slate-900">{value.toLocaleString('en-US')}</span>
        <span className="text-xs text-slate-400">{label}</span>
      </span>
    </div>
  )
}

function ReadingCard({ item }: { item: ReadingItem }) {
  const { t } = useTranslation()
  const { book, read_count, total_chapters, percentage, last_doc_slug, last_doc_title, read_seconds } = item
  const resumeUrl = last_doc_slug
    ? `/book/reader/${encodeURIComponent(book.slug)}/${encodeURIComponent(last_doc_slug)}`
    : `/book/detail/${encodeURIComponent(book.slug)}`
  return (
    <BookCard
      book={book}
      view="grid"
      showViews={false}
      meta={
        <span>
          {t('user.reading.readProgress', { read: String(read_count), total: String(total_chapters) })}
          {read_seconds > 0 && <span className="text-slate-400"> · {t('user.reading.readTime', { time: formatReadTime(read_seconds, t) })}</span>}
        </span>
      }
      actions={
        <div className="w-full">
          <div className="mb-2 flex items-center justify-between gap-2 text-xs text-slate-500">
            <span className="truncate">{t('user.reading.lastRead')}：{last_doc_title || t('user.reading.notStarted')}</span>
            <span className="tabular-nums text-slate-400">{percentage}%</span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-slate-100">
            <span
              className="block h-full rounded-full bg-primary-500 transition-all duration-300"
              style={{ width: `${percentage}%` }}
            />
          </div>
          <ButtonLink href={resumeUrl} className="mt-3 w-full justify-center">
            <BookIcon className="h-4 w-4" /> {t('user.reading.continueReading')}
          </ButtonLink>
        </div>
      }
    />
  )
}

export default function MyReading() {
  const user = useRequireAuth()
  const { site } = useApp()
  const siteName = site.site_name || 'KnowForge'
  const { t } = useTranslation()
  const { ref: gridRef, pageSize, ready } = useGridPageSize({ minItemRem: 18, rows: 3, fallback: 9 })
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<ReadingPage | null>(null)
  const [stats, setStats] = useState<ReadingStats | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!user || !ready) return
    setLoading(true)
    api<ReadingPage>('/users/me/reading', { params: { page, page_size: pageSize } })
      .then(setData)
      .catch(() => setData({ items: [], total: 0, page: 1, page_size: pageSize }))
      .finally(() => setLoading(false))
  }, [user, page, pageSize, ready])

  useEffect(() => {
    if (!user) return
    api<ReadingStats>('/users/me/reading-stats').then(setStats).catch(() => { /* 概览失败不阻塞列表 */ })
  }, [user])

  if (!user) return <Loading className="min-h-[60vh]" label={t('user.reading.verifyingAuth')} />

  return (
    <>
      <Seo siteName={siteName} title={t('user.reading.title')} noindex />
      <Container>
        <div className="pb-6">
          <h1 className="text-2xl font-bold text-ink">{t('user.reading.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('user.reading.description')}</p>
        </div>
        <MyLibraryTabs active="reading" />

        {stats && (
          <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-4">
            <StatTile icon="fa-book-open-reader" label={t('user.reading.readingBooks')} value={stats.reading_books} tone="bg-primary-50 text-primary-500" />
            <StatTile icon="fa-circle-check" label={t('user.reading.completedBooks')} value={stats.completed_books} tone="bg-emerald-50 text-emerald-500" />
            <StatTile icon="fa-list-check" label={t('user.reading.totalChaptersRead')} value={stats.chapters_read} tone="bg-sky-50 text-sky-500" />
            <StatTile icon="fa-fire" label={t('user.reading.streakDays')} value={stats.streak_days} tone="bg-amber-50 text-amber-500" />
          </div>
        )}

        <CheckinCalendar />

        <div ref={gridRef}>
          {loading || data === null ? (
            <Loading label={t('user.reading.loading')} />
          ) : data.total === 0 ? (
            <EmptyState>{t('user.reading.noRecords')}</EmptyState>
          ) : (
            <>
              <div className="grid gap-5 grid-cols-[repeat(auto-fill,minmax(18rem,1fr))]">
                {data.items.map((item) => (
                  <ReadingCard key={item.book.id} item={item} />
                ))}
              </div>
              <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} />
            </>
          )}
        </div>
      </Container>
    </>
  )
}
