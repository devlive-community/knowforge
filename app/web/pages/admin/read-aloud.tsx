import { useEffect, useState } from 'react'
import Link from 'next/link'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import { api } from '@/lib/api'
import { Badge, Button, Card, Field, Input, Loading, useFeedback } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'

interface Settings {
  cache_max_mb: number
  cache_files: number
  cache_bytes: number
  speech_model: string
  voices: string[] | null
}

export default function AdminReadAloud() {
  return <FeatureGate feature="read-aloud"><Inner /></FeatureGate>
}

function formatMB(bytes: number): string {
  return (bytes / 1024 / 1024).toFixed(bytes > 100 << 20 ? 0 : 1)
}

// AI 朗读插件管理：语音合成配置概况、音频缓存上限与清空；每月朗读字数在「权益」中配置（成长等级/会员可提升）。
function Inner() {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [data, setData] = useState<Settings | null>(null)
  const [maxMB, setMaxMB] = useState('')
  const [saving, setSaving] = useState(false)
  const [clearing, setClearing] = useState(false)

  useEffect(() => {
    api<Settings>('/admin/read-aloud/settings')
      .then((d) => { setData(d); setMaxMB(String(d.cache_max_mb)) })
      .catch((e) => showToast({ title: t('admin.readAloud.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [showToast, t])

  async function save() {
    setSaving(true)
    try {
      const d = await api<Settings>('/admin/read-aloud/settings', { method: 'PUT', body: { cache_max_mb: Number(maxMB) } })
      setData(d)
      setMaxMB(String(d.cache_max_mb))
      showToast({ message: t('admin.readAloud.saved'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.readAloud.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  async function clearCache() {
    setClearing(true)
    try {
      setData(await api<Settings>('/admin/read-aloud/cache', { method: 'DELETE' }))
      showToast({ message: t('admin.readAloud.cleared'), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setClearing(false)
    }
  }

  const ready = !!data?.speech_model
  return (
    <AdminLayout current="read-aloud" breadcrumb={t('admin.nav.readAloud')}>
      <div>
        <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.readAloud')}</h1>
        <p className="mt-1.5 text-sm text-slate-500">{t('admin.readAloud.description')}</p>
      </div>
      {!data ? <Loading className="mt-6" /> : (
        <div className="mt-6 max-w-2xl space-y-5">
          <Card className="flex flex-wrap items-center gap-2 p-4 text-sm" data-testid="read-aloud-service">
            <span className="text-slate-500">{t('admin.readAloud.speech')}</span>
            <Badge tone={ready ? 'emerald' : 'rose'}>{ready ? t('admin.readAloud.speechReady', { model: data.speech_model }) : t('admin.readAloud.speechMissing')}</Badge>
            {ready && data.voices && <span className="text-xs text-slate-400">{t('admin.readAloud.voices', { voices: data.voices.join(', ') })}</span>}
            <Link href="/admin/settings/ai" className="ml-auto font-medium text-primary-600 hover:text-primary-700">{t('admin.readAloud.configure')}</Link>
          </Card>
          <Card className="space-y-4 p-6">
            <div>
              <div className="font-medium text-slate-900">{t('admin.readAloud.cacheTitle')}</div>
              <p className="mt-1 text-sm text-slate-500">{t('admin.readAloud.cacheHint')}</p>
            </div>
            <div className="flex flex-wrap items-center gap-3 rounded-lg bg-slate-50 px-3 py-2 text-sm text-slate-600" data-testid="read-aloud-cache">
              <span>{t('admin.readAloud.cacheUsage', { files: data.cache_files.toLocaleString(), mb: formatMB(data.cache_bytes) })}</span>
              <Button size="sm" variant="outline" className="ml-auto" loading={clearing} disabled={data.cache_files === 0} onClick={() => void clearCache()}>
                {t('admin.readAloud.clear')}
              </Button>
            </div>
            <Field label={t('admin.readAloud.cacheMax')} hint={t('admin.readAloud.cacheMaxHint')}>
              <Input type="number" min={0} className="max-w-xs" value={maxMB} onChange={(e) => setMaxMB(e.target.value)} />
            </Field>
            <p className="rounded-lg bg-slate-50 px-3 py-2 text-xs leading-5 text-slate-500">
              {t('admin.readAloud.quotaNote')} <Link href="/admin/settings/entitlements" className="font-medium text-primary-600 hover:text-primary-700">{t('admin.readAloud.quotaLink')}</Link>
            </p>
            <div className="flex justify-end">
              <Button loading={saving} disabled={maxMB === String(data.cache_max_mb) || maxMB === ''} onClick={() => void save()}>{t('common.actions.save')}</Button>
            </div>
          </Card>
        </div>
      )}
    </AdminLayout>
  )
}
