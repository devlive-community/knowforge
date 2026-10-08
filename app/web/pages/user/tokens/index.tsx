import { useCallback, useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import Seo from '@/components/Seo'
import Container from '@/components/Container'
import AccountSettingsLayout from '@/components/AccountSettingsLayout'
import { api, formatDate } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, Checkbox, EmptyState, Field, Input, Loading, Modal, Pagination, SegmentedTabs, Select, useFeedback } from '@/components/ui'

interface AccessToken {
  id: number
  name: string
  prefix: string
  scope: 'all' | 'custom' | 'read' | 'write' // read / write 为旧令牌（write 等同全部权限）
  permissions: string[] | null
  book: { id: number; slug: string; title: string } | null
  expires_at: string | null
  last_used_at: string | null
  last_used_ip: string
  revoked_at: string | null
  expired: boolean
  created_at: string
}
interface TokenList { items: AccessToken[]; total: number; page: number; page_size: number; active: number; limit: number }

const PAGE_SIZE = 20

interface PermissionGroup { resource: string; permissions: string[] }

// resourceKey 权限资源名对应的文案键（book-analytics → account.tokens.resource.bookAnalytics）。
const resourceKey = (resource: string) => `account.tokens.resource.${resource.replace(/-(\w)/g, (_, c: string) => c.toUpperCase())}`

const EXPIRY_OPTIONS = ['30', '7', '90', '365', '0']

// usePermissionLabel 权限的显示名：「资源 · 操作」（如「章节 · 创建」）。
function usePermissionLabel() {
  const { t } = useTranslation()
  return useCallback((perm: string) => {
    const [resource, action] = perm.split(':')
    return `${t(resourceKey(resource))} · ${t(`account.tokens.action.${action}`)}`
  }, [t])
}

