import { useEffect, useState } from 'react'
import Link from 'next/link'
import Seo from '@/components/Seo'
import Container from '@/components/Container'
import AccountSettingsLayout from '@/components/AccountSettingsLayout'
import { api } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Button, Select, Switch, Loading, useFeedback } from '@/components/ui'

interface Prefs {
  comment: boolean
  reaction: boolean
  collaboration: boolean
  moderation: boolean
  system: boolean
  achievement: boolean
  book_update: boolean
  growth: boolean
  digest_mode: DigestMode
}

type DigestMode = 'instant' | 'daily' | 'weekly' | 'off'

const DIGEST_MODES: { value: DigestMode; labelKey: string }[] = [
  { value: 'instant', labelKey: 'notify.digest.instant' },
  { value: 'daily', labelKey: 'notify.digest.daily' },
  { value: 'weekly', labelKey: 'notify.digest.weekly' },
  { value: 'off', labelKey: 'notify.digest.off' },
]

const ITEMS: { key: keyof Prefs; labelKey: string; hintKey: string }[] = [
  { key: 'comment', labelKey: 'notify.itemCommentLabel', hintKey: 'notify.itemCommentHint' },
  { key: 'reaction', labelKey: 'notify.itemReactionLabel', hintKey: 'notify.itemReactionHint' },
  { key: 'collaboration', labelKey: 'notify.itemCollaborationLabel', hintKey: 'notify.itemCollaborationHint' },
  { key: 'moderation', labelKey: 'notify.itemModerationLabel', hintKey: 'notify.itemModerationHint' },
  { key: 'system', labelKey: 'notify.itemSystemLabel', hintKey: 'notify.itemSystemHint' },
  { key: 'achievement', labelKey: 'notify.itemAchievementLabel', hintKey: 'notify.itemAchievementHint' },
  { key: 'book_update', labelKey: 'notify.itemBookUpdateLabel', hintKey: 'notify.itemBookUpdateHint' },
  { key: 'growth', labelKey: 'notify.itemGrowthLabel', hintKey: 'notify.itemGrowthHint' },
]

