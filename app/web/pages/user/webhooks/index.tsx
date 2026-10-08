import { useCallback, useEffect, useState } from 'react'
import Link from 'next/link'
import Seo from '@/components/Seo'
import Container from '@/components/Container'
import AccountSettingsLayout from '@/components/AccountSettingsLayout'
import FeatureGate from '@/components/FeatureGate'
import { api, formatDate } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import type { Book, PageResult } from '@/lib/types'
import { Badge, Button, Checkbox, EmptyState, Field, Input, Loading, Modal, Pagination, Select, Switch, useFeedback } from '@/components/ui'

interface Hook {
  id: number
  url: string
  events: string[]
  book_id: number
  active: boolean
  failures: number
  disabled_reason: string
  last_delivery_at: string | null
  last_status: '' | 'success' | 'failed'
  created_at: string
}
interface Delivery {
  id: number
  event: string
  payload: string
  status: 'pending' | 'success' | 'failed'
  attempts: number
  response_status: number
  response_body: string
  error: string
  duration_ms: number
  created_at: string
}
interface HookList { items: Hook[]; events: string[]; limit: number }

const DELIVERY_TONE = { pending: 'amber', success: 'emerald', failed: 'rose' } as const

export default function WebhooksPage() {
  return <FeatureGate feature="webhooks"><WebhooksInner /></FeatureGate>
}