// 访问令牌：生成个人访问令牌供脚本、CI 调用 API（Authorization: Bearer kf_pat_…）；明文只显示一次，可吊销。
export default function AccessTokensPage() {
  const { site } = useApp()
  const { t } = useTranslation()
  const user = useRequireAuth()
  const { showToast, confirmAction } = useFeedback()
  const permLabel = usePermissionLabel()
  const [data, setData] = useState<TokenList | null>(null)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(false)
  const [creating, setCreating] = useState(false)
  const [created, setCreated] = useState<string>('')
  const [revoking, setRevoking] = useState<number | null>(null)

  const load = useCallback(() => {
    setLoading(true)
    api<TokenList>('/auth/tokens', { params: { page, page_size: PAGE_SIZE } })
      .then((result) => {
        setData(result)
        if (result.page !== page) setPage(result.page)
      })
      .catch((e) => showToast({ title: t('account.tokens.loadFailed'), message: (e as Error).message, tone: 'error' }))
      .finally(() => setLoading(false))
  }, [page, showToast, t])
  useEffect(() => { if (user) load() }, [user, load])

  if (!user) return <Loading className="min-h-[60vh]" label={t('account.common.loadingInfo')} />

  async function revoke(tk: AccessToken) {
    const ok = await confirmAction({ title: t('account.tokens.revokeTitle', { name: tk.name }), message: t('account.tokens.revokeMessage'), confirmLabel: t('account.tokens.revoke'), danger: true })
    if (!ok) return
    setRevoking(tk.id)
    try {
      await api(`/auth/tokens/${tk.id}`, { method: 'DELETE' })
      showToast({ message: t('account.tokens.revoked'), tone: 'success' })
      load()
    } catch (e) {
      showToast({ title: t('account.tokens.revokeFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setRevoking(null)
    }
  }

  const full = !!data && data.limit >= 0 && data.active >= data.limit
  return (
    <>
      <Seo siteName={site.site_name || 'KnowForge'} title={t('account.tokens.seoTitle')} noindex />
      <Container>
        <nav className="flex items-center gap-1.5 py-4 text-sm text-slate-500">
          <Link href="/" className="hover:text-primary-600">{t('account.common.home')}</Link>
          <span className="text-slate-300">/</span>
          <span className="text-slate-900">{t('account.common.settings')}</span>
        </nav>
        <div className="pb-6">
          <h1 className="text-3xl font-bold text-ink">{t('account.common.settings')}</h1>
          <p className="mt-2 text-[15px] text-slate-500">{t('account.tokens.pageSubtitle')}</p>
        </div>

        <AccountSettingsLayout user={user} active="tokens">
          <div className="rounded-2xl border border-slate-200 bg-white shadow-sm">
            <div className="flex flex-wrap items-start justify-between gap-4 p-6 pb-4">
              <div className="min-w-0">
                <h2 className="text-xl font-bold text-slate-900">{t('account.tokens.title')}</h2>
                <p className="mt-1 text-sm text-slate-500">{t('account.tokens.desc')}</p>
                <p className="mt-2 font-mono text-xs text-slate-500">Authorization: Bearer kf_pat_…</p>
              </div>
              <Button className="shrink-0 whitespace-nowrap" disabled={!data || full} onClick={() => setCreating(true)}>
                <i className="fa-solid fa-plus" aria-hidden="true" />{t('account.tokens.create')}
              </Button>
            </div>
            <div className="px-6 pb-6">
              {data && (
                <p className="mb-3 text-xs text-slate-400">
                  {data.limit < 0 ? t('account.tokens.usageUnlimited', { n: data.active }) : t('account.tokens.usage', { n: data.active, limit: data.limit })}
                  {full && ` · ${data.limit === 0 ? t('account.tokens.notAllowed') : t('account.tokens.full')}`}
                </p>
              )}
              {data === null ? <Loading className="py-8" /> : data.items.length === 0 ? <EmptyState>{t('account.tokens.empty')}</EmptyState> : (
                <ul className="divide-y divide-slate-100 rounded-xl border border-slate-200">
                  {data.items.map((tk) => {
                    const inactive = !!tk.revoked_at || tk.expired
                    return (
                      <li key={tk.id} className="flex flex-wrap items-center gap-3 px-4 py-3 text-sm">
                        <div className="min-w-0 flex-1">
                          <div className="flex flex-wrap items-center gap-2">
                            <span className={`font-medium ${inactive ? 'text-slate-400' : 'text-slate-900'}`}>{tk.name}</span>
                            <Badge tone={tk.scope === 'all' || tk.scope === 'write' ? 'amber' : 'sky'}>
                              {tk.scope === 'custom' ? t('account.tokens.scope.customCount', { n: tk.permissions?.length || 0 }) : t(`account.tokens.scope.${tk.scope === 'write' ? 'all' : tk.scope}`)}
                            </Badge>
                            {tk.book && <Badge tone="violet">{t('account.tokens.bookBadge', { title: tk.book.title })}</Badge>}
                            {tk.revoked_at ? <Badge tone="slate">{t('account.tokens.status.revoked')}</Badge> : tk.expired && <Badge tone="slate">{t('account.tokens.status.expired')}</Badge>}
                          </div>
                          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-xs text-slate-400">
                            <code className="font-mono">{tk.prefix}…</code>
                            <span>{t('account.tokens.createdAt', { date: formatDate(tk.created_at) })}</span>
                            <span>{tk.expires_at ? t('account.tokens.expiresAt', { date: formatDate(tk.expires_at) }) : t('account.tokens.neverExpires')}</span>
                            <span>{tk.last_used_at ? t('account.tokens.lastUsed', { date: formatDate(tk.last_used_at), ip: tk.last_used_ip }) : t('account.tokens.neverUsed')}</span>
                          </div>
                          {tk.scope === 'custom' && (tk.permissions?.length || 0) > 0 && (
                            <div className="mt-1.5 flex flex-wrap gap-1">
                              {tk.permissions!.map((p) => <span key={p} className="rounded bg-slate-100 px-1.5 py-0.5 text-[11px] text-slate-500">{permLabel(p)}</span>)}
                            </div>
                          )}
                        </div>
                        {!inactive && (
                          <Button size="sm" variant="ghost" className="shrink-0 whitespace-nowrap text-rose-600 hover:bg-rose-50" loading={revoking === tk.id} onClick={() => void revoke(tk)}>
                            {t('account.tokens.revoke')}
                          </Button>
                        )}
                      </li>
                    )
                  })}
                </ul>
              )}
              {data && data.total > data.page_size && (
                <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} loading={loading} />
              )}
              <p className="mt-4 text-xs leading-5 text-slate-400">{t('account.tokens.rules')}</p>
            </div>
          </div>
        </AccountSettingsLayout>
      </Container>
      {creating && <CreateTokenModal onClose={() => setCreating(false)} onCreated={(token) => { setCreating(false); setCreated(token); if (page === 1) load(); else setPage(1) }} />}
      {created && <CreatedTokenModal token={created} onClose={() => setCreated('')} />}
    </>
  )
}

function CreateTokenModal({ onClose, onCreated }: { onClose: () => void; onCreated: (token: string) => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [name, setName] = useState('')
  const [scope, setScope] = useState<'all' | 'custom'>('custom')
  const [expires, setExpires] = useState('30')
  const [saving, setSaving] = useState(false)
  const [catalog, setCatalog] = useState<PermissionGroup[] | null>(null)
  const [bookScoped, setBookScoped] = useState<Set<string>>(new Set())
  const [books, setBooks] = useState<{ id: number; title: string }[]>([])
  const [bookId, setBookId] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())

  useEffect(() => {
    api<{ groups: PermissionGroup[]; book_scoped: string[] }>('/auth/tokens/permissions').then((d) => { setCatalog(d.groups); setBookScoped(new Set(d.book_scoped)) })
      .catch((e) => { setCatalog([]); showToast({ message: (e as Error).message, tone: 'error' }) })
    api<{ items: { id: number; title: string }[] }>('/books', { params: { mine: 'true', page_size: 100 } })
      .then((d) => setBooks(d.items)).catch(() => setBooks([]))
  }, [showToast])

  // 限定书籍时只能选择书籍相关的权限
  const groups = useMemo(() => {
    if (!catalog || !bookId) return catalog
    return catalog.map((g) => ({ ...g, permissions: g.permissions.filter((p) => bookScoped.has(p)) })).filter((g) => g.permissions.length > 0)
  }, [catalog, bookId, bookScoped])
  const all = useMemo(() => (groups || []).flatMap((g) => g.permissions), [groups])
  useEffect(() => {
    if (bookId) setSelected((cur) => new Set(Array.from(cur).filter((p) => bookScoped.has(p))))
  }, [bookId, bookScoped])
  const toggle = (perms: string[], on: boolean) => setSelected((cur) => {
    const next = new Set(cur)
    perms.forEach((p) => (on ? next.add(p) : next.delete(p)))
    return next
  })
  const preset = (perms: string[]) => setSelected(new Set(perms))

  async function submit() {
    setSaving(true)
    try {
      const body = { name: name.trim(), scope, expires_days: Number(expires), book_id: Number(bookId) || 0, ...(scope === 'custom' ? { permissions: Array.from(selected) } : {}) }
      const r = await api<{ token: string }>('/auth/tokens', { method: 'POST', body })
      onCreated(r.token)
    } catch (e) {
      showToast({ title: t('account.tokens.createFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  const invalid = !name.trim() || (scope === 'custom' && selected.size === 0)
  return (
    <Modal open onClose={onClose} title={t('account.tokens.create')} className="max-w-2xl"
      footer={<><Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button><Button loading={saving} disabled={invalid} onClick={() => void submit()}>{t('account.tokens.generate')}</Button></>}>
      <div className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-[1fr_180px]">
          <Field label={t('account.tokens.name')}>
            <Input value={name} maxLength={100} placeholder={t('account.tokens.namePlaceholder')} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label={t('account.tokens.expiresLabel')}>
            <Select value={expires} onChange={setExpires}
              options={EXPIRY_OPTIONS.map((d) => ({ value: d, label: d === '0' ? t('account.tokens.neverExpires') : t('account.tokens.days', { n: d }) }))} />
          </Field>
        </div>
        <Field label={t('account.tokens.bookLabel')} hint={t(bookId ? 'account.tokens.bookHintOne' : 'account.tokens.bookHintAll')}>
          <Select value={bookId} onChange={setBookId} searchable
            options={[{ value: '', label: t('account.tokens.allBooks') }, ...books.map((b) => ({ value: String(b.id), label: b.title }))]} />
        </Field>
        <Field label={t('account.tokens.scopeLabel')} hint={t(`account.tokens.scopeHint.${scope}`)}>
          <SegmentedTabs fullWidth size="sm" value={scope} ariaLabel={t('account.tokens.scopeLabel')} onChange={(v) => setScope(v as 'all' | 'custom')}
            items={[{ value: 'custom', label: t('account.tokens.scope.custom') }, { value: 'all', label: t('account.tokens.scope.all') }]} />
        </Field>
        {scope === 'custom' && (
          <div className="rounded-xl border border-slate-200">
            <div className="flex flex-wrap items-center gap-2 border-b border-slate-100 px-3 py-2 text-xs">
              <span className="mr-auto text-slate-500">{t('account.tokens.selected', { n: selected.size, total: all.length })}</span>
              <Button size="sm" variant="ghost" onClick={() => preset(all.filter((p) => p.endsWith(':read')))}>{t('account.tokens.presetRead')}</Button>
              <Button size="sm" variant="ghost" onClick={() => preset(all)}>{t('account.tokens.presetAll')}</Button>
              <Button size="sm" variant="ghost" onClick={() => preset([])}>{t('account.tokens.presetNone')}</Button>
            </div>
            {groups === null ? <Loading className="py-8" /> : (
              <ul className="max-h-72 divide-y divide-slate-100 overflow-y-auto">
                {groups.map((g) => {
                  const on = g.permissions.filter((p) => selected.has(p)).length
                  return (
                    <li key={g.resource} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-2.5">
                      <label className="flex w-36 shrink-0 items-center gap-2 text-sm font-medium text-slate-700">
                        <Checkbox checked={on === g.permissions.length} onChange={(v) => toggle(g.permissions, v)} ariaLabel={t(resourceKey(g.resource))} />
                        <span className="truncate">{t(resourceKey(g.resource))}</span>
                      </label>
                      <div className="flex flex-1 flex-wrap gap-x-4 gap-y-1.5">
                        {g.permissions.map((p) => {
                          const label = t(`account.tokens.action.${p.split(':')[1]}`)
                          return (
                            <label key={p} className="flex items-center gap-1.5 text-sm text-slate-600">
                              <Checkbox checked={selected.has(p)} onChange={(v) => toggle([p], v)} ariaLabel={`${t(resourceKey(g.resource))} · ${label}`} />
                              {label}
                            </label>
                          )
                        })}
                      </div>
                    </li>
                  )
                })}
              </ul>
            )}
          </div>
        )}
      </div>
    </Modal>
  )
}

function CreatedTokenModal({ token, onClose }: { token: string; onClose: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  async function copy() {
    try {
      await navigator.clipboard.writeText(token)
      showToast({ message: t('account.tokens.copied'), tone: 'success' })
    } catch {
      showToast({ message: t('account.tokens.copyFailed'), tone: 'error' })
    }
  }
  return (
    <Modal open onClose={onClose} title={t('account.tokens.createdTitle')} footer={<Button onClick={onClose}>{t('account.tokens.done')}</Button>}>
      <p className="rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-800">{t('account.tokens.showOnce')}</p>
      <div className="mt-3 flex items-center gap-2">
        <Input value={token} readOnly aria-label={t('account.tokens.title')} className="min-w-0 flex-1 font-mono text-xs" />
        <Button variant="outline" className="shrink-0 whitespace-nowrap" onClick={() => void copy()}><i className="fa-regular fa-copy" aria-hidden="true" />{t('account.tokens.copy')}</Button>
      </div>
    </Modal>
  )
}