export default function NotifyPrefs() {
  const { site } = useApp()
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const user = useRequireAuth()
  const { showToast } = useFeedback()
  const [prefs, setPrefs] = useState<Prefs | null>(null)
  const [emailEnabled, setEmailEnabled] = useState(false)
  const [saving, setSaving] = useState(false)
  const [previewing, setPreviewing] = useState(false)
  // 插件禁用时隐藏对应的通知偏好项
  const achievementsEnabled = (site.feature_plugins || []).includes('achievements')
  const followEnabled = (site.feature_plugins || []).includes('book-follow')
  const growthEnabled = (site.feature_plugins || []).includes('growth')
  const items = ITEMS.filter((it) =>
    (it.key !== 'achievement' || achievementsEnabled) &&
    (it.key !== 'book_update' || followEnabled) &&
    (it.key !== 'growth' || growthEnabled))

  useEffect(() => {
    if (!user) return
    api<{ email_enabled: boolean; prefs: Prefs }>('/auth/notification-prefs')
      .then((d) => { setPrefs(d.prefs); setEmailEnabled(d.email_enabled) })
      .catch(() => setPrefs({ comment: true, reaction: true, collaboration: true, moderation: true, system: true, achievement: true, book_update: true, growth: true, digest_mode: 'instant' }))
  }, [user])

  if (!user) return <Loading className="min-h-[60vh]" label={t('account.common.loadingInfo')} />

  async function save() {
    if (!prefs) return
    setSaving(true)
    try {
      await api('/auth/notification-prefs', { method: 'PUT', body: prefs })
      showToast({ message: t('notify.saved'), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error).message || t('notify.saveFailed'), tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  // 按最近 7 天的内容立即发一封摘要到自己的邮箱，便于确认摘要的样子
  async function sendPreview() {
    setPreviewing(true)
    try {
      const d = await api<{ sent: boolean; count: number }>('/auth/notification-prefs/digest-preview', { method: 'POST' })
      showToast(d.sent
        ? { message: t('notify.digest.previewSent', { count: d.count }), tone: 'success' }
        : { message: t('notify.digest.previewEmpty'), tone: 'info' })
    } catch (e) {
      showToast({ message: (e as Error).message || t('notify.digest.previewFailed'), tone: 'error' })
    } finally {
      setPreviewing(false)
    }
  }

  const digestMode = prefs?.digest_mode || 'instant'

  return (
    <>
      <Seo siteName={siteName} title={t('notify.seoTitle')} noindex />
      <Container>
        <nav className="flex items-center gap-1.5 py-4 text-sm text-slate-500">
          <Link href="/" className="hover:text-primary-600">{t('account.common.home')}</Link>
          <span className="text-slate-300">/</span>
          <span className="text-slate-900">{t('account.common.settings')}</span>
        </nav>
        <div className="pb-6">
          <h1 className="text-3xl font-bold text-ink">{t('account.common.settings')}</h1>
          <p className="mt-2 text-[15px] text-slate-500">{t('notify.pageSubtitle')}</p>
        </div>

        <AccountSettingsLayout user={user} active="notify">
          <div className="rounded-2xl border border-slate-200 bg-white shadow-sm">
            <div className="p-6 pb-4">
              <h2 className="text-xl font-bold text-slate-900">{t('notify.emailHeading')}</h2>
              <p className="mt-1 text-sm text-slate-500">{t('notify.emailDesc')}</p>
            </div>
            <div className="px-6 pb-6">
              {prefs === null ? (
                <Loading className="py-8" label={t('notify.loading')} />
              ) : (
                <>
                  {!emailEnabled && (
                    <div className="mb-4 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700">
                      {t('notify.adminDisabled')}
                    </div>
                  )}
                  <div className="mb-4 rounded-xl border border-slate-200 px-4 py-3" data-testid="digest-mode">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <div className="min-w-0">
                        <div className="text-sm font-medium text-slate-800">{t('notify.digest.label')}</div>
                        <div className="text-xs text-slate-400">{t('notify.digest.hint')}</div>
                      </div>
                      <Select className="w-44" value={digestMode} onChange={(v) => setPrefs({ ...prefs, digest_mode: v as DigestMode })}
                        options={DIGEST_MODES.map((m) => ({ value: m.value, label: t(m.labelKey) }))} />
                    </div>
                    {(digestMode === 'daily' || digestMode === 'weekly') && emailEnabled && (
                      <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-slate-100 pt-3">
                        <span className="text-xs text-slate-500">{t('notify.digest.previewHint')}</span>
                        <Button size="sm" variant="outline" loading={previewing} onClick={() => void sendPreview()}>{t('notify.digest.preview')}</Button>
                      </div>
                    )}
                  </div>
                  <div className="divide-y divide-slate-100 rounded-xl border border-slate-200">
                    {items.map((it) => (
                      <div key={it.key} className="flex items-center justify-between gap-4 px-4 py-3">
                        <div className="min-w-0">
                          <div className="text-sm font-medium text-slate-800">{t(it.labelKey)}</div>
                          <div className="text-xs text-slate-400">{t(it.hintKey)}</div>
                        </div>
                        <Switch ariaLabel={t(it.labelKey)} checked={prefs[it.key] as boolean} disabled={digestMode === 'off'} onChange={(v) => setPrefs({ ...prefs, [it.key]: v })} />
                      </div>
                    ))}
                  </div>
                  <div className="mt-5 flex justify-end">
                    <Button loading={saving} onClick={save}>{t('notify.save')}</Button>
                  </div>
                </>
              )}
            </div>
          </div>
        </AccountSettingsLayout>
      </Container>
    </>
  )
}