// Webhook：订阅自己书籍上的事件，事件发生时向指定地址投递带签名的 JSON；可测试、查看投递记录并重新投递。
function WebhooksInner() {
  const { site } = useApp()
  const { t } = useTranslation()
  const user = useRequireAuth()
  const { showToast, confirmAction } = useFeedback()
  const [data, setData] = useState<HookList | null>(null)
  const [books, setBooks] = useState<Book[]>([])
  const [editing, setEditing] = useState<Hook | 'new' | null>(null)
  const [secret, setSecret] = useState('')
  const [viewing, setViewing] = useState<Hook | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(() => {
    api<HookList>('/webhooks').then(setData).catch((e) => showToast({ title: t('webhooks.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [showToast, t])
  useEffect(() => {
    if (!user) return
    load()
    api<PageResult<Book>>('/books', { params: { scope: 'owned', page_size: 100 } }).then((r) => setBooks(r.items || [])).catch(() => {})
  }, [user, load])

  if (!user) return <Loading className="min-h-[60vh]" label={t('account.common.loadingInfo')} />

  async function run(key: string, fn: () => Promise<void>) {
    setBusy(key)
    try { await fn() } catch (e) { showToast({ title: t('webhooks.actionFailed'), message: (e as Error).message, tone: 'error' }) } finally { setBusy(null) }
  }
  const test = (h: Hook) => run(`test:${h.id}`, async () => {
    await api(`/webhooks/${h.id}/test`, { method: 'POST' })
    showToast({ message: t('webhooks.testSent'), tone: 'success' })
  })
  const toggle = (h: Hook) => run(`toggle:${h.id}`, async () => {
    await api(`/webhooks/${h.id}`, { method: 'PUT', body: { active: !h.active } })
    load()
  })
  const rotate = (h: Hook) => run(`secret:${h.id}`, async () => {
    const ok = await confirmAction({ title: t('webhooks.rotateTitle'), message: t('webhooks.rotateMessage'), confirmLabel: t('webhooks.rotate') })
    if (!ok) return
    const r = await api<{ secret: string }>(`/webhooks/${h.id}/secret`, { method: 'POST' })
    setSecret(r.secret)
  })
  const remove = (h: Hook) => run(`delete:${h.id}`, async () => {
    const ok = await confirmAction({ title: t('webhooks.deleteTitle'), message: t('webhooks.deleteMessage', { url: h.url }), confirmLabel: t('common.actions.delete'), danger: true })
    if (!ok) return
    await api(`/webhooks/${h.id}`, { method: 'DELETE' })
    load()
  })

  const bookTitle = (id: number) => books.find((b) => b.id === id)?.title || `#${id}`
  const full = !!data && data.limit >= 0 && data.items.length >= data.limit
  return (
    <>
      <Seo siteName={site.site_name || 'KnowForge'} title={t('webhooks.seoTitle')} noindex />
      <Container>
        <nav className="flex items-center gap-1.5 py-4 text-sm text-slate-500">
          <Link href="/" className="hover:text-primary-600">{t('account.common.home')}</Link>
          <span className="text-slate-300">/</span>
          <span className="text-slate-900">{t('account.common.settings')}</span>
        </nav>
        <div className="pb-6">
          <h1 className="text-3xl font-bold text-ink">{t('account.common.settings')}</h1>
          <p className="mt-2 text-[15px] text-slate-500">{t('webhooks.pageSubtitle')}</p>
        </div>
        <AccountSettingsLayout user={user} active="webhooks">
          <div className="rounded-2xl border border-slate-200 bg-white shadow-sm">
            <div className="flex flex-wrap items-start justify-between gap-4 p-6 pb-4">
              <div className="min-w-0">
                <h2 className="text-xl font-bold text-slate-900">{t('webhooks.title')}</h2>
                <p className="mt-1 text-sm text-slate-500">{t('webhooks.desc')}</p>
              </div>
              <Button className="shrink-0 whitespace-nowrap" disabled={!data || full} onClick={() => setEditing('new')}>
                <i className="fa-solid fa-plus" aria-hidden="true" />{t('webhooks.create')}
              </Button>
            </div>
            <div className="px-6 pb-6">
              {data && data.limit >= 0 && <p className="mb-3 text-xs text-slate-400">{t('webhooks.usage', { n: data.items.length, limit: data.limit })}{full ? ` · ${t('webhooks.full')}` : ''}</p>}
              {data === null ? <Loading className="py-8" /> : data.items.length === 0 ? <EmptyState>{t('webhooks.empty')}</EmptyState> : (
                <ul className="space-y-3">
                  {data.items.map((h) => (
                    <li key={h.id} className="rounded-xl border border-slate-200 p-4 text-sm">
                      <div className="flex flex-wrap items-center gap-2">
                        <code className="min-w-0 flex-1 truncate font-mono text-slate-800">{h.url}</code>
                        {!h.active ? <Badge tone="slate">{t('webhooks.status.inactive')}</Badge> : h.last_status === 'failed' ? <Badge tone="rose">{t('webhooks.status.failing')}</Badge> : <Badge tone="emerald">{t('webhooks.status.active')}</Badge>}
                        <Switch checked={h.active} disabled={busy === `toggle:${h.id}`} onChange={() => void toggle(h)} ariaLabel={t('webhooks.activeLabel')} />
                      </div>
                      <div className="mt-2 flex flex-wrap gap-1.5">
                        {h.events.map((e) => <Badge key={e} tone="sky">{t(`webhooks.event.${e}`)}</Badge>)}
                        <Badge tone="slate">{h.book_id ? t('webhooks.scopeBook', { title: bookTitle(h.book_id) }) : t('webhooks.scopeAll')}</Badge>
                      </div>
                      {h.disabled_reason && <p className="mt-2 text-xs text-rose-600">{t('webhooks.autoDisabled', { reason: h.disabled_reason })}</p>}
                      <div className="mt-3 flex flex-wrap items-center gap-2">
                        <span className="mr-auto text-xs text-slate-400">{h.last_delivery_at ? t('webhooks.lastDelivery', { date: formatDate(h.last_delivery_at) }) : t('webhooks.neverDelivered')}</span>
                        <Button size="sm" variant="outline" className="whitespace-nowrap" loading={busy === `test:${h.id}`} onClick={() => void test(h)}>{t('webhooks.test')}</Button>
                        <Button size="sm" variant="outline" className="whitespace-nowrap" onClick={() => setViewing(h)}>{t('webhooks.deliveries')}</Button>
                        <Button size="sm" variant="ghost" className="whitespace-nowrap" onClick={() => setEditing(h)}>{t('common.actions.edit')}</Button>
                        <Button size="sm" variant="ghost" className="whitespace-nowrap" loading={busy === `secret:${h.id}`} onClick={() => void rotate(h)}>{t('webhooks.rotate')}</Button>
                        <Button size="sm" variant="ghost" className="whitespace-nowrap text-rose-600 hover:bg-rose-50" loading={busy === `delete:${h.id}`} onClick={() => void remove(h)}>{t('common.actions.delete')}</Button>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
              <p className="mt-4 text-xs leading-5 text-slate-400">{t('webhooks.signatureHint')}</p>
            </div>
          </div>
        </AccountSettingsLayout>
      </Container>
      {editing && data && (
        <HookModal hook={editing === 'new' ? null : editing} events={data.events} books={books} onClose={() => setEditing(null)}
          onSaved={(s) => { setEditing(null); if (s) setSecret(s); load() }} />
      )}
      {secret && <SecretModal secret={secret} onClose={() => setSecret('')} />}
      {viewing && <DeliveriesModal hook={viewing} onClose={() => { setViewing(null); load() }} />}
    </>
  )
}

function HookModal({ hook, events, books, onClose, onSaved }: { hook: Hook | null; events: string[]; books: Book[]; onClose: () => void; onSaved: (secret?: string) => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [url, setUrl] = useState(hook?.url || '')
  const [selected, setSelected] = useState<string[]>(hook?.events || ['chapter.published'])
  const [bookID, setBookID] = useState(String(hook?.book_id || 0))
  const [saving, setSaving] = useState(false)

  async function submit() {
    setSaving(true)
    try {
      const body = { url: url.trim(), events: selected, book_id: Number(bookID) }
      if (hook) {
        await api(`/webhooks/${hook.id}`, { method: 'PUT', body })
        onSaved()
      } else {
        const r = await api<{ secret: string }>('/webhooks', { method: 'POST', body })
        onSaved(r.secret)
      }
    } catch (e) {
      showToast({ title: t('webhooks.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal open onClose={onClose} title={hook ? t('webhooks.edit') : t('webhooks.create')}
      footer={<><Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button><Button loading={saving} disabled={!url.trim() || selected.length === 0} onClick={() => void submit()}>{t('common.actions.save')}</Button></>}>
      <div className="space-y-4">
        <Field label={t('webhooks.url')} hint={t('webhooks.urlHint')}>
          <Input value={url} maxLength={500} placeholder="https://example.com/knowforge-hook" onChange={(e) => setUrl(e.target.value)} />
        </Field>
        <Field label={t('webhooks.events')}>
          <div className="space-y-2">
            {events.map((e) => (
              <label key={e} className="flex items-start gap-2 text-sm text-slate-700">
                <span className="mt-0.5"><Checkbox checked={selected.includes(e)} onChange={(on) => setSelected((s) => (on ? [...s, e] : s.filter((x) => x !== e)))} ariaLabel={t(`webhooks.event.${e}`)} /></span>
                <span><span className="font-medium">{t(`webhooks.event.${e}`)}</span><code className="ml-2 text-xs text-slate-400">{e}</code></span>
              </label>
            ))}
          </div>
        </Field>
        <Field label={t('webhooks.scope')}>
          <Select value={bookID} onChange={setBookID} options={[{ value: '0', label: t('webhooks.scopeAll') }, ...books.map((b) => ({ value: String(b.id), label: b.title }))]} />
        </Field>
      </div>
    </Modal>
  )
}

function SecretModal({ secret, onClose }: { secret: string; onClose: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  async function copy() {
    try { await navigator.clipboard.writeText(secret); showToast({ message: t('webhooks.secretCopied'), tone: 'success' }) }
    catch { showToast({ message: t('account.tokens.copyFailed'), tone: 'error' }) }
  }
  return (
    <Modal open onClose={onClose} title={t('webhooks.secretTitle')} footer={<Button onClick={onClose}>{t('account.tokens.done')}</Button>}>
      <p className="rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-800">{t('webhooks.secretOnce')}</p>
      <div className="mt-3 flex items-center gap-2">
        <Input value={secret} readOnly aria-label={t('webhooks.secretTitle')} className="min-w-0 flex-1 font-mono text-xs" />
        <Button variant="outline" className="shrink-0 whitespace-nowrap" onClick={() => void copy()}><i className="fa-regular fa-copy" aria-hidden="true" />{t('account.tokens.copy')}</Button>
      </div>
    </Modal>
  )
}

function DeliveriesModal({ hook, onClose }: { hook: Hook; onClose: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [page, setPage] = useState(1)
  const [data, setData] = useState<{ items: Delivery[]; total: number; page: number; page_size: number } | null>(null)
  const [open, setOpen] = useState<number | null>(null)
  const [busy, setBusy] = useState<number | null>(null)

  const load = useCallback(() => {
    api<{ items: Delivery[]; total: number; page: number; page_size: number }>(`/webhooks/${hook.id}/deliveries`, { params: { page, page_size: 20 } })
      .then(setData).catch((e) => showToast({ title: t('webhooks.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [hook.id, page, showToast, t])
  useEffect(() => { load() }, [load])

  async function redeliver(d: Delivery) {
    setBusy(d.id)
    try {
      await api(`/webhooks/deliveries/${d.id}/redeliver`, { method: 'POST' })
      showToast({ message: t('webhooks.redelivered'), tone: 'success' })
      load()
    } catch (e) {
      showToast({ title: t('webhooks.actionFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  return (
    <Modal open onClose={onClose} className="max-w-3xl" title={t('webhooks.deliveriesTitle')}
      footer={<><Button variant="outline" onClick={load}>{t('webhooks.refresh')}</Button><Button onClick={onClose}>{t('common.actions.close')}</Button></>}>
      {data === null ? <Loading className="py-8" /> : data.items.length === 0 ? <EmptyState>{t('webhooks.noDeliveries')}</EmptyState> : (
        <>
          <ul className="divide-y divide-slate-100 rounded-lg border border-slate-200">
            {data.items.map((d) => (
              <li key={d.id} className="px-3 py-2 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge tone={DELIVERY_TONE[d.status]}>{t(`webhooks.deliveryStatus.${d.status}`)}</Badge>
                  <code className="text-xs text-slate-600">{d.event}</code>
                  <span className="text-xs text-slate-400">
                    {d.response_status ? `HTTP ${d.response_status}` : ''}{d.duration_ms ? ` · ${d.duration_ms} ms` : ''} · {t('webhooks.attempts', { n: d.attempts })} · {formatDate(d.created_at)}
                  </span>
                  <span className="ml-auto flex gap-1.5">
                    <Button size="sm" variant="ghost" className="whitespace-nowrap" onClick={() => setOpen(open === d.id ? null : d.id)}>{open === d.id ? t('webhooks.hideDetail') : t('webhooks.showDetail')}</Button>
                    <Button size="sm" variant="outline" className="whitespace-nowrap" loading={busy === d.id} onClick={() => void redeliver(d)}>{t('webhooks.redeliver')}</Button>
                  </span>
                </div>
                {d.error && <p className="mt-1 text-xs text-rose-600">{d.error}</p>}
                {open === d.id && (
                  <div className="mt-2 grid gap-2 md:grid-cols-2">
                    <div><div className="mb-1 text-xs text-slate-500">{t('webhooks.request')}</div><pre className="max-h-60 overflow-auto rounded bg-slate-50 p-2 text-xs">{d.payload}</pre></div>
                    <div><div className="mb-1 text-xs text-slate-500">{t('webhooks.response')}</div><pre className="max-h-60 overflow-auto rounded bg-slate-50 p-2 text-xs">{d.response_body || '—'}</pre></div>
                  </div>
                )}
              </li>
            ))}
          </ul>
          {data.total > data.page_size && <div className="mt-3"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
        </>
      )}
    </Modal>
  )
}
