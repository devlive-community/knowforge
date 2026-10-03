import { useCallback, useEffect, useState } from 'react'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import Seo from '@/components/Seo'
import { api, formatDate } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, Card, EmptyState, Field, Input, Loading, Pagination, Textarea, useFeedback } from '@/components/ui'
import { formatPrice } from '@/lib/commerce'
import { centsFromInput } from '@/lib/membership'
import { useUrlPage } from '@/lib/use-url-page'

interface Ledger { id: number; kind: 'sale' | 'withdrawal' | 'withdrawal_revert' | 'refund'; title: string; gross_cents: number; commission_cents: number; net_cents: number; currency: string; created_at: string }
interface Withdrawal { id: number; amount_cents: number; currency: string; account: string; status: 'pending' | 'paid' | 'rejected'; admin_note: string; created_at: string; processed_at?: string | null }
interface Earnings {
  balance_cents: number; currency: string; min_withdrawal_cents: number; commission_percent: number
  total_gross_cents: number; total_commission_cents: number; total_net_cents: number
  ledger: { items: Ledger[]; total: number; page: number; page_size: number }
  withdrawals: Withdrawal[]
}

const W_TONE = { pending: 'amber', paid: 'emerald', rejected: 'rose' } as const

export default function MyEarningsPage() {
  return <FeatureGate feature="paid-content"><MyEarningsInner /></FeatureGate>
}

// 我的收益（作者）：可提现余额、累计销售与平台抽成、收益流水，申请提现（管理员线下打款后确认）。
function MyEarningsInner() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t, locale } = useTranslation()
  const { showToast } = useFeedback()
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<Earnings | null>(null)
  const [amount, setAmount] = useState('')
  const [account, setAccount] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const load = useCallback(() => {
    api<Earnings>('/users/me/earnings', { params: { page, page_size: 20 } }).then(setData).catch(() => {})
  }, [page])
  useEffect(() => { if (user) load() }, [user, load])

  async function withdraw() {
    setSubmitting(true)
    try {
      await api('/users/me/withdrawals', { method: 'POST', body: { amount_cents: centsFromInput(amount), account } })
      showToast({ message: t('paid.earnings.requested'), tone: 'success' })
      setAmount('')
      load()
    } catch (e) { showToast({ title: t('paid.earnings.requestFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setSubmitting(false) }
  }

  if (!user || !data) return <Loading className="min-h-[60vh]" />
  const money = (c: number) => formatPrice(c, data.currency, locale)
  const pending = data.withdrawals.some((w) => w.status === 'pending')
  return (
    <>
      <Seo siteName={site.site_name || 'KnowForge'} title={t('paid.earnings.title')} noindex />
      <Container>
        <div className="py-8">
          <h1 className="text-2xl font-bold text-ink">{t('paid.earnings.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('paid.earnings.subtitle', { n: data.commission_percent })}</p>
          <div className="mt-6 grid gap-4 sm:grid-cols-3">
            {([['balance', data.balance_cents], ['totalNet', data.total_net_cents], ['totalCommission', data.total_commission_cents]] as const).map(([key, v]) => (
              <Card key={key} className="p-5">
                <div className="text-xs text-slate-400">{t(`paid.earnings.${key}`)}</div>
                <div className="mt-1 text-2xl font-bold tabular-nums text-slate-900">{money(v)}</div>
              </Card>
            ))}
          </div>
          {data.balance_cents < 0 && <p className="mt-3 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-800">{t('paid.earnings.negativeHint')}</p>}
          <div className="mt-6 grid gap-6 lg:grid-cols-[1fr_22rem]">
            <Card className="p-5">
              <h2 className="font-bold text-slate-900">{t('paid.earnings.ledger')}</h2>
              {data.ledger.items.length === 0 ? <div className="mt-3"><EmptyState>{t('paid.earnings.empty')}</EmptyState></div> : (
                <ul className="mt-3 divide-y divide-slate-100">
                  {data.ledger.items.map((l) => (
                    <li key={l.id} className="flex flex-wrap items-center justify-between gap-2 py-2.5 text-sm">
                      <span className="min-w-0">
                        <span className="text-slate-800">{l.kind === 'sale' ? l.title : l.kind === 'refund' ? t('paid.earnings.refundOf', { title: l.title }) : t(`paid.earnings.kind.${l.kind}`)}</span>
                        {l.kind === 'sale' && <span className="ml-2 text-xs text-slate-400">{t('paid.earnings.saleDetail', { gross: money(l.gross_cents), fee: money(l.commission_cents) })}</span>}
                      </span>
                      <span className="flex items-center gap-3">
                        <span className={`font-medium tabular-nums ${l.net_cents >= 0 ? 'text-emerald-600' : 'text-slate-500'}`}>{l.net_cents >= 0 ? '+' : ''}{money(l.net_cents)}</span>
                        <span className="text-xs text-slate-400">{formatDate(l.created_at)}</span>
                      </span>
                    </li>
                  ))}
                </ul>
              )}
              {data.ledger.total > data.ledger.page_size && <div className="mt-4"><Pagination page={data.ledger.page} pageSize={data.ledger.page_size} total={data.ledger.total} onChange={setPage} /></div>}
            </Card>
            <div className="space-y-6">
              <Card className="space-y-3 p-5">
                <h2 className="font-bold text-slate-900">{t('paid.earnings.withdraw')}</h2>
                <p className="text-xs text-slate-400">{t('paid.earnings.withdrawHint', { min: money(data.min_withdrawal_cents) })}</p>
                <Field label={t('paid.earnings.amount')}>
                  <Input type="number" min={0} step="0.01" value={amount} onChange={(e) => setAmount(e.target.value)} trailing={<span className="text-xs text-slate-400">{data.currency}</span>} />
                </Field>
                <Field label={t('paid.earnings.account')} hint={t('paid.earnings.accountHint')}>
                  <Textarea rows={3} maxLength={500} value={account} onChange={(e) => setAccount(e.target.value)} />
                </Field>
                <div className="flex justify-end">
                  <Button loading={submitting} disabled={pending || !amount || !account.trim()} onClick={withdraw}>{pending ? t('paid.earnings.pendingExists') : t('paid.earnings.submit')}</Button>
                </div>
              </Card>
              {data.withdrawals.length > 0 && (
                <Card className="p-5">
                  <h2 className="font-bold text-slate-900">{t('paid.earnings.withdrawals')}</h2>
                  <ul className="mt-3 space-y-2">
                    {data.withdrawals.map((w) => (
                      <li key={w.id} className="rounded-lg bg-slate-50 px-3 py-2 text-xs">
                        <div className="flex items-center justify-between gap-2">
                          <span className="font-medium tabular-nums text-slate-800">{formatPrice(w.amount_cents, w.currency, locale)}</span>
                          <Badge tone={W_TONE[w.status]}>{t(`paid.withdrawal.status.${w.status}`)}</Badge>
                        </div>
                        <div className="mt-1 text-slate-400">{formatDate(w.created_at)}</div>
                        {w.admin_note && <div className="mt-1 text-slate-600">{t('paid.earnings.adminNote')}：{w.admin_note}</div>}
                      </li>
                    ))}
                  </ul>
                </Card>
              )}
            </div>
          </div>
        </div>
      </Container>
    </>
  )
}
