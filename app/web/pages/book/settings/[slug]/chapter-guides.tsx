import { useCallback, useEffect, useMemo, useState } from 'react'
import type { InferGetServerSidePropsType } from 'next'
import Link from 'next/link'
import BookSettingsLayout from '@/components/BookSettingsLayout'
import { requireBookSettingsFeature } from '@/lib/book-settings'
import { api } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { renderMarkdown } from '@/lib/markdown'
import { Badge, Button, Input, Loading, Modal, Switch, Textarea, useFeedback } from '@/components/ui'
import {
  applyGuide, guideState, useBookGuidesStream,
  type BookGuides, type GuideChapter, type GuideState, type OverviewView,
} from '@/lib/chapter-guide'

export const getServerSideProps = requireBookSettingsFeature('chapter-guide')

const STATE_TONE: Record<GuideState, 'slate' | 'primary' | 'emerald' | 'amber' | 'rose' | 'violet'> = {
  none: 'slate', empty: 'slate', queued: 'primary', generating: 'primary', ready: 'emerald', stale: 'amber', edited: 'violet', failed: 'rose',
}

// 书籍设置 · 章节导读：各章节导读状态（生成/过期/已编辑/失败）、批量生成、编辑、全书概览与自动生成开关；状态经 SSE 实时更新。
export default function BookChapterGuidesPage({ book }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [data, setData] = useState<BookGuides | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<string>('') // 'batch' | 'auto' | 'overview' | 'doc-<id>'
  const [editing, setEditing] = useState<GuideChapter | null>(null)
  const [editingOverview, setEditingOverview] = useState(false)

  const load = useCallback(() => {
    api<BookGuides>(`/chapter-guides/books/${book.id}`).then(setData).catch((e) => setError((e as Error).message))
  }, [book.id])
  useEffect(() => { load() }, [load])
  useBookGuidesStream(book.id, !!data,
    (g) => setData((d) => (d ? applyGuide(d, g) : d)),
    (o) => setData((d) => (d ? { ...d, overview: o } : d)))

  const counts = useMemo(() => {
    const c: Partial<Record<GuideState, number>> = {}
    for (const row of data?.chapters || []) c[guideState(row)] = (c[guideState(row)] || 0) + 1
    return c
  }, [data])
  const pending = (counts.none || 0) + (counts.stale || 0)

  async function run(key: string, fn: () => Promise<void>, failKey: string) {
    setBusy(key)
    try { await fn() } catch (e) {
      showToast({ title: t(failKey), message: (e as Error).message, tone: 'error' })
    } finally { setBusy('') }
  }

  const generate = (body: Record<string, unknown>, key: string) => run(key, async () => {
    const d = await api<{ queued: number }>(`/chapter-guides/books/${book.id}/generate`, { method: 'POST', body })
    showToast({ message: t('chapterGuide.manage.queued', { n: d.queued }), tone: 'success' })
    load()
  }, 'chapterGuide.manage.generateFailed')

  const setAuto = (v: boolean) => run('auto', async () => {
    await api(`/chapter-guides/books/${book.id}/settings`, { method: 'PUT', body: { auto_generate: v } })
    setData((d) => (d ? { ...d, auto_generate: v } : d))
  }, 'chapterGuide.manage.saveFailed')

  const generateOverview = (force: boolean) => run('overview', async () => {
    await api(`/chapter-guides/books/${book.id}/overview/generate`, { method: 'POST', body: { force } })
    load()
  }, 'chapterGuide.manage.generateFailed')

  if (error) return <BookSettingsLayout book={book} active="chapter-guides"><p className="text-sm text-rose-600">{error}</p></BookSettingsLayout>
  return (
    <BookSettingsLayout book={book} active="chapter-guides">
      <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
        <h1 className="text-xl font-bold text-slate-900">{t('chapterGuide.manage.heading')}</h1>
        <p className="mt-1 text-sm text-slate-500">{t('chapterGuide.manage.subheading')}</p>
        {!data ? <Loading className="py-10" /> : (
          <div className="mt-6 space-y-6">
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-xl bg-slate-50 px-4 py-3 text-sm text-slate-600">
              <span className="tabular-nums">{data.quota.limit < 0 ? t('chapterGuide.manage.quotaUnlimited', { used: data.quota.used }) : t('chapterGuide.manage.quota', { used: data.quota.used, limit: data.quota.limit })}</span>
              <span className="text-slate-400">·</span>
              <span>{t(`chapterGuide.manage.bearer.${data.cost_bearer}`)}</span>
              <Link href="/user/ai-usage" className="ml-auto text-xs font-medium text-primary-600 hover:text-primary-700">{t('chapterGuide.manage.viewUsage')}</Link>
            </div>
            {!data.available && <p className="rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-700">{t('chapterGuide.manage.unavailable')}</p>}

            <div className="flex items-start justify-between gap-4 rounded-xl border border-slate-200 p-4">
              <div>
                <div className="text-sm font-medium text-slate-900">{t('chapterGuide.manage.auto')}</div>
                <p className="mt-1 text-xs leading-5 text-slate-500">{t('chapterGuide.manage.autoHint')}</p>
              </div>
              <Switch checked={data.auto_generate} disabled={busy === 'auto'} onChange={(v) => void setAuto(v)} ariaLabel={t('chapterGuide.manage.auto')} />
            </div>

            <OverviewSection overview={data.overview} available={data.available} busy={busy === 'overview'} bookSlug={book.slug}
              onGenerate={(force) => void generateOverview(force)} onEdit={() => setEditingOverview(true)} />

            <section>
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="text-sm font-semibold text-slate-900">{t('chapterGuide.manage.chaptersTitle')}</h2>
                <span className="text-xs text-slate-500">{t('chapterGuide.manage.summary', { ready: (counts.ready || 0) + (counts.edited || 0), stale: counts.stale || 0, none: counts.none || 0 })}</span>
                <Button size="sm" className="ml-auto" loading={busy === 'batch'} disabled={!data.available || pending === 0}
                  onClick={() => void generate({ scope: 'missing' }, 'batch')}>
                  <i className="fa-solid fa-wand-magic-sparkles" aria-hidden="true" />{t('chapterGuide.manage.generateMissing', { n: pending })}
                </Button>
              </div>
              <ul className="mt-3 divide-y divide-slate-100 rounded-xl border border-slate-200">
                {data.chapters.map((row) => {
                  const state = guideState(row)
                  const key = `doc-${row.doc.id}`
                  const working = state === 'queued' || state === 'generating'
                  return (
                    <li key={row.doc.id} className="px-4 py-3" style={{ paddingLeft: `${1 + row.doc.depth * 1.25}rem` }}>
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="min-w-0 truncate text-sm font-medium text-slate-800">{row.doc.title}</span>
                        {row.doc.status !== 'published' && <Badge tone="slate">{t(`book.status.${row.doc.status}`)}</Badge>}
                        <Badge tone={STATE_TONE[state]}>{t(`chapterGuide.state.${state}`)}</Badge>
                        <span className="ml-auto flex gap-1.5">
                          {state !== 'empty' && (
                            <Button size="sm" variant="outline" loading={busy === key} disabled={!data.available || working}
                              onClick={() => void generate({ scope: 'ids', doc_ids: [row.doc.id], force: true }, key)}>
                              {row.guide?.summary ? t('chapterGuide.manage.regenerate') : t('chapterGuide.manage.generate')}
                            </Button>
                          )}
                          {state !== 'empty' && <Button size="sm" variant="ghost" disabled={working} onClick={() => setEditing(row)}>{t('chapterGuide.manage.edit')}</Button>}
                        </span>
                      </div>
                      {row.guide?.error && state === 'failed' && <p className="mt-1 text-xs text-rose-600">{row.guide.error}</p>}
                      {row.guide?.summary && <p className="mt-1 line-clamp-2 text-xs leading-5 text-slate-500">{row.guide.summary}</p>}
                    </li>
                  )
                })}
              </ul>
            </section>
          </div>
        )}
      </div>
      {editing && <GuideEditor row={editing} onClose={() => setEditing(null)} onSaved={(g) => { setData((d) => (d ? applyGuide(d, g) : d)); setEditing(null) }} />}
      {editingOverview && data && (
        <OverviewEditor bookId={book.id} initial={data.overview?.content || ''} onClose={() => setEditingOverview(false)}
          onSaved={(o) => { setData((d) => (d ? { ...d, overview: o } : d)); setEditingOverview(false) }} />
      )}
    </BookSettingsLayout>
  )
}

