import { useEffect, useState } from 'react'
import { api, formatNumber } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, Input, Loading, Select, Tooltip, useFeedback } from '@/components/ui'

interface ModelPrice { model: string; input: number; output: number }
interface SeenModel { model: string; kind: 'chat' | 'embed'; calls: number; matched: string }
interface ModelPrices { items: ModelPrice[]; seen: SeenModel[]; currency: string }
interface Row { model: string; input: string; output: string }

const RECALC_DAYS = ['7', '30', '90']

// ModelPricesCard 按模型单价：不同模型单独设置每百万 tokens 的输入/输出单价（可用 * 通配），未设置的模型用默认单价；
// 列出最近调用过的模型及其当前按哪条单价计费，可一键添加；修改后可按新单价重算最近若干天的费用。
export default function ModelPricesCard() {
  const { t } = useTranslation()
  const { showToast, confirmAction } = useFeedback()
  const [data, setData] = useState<ModelPrices | null>(null)
  const [rows, setRows] = useState<Row[]>([])
  const [saving, setSaving] = useState(false)
  const [days, setDays] = useState('30')
  const [recalculating, setRecalculating] = useState(false)

  const apply = (d: ModelPrices) => {
    setData(d)
    setRows(d.items.map((p) => ({ model: p.model, input: String(p.input), output: String(p.output) })))
  }
  useEffect(() => {
    api<ModelPrices>('/admin/ai/model-prices').then(apply).catch((e) => showToast({ message: (e as Error).message, tone: 'error' }))
  }, [showToast])

  const update = (i: number, patch: Partial<Row>) => setRows((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  const has = (model: string) => rows.some((r) => r.model.trim().toLowerCase() === model.toLowerCase())

  async function save() {
    setSaving(true)
    try {
      const items = rows.map((r) => ({ model: r.model.trim(), input: Number(r.input) || 0, output: Number(r.output) || 0 }))
      apply(await api<ModelPrices>('/admin/ai/model-prices', { method: 'PUT', body: { items } }))
      showToast({ message: t('admin.settings.ai.modelPrices.saved'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.settings.ai.modelPrices.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  async function recalculate() {
    if (!(await confirmAction({ title: t('admin.settings.ai.modelPrices.recalcTitle'), message: t('admin.settings.ai.modelPrices.recalcMessage', { days }) }))) return
    setRecalculating(true)
    try {
      const r = await api<{ updated: number }>('/admin/ai/model-prices/recalculate', { method: 'POST', body: { days: Number(days) } })
      showToast({ message: t('admin.settings.ai.modelPrices.recalcDone', { n: r.updated }), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setRecalculating(false)
    }
  }

  const unit = data?.currency || 'USD'
  return (
    <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
      <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.ai.modelPrices.title')}</h2>
      <p className="mt-1 text-sm text-slate-500">{t('admin.settings.ai.modelPrices.description')}</p>
      {!data ? <Loading className="py-8" /> : (
        <>
          <div className="mt-4 overflow-x-auto">
            <table className="w-full min-w-[520px] text-sm">
              <thead className="text-left text-xs text-slate-400">
                <tr>
                  <th className="pb-2 pr-3 font-medium">{t('admin.settings.ai.modelPrices.model')}</th>
                  <th className="w-40 pb-2 pr-3 font-medium">{t('admin.settings.ai.modelPrices.input', { currency: unit })}</th>
                  <th className="w-40 pb-2 pr-3 font-medium">{t('admin.settings.ai.modelPrices.output', { currency: unit })}</th>
                  <th className="w-10 pb-2" />
                </tr>
              </thead>
              <tbody>
                {rows.map((r, i) => (
                  <tr key={i}>
                    <td className="py-1 pr-3"><Input size="sm" value={r.model} maxLength={100} placeholder="gpt-4o-mini*" aria-label={t('admin.settings.ai.modelPrices.model')} onChange={(e) => update(i, { model: e.target.value })} /></td>
                    <td className="py-1 pr-3"><Input size="sm" type="number" min={0} step="0.01" value={r.input} aria-label={t('admin.settings.ai.modelPrices.input', { currency: unit })} onChange={(e) => update(i, { input: e.target.value })} /></td>
                    <td className="py-1 pr-3"><Input size="sm" type="number" min={0} step="0.01" value={r.output} aria-label={t('admin.settings.ai.modelPrices.output', { currency: unit })} onChange={(e) => update(i, { output: e.target.value })} /></td>
                    <td className="py-1">
                      <Tooltip content={t('admin.settings.ai.modelPrices.remove')}>
                        <Button size="sm" variant="ghost" aria-label={t('admin.settings.ai.modelPrices.remove')} onClick={() => setRows((rs) => rs.filter((_, j) => j !== i))}>
                          <i className="fa-solid fa-trash text-slate-400" aria-hidden="true" />
                        </Button>
                      </Tooltip>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {rows.length === 0 && <p className="py-3 text-sm text-slate-400">{t('admin.settings.ai.modelPrices.empty')}</p>}
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <Button size="sm" variant="outline" onClick={() => setRows((rs) => [...rs, { model: '', input: '0', output: '0' }])}>
              <i className="fa-solid fa-plus" aria-hidden="true" />{t('admin.settings.ai.modelPrices.add')}
            </Button>
            <Button size="sm" className="ml-auto" loading={saving} onClick={() => void save()}>{t('admin.settings.ai.modelPrices.save')}</Button>
          </div>
          <p className="mt-2 text-xs text-slate-400">{t('admin.settings.ai.modelPrices.hint')}</p>

          {data.seen.length > 0 && (
            <div className="mt-5">
              <h3 className="text-sm font-medium text-slate-700">{t('admin.settings.ai.modelPrices.seenTitle')}</h3>
              <ul className="mt-2 divide-y divide-slate-100 rounded-xl border border-slate-200 text-sm">
                {data.seen.map((s) => (
                  <li key={`${s.kind}-${s.model}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
                    <span className="font-mono text-slate-700">{s.model}</span>
                    <Badge tone="slate">{t(s.kind === 'embed' ? 'admin.settings.ai.modelPrices.kindEmbed' : 'admin.settings.ai.modelPrices.kindChat')}</Badge>
                    <span className="text-xs text-slate-400">{t('admin.settings.ai.modelPrices.calls', { n: formatNumber(s.calls) })}</span>
                    <span className="ml-auto text-xs text-slate-500">
                      {s.matched ? t('admin.settings.ai.modelPrices.matched', { model: s.matched }) : t('admin.settings.ai.modelPrices.usesDefault')}
                    </span>
                    {!s.matched && !has(s.model) && (
                      <Button size="sm" variant="ghost" onClick={() => setRows((rs) => [...rs, { model: s.model, input: '0', output: '0' }])}>
                        {t('admin.settings.ai.modelPrices.addThis')}
                      </Button>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          )}

          <div className="mt-5 flex flex-wrap items-center gap-2 border-t border-slate-100 pt-4">
            <span className="text-sm text-slate-600">{t('admin.settings.ai.modelPrices.recalcLabel')}</span>
            <Select size="sm" className="w-28" value={days} onChange={setDays}
              options={RECALC_DAYS.map((d) => ({ value: d, label: t('admin.settings.ai.modelPrices.days', { n: d }) }))} />
            <Button size="sm" variant="outline" loading={recalculating} onClick={() => void recalculate()}>{t('admin.settings.ai.modelPrices.recalc')}</Button>
          </div>
        </>
      )}
    </div>
  )
}
