import { useCallback, useEffect, useMemo, useState } from 'react'
import type { InferGetServerSidePropsType } from 'next'
import Link from 'next/link'
import { useRouter } from 'next/router'
import BookSettingsLayout from '@/components/BookSettingsLayout'
import { requireBookSettingsFeature } from '@/lib/book-settings'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, ButtonLink, EmptyState, Field, Input, Loading, Select, Textarea, useFeedback } from '@/components/ui'
import {
  TRANSLATE_LANGUAGES, progressPercent, useJobListStreams, useTranslateJobStream,
  type GlossaryTerm, type JobState, type TranslateJob, type TranslateOverview,
} from '@/lib/ai-translate'

export const getServerSideProps = requireBookSettingsFeature('book-translations')

const JOB_TONE: Record<string, 'primary' | 'amber' | 'emerald' | 'rose'> = { running: 'primary', paused: 'amber', done: 'emerald', failed: 'rose' }
const ITEM_TONE: Record<string, 'slate' | 'primary' | 'emerald' | 'rose'> = { pending: 'slate', running: 'primary', done: 'emerald', failed: 'rose' }

// 书籍设置 · AI 翻译：用 AI 把整本书翻译为新的语言版本（同一翻译分组），或把原书的新增与修改同步到已有译本。
// 任务详情由 URL 承载（?job=ID），进度经 SSE 实时推送。
export default function BookAITranslatePage({ book }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const router = useRouter()
  const jobId = Number(router.query.job) || 0
  const [overview, setOverview] = useState<TranslateOverview | null>(null)
  const [loadError, setLoadError] = useState('')

  const load = useCallback(() => {
    api<TranslateOverview>(`/books/${book.id}/ai-translate`)
      .then((d) => { setOverview(d); setLoadError('') })
      .catch((e) => setLoadError((e as Error).message))
  }, [book.id])
  useEffect(() => { load() }, [load])
  // 列表中进行中的任务实时更新进度（结束后重新加载概览，译本状态随之更新）
  const updateJob = useCallback((job: TranslateJob) => {
    setOverview((o) => (o ? { ...o, jobs: o.jobs.map((j) => (j.id === job.id ? { ...j, status: job.status, stage: job.stage, done: job.done, failed: job.failed, total: job.total } : j)) } : o))
  }, [])
  useJobListStreams(jobId ? [] : overview?.jobs || [], updateJob, load)

  const base = `/book/settings/${encodeURIComponent(book.slug)}/ai-translate`
  return (
    <BookSettingsLayout book={book} active="ai-translate">
      <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
        <h1 className="text-xl font-bold text-slate-900">{t('bookSettings.aiTranslate.heading')}</h1>
        <p className="mt-1 text-sm text-slate-500">{t('bookSettings.aiTranslate.subheading')}</p>
        {loadError ? <p className="mt-6 text-sm text-rose-600">{loadError}</p> : !overview ? <Loading className="py-10" /> : jobId ? (
          <JobDetail jobId={jobId} overview={overview} backHref={base} onChanged={load} />
        ) : (
          <Overview bookId={book.id} bookTitle={book.title} overview={overview} base={base} onChanged={load} />
        )}
      </div>
    </BookSettingsLayout>
  )
}

function charsLeftText(t: (k: string, p?: Record<string, string | number>) => string, left: number) {
  return left < 0 ? t('bookSettings.aiTranslate.charsUnlimited') : t('bookSettings.aiTranslate.charsLeft', { n: left.toLocaleString() })
}

