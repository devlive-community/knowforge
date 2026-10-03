import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, formatDate } from '@/lib/api'
import type { PageResult, User } from '@/lib/types'
import { renderAnswer, type QACitation, type QAQuestion, type QAQuestionDetail, type QAVisibility } from '@/lib/qa'
import ReportButton from '@/components/ReportButton'
import { renderMarkdown } from '@/lib/markdown'
import UserAvatar from '@/components/UserAvatar'
import { CitationList } from '@/components/qa/QAAskPanel'
import { Badge, Button, ButtonLink, Checkbox, EmptyState, Input, Loading, Modal, Pagination, Select, Textarea, useFeedback } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'
import { displayName } from '@/lib/users'
import { useUrlPage } from '@/lib/use-url-page'

type Filter = 'all' | 'open' | 'resolved'

interface Props {
  user: User | null
  book: { id: number; slug: string }
  questionId: number | null
  onOpenQuestion: (id: number | null) => void
  // 阅读页：新提问默认关联当前章节与选中文字
  docId?: number
  selection?: string
  onCite?: (c: QACitation) => void
  loginHref: string
  compact?: boolean
}

// QACommunity 社区问答：本书的公开提问列表与问题详情（回答、采纳、删除）。当前打开的问题由调用方放在 URL 中。
export default function QACommunity(props: Props) {
  return props.questionId ? <QuestionDetail {...props} questionId={props.questionId} /> : <QuestionList {...props} />
}

// VisibilityBadge 本人可见的非公开内容标识（待审核 / 已隐藏）
function VisibilityBadge({ visibility }: { visibility: QAVisibility }) {
  const { t } = useTranslation()
  if (!visibility) return null
  return <Badge tone={visibility === 'held' ? 'amber' : 'rose'}>{t(visibility === 'held' ? 'qa.community.visibilityHeld' : 'qa.community.visibilityHidden')}</Badge>
}



