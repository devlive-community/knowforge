import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/router'
import Container from '@/components/Container'
import Seo from '@/components/Seo'
import { api, formatDate } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { aiFeatureLabel, formatCost, formatTokens, type AIUsageRef, type MyAIUsage } from '@/lib/ai-usage'
import AIUsageRefLink from '@/components/ai/AIUsageRefLink'
import { dateStamp, downloadAuthed } from '@/lib/download'
import { Badge, Button, Card, EmptyState, Loading, Pagination, Select, Tooltip, useFeedback } from '@/components/ui'
import { useUrlPage } from '@/lib/use-url-page'

interface CallView {
  id: number
  feature: string
  kind: 'chat' | 'embed' | 'translate' | 'tts'
  model: string
  input_tokens: number
  output_tokens: number
  characters: number
  estimated: boolean
  duration_ms: number
  status: 'ok' | 'error'
  created_at: string
  cost_micros?: number // 站点允许展示估算费用时返回
}

interface TraceView {
  trace_id: string
  feature: string
  ref_type: string
  ref_id: number
  started_at: string
  ended_at: string
  calls: number
  errors: number
  input_tokens: number
  output_tokens: number
  characters: number
  duration_ms: number
  cost_micros?: number
  items: CallView[]
  ref: AIUsageRef | null
}

