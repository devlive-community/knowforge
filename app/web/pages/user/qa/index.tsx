import { useCallback, useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/router'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import Seo from '@/components/Seo'
import { CitationList } from '@/components/qa/QAAskPanel'
import QATrace from '@/components/qa/QATrace'
import { useAskStreams } from '@/lib/qa-stream'
import { api, formatDate } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { renderAnswer, type QAAsk, type QAQuestion, type QAQuota } from '@/lib/qa'
import { aiFeatureLabel, formatTokens, type MyAIUsage } from '@/lib/ai-usage'
import { Badge, Button, Card, EmptyState, Loading, Pagination, SegmentedTabs, Tooltip, useFeedback } from '@/components/ui'
import { useUrlPage } from '@/lib/use-url-page'

type Tab = 'ai' | 'questions'
interface BookBrief { id: number; slug: string; title: string }
interface AskItem { ask: QAAsk; book: BookBrief; book_available: boolean }
interface QuestionItem { question: QAQuestion; book: BookBrief; book_available: boolean }
interface Page<T> { items: T[]; total: number; page: number; page_size: number }

export default function MyQAPage() {
  return <FeatureGate feature="qa"><MyQAInner /></FeatureGate>
}

// 我的问答：今日额度与本月 AI 用量、跨书的 AI 问答记录（含消耗）与社区提问；Tab 由 URL 承载（?tab=questions）
function MyQAInner() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  const router = useRouter()
  const tab: Tab = router.query.tab === 'questions' ? 'questions' : 'ai'
  const [quota, setQuota] = useState<QAQuota | null>(null)
  const [usage, setUsage] = useState<MyAIUsage | null>(null)

  useEffect(() => {
    if (!user) return
    api<QAQuota>('/qa/me/quota').then(setQuota).catch(() => {})
    api<MyAIUsage>('/users/me/ai-usage').then(setUsage).catch(() => {})
  }, [user])

  if (!user) return <Loading className="min-h-[60vh]" />
  const limitText = (used: number, limit: number) => limit < 0 ? t('qa.mine.usedUnlimited', { used }) : t('qa.mine.usedOf', { used, limit })

  return (
    <>
      <Seo siteName={site.site_name || 'KnowForge'} title={t('qa.mine.title')} noindex />
      <Container>
        <div className="py-8">
          <h1 className="text-2xl font-bold text-ink">{t('qa.mine.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('qa.mine.subtitle')}</p>

          <div className="mt-6 grid gap-4 sm:grid-cols-3">
            <UsageCard label={t('qa.mine.todayQuestions')} value={quota ? limitText(quota.used, quota.limit) : '…'} />
            <UsageCard label={t('qa.mine.todayAgent')} value={quota ? (quota.agent_limit === 0 ? t('qa.mine.agentLocked') : limitText(quota.agent_used, quota.agent_limit)) : '…'} />
            <UsageCard label={t('qa.mine.monthTokens')}
              value={usage ? (usage.limit < 0 ? t('qa.mine.tokensUnlimited', { used: formatTokens(usage.used_tokens) }) : t('qa.mine.tokensOf', { used: formatTokens(usage.used_tokens), limit: formatTokens(usage.limit) })) : '…'}
              progress={usage && usage.limit > 0 ? Math.min(100, (usage.used_tokens / usage.limit) * 100) : undefined}
              hint={usage && usage.by_feature.length > 0 ? usage.by_feature.map((f) => `${aiFeatureLabel(t, f.feature)} ${formatTokens(f.tokens)}`).join(' · ') : undefined} />
          </div>
          <p className="mt-2 text-xs text-slate-400">{t('qa.mine.quotaSource')}</p>

          <SegmentedTabs className="mt-6" value={tab} ariaLabel={t('qa.mine.title')} items={[
            { value: 'ai', label: t('qa.mine.tabAI'), href: '/user/qa' },
            { value: 'questions', label: t('qa.mine.tabQuestions'), href: '/user/qa?tab=questions' },
          ]} />
          <div className="mt-5">{tab === 'ai' ? <AskHistory /> : <MyQuestions />}</div>
        </div>
      </Container>
    </>
  )
}

function UsageCard({ label, value, hint, progress }: { label: string; value: string; hint?: string; progress?: number }) {
  return (
    <Card className="p-4">
      <div className="text-sm text-slate-500">{label}</div>
      <div className="mt-1.5 text-lg font-semibold tabular-nums text-slate-900">{value}</div>
      {progress !== undefined && (
        <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-slate-100">
          <div className={`h-full rounded-full ${progress >= 90 ? 'bg-rose-500' : 'bg-primary-500'}`} style={{ width: `${progress}%` }} />
        </div>
      )}
      {hint && <div className="mt-1.5 truncate text-xs text-slate-400">{hint}</div>}
    </Card>
  )
}

function AskHistory() {
  const { t } = useTranslation()
  const { showToast, confirmAction } = useFeedback()
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<Page<AskItem> | null>(null)
  const [deleting, setDeleting] = useState<number | null>(null)

  const load = useCallback(() => {
    api<Page<AskItem>>('/qa/me/asks', { params: { page, page_size: 10 } }).then(setData)
      .catch((e) => showToast({ title: t('qa.mine.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [page, showToast, t])
  useEffect(() => { load() }, [load])
  // 生成中的记录经 SSE 实时更新状态与调用链
  useAskStreams(data?.items.map((i) => i.ask) || [], (id, next) => setData((d) => d && {
    ...d, items: d.items.map((i) => i.ask.id === id ? { ...i, ask: next(i.ask) } : i),
  }))

  async function remove(id: number) {
    if (!await confirmAction({ title: t('qa.mine.deleteTitle'), message: t('qa.mine.deleteConfirm'), confirmLabel: t('common.actions.delete'), danger: true })) return
    setDeleting(id)
    try {
      await api(`/qa/asks/${id}`, { method: 'DELETE' })
      load()
    } catch (e) {
      showToast({ title: t('qa.mine.deleteFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setDeleting(null)
    }
  }

  if (!data) return <Loading className="py-12" />
  if (data.items.length === 0) return <EmptyState>{t('qa.mine.emptyAsks')}</EmptyState>
  return (
    <div className="space-y-3">
      {data.items.map((item) => <AskCard key={item.ask.id} item={item} deleting={deleting === item.ask.id} disabled={deleting !== null} onDelete={() => void remove(item.ask.id)} />)}
      {data.total > data.page_size && <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} />}
    </div>
  )
}

function AskCard({ item, deleting, disabled, onDelete }: { item: AskItem; deleting: boolean; disabled: boolean; onDelete: () => void }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [showTrace, setShowTrace] = useState(false)
  const { ask, book, book_available: available } = item
  const html = useMemo(() => open && available && ask.status === 'done' ? renderAnswer(ask.answer, book.slug, ask.citations) : '', [open, available, ask, book.slug])
  return (
    <Card className="p-4">
      <div className="flex flex-wrap items-center gap-2 text-xs text-slate-400">
        {available ? <Link href={`/book/detail/${encodeURIComponent(book.slug)}?tab=qa`} className="font-medium text-primary-600 hover:text-primary-700">{book.title}</Link>
          : <span>{t('qa.mine.bookUnavailable')}</span>}
        {ask.mode === 'agent' && <Badge tone="violet">{t('qa.ask.agentSteps', { n: ask.steps })}</Badge>}
        {ask.status !== 'done' && <Badge tone={ask.status === 'running' ? 'sky' : ask.status === 'canceled' ? 'slate' : 'rose'}>{t(`qa.mine.status.${ask.status}`)}</Badge>}
        <span>{formatDate(ask.created_at)}</span>
        {ask.calls > 0 && (
          <Tooltip content={t('qa.ask.usageHint', { calls: ask.calls, input: ask.input_tokens, output: ask.output_tokens })}>
            <span>· {t(ask.estimated ? 'qa.ask.usageEstimated' : 'qa.ask.usage', { tokens: formatTokens(ask.input_tokens + ask.output_tokens) })}</span>
          </Tooltip>
        )}
        <Button size="sm" variant="ghost" className="ml-auto text-rose-600" loading={deleting} disabled={disabled} onClick={onDelete}>
          <i className="fa-solid fa-trash" aria-hidden="true" />{t('common.actions.delete')}
        </Button>
      </div>
      <p className="mt-2 break-words font-medium text-slate-900">{ask.question}</p>
      {ask.selection && <blockquote className="mt-1.5 line-clamp-2 border-l-2 border-primary-300 pl-3 text-sm text-slate-500">{ask.selection}</blockquote>}
      {ask.status !== 'done' ? (
        <p className="mt-2 text-sm text-slate-500">{ask.status === 'running' ? t('qa.mine.runningHint') : ask.status === 'canceled' ? t('qa.ask.canceled') : ask.error}</p>
      ) : open && available ? (
        <div className="mt-3 rounded-lg bg-slate-50 px-4 py-3">
          <div className="markdown-body qa-answer text-sm" dangerouslySetInnerHTML={{ __html: html }} />
          {ask.citations.length > 0 && <CitationList citations={ask.citations} bookSlug={book.slug} />}
        </div>
      ) : (
        <p className="mt-2 line-clamp-2 text-sm text-slate-500">{ask.answer}</p>
      )}
      {showTrace && <div className="mt-3"><QATrace ask={ask} /></div>}
      <div className="mt-2 flex gap-4 text-xs font-medium">
        {available && ask.status === 'done' && (
          <button type="button" onClick={() => setOpen(!open)} className="text-primary-600 hover:text-primary-700">
            {open ? t('qa.mine.collapse') : t('qa.mine.expand')}
          </button>
        )}
        <button type="button" onClick={() => setShowTrace(!showTrace)} className="text-primary-600 hover:text-primary-700">
          {showTrace ? t('qa.trace.hide') : t('qa.trace.show')}
        </button>
      </div>
    </Card>
  )
}

function MyQuestions() {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<Page<QuestionItem> | null>(null)
  useEffect(() => {
    api<Page<QuestionItem>>('/qa/me/questions', { params: { page, page_size: 15 } }).then(setData)
      .catch((e) => showToast({ title: t('qa.mine.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [page, showToast, t])
  if (!data) return <Loading className="py-12" />
  if (data.items.length === 0) return <EmptyState>{t('qa.mine.emptyQuestions')}</EmptyState>
  return (
    <div className="space-y-3">
      <ul className="divide-y divide-slate-100 rounded-xl border border-slate-200 bg-white">
        {data.items.map(({ question: q, book, book_available: available }) => (
          <li key={q.id} className="flex items-start gap-3 px-4 py-3">
            <span className={`mt-0.5 flex h-7 min-w-[2.25rem] shrink-0 items-center justify-center rounded-md px-1 text-xs font-semibold ${q.status === 'resolved' ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 text-slate-500'}`}>{q.answer_count}</span>
            <div className="min-w-0 flex-1">
              {available ? (
                <Link href={`/book/detail/${encodeURIComponent(book.slug)}?tab=qa&question=${q.id}`} className="block break-words font-medium text-slate-900 hover:text-primary-600">{q.title}</Link>
              ) : <span className="block break-words font-medium text-slate-500">{q.title}</span>}
              <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-slate-400">
                {q.status === 'resolved' && <Badge tone="emerald">{t('qa.community.resolved')}</Badge>}
                {q.visibility && <Badge tone={q.visibility === 'held' ? 'amber' : 'rose'}>{t(q.visibility === 'held' ? 'qa.community.visibilityHeld' : 'qa.community.visibilityHidden')}</Badge>}
                <span>{available ? book.title : t('qa.mine.bookUnavailable')}</span>
                <span>{formatDate(q.updated_at)}</span>
              </div>
            </div>
          </li>
        ))}
      </ul>
      {data.total > data.page_size && <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} />}
    </div>
  )
}