function QuestionList({ user, book, onOpenQuestion, docId, selection, loginHref, compact }: Props) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [filter, setFilter] = useState<Filter>('all')
  const [keyword, setKeyword] = useState('')
  const [query, setQuery] = useState('')
  const [page, setPage] = useUrlPage('qaPage')
  const [data, setData] = useState<PageResult<QAQuestion> | null>(null)
  const [loading, setLoading] = useState(true)
  const [asking, setAsking] = useState(false)
  const [form, setForm] = useState({ title: '', body: '', withSelection: true })
  const [saving, setSaving] = useState(false)
  const pageSize = compact ? 10 : 15

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setData(await api<PageResult<QAQuestion>>(`/qa/books/${book.id}/questions`, { params: { filter, q: query, page, page_size: pageSize } }))
    } catch (e) {
      showToast({ title: t('qa.community.loadFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setLoading(false)
    }
  }, [book.id, filter, query, page, pageSize, showToast, t])

  useEffect(() => { void load() }, [load])

  async function submit() {
    setSaving(true)
    try {
      const created = await api<{ question: QAQuestion; held: boolean }>(`/qa/books/${book.id}/questions`, { method: 'POST', body: {
        title: form.title.trim(), body: form.body.trim(), doc_id: docId || 0,
        selection: form.withSelection && selection ? selection : '',
      } })
      setAsking(false)
      setForm({ title: '', body: '', withSelection: true })
      showToast({ message: t(created.held ? 'qa.community.askedHeld' : 'qa.community.asked'), tone: created.held ? 'info' : 'success' })
      onOpenQuestion(created.question.id)
    } catch (e) {
      showToast({ title: t('qa.community.askFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className={compact ? 'space-y-3 p-4' : 'space-y-4'}>
      <div className="flex flex-wrap items-center gap-2">
        <Select size="sm" className="w-32" value={filter} onChange={(v) => { setFilter(v as Filter); setPage(1) }} options={[
          { value: 'all', label: t('qa.community.filterAll') },
          { value: 'open', label: t('qa.community.filterOpen') },
          { value: 'resolved', label: t('qa.community.filterResolved') },
        ]} />
        <form className="min-w-0 flex-1" onSubmit={(e) => { e.preventDefault(); setQuery(keyword.trim()); setPage(1) }}>
          <Input size="sm" value={keyword} onChange={(e) => setKeyword(e.target.value)} placeholder={t('qa.community.searchPlaceholder')} aria-label={t('qa.community.searchPlaceholder')} />
        </form>
        {user ? (
          <Button size="sm" onClick={() => setAsking(true)}><i className="fa-solid fa-plus" aria-hidden="true" />{t('qa.community.ask')}</Button>
        ) : (
          <ButtonLink size="sm" variant="outline" href={loginHref}>{t('qa.community.loginToAsk')}</ButtonLink>
        )}
      </div>

      {loading && !data ? <Loading className="py-10" label={t('qa.community.loading')} /> : !data || data.items.length === 0 ? (
        <EmptyState>{query || filter !== 'all' ? t('qa.community.noMatch') : t('qa.community.empty')}</EmptyState>
      ) : (
        <ul className="divide-y divide-slate-100 rounded-xl border border-slate-200 bg-white">
          {data.items.map((q) => (
            <li key={q.id}>
              <button type="button" onClick={() => onOpenQuestion(q.id)} className="flex w-full items-start gap-3 px-4 py-3 text-left hover:bg-slate-50">
                <span className={`mt-0.5 flex h-7 min-w-[2.25rem] shrink-0 flex-col items-center justify-center rounded-md px-1 text-xs font-semibold ${q.status === 'resolved' ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 text-slate-500'}`}>
                  {q.answer_count}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block break-words font-medium text-slate-900">{q.title}</span>
                  <span className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-slate-400">
                    <VisibilityBadge visibility={q.visibility} />
                    {q.status === 'resolved' && <Badge tone="emerald">{t('qa.community.resolved')}</Badge>}
                    {q.ai_answer && <Badge tone="violet">{t('qa.community.hasAI')}</Badge>}
                    <span>{displayName(q.user)}</span>
                    <span>{formatDate(q.updated_at)}</span>
                  </span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {data && data.total > pageSize && <Pagination size="sm" page={page} pageSize={pageSize} total={data.total} onChange={setPage} loading={loading} />}

      <Modal open={asking} onClose={() => setAsking(false)} title={t('qa.community.askTitle')}
        footer={<><Button variant="ghost" onClick={() => setAsking(false)}>{t('common.actions.cancel')}</Button><Button loading={saving} disabled={!form.title.trim()} onClick={() => void submit()}>{t('qa.community.submitQuestion')}</Button></>}>
        <div className="space-y-3">
          {selection && (
            <div className="flex items-start gap-2 rounded-lg bg-slate-50 px-3 py-2 text-xs text-slate-600">
              <span className="mt-0.5"><Checkbox checked={form.withSelection} onChange={(v) => setForm({ ...form, withSelection: v })} ariaLabel={t('qa.community.attachSelection')} /></span>
              <span className="min-w-0"><span className="font-medium">{t('qa.community.attachSelection')}</span><span className="mt-0.5 line-clamp-2 block text-slate-400">{selection}</span></span>
            </div>
          )}
          <Input value={form.title} maxLength={200} placeholder={t('qa.community.titlePlaceholder')} onChange={(e) => setForm({ ...form, title: e.target.value })} />
          <Textarea rows={6} value={form.body} maxLength={10000} placeholder={t('qa.community.bodyPlaceholder')} onChange={(e) => setForm({ ...form, body: e.target.value })} />
        </div>
      </Modal>
    </div>
  )
}

function QuestionDetail({ user, book, questionId, onOpenQuestion, onCite, loginHref, compact }: Props & { questionId: number }) {
  const { t } = useTranslation()
  const { showToast, confirmAction } = useFeedback()
  const [detail, setDetail] = useState<QAQuestionDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [answer, setAnswer] = useState('')
  const [posting, setPosting] = useState(false)
  const [busy, setBusy] = useState<string | null>(null) // accept:<id> / delete:<id> / delete-question
  const [showAI, setShowAI] = useState(false)

  // 回调由父组件内联传入，放 ref 避免 load 每次渲染都变化导致反复请求
  const openRef = useRef(onOpenQuestion)
  openRef.current = onOpenQuestion

  const load = useCallback(async () => {
    try {
      setDetail(await api<QAQuestionDetail>(`/qa/questions/${questionId}`))
    } catch (e) {
      showToast({ title: t('qa.community.loadFailed'), message: (e as Error).message, tone: 'error' })
      openRef.current(null)
    } finally {
      setLoading(false)
    }
  }, [questionId, showToast, t])

  useEffect(() => { setLoading(true); void load() }, [load])

  const aiHtml = useMemo(() => detail?.question.ai_answer ? renderAnswer(detail.question.ai_answer, book.slug, detail.question.ai_citations) : '', [detail, book.slug])

  async function postAnswer() {
    setPosting(true)
    try {
      const res = await api<{ held: boolean }>(`/qa/questions/${questionId}/answers`, { method: 'POST', body: { body: answer.trim() } })
      setAnswer('')
      showToast({ message: t(res.held ? 'qa.community.answeredHeld' : 'qa.community.answered'), tone: res.held ? 'info' : 'success' })
      await load()
    } catch (e) {
      showToast({ title: t('qa.community.answerFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setPosting(false)
    }
  }

  async function accept(id: number) {
    setBusy(`accept:${id}`)
    try {
      await api(`/qa/answers/${id}/accept`, { method: 'POST' })
      await load()
    } catch (e) {
      showToast({ title: t('qa.community.acceptFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  async function removeAnswer(id: number) {
    if (!await confirmAction({ title: t('qa.community.deleteAnswerTitle'), message: t('qa.community.deleteAnswerConfirm'), confirmLabel: t('common.actions.delete'), danger: true })) return
    setBusy(`delete:${id}`)
    try {
      await api(`/qa/answers/${id}`, { method: 'DELETE' })
      await load()
    } catch (e) {
      showToast({ title: t('qa.community.deleteFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  async function removeQuestion() {
    if (!await confirmAction({ title: t('qa.community.deleteQuestionTitle'), message: t('qa.community.deleteQuestionConfirm'), confirmLabel: t('common.actions.delete'), danger: true })) return
    setBusy('delete-question')
    try {
      await api(`/qa/questions/${questionId}`, { method: 'DELETE' })
      showToast({ message: t('qa.community.deleted'), tone: 'success' })
      onOpenQuestion(null)
    } catch (e) {
      showToast({ title: t('qa.community.deleteFailed'), message: (e as Error).message, tone: 'error' })
      setBusy(null)
    }
  }

  if (loading || !detail) return <Loading className="py-10" label={t('qa.community.loading')} />
  const q = detail.question

  return (
    <div className={compact ? 'space-y-4 p-4' : 'max-w-3xl space-y-4'}>
      <Button size="sm" variant="ghost" onClick={() => onOpenQuestion(null)}><i className="fa-solid fa-arrow-left" aria-hidden="true" />{t('qa.community.back')}</Button>

      <div className="rounded-xl border border-slate-200 bg-white p-4">
        <div className="flex items-start gap-3">
          <h3 className="min-w-0 flex-1 break-words text-lg font-semibold text-slate-900">{q.title}</h3>
          <VisibilityBadge visibility={q.visibility} />
          <Badge tone={q.status === 'resolved' ? 'emerald' : 'slate'}>{t(q.status === 'resolved' ? 'qa.community.resolved' : 'qa.community.open')}</Badge>
        </div>
        <div className="mt-2 flex items-center gap-2 text-xs text-slate-400">
          <UserAvatar user={q.user} size="h-5 w-5" /><span>{displayName(q.user)}</span><span>{formatDate(q.created_at)}</span>
          {user && user.id !== q.user_id && q.visibility === '' && <span className="ml-auto"><ReportButton targetType="qa_question" targetId={q.id} compact /></span>}
          {detail.can_manage && (
            <Button size="sm" variant="ghost" className="ml-auto text-rose-600" loading={busy === 'delete-question'} disabled={busy !== null} onClick={() => void removeQuestion()}>
              <i className="fa-solid fa-trash" aria-hidden="true" />{t('common.actions.delete')}
            </Button>
          )}
        </div>
        {q.selection && <blockquote className="mt-3 border-l-2 border-primary-300 pl-3 text-sm leading-6 text-slate-500">{q.selection}</blockquote>}
        {q.body && <div className="markdown-body mt-3 text-sm" dangerouslySetInnerHTML={{ __html: renderMarkdown(q.body) }} />}
        {q.ai_answer && (
          <div className="mt-3 rounded-lg bg-violet-50/60 px-3 py-2">
            <button type="button" onClick={() => setShowAI(!showAI)} className="flex w-full items-center gap-2 text-left text-xs font-medium text-violet-700">
              <i className="fa-solid fa-wand-magic-sparkles" aria-hidden="true" />{t('qa.community.aiReference')}
              <i className={`fa-solid fa-chevron-${showAI ? 'up' : 'down'} ml-auto`} aria-hidden="true" />
            </button>
            {showAI && (
              <div className="mt-2">
                <div className="markdown-body qa-answer text-sm" onClick={(e) => {
                  const link = (e.target as HTMLElement).closest('a[data-qa-cite]')
                  const c = link && q.ai_citations.find((x) => String(x.n) === link.getAttribute('data-qa-cite'))
                  if (c && onCite) { e.preventDefault(); onCite(c) }
                }} dangerouslySetInnerHTML={{ __html: aiHtml }} />
                {q.ai_citations.length > 0 && <CitationList citations={q.ai_citations} bookSlug={book.slug} onCite={onCite} />}
              </div>
            )}
          </div>
        )}
      </div>

      <div className="text-sm font-semibold text-slate-700">{t('qa.community.answersCount', { n: detail.answers.length })}</div>
      {detail.answers.length === 0 ? <p className="text-sm text-slate-400">{t('qa.community.noAnswers')}</p> : (
        <ul className="space-y-3">
          {detail.answers.map(({ answer: a, user: au, accepted, is_author, can_delete }) => (
            <li key={a.id} className={`rounded-xl border bg-white p-4 ${accepted ? 'border-emerald-300 ring-1 ring-emerald-100' : 'border-slate-200'}`}>
              <div className="flex flex-wrap items-center gap-2 text-xs text-slate-400">
                <UserAvatar user={au} size="h-5 w-5" /><span className="text-slate-600">{displayName(au)}</span>
                {is_author && <Badge tone="primary">{t('qa.community.author')}</Badge>}
                {accepted && <Badge tone="emerald"><i className="fa-solid fa-check mr-1" aria-hidden="true" />{t('qa.community.accepted')}</Badge>}
                <VisibilityBadge visibility={a.visibility} />
                <span>{formatDate(a.created_at)}</span>
                <span className="ml-auto flex items-center gap-1">
                  {user && user.id !== a.user_id && a.visibility === '' && <ReportButton targetType="qa_answer" targetId={a.id} compact />}
                  {detail.can_accept && a.visibility === '' && (
                    <Button size="sm" variant="ghost" loading={busy === `accept:${a.id}`} disabled={busy !== null} onClick={() => void accept(a.id)}>
                      {accepted ? t('qa.community.unaccept') : t('qa.community.accept')}
                    </Button>
                  )}
                  {can_delete && (
                    <Button size="sm" variant="ghost" className="text-rose-600" loading={busy === `delete:${a.id}`} disabled={busy !== null} onClick={() => void removeAnswer(a.id)}>
                      {t('common.actions.delete')}
                    </Button>
                  )}
                </span>
              </div>
              <div className="markdown-body mt-2 text-sm" dangerouslySetInnerHTML={{ __html: renderMarkdown(a.body) }} />
            </li>
          ))}
        </ul>
      )}

      {user ? (
        <div className="space-y-2">
          <Textarea rows={4} value={answer} maxLength={10000} placeholder={t('qa.community.answerPlaceholder')} onChange={(e) => setAnswer(e.target.value)} />
          <div className="flex justify-end"><Button size="sm" loading={posting} disabled={!answer.trim()} onClick={() => void postAnswer()}>{t('qa.community.submitAnswer')}</Button></div>
        </div>
      ) : (
        <ButtonLink size="sm" variant="outline" href={loginHref}>{t('qa.community.loginToAnswer')}</ButtonLink>
      )}
    </div>
  )
}
