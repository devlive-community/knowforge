import { useEffect, useState } from 'react'
import type { InferGetServerSidePropsType } from 'next'
import BookSettingsLayout from '@/components/BookSettingsLayout'
import { requireBookSettingsFeature } from '@/lib/book-settings'
import { api } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { tierOptions, type TierGrant } from '@/lib/paid'
import { Button, Checkbox, EmptyState, Field, Input, Loading, Select, Switch, useFeedback } from '@/components/ui'
import { formatPrice } from '@/lib/commerce'
import { centsFromInput, inputFromCents } from '@/lib/membership'

export const getServerSideProps = requireBookSettingsFeature('paid-content')

interface PaidSettings { enabled: boolean; book_price_cents: number; chapter_price_cents: number; free_chapters: number; preview_percent: number; free_tier: number }
interface DocRow { doc_id: number; title: string; parent_id: number | null; index: number; free: boolean; price_cents: number; effective_free: boolean; effective_price_cents: number }
interface Resp { settings: PaidSettings; docs: DocRow[]; currency: string; max_price_cents: number; commission_percent: number; tier_grants: TierGrant[] }

// 书籍设置 · 付费：整本/章节价格、免费试读章节与比例、内容访问等级免费读，以及逐章单独设置（免费/自定义价格）。
export default function BookPaidSettings({ book }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t, locale } = useTranslation()
  const { showToast } = useFeedback()
  const [data, setData] = useState<Resp | null>(null)
  const [error, setError] = useState('')
  const [form, setForm] = useState({ enabled: false, book: '', chapter: '', freeChapters: 0, preview: 10, freeTier: 0 })
  const [docs, setDocs] = useState<Record<number, { free: boolean; price: string }>>({})
  const [saving, setSaving] = useState(false)

  function apply(r: Resp) {
    setData(r)
    setForm({ enabled: r.settings.enabled, book: inputFromCents(r.settings.book_price_cents), chapter: inputFromCents(r.settings.chapter_price_cents),
      freeChapters: r.settings.free_chapters, preview: r.settings.preview_percent, freeTier: r.settings.free_tier })
    setDocs(Object.fromEntries(r.docs.map((d) => [d.doc_id, { free: d.free, price: inputFromCents(d.price_cents) }])))
  }
  useEffect(() => {
    api<Resp>(`/books/${book.id}/paid-settings`).then(apply).catch((e) => setError((e as Error).message))
  }, [book.id])

  async function save() {
    setSaving(true)
    try {
      apply(await api<Resp>(`/books/${book.id}/paid-settings`, { method: 'PUT', body: {
        enabled: form.enabled, book_price_cents: centsFromInput(form.book), chapter_price_cents: centsFromInput(form.chapter),
        free_chapters: form.freeChapters, preview_percent: form.preview, free_tier: form.freeTier,
        docs: Object.entries(docs).map(([id, d]) => ({ doc_id: Number(id), free: d.free, price_cents: centsFromInput(d.price) })),
      } }))
      showToast({ message: t('paid.settings.saved'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('paid.settings.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally { setSaving(false) }
  }

  const clampInt = (v: string, max: number) => Math.max(0, Math.min(max, Math.round(Number(v)) || 0))
  return (
    <BookSettingsLayout book={book} active="paid">
      <div className="mb-5">
        <h1 className="text-lg font-bold text-slate-900">{t('paid.settings.title')}</h1>
        <p className="mt-1 text-sm text-slate-500">{t('paid.settings.subtitle')}</p>
      </div>
      {error ? <EmptyState>{error}</EmptyState> : !data ? <Loading className="py-16" /> : (
        <div className="max-w-3xl space-y-6">
          <div className="flex items-center justify-between gap-4 rounded-xl border border-slate-200 p-4">
            <div>
              <div className="text-sm font-medium text-slate-900">{t('paid.settings.enable')}</div>
              <p className="mt-1 text-xs leading-5 text-slate-500">{t('paid.settings.enableHint', { n: data.commission_percent })}</p>
            </div>
            <Switch checked={form.enabled} onChange={(v) => setForm({ ...form, enabled: v })} ariaLabel={t('paid.settings.enable')} />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t('paid.settings.bookPrice')} hint={t('paid.settings.bookPriceHint')}>
              <Input type="number" min={0} step="0.01" value={form.book} onChange={(e) => setForm({ ...form, book: e.target.value })} trailing={<span className="text-xs text-slate-400">{data.currency}</span>} />
            </Field>
            <Field label={t('paid.settings.chapterPrice')} hint={t('paid.settings.chapterPriceHint')}>
              <Input type="number" min={0} step="0.01" value={form.chapter} onChange={(e) => setForm({ ...form, chapter: e.target.value })} trailing={<span className="text-xs text-slate-400">{data.currency}</span>} />
            </Field>
            <Field label={t('paid.settings.freeChapters')} hint={t('paid.settings.freeChaptersHint')}>
              <Input type="number" min={0} value={form.freeChapters} onChange={(e) => setForm({ ...form, freeChapters: clampInt(e.target.value, 10000) })} />
            </Field>
            <Field label={t('paid.settings.preview')} hint={t('paid.settings.previewHint')}>
              <Input type="number" min={0} max={50} value={form.preview} onChange={(e) => setForm({ ...form, preview: clampInt(e.target.value, 50) })} trailing={<span className="text-xs text-slate-400">%</span>} />
            </Field>
            <Field label={t('paid.settings.freeTier')} hint={(data.tier_grants || []).some((g) => g.source !== 'base' && g.value > 0) ? t('paid.settings.freeTierHint') : t('paid.settings.freeTierEmpty')}>
              <Select value={String(form.freeTier)} onChange={(v) => setForm({ ...form, freeTier: Number(v) })}
                options={tierOptions(t, locale, data.tier_grants || [], form.freeTier)} />
            </Field>
          </div>

          <div>
            <h2 className="text-sm font-bold text-slate-900">{t('paid.settings.chapters')}</h2>
            <p className="mt-1 text-xs text-slate-400">{t('paid.settings.chaptersHint')}</p>
            {data.docs.length === 0 ? <div className="mt-3"><EmptyState>{t('paid.settings.noChapters')}</EmptyState></div> : (
              <div className="mt-3 divide-y divide-slate-100 rounded-xl border border-slate-200">
                {data.docs.map((d) => {
                  const cur = docs[d.doc_id] || { free: false, price: '' }
                  return (
                    <div key={d.doc_id} className="grid items-center gap-3 px-4 py-2.5 sm:grid-cols-[1fr_auto_9rem_8rem]">
                      <span className={`min-w-0 truncate text-sm text-slate-700 ${d.parent_id ? 'pl-4' : ''}`}>{d.index + 1}. {d.title}</span>
                      <label className="flex items-center gap-1.5 text-xs text-slate-500">
                        <Checkbox checked={cur.free} onChange={(v) => setDocs({ ...docs, [d.doc_id]: { ...cur, free: v } })} ariaLabel={t('paid.settings.free')} />{t('paid.settings.free')}
                      </label>
                      <Input type="number" min={0} step="0.01" value={cur.price} disabled={cur.free} placeholder={t('paid.settings.defaultPrice')} aria-label={t('paid.settings.customPrice')}
                        onChange={(e) => setDocs({ ...docs, [d.doc_id]: { ...cur, price: e.target.value } })} />
                      <span className="text-right text-xs text-slate-400">
                        {d.effective_free ? t('paid.settings.isFree') : d.effective_price_cents > 0 ? formatPrice(d.effective_price_cents, data.currency, locale) : t('paid.settings.bookOnly')}
                      </span>
                    </div>
                  )
                })}
              </div>
            )}
          </div>
          <div className="flex justify-end"><Button loading={saving} onClick={save}>{t('common.actions.save')}</Button></div>
        </div>
      )}
    </BookSettingsLayout>
  )
}
