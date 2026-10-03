import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/router'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import UserAvatar from '@/components/UserAvatar'
import type { UserLite } from '@/components/UserSearchSelect'
import { api, formatDate } from '@/lib/api'
import { Badge, Button, Card, EmptyState, Field, Input, Loading, Pagination, SegmentedTabs, Select, Switch, useFeedback } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'
import { formatPrice } from '@/lib/commerce'
import { centsFromInput, inputFromCents } from '@/lib/membership'
import { displayName } from '@/lib/users'
import { useUrlPage } from '@/lib/use-url-page'

type Tab = 'sales' | 'withdrawals' | 'settings'
const TABS: Tab[] = ['sales', 'withdrawals', 'settings']

export default function AdminPaid() {
  return <FeatureGate feature="paid-content"><AdminPaidInner /></FeatureGate>
}

function AdminPaidInner() {
  const { t } = useTranslation()
  const router = useRouter()
  const tab: Tab = TABS.includes(router.query.tab as Tab) ? (router.query.tab as Tab) : 'sales' // tab 由 URL 驱动
  return (
    <AdminLayout current="paid" breadcrumb={t('admin.nav.paid')}>
      <div>
        <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.paid')}</h1>
        <p className="mt-1.5 text-sm text-slate-500">{t('admin.paid.description')}</p>
      </div>
      <SegmentedTabs className="mt-6" value={tab} ariaLabel={t('admin.nav.paid')}
        items={TABS.map((key) => ({ value: key, label: t(`admin.paid.tab.${key}`), href: `/admin/paid?tab=${key}` }))} />
      <div className="mt-6">
        {tab === 'sales' && <SalesPanel />}
        {tab === 'withdrawals' && <WithdrawalsPanel />}
        {tab === 'settings' && <SettingsPanel />}
      </div>
    </AdminLayout>
  )
}

interface SaleItem { entry: { id: number; title: string; gross_cents: number; commission_cents: number; net_cents: number; currency: string; order_no: string; created_at: string }; author?: UserLite; buyer?: UserLite }