function OverviewSection({ overview, available, busy, bookSlug, onGenerate, onEdit }: {
  overview: OverviewView | null; available: boolean; busy: boolean; bookSlug: string; onGenerate: (force: boolean) => void; onEdit: () => void
}) {
  const { t } = useTranslation()
  const html = useMemo(() => (overview?.content ? renderMarkdown(overview.content, { bookSlug }) : ''), [overview?.content, bookSlug])
  const working = overview?.status === 'queued' || overview?.status === 'generating'
  return (
    <section className="rounded-xl border border-slate-200 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-sm font-semibold text-slate-900">{t('chapterGuide.overview.title')}</h2>
        {working && <Badge tone="primary">{t(`chapterGuide.state.${overview!.status as 'queued' | 'generating'}`)}</Badge>}
        {overview?.stale && !working && <Badge tone="amber">{t('chapterGuide.state.stale')}</Badge>}
        {overview?.edited && <Badge tone="violet">{t('chapterGuide.state.edited')}</Badge>}
        <span className="ml-auto flex gap-1.5">
          <Button size="sm" variant="outline" loading={busy} disabled={!available || working} onClick={() => onGenerate(true)}>
            {overview?.content ? t('chapterGuide.manage.regenerate') : t('chapterGuide.manage.generate')}
          </Button>
          <Button size="sm" variant="ghost" disabled={working} onClick={onEdit}>{t('chapterGuide.manage.edit')}</Button>
        </span>
      </div>
      <p className="mt-1 text-xs text-slate-500">{t('chapterGuide.manage.overviewHint')}</p>
      {overview?.status === 'failed' && <p className="mt-2 text-xs text-rose-600">{overview.error}</p>}
      {html && <div className="markdown-body mt-3 max-h-72 overflow-y-auto rounded-lg bg-slate-50 px-4 py-3 text-sm" dangerouslySetInnerHTML={{ __html: html }} />}
    </section>
  )
}