function Overview({ bookId, bookTitle, overview, base, onChanged }: { bookId: number; bookTitle: string; overview: TranslateOverview; base: string; onChanged: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const router = useRouter()
  const [syncing, setSyncing] = useState<number | null>(null)
  const usable = overview.available && overview.allowed

  async function sync(targetId: number) {
    setSyncing(targetId)
    try {
      const d = await api<JobState>(`/books/${bookId}/ai-translate/jobs`, { method: 'POST', body: { target_book_id: targetId } })
      onChanged()
      void router.push(`${base}?job=${d.job.id}`)
    } catch (e) {
      showToast({ title: t('bookSettings.aiTranslate.syncFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSyncing(null)
    }
  }

  const busyTargets = new Set(overview.jobs.filter((j) => j.status === 'running' || j.status === 'paused').map((j) => j.target_book_id))
  return (
    <div className="mt-6 space-y-6">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-xl bg-slate-50 px-4 py-3 text-sm text-slate-600">
        <span>{t('bookSettings.aiTranslate.sourceStats', { chapters: overview.source.chapters, chars: overview.source.chars.toLocaleString() })}</span>
        <span className="text-slate-400">·</span>
        <span>{charsLeftText(t, overview.chars_left)}</span>
        <Link href="/user/ai-usage" className="ml-auto text-xs font-medium text-primary-600 hover:text-primary-700">{t('bookSettings.aiTranslate.viewUsage')}</Link>
      </div>
      {!overview.available && <p className="rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-700">{t('bookSettings.aiTranslate.unavailable')}</p>}
      {overview.available && !overview.allowed && <p className="rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-700">{t('bookSettings.aiTranslate.notAllowed')}</p>}

      <section>
        <h2 className="text-sm font-semibold text-slate-900">{t('bookSettings.aiTranslate.targetsTitle')}</h2>
        {overview.targets.length === 0 ? (
          <p className="mt-2 text-sm text-slate-400">{t('bookSettings.aiTranslate.noTargets')}</p>
        ) : (
          <ul className="mt-3 space-y-2">
            {overview.targets.map((tg) => {
              const pending = tg.changed + tg.added
              const busy = busyTargets.has(tg.book.id)
              return (
                <li key={tg.book.id} className="flex flex-wrap items-center gap-3 rounded-xl border border-slate-200 px-4 py-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate font-medium text-slate-900">{tg.book.title}</span>
                      <Badge tone="primary">{tg.target_label || tg.book.language}</Badge>
                      <Badge tone={tg.book.status === 'published' ? 'emerald' : 'slate'}>{t(`book.status.${tg.book.status}`)}</Badge>
                    </div>
                    <p className="mt-1 text-xs text-slate-500">
                      {pending === 0 ? t('bookSettings.aiTranslate.upToDate') : t('bookSettings.aiTranslate.pendingSync', { changed: tg.changed, added: tg.added, chars: tg.chars.toLocaleString() })}
                    </p>
                  </div>
                  {tg.last_job && <ButtonLink size="sm" variant="ghost" href={`${base}?job=${tg.last_job.id}`}>{t('bookSettings.aiTranslate.lastJob')}</ButtonLink>}
                  <ButtonLink size="sm" variant="outline" href={`/book/detail/${encodeURIComponent(tg.book.slug)}`}>
                    <i className="fa-solid fa-book" aria-hidden="true" />{t('bookSettings.aiTranslate.viewBook')}
                  </ButtonLink>
                  <ButtonLink size="sm" variant="outline" href={`/book/writer/${encodeURIComponent(tg.book.slug)}`}>{t('bookSettings.aiTranslate.review')}</ButtonLink>
                  <Button size="sm" loading={syncing === tg.book.id} disabled={!usable || pending === 0 || busy} onClick={() => void sync(tg.book.id)}>
                    {busy ? t('bookSettings.aiTranslate.busy') : t('bookSettings.aiTranslate.sync')}
                  </Button>
                </li>
              )
            })}
          </ul>
        )}
      </section>

      <NewTranslation bookId={bookId} bookTitle={bookTitle} overview={overview} disabled={!usable} base={base} onChanged={onChanged} />

      <section>
        <h2 className="text-sm font-semibold text-slate-900">{t('bookSettings.aiTranslate.jobsTitle')}</h2>
        {overview.jobs.length === 0 ? <div className="mt-3"><EmptyState>{t('bookSettings.aiTranslate.noJobs')}</EmptyState></div> : (
          <ul className="mt-3 divide-y divide-slate-100 rounded-xl border border-slate-200">
            {overview.jobs.map((j) => (
              <li key={j.id}>
                <Link href={`${base}?job=${j.id}`} className="flex flex-wrap items-center gap-3 px-4 py-3 text-sm hover:bg-slate-50">
                  <Badge tone={JOB_TONE[j.status] || 'slate'}>{t(`bookSettings.aiTranslate.status.${j.status}`)}</Badge>
                  <span className="font-medium text-slate-800">{t(`bookSettings.aiTranslate.mode.${j.mode}`)} · {j.target_label}</span>
                  <span className="tabular-nums text-slate-500">{t('bookSettings.aiTranslate.progress', { done: j.done, total: j.total })}</span>
                  {j.status === 'running' && j.stage === 'outline' && <span className="text-xs text-slate-400">{t('bookSettings.aiTranslate.stage.outline')}</span>}
                  {j.failed > 0 && <span className="text-rose-600">{t('bookSettings.aiTranslate.failedCount', { n: j.failed })}</span>}
                  <span className="ml-auto text-xs text-slate-400">{formatDate(j.created_at)}</span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

function NewTranslation({ bookId, bookTitle, overview, disabled, base, onChanged }: {
  bookId: number; bookTitle: string; overview: TranslateOverview; disabled: boolean; base: string; onChanged: () => void
}) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const router = useRouter()
  const [lang, setLang] = useState('en')
  const [title, setTitle] = useState('')
  const [slug, setSlug] = useState('')
  const [edited, setEdited] = useState({ title: false, slug: false }) // 作者改过的字段不再被预设覆盖
  const [instructions, setInstructions] = useState('')
  const [terms, setTerms] = useState<GlossaryTerm[] | null>(null)
  const [savingTerms, setSavingTerms] = useState(false)
  const [starting, setStarting] = useState(false)
  const label = TRANSLATE_LANGUAGES.find((l) => l.code === lang)?.label || lang

  // 按插件管理页的预设预填书名与访问路径（切换语言时更新未手动修改的字段）
  useEffect(() => {
    let active = true
    api<{ title: string; slug: string }>(`/books/${bookId}/ai-translate/preset`, { params: { lang, label } })
      .then((p) => {
        if (!active) return
        setTitle((v) => (edited.title ? v : p.title))
        setSlug((v) => (edited.slug ? v : p.slug))
      })
      .catch(() => { /* 忽略：保留当前输入 */ })
    return () => { active = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在切换语言时预填，edited 变化不应重新请求
  }, [bookId, lang, label])

  useEffect(() => {
    setTerms(null)
    api<{ terms: GlossaryTerm[] }>(`/books/${bookId}/ai-translate/glossary`, { params: { lang } })
      .then((d) => setTerms(d.terms.map((x) => ({ source: x.source, target: x.target }))))
      .catch(() => setTerms([]))
  }, [bookId, lang])

  async function saveTerms() {
    setSavingTerms(true)
    try {
      const d = await api<{ terms: GlossaryTerm[] }>(`/books/${bookId}/ai-translate/glossary`, { method: 'PUT', body: { lang, terms: terms || [] } })
      setTerms(d.terms.map((x) => ({ source: x.source, target: x.target })))
      showToast({ message: t('bookSettings.aiTranslate.glossarySaved'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('bookSettings.aiTranslate.glossarySaveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSavingTerms(false)
    }
  }

  async function start() {
    setStarting(true)
    try {
      const d = await api<JobState>(`/books/${bookId}/ai-translate/jobs`, {
        method: 'POST', body: { target_lang: lang, target_label: label, title: title.trim(), slug: slug.trim(), instructions: instructions.trim() },
      })
      onChanged()
      void router.push(`${base}?job=${d.job.id}`)
    } catch (e) {
      showToast({ title: t('bookSettings.aiTranslate.startFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setStarting(false)
    }
  }

  const overQuota = overview.chars_left >= 0 && overview.chars_left < overview.source.chars
  return (
    <section className="rounded-xl border border-slate-200 p-4">
      <h2 className="text-sm font-semibold text-slate-900">{t('bookSettings.aiTranslate.newTitle')}</h2>
      <p className="mt-1 text-xs text-slate-500">{t('bookSettings.aiTranslate.newHint')}</p>
      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        <Field label={t('bookSettings.aiTranslate.language')}>
          <Select value={lang} onChange={setLang} searchable options={TRANSLATE_LANGUAGES.map((l) => ({ value: l.code, label: l.label }))} />
        </Field>
        <Field label={t('bookSettings.aiTranslate.bookTitle')} hint={t('bookSettings.aiTranslate.bookTitleHint')}>
          <Input value={title} maxLength={255} onChange={(e) => { setTitle(e.target.value); setEdited((x) => ({ ...x, title: true })) }} placeholder={`${bookTitle}（${label}）`} />
        </Field>
        <div className="sm:col-span-2">
          <Field label={t('bookSettings.aiTranslate.slug')} hint={t('bookSettings.aiTranslate.slugHint')}>
            <div className="flex items-center gap-2">
              <span className="shrink-0 font-mono text-xs text-slate-400">/book/detail/</span>
              <Input className="min-w-0 flex-1" value={slug} maxLength={198} aria-label={t('bookSettings.aiTranslate.slug')}
                onChange={(e) => { setSlug(e.target.value.toLowerCase()); setEdited((x) => ({ ...x, slug: true })) }} placeholder={t('bookSettings.aiTranslate.slugPlaceholder')} />
            </div>
          </Field>
        </div>
        <div className="sm:col-span-2">
          <Field label={t('bookSettings.aiTranslate.instructions')} hint={t('bookSettings.aiTranslate.instructionsHint')}>
            <Textarea rows={2} maxLength={1000} value={instructions} onChange={(e) => setInstructions(e.target.value)} placeholder={t('bookSettings.aiTranslate.instructionsPlaceholder')} />
          </Field>
        </div>
      </div>

      <div className="mt-4">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium text-slate-700">{t('bookSettings.aiTranslate.glossary', { lang: label })}</span>
          <span className="text-xs text-slate-400">{t('bookSettings.aiTranslate.glossaryHint')}</span>
        </div>
        {terms === null ? <Loading className="py-4" /> : (
          <div className="mt-2 space-y-2">
            {terms.map((term, i) => (
              <div key={i} className="flex items-center gap-2">
                <Input value={term.source} maxLength={200} placeholder={t('bookSettings.aiTranslate.termSource')}
                  onChange={(e) => setTerms(terms.map((x, j) => (j === i ? { ...x, source: e.target.value } : x)))} />
                <i className="fa-solid fa-arrow-right text-xs text-slate-300" aria-hidden="true" />
                <Input value={term.target} maxLength={200} placeholder={t('bookSettings.aiTranslate.termTarget', { lang: label })}
                  onChange={(e) => setTerms(terms.map((x, j) => (j === i ? { ...x, target: e.target.value } : x)))} />
                <Button size="sm" variant="ghost" aria-label={t('bookSettings.aiTranslate.removeTerm')} onClick={() => setTerms(terms.filter((_, j) => j !== i))}>
                  <i className="fa-solid fa-xmark" aria-hidden="true" />
                </Button>
              </div>
            ))}
            <div className="flex gap-2">
              <Button size="sm" variant="outline" onClick={() => setTerms([...terms, { source: '', target: '' }])}>
                <i className="fa-solid fa-plus" aria-hidden="true" />{t('bookSettings.aiTranslate.addTerm')}
              </Button>
              <Button size="sm" variant="outline" loading={savingTerms} onClick={() => void saveTerms()}>{t('bookSettings.aiTranslate.saveGlossary')}</Button>
            </div>
          </div>
        )}
      </div>

      <div className="mt-5 flex flex-wrap items-center gap-3 border-t border-slate-100 pt-4">
        <p className={`text-xs ${overQuota ? 'text-rose-600' : 'text-slate-500'}`}>
          {t('bookSettings.aiTranslate.estimate', { chapters: overview.source.chapters, chars: overview.source.chars.toLocaleString() })}
          {overQuota && ` ${t('bookSettings.aiTranslate.overQuota')}`}
        </p>
        <Button className="ml-auto" loading={starting} disabled={disabled || overview.source.chapters === 0 || overQuota} onClick={() => void start()}>
          <i className="fa-solid fa-language" aria-hidden="true" />{t('bookSettings.aiTranslate.start')}
        </Button>
      </div>
    </section>
  )
}

function JobDetail({ jobId, overview, backHref, onChanged }: { jobId: number; overview: TranslateOverview; backHref: string; onChanged: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [state, setState] = useState<JobState | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<'' | 'pause' | 'resume' | 'retry'>('')

  const load = useCallback(() => {
    api<JobState>(`/ai-translate/jobs/${jobId}`).then(setState).catch((e) => setError((e as Error).message))
  }, [jobId])
  useEffect(() => { load() }, [load])
  useTranslateJobStream(state, (next) => setState((s) => (s ? next(s) : s)), onChanged)

  async function act(action: 'pause' | 'resume' | 'retry') {
    setBusy(action)
    try {
      const d = await api<JobState | { paused: boolean }>(`/ai-translate/jobs/${jobId}/${action}`, { method: 'POST' })
      if ('job' in d) setState(d)
    } catch (e) {
      showToast({ title: t('bookSettings.aiTranslate.actionFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy('')
    }
  }

  const target = useMemo(() => overview.targets.find((tg) => tg.book.id === state?.job.target_book_id), [overview.targets, state?.job.target_book_id])
  if (error) return <p className="mt-6 text-sm text-rose-600">{error}</p>
  if (!state) return <Loading className="py-10" />
  const { job, items, current } = state
  const running = job.status === 'running'
  const currentItem = current ? items.find((it) => it.id === current.item_id) : undefined
  const pct = progressPercent(job)

  return (
    <div className="mt-6 space-y-5">
      <Link href={backHref} className="inline-flex items-center gap-1.5 text-sm text-slate-500 hover:text-primary-600">
        <i className="fa-solid fa-arrow-left text-xs" aria-hidden="true" />{t('bookSettings.aiTranslate.back')}
      </Link>
      <div className="rounded-xl border border-slate-200 p-4">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-semibold text-slate-900">{t(`bookSettings.aiTranslate.mode.${job.mode}`)} · {job.target_label}</span>
          <Badge tone={JOB_TONE[job.status] || 'slate'}>{t(`bookSettings.aiTranslate.status.${job.status}`)}</Badge>
          {running && <span className="text-xs text-slate-500">{t(`bookSettings.aiTranslate.stage.${job.stage}`)}</span>}
          <span className="ml-auto flex flex-wrap gap-2">
            {running && <Button size="sm" variant="outline" loading={busy === 'pause'} onClick={() => void act('pause')}>{t('bookSettings.aiTranslate.pause')}</Button>}
            {job.status === 'paused' && <Button size="sm" loading={busy === 'resume'} onClick={() => void act('resume')}>{t('bookSettings.aiTranslate.resume')}</Button>}
            {!running && job.failed > 0 && <Button size="sm" variant="outline" loading={busy === 'retry'} onClick={() => void act('retry')}>{t('bookSettings.aiTranslate.retry', { n: job.failed })}</Button>}
            {target && (
              <ButtonLink size="sm" variant="outline" href={`/book/detail/${encodeURIComponent(target.book.slug)}`}>
                <i className="fa-solid fa-book" aria-hidden="true" />{t('bookSettings.aiTranslate.viewBook')}
              </ButtonLink>
            )}
            {target && <ButtonLink size="sm" variant="outline" href={`/book/writer/${encodeURIComponent(target.book.slug)}`}>{t('bookSettings.aiTranslate.review')}</ButtonLink>}
          </span>
        </div>
        <div className="mt-3 h-2 overflow-hidden rounded-full bg-slate-100">
          <div className={`h-full rounded-full transition-all ${job.failed > 0 ? 'bg-amber-400' : 'bg-primary-500'}`} style={{ width: `${pct}%` }} />
        </div>
        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs tabular-nums text-slate-500">
          <span>{t('bookSettings.aiTranslate.progress', { done: job.done, total: job.total })}</span>
          {job.failed > 0 && <span className="text-rose-600">{t('bookSettings.aiTranslate.failedCount', { n: job.failed })}</span>}
          <span>{t('bookSettings.aiTranslate.usage', { chars: job.chars.toLocaleString(), input: job.input_tokens.toLocaleString(), output: job.output_tokens.toLocaleString() })}</span>
          <span className="ml-auto">{formatDate(job.created_at)}</span>
        </div>
        {job.error && <p className="mt-3 rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-700">{job.error}</p>}
        {job.status === 'done' && <p className="mt-3 text-xs text-slate-500">{t('bookSettings.aiTranslate.reviewHint')}</p>}
      </div>

      {running && current && currentItem && (
        <div className="rounded-xl border border-primary-100 bg-primary-50/30 p-4">
          <div className="text-xs font-medium text-primary-700">{t('bookSettings.aiTranslate.translating', { title: currentItem.target_title || currentItem.title })}</div>
          <div className="mt-2 max-h-64 overflow-y-auto whitespace-pre-wrap break-words text-sm leading-6 text-slate-700">
            {current.text}<span className="ml-0.5 inline-block h-4 w-1.5 animate-pulse bg-primary-400 align-middle" />
          </div>
        </div>
      )}

      <ul className="divide-y divide-slate-100 rounded-xl border border-slate-200">
        {items.map((it) => (
          <li key={it.id} className="flex flex-wrap items-center gap-2 px-4 py-2.5 text-sm" style={{ paddingLeft: `${1 + it.depth * 1.25}rem` }}>
            <Badge tone={ITEM_TONE[it.status] || 'slate'}>{t(`bookSettings.aiTranslate.itemStatus.${it.status}`)}</Badge>
            <span className="min-w-0 truncate text-slate-800">{it.title}</span>
            {it.target_title && it.target_title !== it.title && <span className="min-w-0 truncate text-slate-400">→ {it.target_title}</span>}
            {it.error && <span className="text-xs text-rose-600">{it.error}</span>}
            {it.status === 'done' && (
              <span className="ml-auto text-xs tabular-nums text-slate-400">{t('bookSettings.aiTranslate.itemUsage', { chars: it.chars.toLocaleString(), tokens: (it.input_tokens + it.output_tokens).toLocaleString(), s: (it.duration_ms / 1000).toFixed(1) })}</span>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}

