import { useEffect, useState } from 'react'
import Link from 'next/link'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import { api } from '@/lib/api'
import { Badge, Button, Card, Field, Input, Loading, Switch, useFeedback } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'

interface QASettings {
  ai_enabled: boolean
  agent_enabled: boolean
  top_k: number
  trace_retention_days: number
  semantic_search: boolean
  insights_asks: boolean
  insights_digest: boolean
}

interface QASettingsResponse {
  settings: QASettings
  ai_chat_available: boolean
  ai_embed_available: boolean
  semantic: { public_books: number; indexed_books: number }
}

export default function AdminQA() {
  return <FeatureGate feature="qa"><AdminQAInner /></FeatureGate>
}

// 问答插件管理：AI 问答 / Agent 模式开关与检索片段数；模型在「系统设置 · AI 服务」配置，每日提问次数为权益（成长等级/会员可提升）
function AdminQAInner() {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [data, setData] = useState<QASettingsResponse | null>(null)
  const [form, setForm] = useState<QASettings>({ ai_enabled: true, agent_enabled: true, top_k: 6, trace_retention_days: 0, semantic_search: false, insights_asks: true, insights_digest: true })
  const [saving, setSaving] = useState(false)
  const [reindexing, setReindexing] = useState(false)

  async function reindex() {
    setReindexing(true)
    try {
      const r = await api<{ queued: number }>('/admin/qa/semantic/reindex', { method: 'POST' })
      showToast({ message: t('admin.qa.semanticQueued', { n: r.queued }), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.qa.semanticReindexFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setReindexing(false)
    }
  }

  useEffect(() => {
    api<QASettingsResponse>('/admin/qa/settings')
      .then((r) => { setData(r); setForm(r.settings) })
      .catch((e) => showToast({ title: t('admin.qa.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [showToast, t])

  async function save() {
    setSaving(true)
    try {
      const r = await api<QASettingsResponse>('/admin/qa/settings', { method: 'PUT', body: form })
      setData(r)
      setForm(r.settings)
      showToast({ message: t('admin.qa.saved'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.qa.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <AdminLayout current="qa" breadcrumb={t('admin.nav.qa')}>
      <div>
        <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.qa')}</h1>
        <p className="mt-1.5 text-sm text-slate-500">{t('admin.qa.description')}</p>
      </div>
      {!data ? <Loading className="mt-6" label={t('admin.qa.loading')} /> : (
        <div className="mt-6 max-w-2xl space-y-5">
          <Card className="flex flex-wrap items-center gap-2 p-4 text-sm">
            <span className="text-slate-500">{t('admin.qa.aiService')}</span>
            <Badge tone={data.ai_chat_available ? 'emerald' : 'rose'}>{t(data.ai_chat_available ? 'admin.qa.chatReady' : 'admin.qa.chatMissing')}</Badge>
            <Badge tone={data.ai_embed_available ? 'emerald' : 'slate'}>{t(data.ai_embed_available ? 'admin.qa.hybrid' : 'admin.qa.keywordOnly')}</Badge>
            <Link href="/admin/settings/ai" className="ml-auto text-sm font-medium text-primary-600 hover:text-primary-700">{t('admin.qa.configureAI')}</Link>
          </Card>

          <Card className="space-y-5 p-6">
            <div className="flex items-start justify-between gap-4">
              <div>
                <div className="font-medium text-slate-900">{t('admin.qa.aiEnabled')}</div>
                <p className="mt-1 text-sm text-slate-500">{t('admin.qa.aiEnabledHint')}</p>
              </div>
              <Switch checked={form.ai_enabled} onChange={(v) => setForm({ ...form, ai_enabled: v })} ariaLabel={t('admin.qa.aiEnabled')} />
            </div>
            <div className="flex items-start justify-between gap-4">
              <div>
                <div className="font-medium text-slate-900">{t('admin.qa.agentEnabled')}</div>
                <p className="mt-1 text-sm text-slate-500">{t('admin.qa.agentEnabledHint')}</p>
              </div>
              <Switch checked={form.agent_enabled} disabled={!form.ai_enabled} onChange={(v) => setForm({ ...form, agent_enabled: v })} ariaLabel={t('admin.qa.agentEnabled')} />
            </div>
            <div className="flex items-start justify-between gap-4">
              <div>
                <div className="font-medium text-slate-900">{t('admin.qa.semantic')}</div>
                <p className="mt-1 text-sm text-slate-500">{t('admin.qa.semanticHint')}</p>
                {!data.ai_embed_available && <p className="mt-1 text-xs text-amber-600">{t('admin.qa.semanticNeedsEmbed')}</p>}
                {data.settings.semantic_search && data.ai_embed_available && (
                  <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-slate-500">
                    <span className="tabular-nums">{t('admin.qa.semanticStats', { indexed: data.semantic.indexed_books, total: data.semantic.public_books })}</span>
                    <Button size="sm" variant="outline" loading={reindexing} onClick={() => void reindex()}>{t('admin.qa.semanticReindex')}</Button>
                  </div>
                )}
              </div>
              <Switch checked={form.semantic_search} disabled={!data.ai_embed_available && !form.semantic_search} onChange={(v) => setForm({ ...form, semantic_search: v })} ariaLabel={t('admin.qa.semantic')} />
            </div>
            <div className="flex items-start justify-between gap-4">
              <div>
                <div className="font-medium text-slate-900">{t('admin.qa.insightsAsks')}</div>
                <p className="mt-1 text-sm text-slate-500">{t('admin.qa.insightsAsksHint')}</p>
              </div>
              <Switch checked={form.insights_asks} onChange={(v) => setForm({ ...form, insights_asks: v })} ariaLabel={t('admin.qa.insightsAsks')} />
            </div>
            <div className="flex items-start justify-between gap-4">
              <div>
                <div className="font-medium text-slate-900">{t('admin.qa.insightsDigest')}</div>
                <p className="mt-1 text-sm text-slate-500">{t('admin.qa.insightsDigestHint')}</p>
              </div>
              <Switch checked={form.insights_digest} onChange={(v) => setForm({ ...form, insights_digest: v })} ariaLabel={t('admin.qa.insightsDigest')} />
            </div>
            <Field label={t('admin.qa.topK')} hint={t('admin.qa.topKHint')}>
              <Input type="number" min={3} max={12} value={form.top_k} onChange={(e) => setForm({ ...form, top_k: Number(e.target.value) || 0 })} className="w-32" />
            </Field>
            <Field label={t('admin.qa.traceRetention')} hint={t('admin.qa.traceRetentionHint')}>
              <Input type="number" min={0} max={3650} value={form.trace_retention_days} onChange={(e) => setForm({ ...form, trace_retention_days: Number(e.target.value) || 0 })} className="w-32" />
            </Field>
            <p className="rounded-lg bg-slate-50 px-3 py-2 text-xs leading-5 text-slate-500">
              {t('admin.qa.quotaNote')} <Link href="/admin/settings/entitlements" className="font-medium text-primary-600 hover:text-primary-700">{t('admin.qa.quotaLink')}</Link>
            </p>
            <div className="flex justify-end">
              <Button loading={saving} disabled={form.top_k < 3 || form.top_k > 12 || (form.trace_retention_days !== 0 && form.trace_retention_days < 7)} onClick={() => void save()}>{t('admin.qa.save')}</Button>
            </div>
          </Card>
        </div>
      )}
    </AdminLayout>
  )
}