function SalesPanel() {
  const { t, locale } = useTranslation()
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<{ items: SaleItem[]; total: number; page: number; page_size: number; gross_cents: number; commission_cents: number; currency: string } | null>(null)
  useEffect(() => {
    api<{ items: SaleItem[]; total: number; page: number; page_size: number; gross_cents: number; commission_cents: number; currency: string }>('/admin/paid/sales', { params: { page, page_size: 20 } }).then(setData).catch(() => {})
  }, [page])
  if (!data) return <Loading className="py-16" />
  const money = (c: number, cur = data.currency) => formatPrice(c, cur, locale)
  return (
    <>
      <div className="mb-4 grid gap-4 sm:grid-cols-2">
        <Card className="p-5"><div className="text-xs text-slate-400">{t('admin.paid.gross')}</div><div className="mt-1 text-2xl font-bold tabular-nums">{money(data.gross_cents)}</div></Card>
        <Card className="p-5"><div className="text-xs text-slate-400">{t('admin.paid.commission')}</div><div className="mt-1 text-2xl font-bold tabular-nums">{money(data.commission_cents)}</div></Card>
      </div>
      {data.items.length === 0 ? <EmptyState>{t('admin.paid.noSales')}</EmptyState> : (
        <Card className="overflow-x-auto">
          <table className="w-full min-w-[760px] text-sm">
            <thead className="bg-slate-50 text-left text-xs text-slate-500">
              <tr><th className="px-4 py-3">{t('admin.paid.col.item')}</th><th className="px-4 py-3">{t('admin.paid.col.author')}</th><th className="px-4 py-3">{t('admin.paid.col.buyer')}</th>
                <th className="px-4 py-3 text-right">{t('admin.paid.col.amount')}</th><th className="px-4 py-3 text-right">{t('admin.paid.col.commission')}</th><th className="px-4 py-3">{t('admin.paid.col.time')}</th></tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {data.items.map(({ entry: e, author, buyer }) => (
                <tr key={e.id}>
                  <td className="px-4 py-3"><div className="text-slate-800">{e.title}</div><div className="text-xs text-slate-400">{e.order_no}</div></td>
                  <td className="px-4 py-3 text-slate-600">{displayName(author)}</td>
                  <td className="px-4 py-3 text-slate-600">{displayName(buyer)}</td>
                  <td className="px-4 py-3 text-right tabular-nums">{money(e.gross_cents, e.currency)}</td>
                  <td className="px-4 py-3 text-right tabular-nums text-slate-500">{money(e.commission_cents, e.currency)}</td>
                  <td className="whitespace-nowrap px-4 py-3 text-xs text-slate-500">{formatDate(e.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
      {data.total > data.page_size && <div className="mt-4"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
    </>
  )
}

interface WItem { withdrawal: { id: number; amount_cents: number; currency: string; account: string; status: 'pending' | 'paid' | 'rejected'; admin_note: string; created_at: string }; author?: UserLite; balance_cents: number }

function WithdrawalsPanel() {
  const { t, locale } = useTranslation()
  const { showToast, requestInput, confirmAction } = useFeedback()
  const [status, setStatus] = useState('pending')
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<{ items: WItem[]; total: number; page: number; page_size: number } | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const load = useCallback(() => {
    api<{ items: WItem[]; total: number; page: number; page_size: number }>('/admin/paid/withdrawals', { params: { status, page, page_size: 20 } }).then(setData).catch(() => {})
  }, [status, page])
  useEffect(() => { load() }, [load])

  async function process(item: WItem, pay: boolean) {
    const amount = formatPrice(item.withdrawal.amount_cents, item.withdrawal.currency, locale)
    let note = ''
    if (pay) {
      if (!(await confirmAction({ title: t('admin.paid.payTitle'), message: t('admin.paid.payMessage', { amount, user: item.author?.username || '' }), confirmLabel: t('admin.paid.pay') }))) return
    } else {
      const input = await requestInput({ title: t('admin.paid.rejectTitle'), label: t('admin.paid.rejectNote'), confirmLabel: t('admin.paid.reject') })
      if (input === null) return
      note = input.trim()
      if (!note) { showToast({ message: t('admin.paid.noteRequired'), tone: 'error' }); return }
    }
    setBusy(`${item.withdrawal.id}:${pay}`)
    try {
      await api(`/admin/paid/withdrawals/${item.withdrawal.id}/${pay ? 'pay' : 'reject'}`, { method: 'POST', body: { note } })
      showToast({ message: t(pay ? 'admin.paid.paid' : 'admin.paid.rejected'), tone: 'success' })
      load()
    } catch (e) { showToast({ title: t('admin.paid.opFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setBusy(null) }
  }

  return (
    <>
      <div className="mb-4 w-40"><Select value={status} onChange={(v) => { setStatus(v); setPage(1) }}
        options={(['pending', 'paid', 'rejected'] as const).map((s) => ({ value: s, label: t(`paid.withdrawal.status.${s}`) }))} /></div>
      {data === null ? <Loading className="py-16" /> : data.items.length === 0 ? <EmptyState>{t('admin.paid.noWithdrawals')}</EmptyState> : (
        <div className="space-y-3">
          {data.items.map((item) => {
            const w = item.withdrawal
            return (
              <Card key={w.id} className="flex flex-wrap items-start justify-between gap-4 p-4">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    {item.author && <UserAvatar user={item.author} />}
                    <span className="font-medium text-slate-800">{displayName(item.author)}</span>
                    <span className="text-lg font-bold tabular-nums">{formatPrice(w.amount_cents, w.currency, locale)}</span>
                    <Badge tone={w.status === 'pending' ? 'amber' : w.status === 'paid' ? 'emerald' : 'rose'}>{t(`paid.withdrawal.status.${w.status}`)}</Badge>
                  </div>
                  <div className="mt-2 whitespace-pre-wrap rounded-lg bg-slate-50 px-3 py-2 text-xs text-slate-600 [overflow-wrap:anywhere]">{w.account}</div>
                  <div className="mt-1 text-xs text-slate-400">{formatDate(w.created_at)} · {t('admin.paid.balanceAfter', { amount: formatPrice(item.balance_cents, w.currency, locale) })}</div>
                  {w.admin_note && <div className="mt-1 text-xs text-slate-500">{t('paid.earnings.adminNote')}：{w.admin_note}</div>}
                </div>
                {w.status === 'pending' && (
                  <span className="flex shrink-0 gap-2">
                    <Button size="sm" loading={busy === `${w.id}:true`} disabled={!!busy} onClick={() => process(item, true)}>{t('admin.paid.pay')}</Button>
                    <Button size="sm" variant="outline" className="text-rose-600" loading={busy === `${w.id}:false`} disabled={!!busy} onClick={() => process(item, false)}>{t('admin.paid.reject')}</Button>
                  </span>
                )}
              </Card>
            )
          })}
        </div>
      )}
      {data && data.total > data.page_size && <div className="mt-4"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
    </>
  )
}

interface Settings { currency: string; commission_percent: number; min_withdrawal_cents: number; max_price_cents: number; allow_authors: boolean; upgrade_link: string }

function SettingsPanel() {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [form, setForm] = useState<{ currency: string; commission: number; min: string; max: string; allowAuthors: boolean; upgradeLink: string } | null>(null)
  const [saving, setSaving] = useState(false)
  function apply(s: Settings) {
    setForm({ currency: s.currency, commission: s.commission_percent, min: inputFromCents(s.min_withdrawal_cents), max: inputFromCents(s.max_price_cents), allowAuthors: s.allow_authors, upgradeLink: s.upgrade_link })
  }
  useEffect(() => { api<Settings>('/admin/paid/settings').then(apply).catch(() => {}) }, [])
  async function save() {
    if (!form) return
    setSaving(true)
    try {
      apply(await api<Settings>('/admin/paid/settings', { method: 'PUT', body: {
        currency: form.currency, commission_percent: form.commission, min_withdrawal_cents: centsFromInput(form.min), max_price_cents: centsFromInput(form.max),
        allow_authors: form.allowAuthors, upgrade_link: form.upgradeLink,
      } }))
      showToast({ message: t('admin.paid.settings.saved'), tone: 'success' })
    } catch (e) { showToast({ title: t('admin.paid.opFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setSaving(false) }
  }
  if (!form) return <Loading className="py-16" />
  return (
    <Card className="max-w-2xl space-y-4 p-6">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t('admin.paid.settings.currency')}><Input value={form.currency} maxLength={3} onChange={(e) => setForm({ ...form, currency: e.target.value.toUpperCase() })} /></Field>
        <Field label={t('admin.paid.settings.commission')} hint={t('admin.paid.settings.commissionHint')}>
          <Input type="number" min={0} max={90} value={form.commission} onChange={(e) => setForm({ ...form, commission: Math.max(0, Math.min(90, Number(e.target.value) || 0)) })} trailing={<span className="text-xs text-slate-400">%</span>} />
        </Field>
        <Field label={t('admin.paid.settings.minWithdrawal')}><Input type="number" min={0} step="0.01" value={form.min} onChange={(e) => setForm({ ...form, min: e.target.value })} trailing={<span className="text-xs text-slate-400">{form.currency}</span>} /></Field>
        <Field label={t('admin.paid.settings.maxPrice')}><Input type="number" min={0} step="0.01" value={form.max} onChange={(e) => setForm({ ...form, max: e.target.value })} trailing={<span className="text-xs text-slate-400">{form.currency}</span>} /></Field>
      </div>
      <Field label={t('admin.paid.settings.upgradeLink')} hint={t('admin.paid.settings.upgradeLinkHint')}>
        <Input value={form.upgradeLink} placeholder="/user/membership" onChange={(e) => setForm({ ...form, upgradeLink: e.target.value })} />
      </Field>
      <label className="flex items-center gap-2 text-sm text-slate-600">
        <Switch checked={form.allowAuthors} onChange={(v) => setForm({ ...form, allowAuthors: v })} ariaLabel={t('admin.paid.settings.allowAuthors')} />{t('admin.paid.settings.allowAuthors')}
      </label>
      <div className="flex justify-end"><Button loading={saving} onClick={save}>{t('common.actions.save')}</Button></div>
    </Card>
  )
}
