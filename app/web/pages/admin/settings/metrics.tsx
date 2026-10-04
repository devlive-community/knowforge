import { useEffect, useState } from 'react'
import SettingsLayout from '@/components/SettingsLayout'
import { API_BASE, api, requestHeaders } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { Button, Loading, Switch, useFeedback } from '@/components/ui'

interface MetricsSettings { enabled: boolean; token_set: boolean; token_hint: string; path: string; token?: string }

// 指标名 → 说明文案的键
const METRICS: [string, string][] = [
  ['knowforge_http_requests_total', 'httpRequests'], ['knowforge_http_request_duration_seconds', 'httpDuration'],
  ['knowforge_http_requests_in_flight', 'httpInFlight'], ['knowforge_sse_connections', 'sseConnections'],
  ['knowforge_ai_calls_total', 'aiCalls'], ['knowforge_ai_tokens_total', 'aiTokens'], ['knowforge_ai_cost_total', 'aiCost'],
  ['knowforge_ai_call_duration_seconds', 'aiDuration'], ['knowforge_jobs', 'jobs'], ['knowforge_jobs_oldest_waiting_seconds', 'jobsWaiting'],
  ['knowforge_cluster_instances', 'instances'], ['knowforge_db_connections', 'dbConnections'],
  ['knowforge_users / knowforge_books / knowforge_documents', 'totals'], ['knowforge_build_info', 'buildInfo'],
]

