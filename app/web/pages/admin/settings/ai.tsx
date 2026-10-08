import { useEffect, useState } from 'react'
import Link from 'next/link'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import SettingsLayout from '@/components/SettingsLayout'
import ModelPricesCard from '@/components/admin/ModelPricesCard'
import { Badge, Button, Input, Field, Select, Loading, Switch } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'

interface AIConfig {
  provider: string
  base_url: string
  model: string
  embed_base_url: string
  embed_model: string
  tts_base_url: string
  tts_model: string
  tts_voices: string
  price_currency: string
  price_input: string
  price_output: string
  price_embed: string
  price_translate: string
  price_tts: string
  alert_daily_cost: string
  alert_user_daily_tokens: string
  alert_trace_tokens: string
  usage_retention_days: string
  show_user_cost: string // 空或 true 为展示
  api_key_set: boolean
  embed_api_key_set: boolean
  tts_api_key_set: boolean
  source: 'ai' | 'translation' | 'none'
  chat_available: boolean
  embed_available: boolean
  tts_available: boolean
}

type TestKind = 'chat' | 'embed' | 'tts'

const DEFAULTS: Record<string, { base: string; model: string }> = {
  openai: { base: 'https://api.openai.com/v1', model: 'gpt-4o-mini' },
  anthropic: { base: 'https://api.anthropic.com', model: 'claude-sonnet-5' },
}

