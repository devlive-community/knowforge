import { useEffect, useState } from 'react'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import { api } from '@/lib/api'
import type { MailConfig } from '@/lib/admin'
import { Badge, Button, Card, Checkbox, Field, Input, Loading, Switch, Textarea, useFeedback } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'

interface IndexNowSettings {
  visit_push: boolean
  visit_books: boolean
  visit_chapters: boolean
  visit_interval_hours: number
}

interface PushLog {
  id: number
  source: 'queue' | 'visit' | 'trigger' | 'manual'
  url_count: number
  sample_url: string
  status: 'ok' | 'error'
  message: string
  created_at: string
}

const SOURCE_KEYS: Record<PushLog['source'], string> = {
  queue: 'admin.indexnow.source.queue',
  visit: 'admin.indexnow.source.visit',
  trigger: 'admin.indexnow.source.trigger',
  manual: 'admin.indexnow.source.manual',
}

interface IndexNowStatus {
  enabled: boolean
  site_url_valid: boolean
  key_configured: boolean
  key_file_url?: string
  pending?: number
  failed?: number
  last_submitted_at?: string | null
  settings?: IndexNowSettings
  trigger_url?: string
  logs?: PushLog[]
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
      {!loading && indexNow?.enabled && indexNow.key_configured && indexNow.site_url_valid && (
        <RealtimePanels status={indexNow} onChange={setIndexNow} />
      )}
    </AdminLayout>
  )
}