// 系统设置 · 运行指标：开启 Prometheus 格式的 /metrics（凭访问令牌抓取），生成 / 重新生成令牌（只显示一次），
// 抓取配置示例、指标说明与当前实例的指标预览。
export default function SettingsMetrics() {
  const { t } = useTranslation()
  const { confirmAction, showToast } = useFeedback()
  const [settings, setSettings] = useState<MetricsSettings | null>(null)
  const [error, setError] = useState('')
  const [toggling, setToggling] = useState(false)
  const [regenerating, setRegenerating] = useState(false)
  const [token, setToken] = useState('') // 刚生成的令牌（只在本次显示）
  const [preview, setPreview] = useState<string | null>(null)
  const [previewing, setPreviewing] = useState(false)

  useEffect(() => {
    api<MetricsSettings>('/metrics/settings').then(setSettings).catch((e) => setError((e as Error).message))
  }, [])

  const origin = (API_BASE || (typeof window !== 'undefined' ? window.location.origin : '')).replace(/\/$/, '')
  const endpoint = `${origin}/metrics`
  let host = 'kb.example.com'
  let scheme = 'https'
  try {
    const u = new URL(origin)
    host = u.host
    scheme = u.protocol.replace(':', '')
  } catch { /* 默认示例 */ }
  const scrapeConfig = `scrape_configs:
  - job_name: knowforge
    scheme: ${scheme}
    metrics_path: /metrics
    authorization:
      credentials: ${token || t('admin.settings.metrics.tokenPlaceholder')}
    static_configs:
      - targets: ['${host}']`

  async function toggle(enabled: boolean) {
    setToggling(true)
    try {
      const s = await api<MetricsSettings>('/metrics/settings', { method: 'PUT', body: { enabled } })
      setSettings(s)
      if (s.token) setToken(s.token)
      showToast({ message: t(enabled ? 'admin.settings.metrics.enabled' : 'admin.settings.metrics.disabled'), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setToggling(false)
  }

  async function regenerate() {
    if (!(await confirmAction({ title: t('admin.settings.metrics.regenerateTitle'), message: t('admin.settings.metrics.regenerateMessage'), confirmLabel: t('admin.settings.metrics.regenerate'), danger: true }))) return
    setRegenerating(true)
    try {
      const s = await api<MetricsSettings>('/metrics/token', { method: 'POST' })
      setSettings(s)
      setToken(s.token || '')
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setRegenerating(false)
  }

  async function loadPreview() {
    setPreviewing(true)
    try {
      const res = await fetch(`${API_BASE}/api/v1/metrics/preview`, { headers: requestHeaders(false) })
      const text = await res.text()
      if (!res.ok) throw new Error(text || `${res.status}`)
      setPreview(text)
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setPreviewing(false)
  }

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text)
      showToast({ message: t('admin.settings.metrics.copied'), tone: 'success' })
    } catch {
      showToast({ message: t('admin.settings.metrics.copyFailed'), tone: 'error' })
    }
  }

  return (
    <SettingsLayout active="metrics" description={t('admin.settings.metrics.description')}>
      {error ? <p className="text-sm text-rose-600">{error}</p> : !settings ? <Loading className="py-16" /> : (
        <div className="max-w-4xl space-y-5">
          <section className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <div className="flex items-start justify-between gap-4">
              <div>
                <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.metrics.switchTitle')}</h2>
                <p className="mt-1 text-sm text-slate-500">{t('admin.settings.metrics.switchHint')}</p>
              </div>
              <div className="flex items-center gap-2">
                {toggling && <span className="h-4 w-4 animate-spin rounded-full border-2 border-slate-200 border-t-primary-500" aria-hidden="true" />}
                <Switch checked={settings.enabled} disabled={toggling} onChange={(v) => void toggle(v)} ariaLabel={t('admin.settings.metrics.switchTitle')} />
              </div>
            </div>

            {settings.enabled && (
              <div className="mt-5 space-y-4 border-t border-slate-100 pt-5" data-testid="metrics-enabled">
                <div>
                  <div className="text-xs font-medium text-slate-500">{t('admin.settings.metrics.endpoint')}</div>
                  <code className="mt-1 block break-all rounded-lg bg-slate-50 px-3 py-2 text-sm text-slate-800">{endpoint}</code>
                </div>
                <div>
                  <div className="text-xs font-medium text-slate-500">{t('admin.settings.metrics.token')}</div>
                  {token ? (
                    <div className="mt-1 space-y-2" data-testid="metrics-token">
                      <div className="flex items-center gap-2">
                        <code className="min-w-0 flex-1 break-all rounded-lg bg-emerald-50 px-3 py-2 text-sm text-emerald-800">{token}</code>
                        <Button size="sm" variant="outline" onClick={() => void copy(token)}>{t('admin.settings.metrics.copy')}</Button>
                      </div>
                      <p className="text-xs text-amber-700">{t('admin.settings.metrics.tokenOnce')}</p>
                    </div>
                  ) : (
                    <p className="mt-1 text-sm text-slate-600">{settings.token_set ? t('admin.settings.metrics.tokenHint', { hint: settings.token_hint }) : '-'}</p>
                  )}
                  <Button size="sm" variant="ghost" className="mt-2" loading={regenerating} onClick={() => void regenerate()} data-testid="metrics-regenerate">
                    <i className="fa-solid fa-rotate" aria-hidden="true" /> {t('admin.settings.metrics.regenerate')}
                  </Button>
                </div>
                <div>
                  <div className="flex items-center justify-between">
                    <div className="text-xs font-medium text-slate-500">{t('admin.settings.metrics.config')}</div>
                    <Button size="sm" variant="ghost" onClick={() => void copy(scrapeConfig)}>{t('admin.settings.metrics.copy')}</Button>
                  </div>
                  <pre className="mt-1 overflow-x-auto rounded-lg bg-slate-900 px-4 py-3 text-xs leading-5 text-slate-100">{scrapeConfig}</pre>
                  <p className="mt-2 text-xs text-slate-400">{t('admin.settings.metrics.clusterHint')}</p>
                </div>
              </div>
            )}
          </section>

          <section className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.metrics.listTitle')}</h2>
            <div className="mt-3 overflow-x-auto">
              <table className="w-full text-left text-sm">
                <tbody className="divide-y divide-slate-100">
                  {METRICS.map(([name, key]) => (
                    <tr key={key}>
                      <td className="py-2 pr-4 align-top font-mono text-xs text-slate-700">{name}</td>
                      <td className="py-2 text-slate-500">{t(`admin.settings.metrics.metric.${key}`)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          <section className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.metrics.previewTitle')}</h2>
                <p className="mt-1 text-sm text-slate-500">{t('admin.settings.metrics.previewHint')}</p>
              </div>
              <Button variant="outline" loading={previewing} onClick={() => void loadPreview()} data-testid="metrics-preview">
                {t(preview === null ? 'admin.settings.metrics.previewLoad' : 'admin.settings.metrics.previewRefresh')}
              </Button>
            </div>
            {preview !== null && (
              <pre className="mt-4 max-h-[480px] overflow-auto rounded-lg bg-slate-50 px-4 py-3 text-[11px] leading-5 text-slate-700" data-testid="metrics-preview-text">{preview}</pre>
            )}
          </section>
        </div>
      )}
    </SettingsLayout>
  )
}
