import { useEffect, useState, FormEvent, Fragment } from 'react'
import Seo from '@/components/Seo'
import Link from 'next/link'
import Container from '@/components/Container'
import AccountSettingsLayout from '@/components/AccountSettingsLayout'
import { api, API_BASE, getToken } from '@/lib/api'
import { resolveMediaUrl } from '@/lib/media'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Button, Input, Textarea, Field, Loading, Select } from '@/components/ui'
import { EyeIcon, SaveIcon } from '@/components/icons'
import UserAvatar from '@/components/UserAvatar'

const MAX_BIO = 200

export default function Profile() {
  const { site } = useApp()
  const siteName = site.site_name || 'KnowForge'
  const user = useRequireAuth()
  const { refreshUser } = useApp()
  const { t } = useTranslation()
  const [email, setEmail] = useState('')
  const [avatar, setAvatar] = useState('')
  const [bio, setBio] = useState('')
  const [githubUrl, setGithubUrl] = useState('')
  const [nickname, setNickname] = useState('')
  const [nameDisplay, setNameDisplay] = useState<'nickname' | 'username'>('nickname')
  const [website, setWebsite] = useState('')
  const [location, setLocation] = useState('')
  const [company, setCompany] = useState('')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [uploadingAvatar, setUploadingAvatar] = useState(false)

  useEffect(() => {
    if (user) {
      setEmail(user.email || '')
      setAvatar(user.avatar || '')
      setBio(user.bio || '')
      setGithubUrl(user.github_url || '')
      setNickname(user.nickname || '')
      setNameDisplay(user.name_display === 'username' ? 'username' : 'nickname')
      setWebsite(user.website || '')
      setLocation(user.location || '')
      setCompany(user.company || '')
    }
  }, [user])

  if (!user) return <Loading className="min-h-[60vh]" label={t('user.profile.loading')} />

  const avatarSrc = resolveMediaUrl(avatar)
  // 与服务端规则一致：选择显示用户名或没有昵称时为用户名，否则为昵称
  const previewName = nameDisplay === 'username' || !nickname.trim() ? user.username : nickname.trim()

  async function uploadAvatar(file: File | undefined) {
    if (!file) return
    setUploadingAvatar(true)
    setError('')
    try {
      const fd = new FormData()
      fd.append('file', file)
      const res = await fetch(`${API_BASE}/api/v1/upload`, { method: 'POST', headers: { Authorization: `Bearer ${getToken()}` }, body: fd })
      const payload = await res.json().catch(() => ({}))
      if (!res.ok || payload.success === false) throw new Error(payload.message || t('user.profile.uploadFailed'))
      setAvatar(payload.data.url)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setUploadingAvatar(false)
    }
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setMessage('')
    setError('')
    setSaving(true)
    try {
      await api('/auth/profile', {
        method: 'PUT',
        body: { email, avatar, bio, github_url: githubUrl, nickname, name_display: nameDisplay, website, location, company },
      })
      await refreshUser()
      setMessage(t('user.profile.saved'))
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <Seo siteName={siteName} title={t('user.profile.title')} noindex />
      <Container>
        {/* 页头 */}
        <nav className="flex items-center gap-1.5 py-4 text-sm text-slate-500">
          <Link href="/" className="hover:text-primary-600">{t('user.profile.home')}</Link>
          <span className="text-slate-300">/</span>
          <span className="text-slate-900">{t('user.profile.title')}</span>
        </nav>
        <div className="pb-6">
          <h1 className="text-3xl font-bold text-ink">{t('user.profile.title')}</h1>
          <p className="mt-2 text-[15px] text-slate-500">{t('user.profile.description')}</p>
        </div>

        <AccountSettingsLayout user={user} active="profile" onAvatarChange={setAvatar}>
          <div className="rounded-2xl border border-slate-200 bg-white shadow-sm">
            <div className="border-b border-slate-100 p-6">
              <h2 className="text-xl font-bold text-slate-900">{t('user.profile.personalInfo')}</h2>
              <p className="mt-1 text-sm text-slate-500">{t('user.profile.personalInfoHint')}</p>
            </div>

            <form onSubmit={submit}>
              <div className="space-y-6 p-6">
                {message && <div className="rounded-lg bg-emerald-50 px-4 py-3 text-sm text-emerald-600">{message}</div>}
                {error && <div className="rounded-lg bg-rose-50 px-4 py-3 text-sm text-rose-600">{error}</div>}

                {/* 头像 */}
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-[120px_1fr]">
                  <label className="pt-1 text-sm font-medium text-slate-700">{t('user.profile.avatar')}</label>
                  <div className="flex items-start gap-4">
                    <UserAvatar user={{ username: user.username, avatar }} size="h-20 w-20 text-2xl" link={false} />
                    <div className="space-y-2">
                      <label className="inline-flex cursor-pointer items-center gap-2 rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-600 transition-colors hover:bg-slate-50">
                        {uploadingAvatar ? t('user.profile.uploading') : t('user.profile.uploadAvatar')}
                        <input type="file" accept="image/*" hidden disabled={uploadingAvatar}
                          onChange={(e) => { void uploadAvatar(e.target.files?.[0]); e.target.value = '' }} />
                      </label>
                      <p className="text-xs text-slate-400">{t('user.profile.avatarHint')}</p>
                    </div>
                  </div>
                </div>

                {/* 头像地址 */}
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-[120px_1fr]">
                  <label className="pt-2.5 text-sm font-medium text-slate-700">{t('user.profile.avatarUrl')}</label>
                  <Input value={avatar} onChange={(e) => setAvatar(e.target.value)} placeholder="https://images.example.com/avatar.jpg" />
                </div>

                {/* 基本信息 */}
                <div className="border-t border-slate-100 pt-6">
                  <h3 className="mb-4 text-lg font-semibold text-slate-900">{t('user.profile.basicInfo')}</h3>
                  <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    <Field label={t('user.profile.username')} hint={t('user.profile.usernameHint')}>
                      <Input value={user.username} disabled />
                    </Field>
                    <Field label={t('user.profile.email')}>
                      <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
                    </Field>
                  </div>
                </div>

                {/* 公开资料 */}
                <div className="border-t border-slate-100 pt-6">
                  <h3 className="mb-4 text-lg font-semibold text-slate-900">{t('user.profile.publicInfo')}</h3>
                  <div className="space-y-4">
                    <div className="grid gap-4 sm:grid-cols-2">
                      <Field label={t('user.profile.nickname')} hint={t('user.profile.nicknameHint')}>
                        <Input value={nickname} onChange={(e) => setNickname(e.target.value)} placeholder={t('user.profile.nicknamePlaceholder')} maxLength={50} />
                      </Field>
                      <Field label={t('user.profile.nameDisplay')} hint={t('user.profile.nameDisplayHint')}>
                        <Select value={nameDisplay} onChange={(v) => setNameDisplay(v as 'nickname' | 'username')}
                          options={[{ value: 'nickname', label: t('user.profile.nameDisplayNickname') }, { value: 'username', label: t('user.profile.nameDisplayUsername') }]} />
                      </Field>
                      <Field label={t('user.profile.location')}>
                        <Input value={location} onChange={(e) => setLocation(e.target.value)} placeholder={t('user.profile.locationPlaceholder')} maxLength={100} />
                      </Field>
                      <Field label={t('user.profile.company')}>
                        <Input value={company} onChange={(e) => setCompany(e.target.value)} placeholder={t('user.profile.companyPlaceholder')} maxLength={100} />
                      </Field>
                      <Field label={t('user.profile.website')}>
                        <Input type="url" value={website} onChange={(e) => setWebsite(e.target.value)} placeholder="https://example.com" maxLength={255} />
                      </Field>
                    </div>
                    <Field label={t('user.profile.bio')}>
                      <div className="relative">
                        <Textarea maxLength={MAX_BIO} value={bio} onChange={(e) => setBio(e.target.value)}
                          placeholder={t('user.profile.bioPlaceholder')} className="pb-6" />
                        <span className="pointer-events-none absolute bottom-2 right-3 text-xs text-slate-400">{bio.length} / {MAX_BIO}</span>
                      </div>
                    </Field>
                    <div className="grid grid-cols-1 gap-4 sm:grid-cols-[120px_1fr]">
                      <label className="pt-2.5 text-sm font-medium text-slate-700">GitHub</label>
                      <div className="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 transition-colors focus-within:border-primary-500"
                        style={{ height: 'var(--control-height)' }}>
                        <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className="h-4 w-4 shrink-0 text-slate-700">
                          <path d="M12 .5C5.65.5.5 5.65.5 12c0 5.08 3.29 9.39 7.86 10.91.58.11.79-.25.79-.55 0-.27-.01-1.17-.02-2.12-3.2.7-3.87-1.36-3.87-1.36-.52-1.33-1.28-1.68-1.28-1.68-1.04-.71.08-.7.08-.7 1.15.08 1.76 1.19 1.76 1.19 1.03 1.75 2.69 1.25 3.34.95.1-.74.4-1.25.72-1.54-2.55-.29-5.23-1.28-5.23-5.68 0-1.26.45-2.28 1.19-3.09-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.17 1.18a11 11 0 0 1 5.77 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.81 1.19 1.83 1.19 3.09 0 4.41-2.69 5.38-5.25 5.67.41.35.77 1.05.77 2.12 0 1.53-.01 2.76-.01 3.14 0 .3.2.67.8.55A11.51 11.51 0 0 0 23.5 12C23.5 5.65 18.35.5 12 .5z" />
                        </svg>
                        <input value={githubUrl} onChange={(e) => setGithubUrl(e.target.value)} placeholder="https://github.com/username"
                          className="flex-1 border-0 bg-transparent text-sm text-slate-900 placeholder:text-slate-400 focus:outline-none focus:ring-0" />
                      </div>
                    </div>
                  </div>
                </div>

                {/* 公开主页预览 */}
                <div className="flex items-center gap-4 rounded-xl bg-emerald-50/70 px-5 py-4 ring-1 ring-inset ring-emerald-100">
                  <span className="flex items-center gap-2 text-sm font-semibold text-emerald-700">
                    <EyeIcon className="h-4 w-4" /> {t('user.profile.preview')}
                  </span>
                  <UserAvatar user={{ username: user.username, display_name: previewName, avatar }} size="h-11 w-11" link={false} />
                  <div className="min-w-0">
                    <div className="truncate font-semibold text-slate-900">{previewName}</div>
                    <div className="truncate text-xs text-slate-500">{bio || t('user.profile.bioPlaceholder')}</div>
                  </div>
                </div>
              </div>

              {/* 底部操作 */}
              <div className="flex items-center justify-end gap-3 border-t border-slate-100 px-6 py-4">
                <Button variant="outline" type="button" onClick={() => { setEmail(user.email || ''); setAvatar(user.avatar || ''); setBio(user.bio || ''); setGithubUrl(user.github_url || '') }}>{t('user.profile.cancel')}</Button>
                <Button type="submit" loading={saving}><SaveIcon className="h-4 w-4" /> {t('user.profile.save')}</Button>
              </div>
            </form>
          </div>
        </AccountSettingsLayout>
      </Container>
    </>
  )
}