function GuideEditor({ row, onClose, onSaved }: { row: GuideChapter; onClose: () => void; onSaved: (g: import('@/lib/chapter-guide').GuideView) => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [summary, setSummary] = useState(row.guide?.summary || '')
  const [points, setPoints] = useState<string[]>(row.guide?.points.length ? row.guide.points : [''])
  const [saving, setSaving] = useState(false)

  async function save() {
    setSaving(true)
    try {
      onSaved(await api(`/chapter-guides/docs/${row.doc.id}`, { method: 'PUT', body: { summary, points } }))
    } catch (e) {
      showToast({ title: t('chapterGuide.manage.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal open onClose={onClose} title={t('chapterGuide.manage.editTitle', { title: row.doc.title })}
      footer={<><Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button><Button loading={saving} disabled={!summary.trim()} onClick={() => void save()}>{t('common.actions.save')}</Button></>}>
      <div className="space-y-4">
        <div>
          <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('chapterGuide.manage.summaryLabel')}</label>
          <Textarea rows={5} maxLength={2000} value={summary} onChange={(e) => setSummary(e.target.value)} />
        </div>
        <div>
          <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('chapterGuide.reader.points')}</label>
          <div className="space-y-2">
            {points.map((p, i) => (
              <div key={i} className="flex items-center gap-2">
                <Input className="min-w-0 flex-1" value={p} maxLength={300} onChange={(e) => setPoints(points.map((x, j) => (j === i ? e.target.value : x)))} />
                <Button size="sm" variant="ghost" className="shrink-0" aria-label={t('chapterGuide.manage.removePoint')} onClick={() => setPoints(points.filter((_, j) => j !== i))}>
                  <i className="fa-solid fa-xmark" aria-hidden="true" />
                </Button>
              </div>
            ))}
            {points.length < 12 && (
              <Button size="sm" variant="outline" onClick={() => setPoints([...points, ''])}><i className="fa-solid fa-plus" aria-hidden="true" />{t('chapterGuide.manage.addPoint')}</Button>
            )}
          </div>
        </div>
        <p className="text-xs text-slate-400">{t('chapterGuide.manage.editHint')}</p>
      </div>
    </Modal>
  )
}

function OverviewEditor({ bookId, initial, onClose, onSaved }: { bookId: number; initial: string; onClose: () => void; onSaved: (o: OverviewView | null) => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [content, setContent] = useState(initial)
  const [saving, setSaving] = useState(false)
  async function save() {
    setSaving(true)
    try {
      const d = await api<{ overview: OverviewView | null }>(`/chapter-guides/books/${bookId}/overview`, { method: 'PUT', body: { content } })
      onSaved(d.overview)
    } catch (e) {
      showToast({ title: t('chapterGuide.manage.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }
  return (
    <Modal open onClose={onClose} title={t('chapterGuide.overview.editTitle')}
      footer={<><Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button><Button loading={saving} onClick={() => void save()}>{t('common.actions.save')}</Button></>}>
      <Textarea rows={14} maxLength={10000} className="font-mono text-sm" value={content} onChange={(e) => setContent(e.target.value)} />
      <p className="mt-2 text-xs text-slate-400">{t('chapterGuide.overview.editHint')}</p>
    </Modal>
  )
}
