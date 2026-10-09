import { useEffect, useState } from 'react'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import { api } from '@/lib/api'
import type { MailConfig } from '@/lib/admin'
import { Badge, Button, Card, Field, Input, Loading, useFeedback } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'

interface IndexNowStatus {
  enabled: boolean
  site_url_valid: boolean
  key_configured: boolean
  key_file_url?: string
  pending?: number
  failed?: number
  last_submitted_at?: string | null
}

export default function AdminIndexNow() {
  return <FeatureGate feature="indexnow"><Inner /></FeatureGate>
}

function Inner() {
  const { t } = useTranslation()
  const { confirmAction } = useFeedback()
  const [indexNow, setIndexNow] = useState<IndexNowStatus | null>(null)
  const [siteUrl, setSiteUrl] = useState('')
  const [loading, setLoading] = useState(true)
  const [savingSiteUrl, setSavingSiteUrl] = useState(false)
  const [siteMessage, setSiteMessage] = useState('')
  const [indexNowBusy, setIndexNowBusy] = useState<'key' | 'retry' | null>(null)
  const [indexNowMessage, setIndexNowMessage] = useState('')

  useEffect(() => {
    Promise.all([
      api<MailConfig>('/mail').then((mail) => setSiteUrl(mail.site_url || '')).catch(() => {}),
      api<IndexNowStatus>('/admin/indexnow').then(setIndexNow).catch(() => setIndexNow(null)),
    ]).finally(() => setLoading(false))
  }, [])

  async function saveSiteUrl() {
    setSavingSiteUrl(true)
    setSiteMessage('')
    try {
      await api('/mail', { method: 'PUT', body: { site_url: siteUrl } })
      const [mail, status] = await Promise.all([
        api<MailConfig>('/mail'),
        api<IndexNowStatus>('/admin/indexnow'),
      ])
      setSiteUrl(mail.site_url || '')
      setIndexNow(status)
      setSiteMessage(t('admin.settings.site.saved'))
    } catch (e) {
      setSiteMessage((e as Error).message)
    } finally {
      setSavingSiteUrl(false)
    }
  }

  async function configureIndexNowKey() {
    const rotate = !!indexNow?.key_configured
    if (rotate && !await confirmAction({
      title: t('admin.settings.site.indexNowRotate'),
      message: t('admin.settings.site.indexNowRotateConfirm'),
      confirmLabel: t('admin.settings.site.indexNowRotate'),
      danger: true,
    })) return
    setIndexNowBusy('key')
    setIndexNowMessage('')
    try {
      const result = await api<Pick<IndexNowStatus, 'key_configured' | 'key_file_url'>>('/admin/indexnow/key', { method: 'POST', body: { rotate } })
      setIndexNow((current) => current ? { ...current, ...result } : current)
    } catch (e) {
      setIndexNowMessage((e as Error).message || t('admin.settings.site.indexNowGenerateFailed'))
    } finally {
      setIndexNowBusy(null)
    }
  }

  async function retryIndexNow() {
    setIndexNowBusy('retry')
    setIndexNowMessage('')
    try {
      await api('/admin/indexnow/retry', { method: 'POST' })
      setIndexNowMessage(t('admin.settings.site.indexNowRetryQueued'))
      setIndexNow(await api<IndexNowStatus>('/admin/indexnow'))
    } catch (e) {
      setIndexNowMessage((e as Error).message || t('admin.settings.site.indexNowRetryFailed'))
    } finally {
      setIndexNowBusy(null)
    }
  }

  return (
    <AdminLayout current="indexnow" breadcrumb={t('admin.nav.indexNow')}>
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.indexNow')}</h1>
        <p className="mt-1.5 text-sm text-slate-500">{t('admin.settings.site.indexNowHint')}</p>
      </div>
      {loading ? <Loading className="mt-6" /> : !indexNow ? (
        <p role="status" className="text-sm text-rose-600">{t('admin.settings.site.indexNowLoadFailed')}</p>
      ) : (
        <Card className="max-w-2xl space-y-5 p-6" data-testid="indexnow-settings">
          <Field label={t('admin.settings.mail.siteUrl')} hint={t('admin.settings.mail.siteUrlHint')}>
            <div className="flex flex-wrap items-center gap-2">
              <Input className="min-w-0 flex-1" value={siteUrl} onChange={(e) => setSiteUrl(e.target.value)} placeholder="https://kb.example.com" />
              <Button size="md" loading={savingSiteUrl} onClick={() => void saveSiteUrl()}>{t('admin.settings.site.save')}</Button>
            </div>
          </Field>
          {siteMessage && <p role="status" className="break-words text-sm text-slate-600">{siteMessage}</p>}
          {!indexNow.site_url_valid && <p className="text-sm text-rose-600">{t('admin.settings.site.indexNowSiteInvalid')}</p>}
          <section className="rounded-xl border border-slate-200 bg-slate-50/70 p-4">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-sm font-semibold text-slate-800">{t('admin.settings.site.indexNowTitle')}</h2>
              <Badge tone={indexNow.enabled ? 'emerald' : 'slate'}>
                {indexNow.enabled ? t('admin.settings.site.indexNowEnabled') : t('admin.settings.site.indexNowDisabled')}
              </Badge>
            </div>
            <p className="mt-1 text-xs leading-5 text-slate-500">{t('admin.settings.site.indexNowHint')}</p>
            <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-slate-500">
              <span>{indexNow.key_configured ? t('admin.settings.site.indexNowKeyReady') : t('admin.settings.site.indexNowKeyMissing')}</span>
              {indexNow.key_file_url && <a href={indexNow.key_file_url} target="_blank" rel="noopener noreferrer" className="min-w-0 break-all font-mono text-primary-600 hover:underline">{indexNow.key_file_url}</a>}
              <span>{t('admin.settings.site.indexNowPending', { n: indexNow.pending || 0 })}</span>
              {(indexNow.failed || 0) > 0 && <span className="text-rose-600">{t('admin.settings.site.indexNowFailed', { n: indexNow.failed || 0 })}</span>}
              {indexNow.last_submitted_at && <span>{t('admin.settings.site.indexNowLastSent', { time: new Date(indexNow.last_submitted_at).toLocaleString() })}</span>}
            </div>
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <Button size="sm" variant="outline" loading={indexNowBusy === 'key'} disabled={!indexNow.enabled || !indexNow.site_url_valid || indexNowBusy !== null} onClick={() => void configureIndexNowKey()}>
                {t(indexNow.key_configured ? 'admin.settings.site.indexNowRotate' : 'admin.settings.site.indexNowGenerate')}
              </Button>
              {(indexNow.failed || 0) > 0 && <Button size="sm" variant="outline" loading={indexNowBusy === 'retry'} disabled={indexNowBusy !== null} onClick={() => void retryIndexNow()}>{t('admin.settings.site.indexNowRetry')}</Button>}
            </div>
            {indexNowMessage && <p role="status" className="mt-2 break-words text-xs text-slate-600">{indexNowMessage}</p>}
          </section>
        </Card>
      )}
    </AdminLayout>
  )
}
