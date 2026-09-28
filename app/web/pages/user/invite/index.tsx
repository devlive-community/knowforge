import { useEffect, useState } from 'react'
import Link from 'next/link'
import Seo from '@/components/Seo'
import Container from '@/components/Container'
import AccountSettingsLayout from '@/components/AccountSettingsLayout'
import UserAvatar from '@/components/UserAvatar'
import { api, formatDate } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Button, Input, Loading, EmptyState, useFeedback } from '@/components/ui'
import { displayName } from '@/lib/users'

interface InvitedUser {
  username: string
  avatar?: string
  created_at: string
}

export default function InvitePage() {
  const { site } = useApp()
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const user = useRequireAuth()
  const { showToast } = useFeedback()
  const [code, setCode] = useState('')
  const [enabled, setEnabled] = useState(false)
  const [customCode, setCustomCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [invited, setInvited] = useState<InvitedUser[] | null>(null)

  useEffect(() => {
    if (!user) return
    api<{ invite_code: string; enabled: boolean }>('/auth/invite-code')
      .then((d) => { setCode(d.invite_code || ''); setEnabled(!!d.enabled) }).catch(() => {})
    api<{ items: InvitedUser[] }>('/auth/invited').then((d) => setInvited(d.items || [])).catch(() => setInvited([]))
  }, [user])

  if (!user) return <Loading className="min-h-[60vh]" label={t('account.common.loadingInfo')} />

  async function enable() {
    setBusy(true)
    try {
      const d = await api<{ invite_code: string; enabled: boolean }>('/auth/invite-code', { method: 'POST', body: { code: customCode.trim() } })
      setCode(d.invite_code || '')
      setEnabled(!!d.enabled)
      setCustomCode('')
    } catch (e) {
      showToast({ message: (e as Error).message || t('invite.enableFailed'), tone: 'error' })
    } finally {
      setBusy(false)
    }
  }
  async function disable() {
    setBusy(true)
    try {
      const d = await api<{ enabled: boolean }>('/auth/invite-code', { method: 'DELETE' })
      setEnabled(!!d.enabled)
    } catch (e) {
      showToast({ message: (e as Error).message || t('invite.disableFailed'), tone: 'error' })
    } finally {
      setBusy(false)
    }
  }
  async function copy() {
    try {
      await navigator.clipboard.writeText(code)
      showToast({ message: t('invite.copied'), tone: 'success' })
    } catch {
      showToast({ message: t('invite.copyFailed'), tone: 'error' })
    }
  }
  async function copyLink() {
    const link = `${window.location.origin}/register?invite=${encodeURIComponent(code)}`
    try {
      await navigator.clipboard.writeText(link)
      showToast({ message: t('invite.linkCopied'), tone: 'success' })
    } catch {
      showToast({ message: t('invite.copyFailed'), tone: 'error' })
    }
  }

  return (
    <>
      <Seo siteName={siteName} title={t('invite.seoTitle')} noindex />
      <Container>
        <nav className="flex items-center gap-1.5 py-4 text-sm text-slate-500">
          <Link href="/" className="hover:text-primary-600">{t('account.common.home')}</Link>
          <span className="text-slate-300">/</span>
          <span className="text-slate-900">{t('account.common.settings')}</span>
        </nav>
        <div className="pb-6">
          <h1 className="text-3xl font-bold text-ink">{t('account.common.settings')}</h1>
          <p className="mt-2 text-[15px] text-slate-500">{t('invite.pageSubtitle')}</p>
        </div>

        <AccountSettingsLayout user={user} active="invite">
          {/* 邀请码 */}
          <div className="rounded-2xl border border-slate-200 bg-white shadow-sm">
            <div className="p-6 pb-4">
              <h2 className="text-xl font-bold text-slate-900">{t('invite.myCode')}</h2>
              <p className="mt-1 text-sm text-slate-500">{t('invite.myCodeDesc')}</p>
            </div>
            <div className="px-6 pb-6">
              {code ? (
                <div>
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="min-w-0 flex-1">
                      <Input value={code} readOnly aria-label={t('invite.myCode')}
                        className={`font-mono text-base tracking-[0.3em] ${enabled ? '' : 'text-slate-400'}`} />
                    </span>
                    <Button type="button" variant="outline" className="shrink-0 whitespace-nowrap" onClick={copy}>{t('invite.copy')}</Button>
                    <Button type="button" variant="outline" className="shrink-0 whitespace-nowrap" disabled={!enabled} onClick={copyLink}>{t('invite.copyLink')}</Button>
                    {enabled ? (
                      <Button type="button" variant="ghost" loading={busy} className="shrink-0 whitespace-nowrap text-rose-600 hover:bg-rose-50" onClick={disable}>{t('invite.disable')}</Button>
                    ) : (
                      <Button type="button" variant="outline" loading={busy} className="shrink-0 whitespace-nowrap" onClick={enable}>{t('invite.enable')}</Button>
                    )}
                  </div>
                  {!enabled && <p className="mt-2 text-sm text-amber-600">{t('invite.disabledNote')}</p>}
                </div>
              ) : (
                <div className="flex flex-wrap items-end gap-2">
                  <span className="min-w-0 flex-1">
                    <label className="mb-1 block text-xs text-slate-500">{t('invite.customLabel')}</label>
                    <Input value={customCode} onChange={(e) => setCustomCode(e.target.value)} placeholder={t('invite.customPlaceholder')} maxLength={20} aria-label={t('invite.customAria')} />
                  </span>
                  <Button type="button" variant="outline" loading={busy} className="shrink-0 whitespace-nowrap" onClick={enable}>{t('invite.enableBtn')}</Button>
                </div>
              )}
            </div>
          </div>

          {/* 我邀请的用户 */}
          <div className="mt-6 rounded-2xl border border-slate-200 bg-white shadow-sm">
            <div className="p-6 pb-4">
              <h2 className="text-xl font-bold text-slate-900">{t('invite.invitedHeading')}</h2>
              <p className="mt-1 text-sm text-slate-500">{t('invite.invitedDesc')}</p>
            </div>
            <div className="px-6 pb-6">
              {invited === null ? (
                <Loading className="py-8" label={t('invite.invitedLoading')} />
              ) : invited.length === 0 ? (
                <EmptyState>{t('invite.invitedEmpty')}</EmptyState>
              ) : (
                <div className="divide-y divide-slate-100">
                  {invited.map((u) => (
                    <div key={u.username} className="flex items-center gap-3 py-3">
                      <UserAvatar user={u} size="h-9 w-9" link={false} tooltip={false} />
                      <div className="min-w-0">
                        <Link href={`/user/${encodeURIComponent(u.username)}`} className="font-medium text-slate-800 hover:text-primary-600">{displayName(u)}</Link>
                        <div className="text-xs text-slate-400">{t('invite.registeredAt', { date: formatDate(u.created_at).slice(0, 10) })}</div>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        </AccountSettingsLayout>
      </Container>
    </>
  )
}