// 系统设置 · AI 服务：站点级的大模型（对话 + 向量嵌入 + 语音合成）配置，供问答等插件使用；密钥只写不读
export default function SettingsAI() {
  const { user } = useApp()
  const isAdmin = user?.role === 'admin'
  const { t } = useTranslation()
  const [cfg, setCfg] = useState<AIConfig | null>(null)
  const [apiKey, setApiKey] = useState('')
  const [embedKey, setEmbedKey] = useState('')
  const [ttsKey, setTtsKey] = useState('')
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState<TestKind | null>(null)
  const [testResult, setTestResult] = useState<Partial<Record<TestKind, { ok: boolean; text: string }>>>({})

  useEffect(() => {
    if (!isAdmin) return
    api<AIConfig>('/admin/ai').then(setCfg).catch((e) => setMessage((e as Error).message))
  }, [isAdmin])

  async function save(clear?: 'api_key' | 'embed_api_key' | 'tts_api_key') {
    if (!cfg) return
    setSaving(true)
    setMessage('')
    try {
      const body: Record<string, string> = {
        provider: cfg.provider, base_url: cfg.base_url, model: cfg.model,
        embed_base_url: cfg.embed_base_url, embed_model: cfg.embed_model,
        tts_base_url: cfg.tts_base_url, tts_model: cfg.tts_model, tts_voices: cfg.tts_voices, price_tts: cfg.price_tts,
        price_currency: cfg.price_currency, price_input: cfg.price_input, price_output: cfg.price_output, price_embed: cfg.price_embed, price_translate: cfg.price_translate,
        alert_daily_cost: cfg.alert_daily_cost, alert_user_daily_tokens: cfg.alert_user_daily_tokens, alert_trace_tokens: cfg.alert_trace_tokens,
        usage_retention_days: cfg.usage_retention_days, show_user_cost: cfg.show_user_cost === 'false' ? 'false' : 'true',
        api_key: clear === 'api_key' ? '-' : apiKey, embed_api_key: clear === 'embed_api_key' ? '-' : embedKey,
        tts_api_key: clear === 'tts_api_key' ? '-' : ttsKey,
      }
      setCfg(await api<AIConfig>('/admin/ai', { method: 'PUT', body }))
      setApiKey('')
      setEmbedKey('')
      setTtsKey('')
      setMessage(t('admin.settings.ai.saved'))
    } catch (e) {
      setMessage((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  async function test(kind: TestKind) {
    setTesting(kind)
    try {
      const d = await api<{ reply?: string; dimensions?: number; bytes?: number; elapsed_ms: number }>('/admin/ai/test', { method: 'POST', body: { kind } })
      const text = kind === 'chat'
        ? t('admin.settings.ai.testChatOk', { reply: d.reply || '-', ms: d.elapsed_ms })
        : kind === 'tts'
          ? t('admin.settings.ai.testTtsOk', { kb: Math.max(1, Math.round((d.bytes ?? 0) / 1024)), ms: d.elapsed_ms })
          : t('admin.settings.ai.testEmbedOk', { dimensions: d.dimensions ?? 0, ms: d.elapsed_ms })
      setTestResult((r) => ({ ...r, [kind]: { ok: true, text } }))
    } catch (e) {
      setTestResult((r) => ({ ...r, [kind]: { ok: false, text: (e as Error).message } }))
    } finally {
      setTesting(null)
    }
  }

  const hint = cfg ? DEFAULTS[cfg.provider] : undefined
  const set = (patch: Partial<AIConfig>) => cfg && setCfg({ ...cfg, ...patch })

  return (
    <SettingsLayout active="ai" description={t('admin.settings.ai.description')}>
      {!cfg ? <Loading className="max-w-2xl rounded-2xl border border-slate-200 bg-white shadow-sm" label={t('admin.settings.ai.loading')} /> : (
        <div className="max-w-2xl space-y-5">
          <div className="flex flex-wrap items-center gap-2 rounded-2xl border border-slate-200 bg-white px-5 py-4 text-sm shadow-sm">
            <span className="text-slate-500">{t('admin.settings.ai.status')}</span>
            <Badge tone={cfg.chat_available ? 'emerald' : 'slate'}>{t(cfg.chat_available ? 'admin.settings.ai.chatOn' : 'admin.settings.ai.chatOff')}</Badge>
            <Badge tone={cfg.embed_available ? 'emerald' : 'slate'}>{t(cfg.embed_available ? 'admin.settings.ai.embedOn' : 'admin.settings.ai.embedOff')}</Badge>
            <Badge tone={cfg.tts_available ? 'emerald' : 'slate'}>{t(cfg.tts_available ? 'admin.settings.ai.ttsOn' : 'admin.settings.ai.ttsOff')}</Badge>
            {cfg.source === 'translation' && <Badge tone="amber">{t('admin.settings.ai.sourceTranslation')}</Badge>}
          </div>

          <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.ai.chatTitle')}</h2>
            <p className="mt-1 text-sm text-slate-500">{t('admin.settings.ai.chatDescription')}</p>
            <div className="mt-4 space-y-4">
              <Field label={t('admin.settings.ai.provider')}>
                <Select value={cfg.provider} onChange={(v) => set({ provider: v })} options={[
                  { value: 'openai', label: t('admin.settings.ai.providerOpenai') },
                  { value: 'anthropic', label: t('admin.settings.ai.providerAnthropic') },
                ]} />
              </Field>
              <Field label={t('admin.settings.ai.baseUrl')} hint={t('admin.settings.ai.baseUrlHint', { default: hint?.base || '' })}>
                <Input value={cfg.base_url} onChange={(e) => set({ base_url: e.target.value })} placeholder={hint?.base} />
              </Field>
              <Field label="API Key" hint={t('admin.settings.ai.apiKeyHint')}>
                <div className="flex min-w-0 items-center gap-2">
                  <Input type="password" autoComplete="new-password" value={apiKey} onChange={(e) => setApiKey(e.target.value)}
                    className="min-w-0 flex-1" placeholder={cfg.api_key_set ? t('admin.settings.ai.secretSet') : t('admin.settings.ai.secretEmpty')} />
                  {cfg.api_key_set && <Button variant="outline" className="shrink-0 whitespace-nowrap" loading={saving} disabled={saving} onClick={() => save('api_key')}>{t('admin.settings.ai.clearSecret')}</Button>}
                </div>
              </Field>
              <Field label={t('admin.settings.ai.model')} hint={t('admin.settings.ai.modelHint', { default: hint?.model || '' })}>
                <Input value={cfg.model} onChange={(e) => set({ model: e.target.value })} placeholder={hint?.model} />
              </Field>
            </div>
          </div>

          <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.ai.embedTitle')}</h2>
            <p className="mt-1 text-sm text-slate-500">{t('admin.settings.ai.embedDescription')}</p>
            <div className="mt-4 space-y-4">
              <Field label={t('admin.settings.ai.embedModel')} hint={t('admin.settings.ai.embedModelHint')}>
                <Input value={cfg.embed_model} onChange={(e) => set({ embed_model: e.target.value })} placeholder="text-embedding-3-small" />
              </Field>
              <Field label={t('admin.settings.ai.embedBaseUrl')} hint={t('admin.settings.ai.embedBaseUrlHint')}>
                <Input value={cfg.embed_base_url} onChange={(e) => set({ embed_base_url: e.target.value })} placeholder="https://api.openai.com/v1" />
              </Field>
              <Field label={t('admin.settings.ai.embedApiKey')} hint={t('admin.settings.ai.embedApiKeyHint')}>
                <div className="flex min-w-0 items-center gap-2">
                  <Input type="password" autoComplete="new-password" value={embedKey} onChange={(e) => setEmbedKey(e.target.value)}
                    className="min-w-0 flex-1" placeholder={cfg.embed_api_key_set ? t('admin.settings.ai.secretSet') : t('admin.settings.ai.secretEmpty')} />
                  {cfg.embed_api_key_set && <Button variant="outline" className="shrink-0 whitespace-nowrap" loading={saving} disabled={saving} onClick={() => save('embed_api_key')}>{t('admin.settings.ai.clearSecret')}</Button>}
                </div>
              </Field>
            </div>
          </div>

          <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm" data-testid="ai-tts">
            <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.ai.ttsTitle')}</h2>
            <p className="mt-1 text-sm text-slate-500">{t('admin.settings.ai.ttsDescription')}</p>
            <div className="mt-4 space-y-4">
              <Field label={t('admin.settings.ai.ttsModel')} hint={t('admin.settings.ai.ttsModelHint')}>
                <Input value={cfg.tts_model} onChange={(e) => set({ tts_model: e.target.value })} placeholder="gpt-4o-mini-tts" />
              </Field>
              <Field label={t('admin.settings.ai.ttsVoices')} hint={t('admin.settings.ai.ttsVoicesHint')}>
                <Input value={cfg.tts_voices} onChange={(e) => set({ tts_voices: e.target.value })} placeholder="alloy, echo, fable, onyx, nova, shimmer" />
              </Field>
              <Field label={t('admin.settings.ai.ttsBaseUrl')} hint={t('admin.settings.ai.ttsBaseUrlHint')}>
                <Input value={cfg.tts_base_url} onChange={(e) => set({ tts_base_url: e.target.value })} placeholder="https://api.openai.com/v1" />
              </Field>
              <Field label={t('admin.settings.ai.ttsApiKey')} hint={t('admin.settings.ai.ttsApiKeyHint')}>
                <div className="flex min-w-0 items-center gap-2">
                  <Input type="password" autoComplete="new-password" value={ttsKey} onChange={(e) => setTtsKey(e.target.value)}
                    className="min-w-0 flex-1" placeholder={cfg.tts_api_key_set ? t('admin.settings.ai.secretSet') : t('admin.settings.ai.secretEmpty')} />
                  {cfg.tts_api_key_set && <Button variant="outline" className="shrink-0 whitespace-nowrap" loading={saving} disabled={saving} onClick={() => save('tts_api_key')}>{t('admin.settings.ai.clearSecret')}</Button>}
                </div>
              </Field>
            </div>
          </div>

          <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.ai.pricingTitle')}</h2>
              <Link href="/admin/ai-usage" className="ml-auto text-sm font-medium text-primary-600 hover:text-primary-700">{t('admin.settings.ai.viewUsage')}</Link>
            </div>
            <p className="mt-1 text-sm text-slate-500">{t('admin.settings.ai.pricingDescription')}</p>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <Field label={t('admin.settings.ai.priceCurrency')}>
                <Input value={cfg.price_currency} maxLength={3} onChange={(e) => set({ price_currency: e.target.value.toUpperCase() })} placeholder="USD" />
              </Field>
              <Field label={t('admin.settings.ai.priceInput')}>
                <Input type="number" min={0} step="0.01" value={cfg.price_input} onChange={(e) => set({ price_input: e.target.value })} placeholder="0" />
              </Field>
              <Field label={t('admin.settings.ai.priceOutput')}>
                <Input type="number" min={0} step="0.01" value={cfg.price_output} onChange={(e) => set({ price_output: e.target.value })} placeholder="0" />
              </Field>
              <Field label={t('admin.settings.ai.priceEmbed')}>
                <Input type="number" min={0} step="0.01" value={cfg.price_embed} onChange={(e) => set({ price_embed: e.target.value })} placeholder="0" />
              </Field>
              <Field label={t('admin.settings.ai.priceTranslate')} hint={t('admin.settings.ai.priceTranslateHint')}>
                <Input type="number" min={0} step="0.01" value={cfg.price_translate} onChange={(e) => set({ price_translate: e.target.value })} placeholder="0" />
              </Field>
              <Field label={t('admin.settings.ai.priceTts')} hint={t('admin.settings.ai.priceTtsHint')}>
                <Input type="number" min={0} step="0.01" value={cfg.price_tts} onChange={(e) => set({ price_tts: e.target.value })} placeholder="0" />
              </Field>
            </div>
            <div className="mt-4 max-w-xs">
              <Field label={t('admin.settings.ai.retentionDays')} hint={t('admin.settings.ai.retentionHint')}>
                <Input type="number" min={0} max={3650} value={cfg.usage_retention_days} onChange={(e) => set({ usage_retention_days: e.target.value })} placeholder="0" />
              </Field>
            </div>
            <label className="mt-4 flex items-start justify-between gap-4 rounded-xl border border-slate-100 bg-slate-50/60 px-4 py-3">
              <span>
                <span className="block text-sm font-medium text-slate-800">{t('admin.settings.ai.showUserCost')}</span>
                <span className="mt-0.5 block text-xs text-slate-500">{t('admin.settings.ai.showUserCostHint')}</span>
              </span>
              <Switch checked={cfg.show_user_cost !== 'false'} onChange={(v) => set({ show_user_cost: v ? 'true' : 'false' })} ariaLabel={t('admin.settings.ai.showUserCost')} />
            </label>
            <p className="mt-3 text-xs text-slate-400">{t('admin.settings.ai.quotaNote')}</p>
          </div>

          <ModelPricesCard />

          <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.ai.alertTitle')}</h2>
            <p className="mt-1 text-sm text-slate-500">{t('admin.settings.ai.alertDescription')}</p>
            <div className="mt-4 grid gap-4 sm:grid-cols-3">
              <Field label={t('admin.settings.ai.alertDailyCost', { currency: cfg.price_currency || 'USD' })}>
                <Input type="number" min={0} step="0.01" value={cfg.alert_daily_cost} onChange={(e) => set({ alert_daily_cost: e.target.value })} placeholder="0" />
              </Field>
              <Field label={t('admin.settings.ai.alertUserDailyTokens')}>
                <Input type="number" min={0} step="1000" value={cfg.alert_user_daily_tokens} onChange={(e) => set({ alert_user_daily_tokens: e.target.value })} placeholder="0" />
              </Field>
              <Field label={t('admin.settings.ai.alertTraceTokens')}>
                <Input type="number" min={0} step="1000" value={cfg.alert_trace_tokens} onChange={(e) => set({ alert_trace_tokens: e.target.value })} placeholder="0" />
              </Field>
            </div>
            <p className="mt-3 text-xs text-slate-400">{t('admin.settings.ai.alertHint')}</p>
          </div>

          {message && <div className="rounded-lg bg-slate-100 px-4 py-3 text-sm text-slate-600">{message}</div>}

          <div className="flex flex-wrap items-center justify-end gap-2">
            <Button variant="outline" loading={testing === 'chat'} disabled={testing !== null || !cfg.chat_available} onClick={() => test('chat')}>{t('admin.settings.ai.testChat')}</Button>
            <Button variant="outline" loading={testing === 'embed'} disabled={testing !== null || !cfg.embed_available} onClick={() => test('embed')}>{t('admin.settings.ai.testEmbed')}</Button>
            <Button variant="outline" loading={testing === 'tts'} disabled={testing !== null || !cfg.tts_available} onClick={() => test('tts')}>{t('admin.settings.ai.testTts')}</Button>
            <Button loading={saving} onClick={() => save()}>{t('admin.settings.ai.save')}</Button>
          </div>
          {(['chat', 'embed', 'tts'] as TestKind[]).map((kind) => testResult[kind] && (
            <div key={kind} className={`rounded-lg px-4 py-3 text-sm ${testResult[kind]!.ok ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700'}`}>
              {testResult[kind]!.text}
            </div>
          ))}
        </div>
      )}
    </SettingsLayout>
  )
}