function secs(ms: number) {
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`
}

// 我的 AI 用量：本月 tokens / 翻译字数与额度、每日用量、按功能分布，以及按调用链分组的全部调用记录（?trace= 定位到某条链）
export default function MyAIUsagePage() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  const [usage, setUsage] = useState<MyAIUsage | null>(null)

  useEffect(() => {
    if (!user) return
    api<MyAIUsage>('/users/me/ai-usage').then(setUsage).catch(() => {})
  }, [user])

  if (!user) return <Loading className="min-h-[60vh]" />
  const maxDay = Math.max(1, ...(usage?.daily || []).map((d) => d.tokens))
  return (
    <>
      <Seo siteName={site.site_name || 'KnowForge'} title={t('aiUsage.mine.title')} noindex />
      <Container>
        <div className="py-8">
          <h1 className="text-2xl font-bold text-ink">{t('aiUsage.mine.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('aiUsage.mine.subtitle')}</p>

          {!usage ? <Loading className="py-12" /> : (
            <>
              <div className={`mt-6 grid gap-4 ${usage.show_cost ? 'sm:grid-cols-2 lg:grid-cols-4' : 'sm:grid-cols-3'}`}>
                <Quota label={t('aiUsage.mine.monthTokens')} used={usage.used_tokens} limit={usage.limit} unit="tokens" />
                <Quota label={t('aiUsage.mine.monthChars')} used={usage.translate_chars} limit={usage.translate_limit} unit="chars" />
                <Card className="p-4">
                  <div className="text-sm text-slate-500">{t('aiUsage.mine.monthCalls')}</div>
                  <div className="mt-1.5 text-lg font-semibold tabular-nums text-slate-900">{usage.calls.toLocaleString()}</div>
                  {usage.by_feature.length > 0 && <div className="mt-1.5 truncate text-xs text-slate-400">{usage.by_feature.map((f) => `${aiFeatureLabel(t, f.feature)} ${formatTokens(f.tokens)}`).join(' · ')}</div>}
                </Card>
                {usage.show_cost && (
                  <Card className="p-4" data-testid="ai-usage-cost">
                    <div className="flex items-center gap-1 text-sm text-slate-500">
                      {t('aiUsage.mine.monthCost')}
                      <Tooltip content={t('aiUsage.mine.costHint')}><i className="fa-regular fa-circle-question text-xs text-slate-400" aria-hidden="true" /></Tooltip>
                    </div>
                    <div className="mt-1.5 text-lg font-semibold tabular-nums text-slate-900">≈ {formatCost(usage.cost_micros, usage.currency || 'USD')}</div>
                    {usage.by_feature.some((f) => f.cost_micros) && (
                      <div className="mt-1.5 truncate text-xs text-slate-400">
                        {usage.by_feature.filter((f) => f.cost_micros).map((f) => `${aiFeatureLabel(t, f.feature)} ${formatCost(f.cost_micros, usage.currency || 'USD')}`).join(' · ')}
                      </div>
                    )}
                  </Card>
                )}
              </div>
              <p className="mt-2 text-xs text-slate-400">{t('aiUsage.mine.quotaSource')}</p>

              <Card className="mt-4 p-5">
                <h2 className="text-sm font-semibold text-slate-900">{t('aiUsage.mine.dailyTitle')}</h2>
                <div className="mt-3 flex h-28 items-end gap-[2px]">
                  {usage.daily.map((d) => (
                    <Tooltip className="min-w-0 flex-1" key={d.date} content={t('aiUsage.mine.dayTip', { date: d.date, tokens: formatTokens(d.tokens), chars: d.characters })}>
                      <div className="flex h-28 w-full items-end">
                        <div className={`w-full rounded-t ${d.tokens ? 'bg-primary-400' : 'bg-slate-100'}`} style={{ height: `${Math.max(2, (d.tokens / maxDay) * 100)}%` }} />
                      </div>
                    </Tooltip>
                  ))}
                </div>
                <div className="mt-2 flex justify-between text-xs text-slate-400">
                  <span>{usage.daily[0]?.date}</span><span>{usage.daily[usage.daily.length - 1]?.date}</span>
                </div>
              </Card>
            </>
          )}

          <TraceList features={usage?.by_feature.map((f) => f.feature) || []} />
        </div>
      </Container>
    </>
  )
}

function Quota({ label, used, limit, unit }: { label: string; used: number; limit: number; unit: 'tokens' | 'chars' }) {
  const { t } = useTranslation()
  const pct = limit > 0 ? Math.min(100, (used / limit) * 100) : undefined
  return (
    <Card className="p-4">
      <div className="text-sm text-slate-500">{label}</div>
      <div className="mt-1.5 text-lg font-semibold tabular-nums text-slate-900">
        {limit < 0 ? t(`aiUsage.mine.${unit}Unlimited`, { used: formatTokens(used) }) : t(`aiUsage.mine.${unit}Of`, { used: formatTokens(used), limit: formatTokens(limit) })}
      </div>
      {pct !== undefined && (
        <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-slate-100">
          <div className={`h-full rounded-full ${pct >= 90 ? 'bg-rose-500' : 'bg-primary-500'}`} style={{ width: `${pct}%` }} />
        </div>
      )}
    </Card>
  )
}

function TraceList({ features }: { features: string[] }) {
  const { t } = useTranslation()
  const router = useRouter()
  const { showToast } = useFeedback()
  const trace = typeof router.query.trace === 'string' ? router.query.trace : '' // 由问答等页面跳转定位到某条调用链
  const [feature, setFeature] = useState('')
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<{ items: TraceView[]; total: number; page: number; page_size: number; currency?: string } | null>(null)
  const [exporting, setExporting] = useState(false)

  async function exportCSV() {
    setExporting(true)
    try {
      await downloadAuthed('/users/me/ai-usage/export', { feature }, `my-ai-usage-${dateStamp()}.csv`)
    } catch (e) {
      showToast({ title: t('aiUsage.mine.exportFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setExporting(false)
    }
  }

  const load = useCallback(async () => {
    try {
      setData(await api('/users/me/ai-usage/logs', { params: { feature, trace_id: trace, page, page_size: 15 } }))
    } catch (e) {
      showToast({ title: t('aiUsage.mine.loadFailed'), message: (e as Error).message, tone: 'error' })
    }
  }, [feature, trace, page, showToast, t])
  useEffect(() => { if (router.isReady) void load() }, [router.isReady, load])

  return (
    <Card className="mt-4 p-5">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="mr-auto text-sm font-semibold text-slate-900">{t('aiUsage.mine.logsTitle')}</h2>
        <Button size="sm" variant="outline" loading={exporting} onClick={() => void exportCSV()}><i className="fa-solid fa-file-csv" aria-hidden="true" />{t('aiUsage.mine.export')}</Button>
        {trace ? (
          <Button size="sm" variant="outline" onClick={() => router.push('/user/ai-usage', undefined, { shallow: true })}>
            <i className="fa-solid fa-xmark" aria-hidden="true" />{t('aiUsage.mine.clearTrace')}
          </Button>
        ) : (
          <Select size="sm" className="w-44" value={feature} onChange={(v) => { setFeature(v); setPage(1) }}
            options={[{ value: '', label: t('aiUsage.mine.allFeatures') }, ...features.map((f) => ({ value: f, label: aiFeatureLabel(t, f) }))]} />
        )}
      </div>
      <p className="mt-1 text-xs text-slate-400">{t('aiUsage.mine.logsHint')}</p>
      {!data ? <Loading className="py-8" /> : data.items.length === 0 ? <div className="mt-4"><EmptyState>{t('aiUsage.mine.empty')}</EmptyState></div> : (
        <ul className="mt-4 space-y-2">
          {data.items.map((tr) => <TraceItem key={tr.trace_id} trace={tr} currency={data.currency || 'USD'} defaultOpen={Boolean(trace)} />)}
        </ul>
      )}
      {data && data.total > data.page_size && <div className="mt-4"><Pagination size="sm" page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
    </Card>
  )
}

function TraceItem({ trace: tr, currency, defaultOpen }: { trace: TraceView; currency: string; defaultOpen: boolean }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(defaultOpen)
  const tokens = tr.input_tokens + tr.output_tokens
  return (
    <li className="rounded-xl border border-slate-200">
      <button type="button" onClick={() => setOpen(!open)} className="flex w-full flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3 text-left text-sm hover:bg-slate-50">
        <i className={`fa-solid fa-chevron-${open ? 'down' : 'right'} w-3 text-xs text-slate-400`} aria-hidden="true" />
        <span className="font-medium text-slate-800">{aiFeatureLabel(t, tr.feature)}</span>
        <span className="text-xs text-slate-400">{formatDate(tr.started_at)}</span>
        {tr.errors > 0 && <Badge tone="rose">{t('aiUsage.mine.errors', { n: tr.errors })}</Badge>}
        <span className="ml-auto flex flex-wrap items-center gap-x-3 text-xs tabular-nums text-slate-500">
          <span>{t('aiUsage.mine.calls', { n: tr.calls })}</span>
          {tokens > 0 && <span>{t('aiUsage.mine.tokensInOut', { input: formatTokens(tr.input_tokens), output: formatTokens(tr.output_tokens) })}</span>}
          {tr.characters > 0 && <span>{t('aiUsage.mine.chars', { n: tr.characters.toLocaleString() })}</span>}
          <span>{secs(tr.duration_ms)}</span>
          {tr.cost_micros !== undefined && <span className="font-medium text-slate-700">≈ {formatCost(tr.cost_micros, currency)}</span>}
        </span>
      </button>
      {tr.ref && <div className="-mt-1.5 px-4 pb-2.5 pl-10"><AIUsageRefLink refInfo={tr.ref} /></div>}
      {open && (
        <ol className="space-y-1.5 border-t border-slate-100 px-4 py-3 text-xs">
          {tr.items.map((c, i) => (
            <li key={c.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md bg-slate-50 px-3 py-2">
              <span className="w-5 text-right tabular-nums text-slate-400">{i + 1}</span>
              <span className="font-medium text-slate-700">{t(`aiUsage.mine.kind.${c.kind}`)}</span>
              <span className="text-slate-500">{c.model || '-'}</span>
              <span className="text-slate-400">{formatDate(c.created_at)}</span>
              <span className="ml-auto flex items-center gap-x-3 tabular-nums text-slate-600">
                {c.kind === 'translate' || c.kind === 'tts'
                  ? <span>{t('aiUsage.mine.chars', { n: c.characters.toLocaleString() })}</span>
                  : <span>{c.estimated ? '≈' : ''}{t('aiUsage.mine.tokensInOut', { input: formatTokens(c.input_tokens), output: formatTokens(c.output_tokens) })}</span>}
                <span>{secs(c.duration_ms)}</span>
                {c.cost_micros !== undefined && <span>≈ {formatCost(c.cost_micros, currency)}</span>}
                {c.status === 'ok' ? <Badge tone="emerald">{t('aiUsage.mine.ok')}</Badge> : <Badge tone="rose">{t('aiUsage.mine.failed')}</Badge>}
              </span>
            </li>
          ))}
        </ol>
      )}
    </li>
  )
}
