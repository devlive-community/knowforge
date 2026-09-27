import { MouseEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, formatDate } from '@/lib/api'
import type { PageResult, User } from '@/lib/types'
import { renderAnswer, citationHref, type QAAsk, type QACitation, type QAStatus } from '@/lib/qa'
import { formatTokens } from '@/lib/ai-usage'
import QATrace from '@/components/qa/QATrace'
import { useAskStreams } from '@/lib/qa-stream'
import { Badge, Button, ButtonLink, Loading, Switch, Textarea, Tooltip, useFeedback } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'

interface Props {
  user: User | null
  book: { id: number; slug: string }
  docId?: number
  selection: string
  onClearSelection: () => void
  // onCite 点击出处：阅读页内同章节可直接滚动定位；未提供时按链接跳转
  onCite?: (c: QACitation) => void
  // onShared 发到社区问答后（阅读页据此切到社区 Tab 打开该问题）
  onShared?: (questionId: number) => void
  loginHref: string
}

// QAAskPanel AI 问答：基于本书内容作答并标注出处（标准检索 / Agent 多步检索），支持划词提问与发到社区。
export default function QAAskPanel({ user, book, docId, selection, onClearSelection, onCite, onShared, loginHref }: Props) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [status, setStatus] = useState<QAStatus | null>(null)
  const [asks, setAsks] = useState<QAAsk[]>([])
  const [loading, setLoading] = useState(true)
  const [question, setQuestion] = useState('')
  const [agent, setAgent] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [canceling, setCanceling] = useState<number | null>(null)
  const [sharing, setSharing] = useState<number | null>(null)
  const [reindexing, setReindexing] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const st = await api<QAStatus>(`/qa/books/${book.id}/status`)
      setStatus(st)
      if (user) {
        const page = await api<PageResult<QAAsk>>(`/qa/books/${book.id}/asks`, { params: { page_size: 20 } })
        setAsks([...page.items].reverse())
      }
    } catch (e) {
      showToast({ title: t('qa.ask.loadFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setLoading(false)
    }
  }, [book.id, user, showToast, t])

  useEffect(() => { void load() }, [load])
  useEffect(() => { bottomRef.current?.scrollIntoView({ block: 'end' }) }, [asks.length])

  // 进行中的问答在后台生成：经 SSE 实时接收调用链与结果；结束后刷新今日额度
  useAskStreams(asks, (id, next) => setAsks((list) => list.map((a) => a.id === id ? next(a) : a)),
    () => { api<QAStatus>(`/qa/books/${book.id}/status`).then(setStatus).catch(() => {}) })
  const runningKey = asks.filter((a) => a.status === 'running').map((a) => a.id).join(',')
  const anyRunning = runningKey !== ''
  useEffect(() => { if (selection) inputRef.current?.focus() }, [selection])

  const left = (used: number, limit: number) => (limit < 0 ? Infinity : Math.max(0, limit - used))
  const quotaLeft = status?.quota ? left(status.quota.used, status.quota.limit) : 0
  const agentLeft = status?.quota ? left(status.quota.agent_used, status.quota.agent_limit) : 0
  const useAgent = agent && Boolean(status?.agent_available) && agentLeft > 0

  async function ask() {
    const q = question.trim()
    if ((!q && !selection) || submitting || anyRunning) return
    setSubmitting(true)
    try {
      const created = await api<QAAsk>(`/qa/books/${book.id}/ask`, { method: 'POST', body: {
        question: q, selection, doc_id: docId || 0, mode: useAgent ? 'agent' : 'rag',
      } })
      setAsks((list) => [...list, created])
      setQuestion('')
      onClearSelection()
      setStatus((s) => s && s.quota ? { ...s, quota: { ...s.quota, used: s.quota.used + 1, agent_used: s.quota.agent_used + (created.mode === 'agent' ? 1 : 0) } } : s)
    } catch (e) {
      showToast({ title: t('qa.ask.failed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSubmitting(false)
    }
  }

  async function cancel(item: QAAsk) {
    setCanceling(item.id)
    try {
      await api(`/qa/asks/${item.id}/cancel`, { method: 'POST' })
    } catch (e) {
      showToast({ title: t('qa.ask.cancelFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setCanceling(null)
    }
  }

  async function share(item: QAAsk) {
    setSharing(item.id)
    try {
      const q = await api<{ id: number }>(`/qa/books/${book.id}/questions`, { method: 'POST', body: {
        title: item.question.slice(0, 200), ask_id: item.id,
      } })
      showToast({ message: t('qa.ask.shared'), tone: 'success' })
      onShared?.(q.id)
    } catch (e) {
      showToast({ title: t('qa.ask.shareFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSharing(null)
    }
  }

  async function reindex() {
    setReindexing(true)
    try {
      const index = await api<QAStatus['index']>(`/qa/books/${book.id}/reindex`, { method: 'POST' })
      setStatus((s) => s && { ...s, index })
      showToast({ message: t('qa.ask.reindexed'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('qa.ask.reindexFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setReindexing(false)
    }
  }

  function citeClick(event: MouseEvent<HTMLElement>, item: QAAsk) {
    const link = (event.target as HTMLElement).closest('a[data-qa-cite]')
    if (!link || !onCite) return
    const c = item.citations.find((x) => String(x.n) === link.getAttribute('data-qa-cite'))
    if (!c) return
    event.preventDefault()
    onCite(c)
  }

  if (loading) return <Loading className="py-16" label={t('qa.ask.loading')} />
  if (!status?.ai_available) {
    return <div className="m-4 rounded-xl border border-dashed border-slate-300 px-4 py-10 text-center text-sm text-slate-500">{t('qa.ask.unavailable')}</div>
  }
  if (!user) {
    return (
      <div className="m-4 rounded-xl border border-slate-200 bg-slate-50 px-4 py-10 text-center text-sm text-slate-500">
        <p>{t('qa.ask.loginHint')}</p>
        <ButtonLink href={loginHref} size="sm" className="mt-3">{t('qa.ask.login')}</ButtonLink>
      </div>
    )
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-slate-100 px-4 py-2 text-xs text-slate-500">
        <Badge tone={status.vector_search ? 'emerald' : 'slate'}>{t(status.vector_search ? 'qa.ask.hybridSearch' : 'qa.ask.keywordSearch')}</Badge>
        {status.quota && (
          <span>{status.quota.limit < 0 ? t('qa.ask.quotaUnlimited') : t('qa.ask.quota', { used: status.quota.used, limit: status.quota.limit })}</span>
        )}
        {status.quota && status.agent_available && status.quota.agent_limit >= 0 && (
          <span>· {t('qa.ask.agentQuota', { used: status.quota.agent_used, limit: status.quota.agent_limit })}</span>
        )}
        {status.insights_share && (
          <Tooltip content={t('qa.ask.insightsSharedHint')}>
            <span className="flex items-center gap-1 text-slate-400"><i className="fa-solid fa-user-shield" aria-hidden="true" />{t('qa.ask.insightsShared')}</span>
          </Tooltip>
        )}
        {status.can_reindex && (
          <Tooltip content={t('qa.ask.reindexHint')}>
            <Button size="sm" variant="ghost" className="ml-auto" loading={reindexing} onClick={() => void reindex()}>
              <i className="fa-solid fa-rotate" aria-hidden="true" />{t('qa.ask.reindex')}
            </Button>
          </Tooltip>
        )}
      </div>

      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-4 py-4">
        {asks.length === 0 && (
          <div className="rounded-xl bg-slate-50 px-4 py-6 text-sm leading-6 text-slate-500">
            <p className="font-medium text-slate-700">{t('qa.ask.introTitle')}</p>
            <p className="mt-1">{t('qa.ask.intro')}</p>
          </div>
        )}
        {asks.map((item) => (
          <AskItem key={item.id} item={item} bookSlug={book.slug} sharing={sharing === item.id} shareDisabled={sharing !== null}
            onShare={() => void share(item)} onClick={(e) => citeClick(e, item)} onCite={onCite}
            canceling={canceling === item.id} onCancel={() => void cancel(item)} />
        ))}
        <div ref={bottomRef} />
      </div>

      <div className="shrink-0 space-y-2 border-t border-slate-200 bg-white px-4 py-3">
        {selection && (
          <div className="flex items-start gap-2 rounded-lg bg-primary-50 px-3 py-2 text-xs text-primary-800">
            <i className="fa-solid fa-quote-left mt-0.5" aria-hidden="true" />
            <span className="line-clamp-3 min-w-0 flex-1 break-words">{selection}</span>
            <button type="button" aria-label={t('qa.ask.clearSelection')} onClick={onClearSelection} className="shrink-0 text-primary-500 hover:text-primary-700">
              <i className="fa-solid fa-xmark" aria-hidden="true" />
            </button>
          </div>
        )}
        <Textarea ref={inputRef} rows={3} value={question} maxLength={1000} disabled={submitting}
          placeholder={selection ? t('qa.ask.placeholderSelection') : t('qa.ask.placeholder')}
          onChange={(e) => setQuestion(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void ask() } }} />
        <div className="flex items-center gap-3">
          {status.agent_available ? (
            <Tooltip content={agentLeft > 0 ? t('qa.ask.agentHint') : t('qa.ask.agentUsedUp')}>
              <label className="flex cursor-pointer items-center gap-2 text-xs text-slate-600">
                <Switch checked={useAgent} onChange={setAgent} ariaLabel={t('qa.ask.agent')} disabled={submitting || agentLeft <= 0} />
                {t('qa.ask.agent')}
              </label>
            </Tooltip>
          ) : status.quota?.agent_limit === 0 && (
            <span className="text-xs text-slate-400">{t('qa.ask.agentLocked')}</span>
          )}
          <Tooltip content={anyRunning ? t('qa.ask.waitRunning') : t('qa.ask.submit')}>
            <Button className="ml-auto" size="sm" loading={submitting} disabled={(!question.trim() && !selection) || quotaLeft <= 0 || anyRunning} onClick={() => void ask()}>
              <i className="fa-solid fa-paper-plane" aria-hidden="true" />{t('qa.ask.submit')}
            </Button>
          </Tooltip>
        </div>
      </div>
    </div>
  )
}

function QuestionBubble({ question, selection }: { question: string; selection: string }) {
  return (
    <div className="ml-8 rounded-2xl rounded-tr-sm bg-primary-500 px-4 py-2.5 text-sm leading-6 text-white">
      {selection && <blockquote className="mb-1.5 line-clamp-3 border-l-2 border-white/60 pl-2 text-xs text-white/85">{selection}</blockquote>}
      <p className="whitespace-pre-wrap break-words">{question}</p>
    </div>
  )
}

function AskItem({ item, bookSlug, sharing, shareDisabled, onShare, onClick, onCite, canceling, onCancel }: {
  item: QAAsk
  bookSlug: string
  sharing: boolean
  shareDisabled: boolean
  onShare: () => void
  onClick: (e: MouseEvent<HTMLElement>) => void
  onCite?: (c: QACitation) => void
  canceling: boolean
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const [showTrace, setShowTrace] = useState(false)
  // 生成中也实时渲染已生成的部分（出处角标在完成后才可点击）
  const html = useMemo(() => item.status === 'done' || (item.status === 'running' && item.answer) ? renderAnswer(item.answer, bookSlug, item.citations) : '', [item.status, item.answer, bookSlug, item.citations])
  const running = item.status === 'running'
  return (
    <div className="space-y-2">
      <QuestionBubble question={item.question} selection={item.selection} />
      <div className="rounded-2xl rounded-tl-sm border border-slate-200 bg-white px-4 py-3">
        {running ? (
          <>
          {item.answer && <div className="markdown-body qa-answer mb-2 text-sm" dangerouslySetInnerHTML={{ __html: html }} />}
          <div className="flex items-center gap-2 text-sm text-slate-500">
            <Loading className="!p-0" label={t(item.mode === 'agent' ? 'qa.ask.thinkingAgent' : 'qa.ask.thinking')} />
            <Button size="sm" variant="ghost" className="ml-auto text-rose-600" loading={canceling} onClick={onCancel}>
              <i className="fa-solid fa-stop" aria-hidden="true" />{t('qa.ask.cancel')}
            </Button>
          </div>
          </>
        ) : item.status === 'done' ? (
          <>
            <div className="markdown-body qa-answer text-sm" onClick={onClick} dangerouslySetInnerHTML={{ __html: html }} />
            {item.citations.length > 0 && <CitationList citations={item.citations} bookSlug={bookSlug} onCite={onCite} />}
          </>
        ) : (
          <p className={`text-sm ${item.status === 'canceled' ? 'text-slate-500' : 'text-rose-600'}`}>
            <i className={`fa-solid ${item.status === 'canceled' ? 'fa-ban' : 'fa-circle-exclamation'} mr-1.5`} aria-hidden="true" />
            {item.status === 'canceled' ? t('qa.ask.canceled') : (item.error || t('qa.ask.failed'))}
          </p>
        )}
        {(running || showTrace) && <div className="mt-3"><QATrace ask={item} /></div>}
        <div className="mt-3 flex flex-wrap items-center gap-2 text-xs text-slate-400">
          {item.mode === 'agent' && <Badge tone="violet">{t('qa.ask.agentSteps', { n: item.steps })}</Badge>}
          <span>{formatDate(item.created_at)}</span>
          {item.calls > 0 && !running && (
            <Tooltip content={t('qa.ask.usageHint', { calls: item.calls, input: item.input_tokens, output: item.output_tokens })}>
              <span>· {t(item.estimated ? 'qa.ask.usageEstimated' : 'qa.ask.usage', { tokens: formatTokens(item.input_tokens + item.output_tokens) })}</span>
            </Tooltip>
          )}
          {!running && (
            <Button size="sm" variant="ghost" onClick={() => setShowTrace(!showTrace)}>
              <i className="fa-solid fa-diagram-project" aria-hidden="true" />{showTrace ? t('qa.trace.hide') : t('qa.trace.show')}
            </Button>
          )}
          {item.status === 'done' && (
            <Tooltip content={t('qa.ask.shareHint')}>
              <Button size="sm" variant="ghost" className="ml-auto" loading={sharing} disabled={shareDisabled} onClick={onShare}>
                <i className="fa-solid fa-people-group" aria-hidden="true" />{t('qa.ask.share')}
              </Button>
            </Tooltip>
          )}
        </div>
      </div>
    </div>
  )
}

// CitationList 出处列表：章节 › 小节 + 片段摘要，点击定位到阅读位置
export function CitationList({ citations, bookSlug, onCite }: { citations: QACitation[]; bookSlug: string; onCite?: (c: QACitation) => void }) {
  const { t } = useTranslation()
  return (
    <div className="mt-3 border-t border-slate-100 pt-2">
      <div className="mb-1.5 text-xs font-medium text-slate-500">{t('qa.citations')}</div>
      <ol className="space-y-1.5">
        {citations.map((c) => (
          <li key={c.n}>
            <a href={citationHref(bookSlug, c)} onClick={(e) => { if (onCite) { e.preventDefault(); onCite(c) } }}
              className="group flex gap-2 rounded-lg px-2 py-1.5 text-xs hover:bg-slate-50">
              <span className="qa-cite shrink-0">{c.n}</span>
              <span className="min-w-0">
                <span className="block truncate font-medium text-slate-700 group-hover:text-primary-600">{c.doc_title}{c.heading ? ` › ${c.heading}` : ''}</span>
                <span className="line-clamp-2 text-slate-400">{c.snippet}</span>
              </span>
            </a>
          </li>
        ))}
      </ol>
    </div>
  )
}
