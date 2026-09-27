import { useEffect, useState } from 'react'
import type { InferGetServerSidePropsType } from 'next'
import Link from 'next/link'
import { useRouter } from 'next/router'
import BookSettingsLayout from '@/components/BookSettingsLayout'
import { requireBookSettingsFeature } from '@/lib/book-settings'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { Badge, EmptyState, Loading, SegmentedTabs } from '@/components/ui'

export const getServerSideProps = requireBookSettingsFeature('qa')

interface ChapterRef { id: number; title: string; slug: string; count: number }
interface Topic { question: string; count: number; asks: number; community: number; gaps: number; samples: string[]; chapters: ChapterRef[]; last_at: string }
interface Insights {
  days: number
  include_asks: boolean
  semantic: boolean
  stats: { ai_asks?: number; askers?: number; gaps?: number; community: number; open: number }
  topics: Topic[]
  gaps: Topic[]
  chapters: ChapterRef[]
  unanswered: { id: number; title: string; created_at: string }[]
}

const RANGES = [7, 30, 90]

// 书籍设置 · 读者问题：读者就本书向 AI 与社区提出的问题（相近问题归类）、书中答不上来的内容缺口、问题最多的章节与未回答的社区提问。
// 时间范围由 URL 承载（?days=7|30|90）。
export default function ReaderQuestionsPage({ book }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const router = useRouter()
  const days = RANGES.includes(Number(router.query.days)) ? Number(router.query.days) : 30
  const [data, setData] = useState<Insights | null>(null)
  const [error, setError] = useState('')
  const base = `/book/settings/${encodeURIComponent(book.slug)}/reader-questions`

  useEffect(() => {
    setData(null)
    setError('')
    api<Insights>(`/qa/books/${book.id}/insights`, { params: { days } }).then(setData).catch((e) => setError((e as Error).message))
  }, [book.id, days])

  const reader = (c: ChapterRef) => `/book/reader/${encodeURIComponent(book.slug)}/${encodeURIComponent(c.slug)}`
  return (
    <BookSettingsLayout book={book} active="reader-questions">
      <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
        <div className="flex flex-wrap items-start gap-3">
          <div className="min-w-0 flex-1">
            <h1 className="text-xl font-bold text-slate-900">{t('qa.insights.heading')}</h1>
            <p className="mt-1 text-sm text-slate-500">{t('qa.insights.subheading')}</p>
          </div>
          <SegmentedTabs size="sm" value={String(days)} ariaLabel={t('qa.insights.range')}
            items={RANGES.map((d) => ({ value: String(d), label: t('qa.insights.days', { n: d }), href: `${base}?days=${d}` }))} />
        </div>
        {error ? <p className="mt-6 text-sm text-rose-600">{error}</p> : !data ? <Loading className="py-10" /> : (
          <div className="mt-6 space-y-8">
            <div className="grid gap-3 sm:grid-cols-4">
              {data.include_asks && <Stat label={t('qa.insights.aiAsks')} value={data.stats.ai_asks ?? 0} sub={t('qa.insights.askers', { n: data.stats.askers ?? 0 })} />}
              {data.include_asks && <Stat label={t('qa.insights.gapCount')} value={data.stats.gaps ?? 0} tone="amber" />}
              <Stat label={t('qa.insights.community')} value={data.stats.community} />
              <Stat label={t('qa.insights.open')} value={data.stats.open} tone={data.stats.open > 0 ? 'rose' : undefined} />
            </div>
            {!data.include_asks && <p className="rounded-lg bg-slate-50 px-3 py-2 text-xs text-slate-500">{t('qa.insights.asksExcluded')}</p>}
            {!data.semantic && <p className="rounded-lg bg-slate-50 px-3 py-2 text-xs text-slate-500">{t('qa.insights.keywordOnly')}</p>}

            {data.include_asks && (
              <section>
                <h2 className="text-sm font-semibold text-slate-900">{t('qa.insights.gapsTitle')}</h2>
                <p className="mt-1 text-xs text-slate-500">{t('qa.insights.gapsHint')}</p>
                {data.gaps.length === 0 ? <p className="mt-3 text-sm text-slate-400">{t('qa.insights.noGaps')}</p> : (
                  <ul className="mt-3 space-y-2">{data.gaps.map((tp, i) => <TopicRow key={i} topic={tp} reader={reader} highlightGaps />)}</ul>
                )}
              </section>
            )}

            <section>
              <h2 className="text-sm font-semibold text-slate-900">{t('qa.insights.topicsTitle')}</h2>
              <p className="mt-1 text-xs text-slate-500">{t('qa.insights.topicsHint')}</p>
              {data.topics.length === 0 ? <div className="mt-3"><EmptyState>{t('qa.insights.noQuestions')}</EmptyState></div> : (
                <ul className="mt-3 space-y-2">{data.topics.map((tp, i) => <TopicRow key={i} topic={tp} reader={reader} />)}</ul>
              )}
            </section>

            <div className="grid gap-6 lg:grid-cols-2">
              <section>
                <h2 className="text-sm font-semibold text-slate-900">{t('qa.insights.chaptersTitle')}</h2>
                {data.chapters.length === 0 ? <p className="mt-3 text-sm text-slate-400">{t('qa.insights.noChapters')}</p> : (
                  <ul className="mt-3 divide-y divide-slate-100 rounded-xl border border-slate-200">
                    {data.chapters.map((c) => (
                      <li key={c.id} className="flex items-center gap-2 px-4 py-2.5 text-sm">
                        <Link href={reader(c)} className="min-w-0 truncate text-slate-700 hover:text-primary-600">{c.title}</Link>
                        <span className="ml-auto shrink-0 tabular-nums text-xs text-slate-400">{t('qa.insights.questionCount', { n: c.count })}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
              <section>
                <h2 className="text-sm font-semibold text-slate-900">{t('qa.insights.unansweredTitle')}</h2>
                {data.unanswered.length === 0 ? <p className="mt-3 text-sm text-slate-400">{t('qa.insights.noUnanswered')}</p> : (
                  <ul className="mt-3 divide-y divide-slate-100 rounded-xl border border-slate-200">
                    {data.unanswered.map((q) => (
                      <li key={q.id} className="flex items-center gap-2 px-4 py-2.5 text-sm">
                        <Link href={`/book/detail/${encodeURIComponent(book.slug)}?tab=qa&qaq=${q.id}`} className="min-w-0 truncate text-slate-700 hover:text-primary-600">{q.title}</Link>
                        <span className="ml-auto shrink-0 text-xs text-slate-400">{formatDate(q.created_at).slice(0, 10)}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            </div>
          </div>
        )}
      </div>
    </BookSettingsLayout>
  )
}

function Stat({ label, value, sub, tone }: { label: string; value: number; sub?: string; tone?: 'amber' | 'rose' }) {
  const color = tone === 'amber' ? 'text-amber-600' : tone === 'rose' ? 'text-rose-600' : 'text-slate-900'
  return (
    <div className="rounded-xl border border-slate-200 px-4 py-3">
      <div className="text-xs text-slate-500">{label}</div>
      <div className={`mt-1 text-xl font-semibold tabular-nums ${color}`}>{value.toLocaleString()}</div>
      {sub && <div className="mt-0.5 text-xs text-slate-400">{sub}</div>}
    </div>
  )
}

function TopicRow({ topic, reader, highlightGaps }: { topic: Topic; reader: (c: ChapterRef) => string; highlightGaps?: boolean }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <li className="rounded-xl border border-slate-200">
      <button type="button" onClick={() => setOpen((v) => !v)} aria-expanded={open} className="flex w-full items-center gap-2 px-4 py-3 text-left text-sm">
        <span className="min-w-0 flex-1 truncate font-medium text-slate-800">{topic.question}</span>
        {highlightGaps ? <Badge tone="amber">{t('qa.insights.gapBadge', { n: topic.gaps })}</Badge> : topic.gaps > 0 && <Badge tone="amber">{t('qa.insights.gapBadge', { n: topic.gaps })}</Badge>}
        <Badge tone="primary">{t('qa.insights.questionCount', { n: topic.count })}</Badge>
        <i className={`fa-solid fa-chevron-down text-xs text-slate-400 transition-transform ${open ? '' : '-rotate-90'}`} aria-hidden="true" />
      </button>
      {open && (
        <div className="space-y-2 border-t border-slate-100 px-4 py-3 text-sm">
          <div className="text-xs text-slate-400">{t('qa.insights.breakdown', { asks: topic.asks, community: topic.community })} · {t('qa.insights.lastAt', { date: formatDate(topic.last_at).slice(0, 10) })}</div>
          <ul className="list-disc space-y-1 pl-5 text-slate-600">{topic.samples.map((s, i) => <li key={i} className="[overflow-wrap:anywhere]">{s}</li>)}</ul>
          {topic.chapters.length > 0 && (
            <div className="flex flex-wrap items-center gap-2 text-xs text-slate-500">
              {t('qa.insights.relatedChapters')}
              {topic.chapters.map((c) => <Link key={c.id} href={reader(c)} className="rounded bg-slate-100 px-2 py-0.5 text-slate-600 hover:text-primary-600">{c.title}</Link>)}
            </div>
          )}
        </div>
      )}
    </li>
  )
}