// RealtimePanels 实时推送：访问时推送、触发地址、手动推送与最近推送记录。
function RealtimePanels({ status, onChange }: { status: IndexNowStatus; onChange: (next: IndexNowStatus) => void }) {
  const { t } = useTranslation()
  const { showToast, confirmAction } = useFeedback()
  const initial = status.settings || { visit_push: false, visit_books: true, visit_chapters: true, visit_interval_hours: 24 }
  const [settings, setSettings] = useState<IndexNowSettings>(initial)
  const [intervalHours, setIntervalHours] = useState(String(initial.visit_interval_hours))
  const [urls, setUrls] = useState('')
  const [busy, setBusy] = useState<'settings' | 'trigger' | 'disable' | 'submit' | 'refresh' | null>(null)

  async function run(kind: NonNullable<typeof busy>, fn: () => Promise<void>) {
    setBusy(kind)
    try {
      await fn()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  const saveSettings = () => run('settings', async () => {
    onChange(await api<IndexNowStatus>('/admin/indexnow/settings', { method: 'PUT', body: { ...settings, visit_interval_hours: Number(intervalHours) || 0 } }))
    showToast({ message: t('admin.indexnow.visit.saved'), tone: 'success' })
  })

  const setTrigger = async (enabled: boolean) => {
    if (status.trigger_url && !await confirmAction({
      title: enabled ? t('admin.indexnow.trigger.rotate') : t('admin.indexnow.trigger.disable'),
      message: enabled ? t('admin.indexnow.trigger.rotateConfirm') : t('admin.indexnow.trigger.disableConfirm'),
      confirmLabel: enabled ? t('admin.indexnow.trigger.rotate') : t('admin.indexnow.trigger.disable'),
      danger: !enabled,
    })) return
    await run(enabled ? 'trigger' : 'disable', async () => {
      onChange(await api<IndexNowStatus>('/admin/indexnow/trigger', { method: 'POST', body: { enabled } }))
    })
  }

  const copyTrigger = async () => {
    if (!status.trigger_url) return
    try {
      await navigator.clipboard.writeText(status.trigger_url)
      showToast({ message: t('admin.indexnow.trigger.copied'), tone: 'success' })
    } catch {
      showToast({ message: t('admin.indexnow.trigger.copyFailed'), tone: 'error' })
    }
  }

  const submit = () => run('submit', async () => {
    const list = urls.split(/\s+/).map((u) => u.trim()).filter(Boolean)
    const r = await api<{ submitted: number; invalid: string[] }>('/admin/indexnow/submit', { method: 'POST', body: { urls: list } })
    showToast({ message: r.invalid.length ? t('admin.indexnow.manual.doneWithInvalid', { n: r.submitted, invalid: r.invalid.length }) : t('admin.indexnow.manual.done', { n: r.submitted }), tone: 'success' })
    setUrls('')
    onChange(await api<IndexNowStatus>('/admin/indexnow'))
  })

  const refresh = () => run('refresh', async () => { onChange(await api<IndexNowStatus>('/admin/indexnow')) })

  const logs = status.logs || []
  return (
    <div className="mt-5 max-w-2xl space-y-5">
      <Card className="space-y-4 p-6" data-testid="indexnow-visit">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 className="text-sm font-semibold text-slate-800">{t('admin.indexnow.visit.title')}</h2>
            <p className="mt-1 text-xs leading-5 text-slate-500">{t('admin.indexnow.visit.hint')}</p>
          </div>
          <Switch checked={settings.visit_push} ariaLabel={t('admin.indexnow.visit.title')} onChange={(v) => setSettings({ ...settings, visit_push: v })} />
        </div>
        <div className="flex flex-wrap items-center gap-x-6 gap-y-3 text-sm text-slate-700">
          <label className="flex items-center gap-2">
            <Checkbox checked={settings.visit_books} disabled={!settings.visit_push} ariaLabel={t('admin.indexnow.visit.books')} onChange={(v) => setSettings({ ...settings, visit_books: v })} />
            {t('admin.indexnow.visit.books')}
          </label>
          <label className="flex items-center gap-2">
            <Checkbox checked={settings.visit_chapters} disabled={!settings.visit_push} ariaLabel={t('admin.indexnow.visit.chapters')} onChange={(v) => setSettings({ ...settings, visit_chapters: v })} />
            {t('admin.indexnow.visit.chapters')}
          </label>
        </div>
        <Field label={t('admin.indexnow.visit.interval')} hint={t('admin.indexnow.visit.intervalHint')}>
          <Input type="number" min={1} max={720} className="max-w-[10rem]" value={intervalHours} disabled={!settings.visit_push} onChange={(e) => setIntervalHours(e.target.value)} />
        </Field>
        <div className="flex justify-end">
          <Button loading={busy === 'settings'} disabled={busy !== null} onClick={() => void saveSettings()}>{t('admin.settings.site.save')}</Button>
        </div>
      </Card>

      <Card className="space-y-3 p-6" data-testid="indexnow-trigger">
        <h2 className="text-sm font-semibold text-slate-800">{t('admin.indexnow.trigger.title')}</h2>
        <p className="text-xs leading-5 text-slate-500">{t('admin.indexnow.trigger.hint')}</p>
        {status.trigger_url ? (
          <>
            <code className="block break-all rounded-lg bg-slate-50 px-3 py-2 font-mono text-xs text-slate-700">{status.trigger_url}</code>
            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="outline" onClick={() => void copyTrigger()}>{t('admin.indexnow.trigger.copy')}</Button>
              <Button size="sm" variant="outline" loading={busy === 'trigger'} disabled={busy !== null} onClick={() => void setTrigger(true)}>{t('admin.indexnow.trigger.rotate')}</Button>
              <Button size="sm" variant="ghost" className="text-rose-600" loading={busy === 'disable'} disabled={busy !== null} onClick={() => void setTrigger(false)}>{t('admin.indexnow.trigger.disable')}</Button>
            </div>
          </>
        ) : (
          <Button size="sm" variant="outline" loading={busy === 'trigger'} disabled={busy !== null} onClick={() => void setTrigger(true)}>{t('admin.indexnow.trigger.enable')}</Button>
        )}
      </Card>

      <Card className="space-y-3 p-6" data-testid="indexnow-manual">
        <h2 className="text-sm font-semibold text-slate-800">{t('admin.indexnow.manual.title')}</h2>
        <p className="text-xs leading-5 text-slate-500">{t('admin.indexnow.manual.hint')}</p>
        <Textarea rows={4} value={urls} onChange={(e) => setUrls(e.target.value)} placeholder={'/book/detail/my-book\nhttps://kb.example.com/explore?category=go'} />
        <div className="flex justify-end">
          <Button loading={busy === 'submit'} disabled={busy !== null || !urls.trim()} onClick={() => void submit()}>{t('admin.indexnow.manual.submit')}</Button>
        </div>
      </Card>

      <Card className="p-6" data-testid="indexnow-logs">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold text-slate-800">{t('admin.indexnow.logs.title')}</h2>
          <Button size="sm" variant="ghost" className="ml-auto" loading={busy === 'refresh'} disabled={busy !== null} onClick={() => void refresh()}>{t('admin.indexnow.logs.refresh')}</Button>
        </div>
        <p className="mt-1 text-xs leading-5 text-slate-500">{t('admin.indexnow.logs.hint')}</p>
        {logs.length === 0 ? <p className="mt-3 text-sm text-slate-400">{t('admin.indexnow.logs.empty')}</p> : (
          <ul className="mt-3 divide-y divide-slate-100 text-xs">
            {logs.map((l) => (
              <li key={l.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
                <span className="tabular-nums text-slate-400">{new Date(l.created_at).toLocaleString()}</span>
                <Badge tone="slate">{t(SOURCE_KEYS[l.source] || SOURCE_KEYS.queue)}</Badge>
                <Badge tone={l.status === 'ok' ? 'emerald' : 'rose'}>{l.status === 'ok' ? t('admin.indexnow.logs.ok') : t('admin.indexnow.logs.error')}</Badge>
                <span className="text-slate-600">{t('admin.indexnow.logs.count', { n: l.url_count })}</span>
                <span className="min-w-0 flex-1 truncate font-mono text-slate-500">{l.sample_url}</span>
                {l.message && <span className="w-full break-words text-rose-600">{l.message}</span>}
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  )
}
