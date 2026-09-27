import { useCallback, useEffect, useState } from 'react'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { formatPrice } from '@/lib/commerce'
import { centsFromInput, type MembershipPlan } from '@/lib/membership'
import UserAvatar from '@/components/UserAvatar'
import type { UserLite } from '@/components/UserSearchSelect'
import { Badge, Button, Card, Checkbox, DateTimePicker, EmptyState, Field, Input, Loading, Modal, Pagination, SegmentedTabs, Switch, useFeedback } from '@/components/ui'

interface Coupon {
  id: number
  code: string
  name: string
  type: 'percent' | 'amount'
  percent_off: number
  amount_off_cents: number
  min_amount_cents: number
  currency: string
  plan_ids: number[] | null
  include_gifts: boolean
  new_members_only: boolean
  max_uses: number
  per_user_limit: number
  starts_at: string | null
  expires_at: string | null
  status: 'active' | 'disabled'
  used: number
  reserved: number
}
interface CouponUse { use: { id: number; order_no: string; discount_cents: number; status: 'reserved' | 'used' | 'released'; created_at: string }; user?: UserLite }

const useTone = { reserved: 'amber', used: 'emerald', released: 'slate' } as const

// CouponPanel 会员优惠券：折扣或满减优惠码，用户在结算页输入；可限定方案、礼品卡、首次开通、次数与时间。
export default function CouponPanel({ plans }: { plans: { plan: MembershipPlan }[] }) {
  const { t, locale } = useTranslation()
  const { showToast } = useFeedback()
  const [page, setPage] = useState(1)
  const [data, setData] = useState<{ items: Coupon[]; total: number; page: number; page_size: number; currency: string } | null>(null)
  const [creating, setCreating] = useState(false)
  const [viewing, setViewing] = useState<Coupon | null>(null)
  const [busy, setBusy] = useState<number | null>(null)

  const load = useCallback(() => {
    api<{ items: Coupon[]; total: number; page: number; page_size: number; currency: string }>('/admin/membership/coupons', { params: { page } })
      .then(setData)
      .catch((e) => showToast({ title: t('admin.membership.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [page, showToast, t])
  useEffect(() => { load() }, [load])

  async function toggle(c: Coupon) {
    setBusy(c.id)
    try {
      const next = await api<Coupon>(`/admin/membership/coupons/${c.id}`, { method: 'PUT', body: { status: c.status === 'active' ? 'disabled' : 'active' } })
      setData((d) => (d ? { ...d, items: d.items.map((it) => (it.id === c.id ? next : it)) } : d))
    } catch (e) { showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setBusy(null) }
  }

  async function copy(text: string) {
    try { await navigator.clipboard.writeText(text); showToast({ message: t('admin.membership.redeem.copied'), tone: 'success' }) }
    catch { showToast({ message: t('admin.membership.redeem.copyFailed'), tone: 'error' }) }
  }

  const planName = (id: number) => plans.find(({ plan }) => plan.id === id)?.plan.name || `#${id}`
  function describe(c: Coupon) {
    const off = c.type === 'percent'
      ? t('admin.membership.coupon.percentSummary', { percent: c.percent_off })
      : t('admin.membership.coupon.amountSummary', { min: formatPrice(c.min_amount_cents, c.currency, locale), off: formatPrice(c.amount_off_cents, c.currency, locale) })
    const parts = [off]
    if (c.type === 'percent' && c.min_amount_cents > 0) parts.push(t('admin.membership.coupon.minSummary', { min: formatPrice(c.min_amount_cents, c.currency, locale) }))
    parts.push(c.plan_ids && c.plan_ids.length > 0 ? c.plan_ids.map(planName).join('、') : t('admin.membership.coupon.allPlans'))
    if (c.include_gifts) parts.push(t('admin.membership.coupon.withGifts'))
    if (c.new_members_only) parts.push(t('admin.membership.coupon.newOnly'))
    parts.push(c.max_uses > 0 ? t('admin.membership.coupon.usage', { used: c.used, max: c.max_uses }) : t('admin.membership.coupon.usageUnlimited', { used: c.used }))
    if (c.reserved > 0) parts.push(t('admin.membership.coupon.reservedCount', { n: c.reserved }))
    if (c.per_user_limit > 0) parts.push(t('admin.membership.coupon.perUser', { n: c.per_user_limit }))
    if (c.starts_at) parts.push(t('admin.membership.coupon.from', { date: formatDate(c.starts_at) }))
    if (c.expires_at) parts.push(t('admin.membership.redeem.until', { date: formatDate(c.expires_at) }))
    return parts.join(' · ')
  }

  return (
    <>
      <div className="mb-4 flex items-center gap-3">
        <p className="flex-1 text-sm text-slate-500">{t('admin.membership.coupon.hint')}</p>
        <Button onClick={() => setCreating(true)}><i className="fa-solid fa-plus" aria-hidden="true" />{t('admin.membership.coupon.create')}</Button>
      </div>
      {data === null ? <Loading className="py-16" /> : data.items.length === 0 ? <EmptyState>{t('admin.membership.coupon.empty')}</EmptyState> : (
        <div className="space-y-3">
          {data.items.map((c) => {
            const expired = !!c.expires_at && new Date(c.expires_at) < new Date()
            const usedUp = c.max_uses > 0 && c.used + c.reserved >= c.max_uses
            return (
              <Card key={c.id} className="flex flex-wrap items-center gap-3 p-4 text-sm">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium text-slate-900">{c.name}</span>
                    <Badge tone={c.type === 'percent' ? 'violet' : 'primary'}>{t(`admin.membership.coupon.type.${c.type}`)}</Badge>
                    {c.status === 'disabled' ? <Badge tone="slate">{t('admin.membership.redeem.disabled')}</Badge>
                      : expired ? <Badge tone="amber">{t('admin.membership.redeem.expired')}</Badge>
                        : usedUp && <Badge tone="amber">{t('admin.membership.coupon.usedUp')}</Badge>}
                  </div>
                  <p className="mt-1 text-xs text-slate-500">{describe(c)}</p>
                </div>
                <Button size="sm" variant="outline" onClick={() => void copy(c.code)}><span className="font-mono">{c.code}</span><i className="fa-regular fa-copy" aria-hidden="true" /></Button>
                <Button size="sm" variant="outline" onClick={() => setViewing(c)}>{t('admin.membership.coupon.viewUses')}</Button>
                <Button size="sm" variant="ghost" loading={busy === c.id} onClick={() => void toggle(c)}>
                  {c.status === 'active' ? t('admin.membership.redeem.disable') : t('admin.membership.redeem.enable')}
                </Button>
              </Card>
            )
          })}
        </div>
      )}
      {data && data.total > data.page_size && <div className="mt-4"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
      {creating && data && <CreateCouponModal plans={plans} currency={data.currency} onClose={() => setCreating(false)} onCreated={() => { setCreating(false); setPage(1); load() }} />}
      {viewing && <UsesModal coupon={viewing} onClose={() => setViewing(null)} />}
    </>
  )
}

function CreateCouponModal({ plans, currency, onClose, onCreated }: { plans: { plan: MembershipPlan }[]; currency: string; onClose: () => void; onCreated: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [type, setType] = useState<'percent' | 'amount'>('percent')
  const [name, setName] = useState('')
  const [code, setCode] = useState('')
  const [percent, setPercent] = useState('20')
  const [amountOff, setAmountOff] = useState('10')
  const [minAmount, setMinAmount] = useState('')
  const [planIDs, setPlanIDs] = useState<number[]>([])
  const [includeGifts, setIncludeGifts] = useState(false)
  const [newOnly, setNewOnly] = useState(false)
  const [maxUses, setMaxUses] = useState('')
  const [perUser, setPerUser] = useState('1')
  const [startsAt, setStartsAt] = useState('')
  const [expiresAt, setExpiresAt] = useState('')
  const [saving, setSaving] = useState(false)

  async function submit() {
    setSaving(true)
    try {
      await api('/admin/membership/coupons', {
        method: 'POST',
        body: {
          name: name.trim(), code: code.trim(), type,
          percent_off: type === 'percent' ? Number(percent) : undefined,
          amount_off_cents: type === 'amount' ? centsFromInput(amountOff) : undefined,
          min_amount_cents: minAmount ? centsFromInput(minAmount) : 0,
          plan_ids: planIDs, include_gifts: includeGifts, new_members_only: newOnly,
          max_uses: maxUses ? Number(maxUses) : 0, per_user_limit: perUser ? Number(perUser) : 0,
          starts_at: startsAt ? new Date(startsAt).toISOString() : undefined,
          expires_at: expiresAt ? new Date(expiresAt).toISOString() : undefined,
        },
      })
      showToast({ message: t('admin.membership.coupon.created'), tone: 'success' })
      onCreated()
    } catch (e) { showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setSaving(false) }
  }

  const togglePlan = (id: number, on: boolean) => setPlanIDs((ids) => (on ? [...ids, id] : ids.filter((x) => x !== id)))
  return (
    <Modal open onClose={onClose} title={t('admin.membership.coupon.create')}
      footer={<><Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button><Button loading={saving} disabled={!name.trim()} onClick={() => void submit()}>{t('admin.membership.coupon.submit')}</Button></>}>
      <div className="space-y-4">
        <SegmentedTabs fullWidth size="sm" value={type} ariaLabel={t('admin.membership.coupon.typeLabel')} onChange={(v) => setType(v as 'percent' | 'amount')}
          items={[{ value: 'percent', label: t('admin.membership.coupon.type.percent') }, { value: 'amount', label: t('admin.membership.coupon.type.amount') }]} />
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('admin.membership.coupon.name')}><Input value={name} maxLength={120} placeholder={t('admin.membership.coupon.namePlaceholder')} onChange={(e) => setName(e.target.value)} /></Field>
          <Field label={t('admin.membership.coupon.code')} hint={t('admin.membership.coupon.codeHint')}><Input value={code} maxLength={40} placeholder="NEW20" onChange={(e) => setCode(e.target.value)} /></Field>
        </div>
        {type === 'percent' ? (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t('admin.membership.coupon.percentOff')} hint={t('admin.membership.coupon.percentHint')}><Input type="number" min={1} max={99} value={percent} onChange={(e) => setPercent(e.target.value)} /></Field>
            <Field label={t('admin.membership.coupon.minAmount', { currency })} hint={t('admin.membership.coupon.minOptional')}><Input type="number" min={0} step="0.01" value={minAmount} onChange={(e) => setMinAmount(e.target.value)} /></Field>
          </div>
        ) : (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t('admin.membership.coupon.minAmount', { currency })} hint={t('admin.membership.coupon.minRequired')}><Input type="number" min={0} step="0.01" value={minAmount} onChange={(e) => setMinAmount(e.target.value)} /></Field>
            <Field label={t('admin.membership.coupon.amountOff', { currency })}><Input type="number" min={0.01} step="0.01" value={amountOff} onChange={(e) => setAmountOff(e.target.value)} /></Field>
          </div>
        )}
        <Field label={t('admin.membership.coupon.plans')} hint={t('admin.membership.coupon.plansHint')}>
          <div className="flex flex-wrap gap-x-5 gap-y-2">
            {plans.map(({ plan: p }) => (
              <label key={p.id} className="flex items-center gap-2 text-sm text-slate-700">
                <Checkbox checked={planIDs.includes(p.id)} onChange={(on) => togglePlan(p.id, on)} ariaLabel={p.name} />{p.name}
              </label>
            ))}
          </div>
        </Field>
        <div className="space-y-3 rounded-lg border border-slate-200 p-3">
          <div className="flex items-center justify-between gap-3 text-sm"><span><span className="text-slate-800">{t('admin.membership.coupon.includeGifts')}</span><span className="block text-xs text-slate-400">{t('admin.membership.coupon.includeGiftsHint')}</span></span><Switch checked={includeGifts} onChange={setIncludeGifts} ariaLabel={t('admin.membership.coupon.includeGifts')} /></div>
          <div className="flex items-center justify-between gap-3 text-sm"><span><span className="text-slate-800">{t('admin.membership.coupon.newMembersOnly')}</span><span className="block text-xs text-slate-400">{t('admin.membership.coupon.newMembersOnlyHint')}</span></span><Switch checked={newOnly} onChange={setNewOnly} ariaLabel={t('admin.membership.coupon.newMembersOnly')} /></div>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('admin.membership.coupon.maxUses')} hint={t('admin.membership.coupon.unlimitedHint')}><Input type="number" min={0} value={maxUses} onChange={(e) => setMaxUses(e.target.value)} /></Field>
          <Field label={t('admin.membership.coupon.perUserLimit')} hint={t('admin.membership.coupon.unlimitedHint')}><Input type="number" min={0} max={1000} value={perUser} onChange={(e) => setPerUser(e.target.value)} /></Field>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('admin.membership.coupon.startsAt')} hint={t('admin.membership.coupon.startsHint')}><DateTimePicker value={startsAt} onChange={setStartsAt} ariaLabel={t('admin.membership.coupon.startsAt')} /></Field>
          <Field label={t('admin.membership.coupon.expiresAt')} hint={t('admin.membership.redeem.expiresHint')}><DateTimePicker value={expiresAt} onChange={setExpiresAt} ariaLabel={t('admin.membership.coupon.expiresAt')} /></Field>
        </div>
      </div>
    </Modal>
  )
}

function UsesModal({ coupon, onClose }: { coupon: Coupon; onClose: () => void }) {
  const { t, locale } = useTranslation()
  const { showToast } = useFeedback()
  const [page, setPage] = useState(1)
  const [data, setData] = useState<{ items: CouponUse[]; total: number; page: number; page_size: number } | null>(null)

  useEffect(() => {
    api<{ items: CouponUse[]; total: number; page: number; page_size: number }>(`/admin/membership/coupons/${coupon.id}/uses`, { params: { page, page_size: 20 } })
      .then(setData)
      .catch((e) => showToast({ title: t('admin.membership.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [coupon.id, page, showToast, t])

  return (
    <Modal open onClose={onClose} title={t('admin.membership.coupon.usesTitle', { name: coupon.name })} footer={<Button onClick={onClose}>{t('common.actions.close')}</Button>}>
      {data === null ? <Loading className="py-8" /> : data.items.length === 0 ? <EmptyState>{t('admin.membership.coupon.noUses')}</EmptyState> : (
        <>
          <ul className="divide-y divide-slate-100 rounded-lg border border-slate-200">
            {data.items.map(({ use, user }) => (
              <li key={use.id} className="flex flex-wrap items-center gap-3 px-3 py-2 text-sm">
                {user && <span className="flex min-w-0 items-center gap-2"><UserAvatar user={user} /><span className="truncate text-slate-800">{user.nickname || user.username}</span></span>}
                <Badge tone={useTone[use.status]}>{t(`admin.membership.coupon.useStatus.${use.status}`)}</Badge>
                <span className="ml-auto text-right text-xs text-slate-500">
                  <span className="block tabular-nums text-emerald-600">-{formatPrice(use.discount_cents, coupon.currency, locale)}</span>
                  <span className="block">{use.order_no} · {formatDate(use.created_at)}</span>
                </span>
              </li>
            ))}
          </ul>
          {data.total > data.page_size && <div className="mt-3"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
        </>
      )}
    </Modal>
  )
}
