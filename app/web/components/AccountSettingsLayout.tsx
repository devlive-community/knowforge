import Link from 'next/link'
import { ReactNode, useRef, useState } from 'react'
import UserAvatar from '@/components/UserAvatar'
import { getToken } from '@/lib/api'
import type { User } from '@/lib/types'
import { ShieldIcon, UserCircleIcon, DownloadIcon, LinkIcon, PaletteIcon, TrashIcon } from '@/components/icons'
import { useFeedback } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'

interface AccountSettingsLayoutProps {
  user: User
  active: 'profile' | 'security' | 'invite' | 'notify' | 'export' | 'oauth' | 'theme' | 'tokens' | 'danger'
  /** 头像上传后回调（个人资料页用） */
  onAvatarChange?: (url: string) => void
  children: ReactNode
}

// AccountSettingsLayout 账户设置：左侧身份卡与导航 + 右侧内容区（原型双栏布局）
export default function AccountSettingsLayout({ user, active, onAvatarChange, children }: AccountSettingsLayoutProps) {
  const { showToast } = useFeedback()
  const { t } = useTranslation()
  const fileRef = useRef<HTMLInputElement>(null)
  const [uploading, setUploading] = useState(false)

  async function uploadAvatar(file: File | undefined) {
    if (!file || !onAvatarChange) return
    setUploading(true)
    try {
      const fd = new FormData()
      fd.append('file', file)
      const res = await fetch('/api/v1/upload', {
        method: 'POST',
        headers: { Authorization: `Bearer ${getToken()}` },
        body: fd,
      })
      const payload = await res.json().catch(() => ({}))
      if (!res.ok || payload.success === false) throw new Error(payload.message || t('common.uploadFailed'))
      onAvatarChange(payload.data.url)
    } catch (e) {
      showToast({ title: t('account.avatar.failed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setUploading(false)
    }
  }

  const nav = [
    { key: 'profile' as const, label: t('account.nav.profile'), icon: <UserCircleIcon className="h-4 w-4" />, href: '/user/profile' },
    { key: 'security' as const, label: t('account.nav.security'), icon: <ShieldIcon className="h-4 w-4" />, href: '/user/security' },
    { key: 'oauth' as const, label: t('account.nav.oauth'), icon: <LinkIcon className="h-4 w-4" />, href: '/user/oauth' },
    { key: 'invite' as const, label: t('account.nav.invite'), icon: <i className="fa-solid fa-user-plus w-4 text-center text-[13px]" aria-hidden="true" />, href: '/user/invite' },
    { key: 'notify' as const, label: t('account.nav.notify'), icon: <i className="fa-solid fa-bell w-4 text-center text-[13px]" aria-hidden="true" />, href: '/user/notify' },
    { key: 'theme' as const, label: t('account.nav.theme'), icon: <PaletteIcon className="h-4 w-4" />, href: '/user/theme' },
    { key: 'export' as const, label: t('account.nav.export'), icon: <DownloadIcon className="h-4 w-4" />, href: '/user/export' },
    { key: 'tokens' as const, label: t('account.nav.tokens'), icon: <i className="fa-solid fa-key w-4 text-center text-[13px]" aria-hidden="true" />, href: '/user/tokens' },
    { key: 'danger' as const, label: t('account.nav.danger'), icon: <TrashIcon className="h-4 w-4" />, href: '/user/danger' },
  ]

  return (
    <div className="grid gap-6 lg:grid-cols-[260px_1fr]">
      {/* 左：身份卡 + 导航 */}
      <aside className="h-fit rounded-2xl border border-slate-200 bg-white p-5 shadow-sm lg:sticky lg:top-20">
        <div className="flex items-center gap-3">
          <UserAvatar user={user} size="h-14 w-14" link={false} />
          <div className="min-w-0">
            <div className="truncate font-bold text-slate-900">{user.username}</div>
            <div className="truncate text-sm text-slate-400">@{user.username}</div>
          </div>
        </div>
        {onAvatarChange && (
          <div className="mt-3 flex gap-2">
            <button type="button" onClick={() => fileRef.current?.click()} disabled={uploading}
              className="flex-1 rounded-lg border border-slate-200 px-2 py-1.5 text-xs text-slate-600 transition-colors hover:border-primary-400 hover:text-primary-600 disabled:opacity-50">
              {uploading ? t('account.avatar.uploading') : t('account.avatar.change')}
            </button>
            {user.avatar && (
              <button type="button" onClick={() => onAvatarChange('')}
                className="rounded-lg px-2 py-1.5 text-xs text-slate-400 transition-colors hover:text-rose-500">{t('account.avatar.remove')}</button>
            )}
          </div>
        )}
        <input ref={fileRef} type="file" accept="image/*" hidden onChange={(e) => { uploadAvatar(e.target.files?.[0]); e.target.value = '' }} />

        <nav className="mt-4 space-y-1 border-t border-slate-100 pt-4">
          {nav.map((item) => {
            const isActive = active === item.key
            const isDanger = item.key === 'danger'
            const cls = isDanger
              ? (isActive ? 'bg-rose-50 font-medium text-rose-700 ring-1 ring-inset ring-rose-100' : 'text-rose-600 hover:bg-rose-50')
              : (isActive ? 'bg-primary-50 font-medium text-primary-700 ring-1 ring-inset ring-primary-100' : 'text-slate-600 hover:bg-slate-50')
            return (
              <Link key={item.key} href={item.href}
                className={`flex items-center gap-2.5 rounded-lg px-3 py-2.5 text-sm transition-colors ${cls}`}>
                {item.icon} {item.label}
              </Link>
            )
          })}
        </nav>

        <Link href={`/user/${encodeURIComponent(user.username)}`}
          className="mt-6 flex w-full items-center justify-center gap-2 rounded-lg border border-slate-300 text-sm text-slate-700 transition-colors hover:border-primary-400 hover:text-primary-600"
          style={{ height: 'var(--control-height)' }}>
          {t('account.nav.viewProfile')}
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="h-3.5 w-3.5">
            <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" /><path d="M15 3h6v6" /><path d="M10 14 21 3" />
          </svg>
        </Link>
      </aside>

      {/* 右：内容 */}
      <div className="min-w-0">{children}</div>
    </div>
  )
}
