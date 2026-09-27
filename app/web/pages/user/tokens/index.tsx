import { useCallback, useEffect, useState } from 'react'
import Link from 'next/link'
import Seo from '@/components/Seo'
import Container from '@/components/Container'
import AccountSettingsLayout from '@/components/AccountSettingsLayout'
import { api, formatDate } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, EmptyState, Field, Input, Loading, Modal, SegmentedTabs, Select, useFeedback } from '@/components/ui'

interface AccessToken {
  id: number
  name: string
  prefix: string
  scope: 'read' | 'write'
  expires_at: string | null
  last_used_at: string | null
  last_used_ip: string
  revoked_at: string | null
  expired: boolean
  created_at: string
}
interface TokenList { items: AccessToken[]; active: number; limit: number }

const EXPIRY_OPTIONS = ['30', '7', '90', '365', '0']

// 访问令牌：生成个人访问令牌供脚本、CI 调用 API（Authorization: Bearer kf_pat_…）；明文只显示一次，可吊销。
export default function AccessTokensPage() {
  const { site } = useApp()
  const { t } = useTranslation()
  const user = useRequireAuth()
  const { showToast, confirmAction } = useFeedback()
  const [data, setData] = useState<TokenList | null>(null)
  const [creating, setCreating] = useState(false)
  const [created, setCreated] = useState<string>('')
  const [revoking, setRevoking] = useState<number | null>(null)

  const load = useCallback(() => {
    api<TokenList>('/auth/tokens').then(setData).catch((e) => showToast({ title: t('account.tokens.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [showToast, t])
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
                            <Badge tone={tk.scope === 'write' ? 'amber' : 'sky'}>{t(`account.tokens.scope.${tk.scope}`)}</Badge>
                            {tk.revoked_at ? <Badge tone="slate">{t('account.tokens.status.revoked')}</Badge> : tk.expired && <Badge tone="slate">{t('account.tokens.status.expired')}</Badge>}
                          </div>
                          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-xs text-slate-400">
                            <code className="font-mono">{tk.prefix}…</code>
                            <span>{t('account.tokens.createdAt', { date: formatDate(tk.created_at) })}</span>
                            <span>{tk.expires_at ? t('account.tokens.expiresAt', { date: formatDate(tk.expires_at) }) : t('account.tokens.neverExpires')}</span>
                            <span>{tk.last_used_at ? t('account.tokens.lastUsed', { date: formatDate(tk.last_used_at), ip: tk.last_used_ip }) : t('account.tokens.neverUsed')}</span>
                          </div>
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
              <p className="mt-4 text-xs leading-5 text-slate-400">{t('account.tokens.rules')}</p>
            </div>
          </div>
        </AccountSettingsLayout>
      </Container>
      {creating && <CreateTokenModal onClose={() => setCreating(false)} onCreated={(token) => { setCreating(false); setCreated(token); load() }} />}
      {created && <CreatedTokenModal token={created} onClose={() => setCreated('')} />}
    </>
  )
}

function CreateTokenModal({ onClose, onCreated }: { onClose: () => void; onCreated: (token: string) => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [name, setName] = useState('')
  const [scope, setScope] = useState<'read' | 'write'>('read')
  const [expires, setExpires] = useState('30')
  const [saving, setSaving] = useState(false)

  async function submit() {
    setSaving(true)
    try {
      const r = await api<{ token: string }>('/auth/tokens', { method: 'POST', body: { name: name.trim(), scope, expires_days: Number(expires) } })
      onCreated(r.token)
    } catch (e) {
      showToast({ title: t('account.tokens.createFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal open onClose={onClose} title={t('account.tokens.create')}
      footer={<><Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button><Button loading={saving} disabled={!name.trim()} onClick={() => void submit()}>{t('account.tokens.generate')}</Button></>}>
      <div className="space-y-4">
        <Field label={t('account.tokens.name')}>
          <Input value={name} maxLength={100} placeholder={t('account.tokens.namePlaceholder')} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label={t('account.tokens.scopeLabel')} hint={t(`account.tokens.scopeHint.${scope}`)}>
          <SegmentedTabs fullWidth size="sm" value={scope} ariaLabel={t('account.tokens.scopeLabel')} onChange={(v) => setScope(v as 'read' | 'write')}
            items={[{ value: 'read', label: t('account.tokens.scope.read') }, { value: 'write', label: t('account.tokens.scope.write') }]} />
        </Field>
        <Field label={t('account.tokens.expiresLabel')}>
          <Select value={expires} onChange={setExpires}
            options={EXPIRY_OPTIONS.map((d) => ({ value: d, label: d === '0' ? t('account.tokens.neverExpires') : t('account.tokens.days', { n: d }) }))} />
        </Field>
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
      <div className="mt-3 flex gap-2">
        <Input value={token} readOnly aria-label={t('account.tokens.title')} className="font-mono text-xs" />
        <Button variant="outline" className="shrink-0 whitespace-nowrap" onClick={() => void copy()}><i className="fa-regular fa-copy" aria-hidden="true" />{t('account.tokens.copy')}</Button>
      </div>
    </Modal>
  )
}
