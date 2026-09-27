import { useCallback, useEffect, useState } from 'react'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { durationLabel, formatPrice } from '@/lib/commerce'
import type { MembershipPlan } from '@/lib/membership'
import { Card, Select, Switch, useFeedback } from '@/components/ui'

interface RenewalState {
  enabled: boolean
  price_id: number
  mode: 'auto' | 'notify'
  next_renew_at?: string
  last_error: string
}

// RenewalCard 自动续费：为当前方案选一档时长开启续费计划。支付方式支持自动扣款时到期前自动续期，否则到期前提醒一键续费。
export default function RenewalCard({ plan, currency, onChanged }: { plan: MembershipPlan; currency: string; onChanged?: () => void }) {
  const { t, locale } = useTranslation()
  const { showToast } = useFeedback()
  const [state, setState] = useState<RenewalState | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    api<RenewalState>('/users/me/membership/renewal').then(setState).catch(() => {})
  }, [])
  useEffect(() => { load() }, [load])

  async function save(enabled: boolean, priceID: number) {
    setSaving(true)
    try {
      setState(await api<RenewalState>('/users/me/membership/renewal', { method: 'PUT', body: { enabled, price_id: priceID } }))
      showToast({ message: enabled ? t('membership.renewal.enabled') : t('membership.renewal.disabled'), tone: 'success' })
      onChanged?.()
    } catch (e) {
      showToast({ title: t('membership.renewal.failed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  if (!state || plan.prices.length === 0) return null
  const priceID = plan.prices.some((p) => p.id === state.price_id) ? state.price_id : plan.prices[0].id
  return (
    <Card className="mt-4 p-5">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="text-sm font-semibold text-slate-900"><i className="fa-solid fa-rotate mr-1.5 text-primary-500" aria-hidden="true" />{t('membership.renewal.title')}</div>
          <p className="mt-1 text-xs text-slate-500">{state.mode === 'auto' ? t('membership.renewal.hintAuto') : t('membership.renewal.hintNotify')}</p>
        </div>
        <Switch checked={state.enabled} disabled={saving} onChange={(v) => void save(v, priceID)} ariaLabel={t('membership.renewal.title')} />
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <div className="w-56 max-w-full">
          <Select value={String(priceID)} disabled={saving} onChange={(v) => { if (state.enabled) void save(true, Number(v)); else setState({ ...state, price_id: Number(v) }) }}
            options={plan.prices.map((p) => ({ value: String(p.id), label: `${durationLabel(t, p.duration_days)} · ${formatPrice(p.price_cents, currency, locale)}` }))} />
        </div>
        {state.enabled && state.next_renew_at && (
          <span className="text-xs text-slate-500">
            {state.mode === 'auto' ? t('membership.renewal.nextAuto', { date: formatDate(state.next_renew_at) }) : t('membership.renewal.nextNotify', { date: formatDate(state.next_renew_at) })}
          </span>
        )}
      </div>
      {state.enabled && state.last_error && <p className="mt-2 text-xs text-rose-600">{t('membership.renewal.lastError', { error: state.last_error })}</p>}
    </Card>
  )
}
