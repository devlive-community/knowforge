import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/router'
import AdminLayout from '@/components/AdminLayout'
import RedeemPanel from '@/components/membership/RedeemPanel'
import CouponPanel from '@/components/membership/CouponPanel'
import ReferralPanel from '@/components/membership/ReferralPanel'
import GroupsPanel from '@/components/membership/GroupsPanel'
import FeatureGate from '@/components/FeatureGate'
import ResourceIcon from '@/components/ResourceIcon'
import IconPicker from '@/components/IconPicker'
import UserSearchSelect, { type UserLite } from '@/components/UserSearchSelect'
import UserAvatar from '@/components/UserAvatar'
import EntitlementEditor from '@/components/EntitlementEditor'
import LocalizedFields, { type ResourceTranslations } from '@/components/LocalizedFields'
import { api, formatDate } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { Badge, Button, Card, DateTimePicker, EmptyState, Field, Input, Loading, Modal, Pagination, Select, SegmentedTabs, Switch, useFeedback } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'
import type { EntitlementDef } from '@/lib/entitlements'
import { centsFromInput, inputFromCents, type MembershipPlan, type MembershipRecord } from '@/lib/membership'
import { durationLabel, formatPrice } from '@/lib/commerce'
import { displayName } from '@/lib/users'
import { useUrlPage } from '@/lib/use-url-page'

type Tab = 'plans' | 'members' | 'groups' | 'redeem' | 'coupons' | 'referral' | 'records' | 'settings'
const TABS: Tab[] = ['plans', 'members', 'groups', 'redeem', 'coupons', 'referral', 'records', 'settings']

interface PlanItem { plan: MembershipPlan; active_members: number }
interface PriceRow { key: string; id?: number; duration_days: string; price: string; original: string }
interface PlanForm {
  id?: number
  icon_type: string
  icon_value: string
  color: string
  status: 'active' | 'archived'
  sort_order: number
  trial_days: string
  group_enabled: boolean
  entitlements: Record<string, number>
  prices: PriceRow[]
  translations: ResourceTranslations
}
interface MemberItem {
  user: UserLite
  plan?: { id: number; name: string; icon_type?: string; icon_value?: string; color?: string; status: string }
  started_at: string
  expires_at: string
  active: boolean
  trial: boolean
  auto_renew: boolean
}
interface RecordItem { record: MembershipRecord; user?: UserLite; operator?: UserLite }

let rowSeq = 0
const newRow = (p?: Partial<PriceRow>): PriceRow => ({ key: `r${++rowSeq}`, duration_days: '30', price: '', original: '', ...p })

// toLocalInput ISO 时间 → DateTimePicker 的本地 'YYYY-MM-DDTHH:mm'。
function toLocalInput(value: string): string {
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

export default function AdminMembership() {
  return <FeatureGate feature="membership"><AdminMembershipInner /></FeatureGate>
}

function AdminMembershipInner() {
  const { t } = useTranslation()
  const router = useRouter()
  const { site } = useApp()
  const groupsOn = (site.feature_plugins || []).includes('teams') // 团队会员需要成员组（团队空间）
  const tabs = TABS.filter((key) => key !== 'groups' || groupsOn)
  const tab: Tab = tabs.includes(router.query.tab as Tab) ? (router.query.tab as Tab) : 'plans' // tab 由 URL 驱动
  const [plans, setPlans] = useState<PlanItem[] | null>(null)
  const [currency, setCurrency] = useState('CNY')
  const { showToast } = useFeedback()

  const loadPlans = useCallback(() => {
    api<{ items: PlanItem[]; currency: string }>('/admin/membership/plans')
      .then((r) => { setPlans(r.items || []); setCurrency(r.currency) })
      .catch((e) => showToast({ title: t('admin.membership.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [showToast, t])
  useEffect(() => { loadPlans() }, [loadPlans])

  return (
    <AdminLayout current="membership" breadcrumb={t('admin.nav.membership')}>
      <div>
        <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.membership')}</h1>
        <p className="mt-1.5 text-sm text-slate-500">{t('admin.membership.description')}</p>
      </div>
      <SegmentedTabs className="mt-6" value={tab} ariaLabel={t('admin.nav.membership')}
        items={tabs.map((key) => ({ value: key, label: t(`admin.membership.tab.${key}`), href: `/admin/membership?tab=${key}` }))} />
      <div className="mt-6">
        {tab === 'plans' && <PlansPanel plans={plans} currency={currency} onChanged={loadPlans} />}
        {tab === 'members' && <MembersPanel plans={plans || []} onChanged={loadPlans} />}
        {tab === 'groups' && (plans ? <GroupsPanel plans={plans.map((p) => p.plan)} /> : <Loading className="py-16" />)}
        {tab === 'redeem' && (plans ? <RedeemPanel plans={plans} /> : <Loading className="py-16" />)}
        {tab === 'coupons' && (plans ? <CouponPanel plans={plans} /> : <Loading className="py-16" />)}
        {tab === 'referral' && (plans ? <ReferralPanel plans={plans} /> : <Loading className="py-16" />)}
        {tab === 'records' && <RecordsPanel />}
        {tab === 'settings' && <SettingsPanel onSaved={loadPlans} />}
      </div>
    </AdminLayout>
  )
}

// —— 方案 ——

function PlansPanel({ plans, currency, onChanged }: { plans: PlanItem[] | null; currency: string; onChanged: () => void }) {
  const { t, locale, defaultLocale } = useTranslation()
  const { showToast, confirmAction } = useFeedback()
  const [defs, setDefs] = useState<EntitlementDef[]>([])
  const [form, setForm] = useState<PlanForm | null>(null)
  const [saving, setSaving] = useState(false)
  const [translationBusy, setTranslationBusy] = useState(false)
  const [deleting, setDeleting] = useState<number | null>(null)
  useEffect(() => {
    api<{ items: EntitlementDef[] }>('/entitlements/definitions').then((r) => setDefs(r.items || [])).catch(() => {})
  }, [])

  function openNew() {
    setForm({ icon_type: 'fa', icon_value: 'fa-crown', color: '', status: 'active', sort_order: (plans?.length || 0) + 1, trial_days: '0', group_enabled: false, entitlements: {},
      prices: [newRow({ duration_days: '30' }), newRow({ duration_days: '365' })],
      translations: { [defaultLocale]: { fields: {}, revision: 0, publish: true } } })
  }
  function openEdit(p: MembershipPlan) {
    setForm({ id: p.id, icon_type: p.icon_type || 'fa', icon_value: p.icon_value || 'fa-crown', color: p.color || '', status: p.status,
      sort_order: p.sort_order, trial_days: String(p.trial_days || 0), group_enabled: !!p.group_enabled, entitlements: { ...(p.entitlements || {}) },
      prices: p.prices.map((pr) => newRow({ id: pr.id, duration_days: String(pr.duration_days), price: inputFromCents(pr.price_cents), original: inputFromCents(pr.original_price_cents) })),
      translations: Object.fromEntries(Object.entries(p.translations || {}).map(([code, entry]) => [code, { ...entry, publish: false }])) })
  }
  function setPrice(key: string, patch: Partial<PriceRow>) {
    if (!form) return
    setForm({ ...form, prices: form.prices.map((r) => (r.key === key ? { ...r, ...patch } : r)) })
  }

  async function save() {
    if (!form) return
    setSaving(true)
    try {
      await api(form.id ? `/admin/membership/plans/${form.id}` : '/admin/membership/plans', { method: form.id ? 'PUT' : 'POST', body: {
        icon_type: form.icon_type, icon_value: form.icon_value, color: form.color, status: form.status, sort_order: form.sort_order, trial_days: Number(form.trial_days) || 0, group_enabled: form.group_enabled,
        entitlements: form.entitlements,
        prices: form.prices.map((r) => ({ id: r.id, duration_days: Number(r.duration_days) || 0, price_cents: centsFromInput(r.price), original_price_cents: centsFromInput(r.original) })),
        translations: Object.fromEntries(Object.entries(form.translations).filter(([, entry]) => entry.dirty)),
      } })
      setForm(null); onChanged()
      showToast({ message: t('admin.membership.plan.saved'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally { setSaving(false) }
  }

  async function remove(p: MembershipPlan) {
    if (!(await confirmAction({ title: t('admin.membership.plan.deleteTitle'), message: t('admin.membership.plan.deleteMessage', { name: p.name }), confirmLabel: t('common.actions.delete'), danger: true }))) return
    setDeleting(p.id)
    try { await api(`/admin/membership/plans/${p.id}`, { method: 'DELETE' }); onChanged(); showToast({ message: t('admin.membership.plan.deleted'), tone: 'success' }) }
    catch (e) { showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setDeleting(null) }
  }

  return (
    <>
      <div className="mb-4 flex justify-end">
        <Button onClick={openNew}><i className="fa-solid fa-plus" aria-hidden="true" /> {t('admin.membership.plan.add')}</Button>
      </div>
      {plans === null ? <Loading className="py-16" /> : plans.length === 0 ? <EmptyState>{t('admin.membership.plan.empty')}</EmptyState> : (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {plans.map(({ plan: p, active_members }) => (
            <Card key={p.id} className="flex flex-col p-5">
              <div className="flex items-start gap-3">
                <ResourceIcon iconType={p.icon_type} iconValue={p.icon_value} fallback="fa-crown" className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-amber-100 bg-amber-50 text-amber-600" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate font-bold text-slate-900">{p.name}</span>
                    <Badge tone={p.status === 'active' ? 'emerald' : 'slate'}>{t(`admin.membership.status.${p.status}`)}</Badge>
                  </div>
                  {p.description && <p className="mt-1 line-clamp-2 text-xs text-slate-500">{p.description}</p>}
                </div>
              </div>
              <div className="mt-4 flex flex-wrap gap-2">
                {p.prices.length === 0 ? <span className="text-xs text-slate-400">{t('admin.membership.plan.noPrices')}</span> : p.prices.map((pr) => (
                  <span key={pr.id} className="rounded-lg border border-slate-200 px-2.5 py-1 text-xs text-slate-600">
                    {durationLabel(t, pr.duration_days)} · <span className="font-medium text-slate-900">{formatPrice(pr.price_cents, currency, locale)}</span>
                  </span>
                ))}
                {p.trial_days > 0 && <span className="rounded-lg border border-sky-200 bg-sky-50 px-2.5 py-1 text-xs text-sky-700">{t('membership.trial.days', { n: p.trial_days })}</span>}
                {p.group_enabled && <span className="rounded-lg border border-violet-200 bg-violet-50 px-2.5 py-1 text-xs text-violet-700">{t('admin.membership.plan.groupBadge')}</span>}
              </div>
              <div className="mt-4 flex items-center justify-between gap-3 border-t border-slate-100 pt-3 text-xs text-slate-500">
                <span>{t('admin.membership.plan.stats', { members: active_members, privileges: Object.keys(p.entitlements || {}).length })}</span>
                <span className="flex gap-2">
                  <Button variant="outline" size="sm" onClick={() => openEdit(p)}>{t('common.actions.edit')}</Button>
                  <Button variant="ghost" size="sm" className="text-rose-600" loading={deleting === p.id} onClick={() => remove(p)}>{t('common.actions.delete')}</Button>
                </span>
              </div>
            </Card>
          ))}
        </div>
      )}

      <Modal className="max-w-3xl" open={form !== null} onClose={() => setForm(null)} title={form?.id ? t('admin.membership.plan.edit') : t('admin.membership.plan.add')}
        footer={<><Button variant="outline" onClick={() => setForm(null)}>{t('common.actions.cancel')}</Button><Button loading={saving} disabled={translationBusy} onClick={save}>{t('common.actions.save')}</Button></>}>
        {form && (
          <div className="space-y-5">
            <LocalizedFields value={form.translations} onBusyChange={setTranslationBusy} onChange={(translations) => setForm({ ...form, translations })} fields={[
              { key: 'name', label: t('admin.membership.plan.name'), maxLength: 120 },
              { key: 'description', label: t('admin.membership.plan.descriptionField'), maxLength: 500, multiline: true },
            ]} />
            <div className="grid gap-4 sm:grid-cols-3">
              <Field label={t('admin.membership.plan.status')}>
                <Select value={form.status} onChange={(v) => setForm({ ...form, status: v as PlanForm['status'] })}
                  options={[{ value: 'active', label: t('admin.membership.status.active') }, { value: 'archived', label: t('admin.membership.status.archived') }]} />
              </Field>
              <Field label={t('admin.membership.plan.sortOrder')}><Input type="number" value={form.sort_order} onChange={(e) => setForm({ ...form, sort_order: Number(e.target.value) || 0 })} /></Field>
              <Field label={t('admin.membership.plan.color')}><Input value={form.color} onChange={(e) => setForm({ ...form, color: e.target.value })} placeholder="#f59e0b" /></Field>
            </div>
            <Field label={t('admin.membership.plan.trialDays')} hint={t('admin.membership.plan.trialDaysHint')}>
              <Input type="number" min={0} max={365} value={form.trial_days} onChange={(e) => setForm({ ...form, trial_days: e.target.value })}
                trailing={<span className="text-xs text-slate-400">{t('admin.membership.plan.daysUnit')}</span>} />
            </Field>
            <label className="flex items-start justify-between gap-4 rounded-xl border border-slate-100 bg-slate-50/60 px-4 py-3">
              <span>
                <span className="block text-sm font-medium text-slate-800">{t('admin.membership.plan.groupEnabled')}</span>
                <span className="mt-0.5 block text-xs text-slate-500">{t('admin.membership.plan.groupEnabledHint')}</span>
              </span>
              <Switch checked={form.group_enabled} onChange={(v) => setForm({ ...form, group_enabled: v })} ariaLabel={t('admin.membership.plan.groupEnabled')} />
            </label>
            <Field label={t('admin.membership.plan.icon')}>
              <IconPicker value={{ icon_type: form.icon_type, icon_value: form.icon_value }} onChange={(v) => setForm({ ...form, icon_type: v.icon_type || 'fa', icon_value: v.icon_value })} fallback="fa-crown" />
            </Field>

            <div>
              <div className="text-sm font-medium text-slate-700">{t('admin.membership.plan.prices')}</div>
              <p className="mb-2 mt-0.5 text-xs text-slate-400">{t('admin.membership.plan.pricesHint', { currency })}</p>
              <div className="space-y-2">
                {form.prices.map((r) => (
                  <div key={r.key} className="grid grid-cols-[1fr_1fr_1fr_auto] items-center gap-2">
                    <Input type="number" min={1} value={r.duration_days} aria-label={t('admin.membership.plan.durationDays')} onChange={(e) => setPrice(r.key, { duration_days: e.target.value })}
                      trailing={<span className="text-xs text-slate-400">{t('admin.membership.plan.daysUnit')}</span>} />
                    <Input type="number" min={0} step="0.01" value={r.price} placeholder={t('admin.membership.plan.price')} aria-label={t('admin.membership.plan.price')} onChange={(e) => setPrice(r.key, { price: e.target.value })}
                      trailing={<span className="text-xs text-slate-400">{currency}</span>} />
                    <Input type="number" min={0} step="0.01" value={r.original} placeholder={t('admin.membership.plan.originalPrice')} aria-label={t('admin.membership.plan.originalPrice')} onChange={(e) => setPrice(r.key, { original: e.target.value })}
                      trailing={<span className="text-xs text-slate-400">{currency}</span>} />
                    <Button variant="ghost" size="sm" className="text-rose-600" aria-label={t('common.actions.delete')} onClick={() => setForm({ ...form, prices: form.prices.filter((x) => x.key !== r.key) })}>
                      <i className="fa-solid fa-xmark" aria-hidden="true" />
                    </Button>
                  </div>
                ))}
              </div>
              <Button className="mt-2" variant="outline" size="sm" onClick={() => setForm({ ...form, prices: [...form.prices, newRow({ duration_days: '' })] })}>
                <i className="fa-solid fa-plus" aria-hidden="true" /> {t('admin.membership.plan.addPrice')}
              </Button>
            </div>

            {defs.length > 0 && (
              <div>
                <div className="text-sm font-medium text-slate-700">{t('admin.membership.plan.entitlements')}</div>
                <p className="mb-2 mt-0.5 text-xs text-slate-400">{t('admin.membership.plan.entitlementsHint')}</p>
                <EntitlementEditor mode="source" defs={defs} value={form.entitlements} onChange={(entitlements) => setForm({ ...form, entitlements })} />
              </div>
            )}
          </div>
        )}
      </Modal>
    </>
  )
}

// —— 会员 ——

function MembersPanel({ plans, onChanged }: { plans: PlanItem[]; onChanged: () => void }) {
  const { t } = useTranslation()
  const { showToast, requestInput } = useFeedback()
  const [q, setQ] = useState('')
  const [status, setStatus] = useState('')
  const [planID, setPlanID] = useState('')
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<{ items: MemberItem[]; total: number; page: number; page_size: number } | null>(null)
  const [grantOpen, setGrantOpen] = useState(false)
  const [adjusting, setAdjusting] = useState<MemberItem | null>(null)
  const [revoking, setRevoking] = useState<number | null>(null)

  const load = useCallback(() => {
    const params = new URLSearchParams({ page: String(page), page_size: '20' })
    if (q.trim()) params.set('q', q.trim())
    if (status) params.set('status', status)
    if (planID) params.set('plan_id', planID)
    api<{ items: MemberItem[]; total: number; page: number; page_size: number }>(`/admin/membership/members?${params}`).then(setData)
      .catch((e) => showToast({ title: t('admin.membership.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [page, q, status, planID, showToast, t])
  useEffect(() => { load() }, [load])

  function changed() { load(); onChanged() }

  async function revoke(m: MemberItem) {
    const reason = await requestInput({ title: t('admin.membership.member.revokeTitle', { user: m.user.username }), label: t('admin.membership.member.reason'), confirmLabel: t('admin.membership.member.revoke') })
    if (reason === null) return
    setRevoking(m.user.id)
    try {
      await api(`/admin/membership/members/${m.user.id}/revoke`, { method: 'POST', body: { reason } })
      changed(); showToast({ message: t('admin.membership.member.revoked'), tone: 'success' })
    } catch (e) { showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setRevoking(null) }
  }

  const planOptions = [{ value: '', label: t('admin.membership.member.allPlans') }, ...plans.map(({ plan }) => ({ value: String(plan.id), label: plan.name }))]
  return (
    <>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <span className="block w-full sm:w-64"><Input value={q} placeholder={t('admin.membership.member.search')} onChange={(e) => { setQ(e.target.value); setPage(1) }} /></span>
        <span className="block w-40"><Select value={status} onChange={(v) => { setStatus(v); setPage(1) }} options={[
          { value: '', label: t('admin.membership.member.allStatus') }, { value: 'active', label: t('admin.membership.member.active') }, { value: 'expired', label: t('admin.membership.member.expired') },
        ]} /></span>
        <span className="block w-44"><Select value={planID} onChange={(v) => { setPlanID(v); setPage(1) }} options={planOptions} /></span>
        <Button className="sm:ml-auto" onClick={() => setGrantOpen(true)} disabled={plans.length === 0}><i className="fa-solid fa-plus" aria-hidden="true" /> {t('admin.membership.member.grant')}</Button>
      </div>
      {data === null ? <Loading className="py-16" /> : data.items.length === 0 ? <EmptyState>{t('admin.membership.member.empty')}</EmptyState> : (
        <Card className="overflow-x-auto">
          <table className="w-full min-w-[720px] text-sm">
            <thead className="bg-slate-50 text-left text-xs text-slate-500">
              <tr>
                <th className="px-4 py-3">{t('admin.membership.member.col.user')}</th><th className="px-4 py-3">{t('admin.membership.member.col.plan')}</th>
                <th className="px-4 py-3">{t('admin.membership.member.col.started')}</th><th className="px-4 py-3">{t('admin.membership.member.col.expires')}</th>
                <th className="px-4 py-3">{t('admin.membership.member.col.status')}</th><th className="px-4 py-3 text-right">{t('admin.membership.member.col.action')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {data.items.map((m) => (
                <tr key={m.user.id}>
                  <td className="px-4 py-3"><span className="flex items-center gap-2"><UserAvatar user={m.user} /><span className="font-medium text-slate-800">{displayName(m.user)}</span></span></td>
                  <td className="px-4 py-3 text-slate-700">{m.plan?.name || '—'}</td>
                  <td className="px-4 py-3 text-slate-500">{formatDate(m.started_at)}</td>
                  <td className="px-4 py-3 text-slate-500">{formatDate(m.expires_at)}</td>
                  <td className="px-4 py-3"><span className="flex flex-wrap gap-1.5"><Badge tone={m.active ? 'emerald' : 'slate'}>{m.active ? t('admin.membership.member.active') : t('admin.membership.member.expired')}</Badge>{m.trial && <Badge tone="sky">{t('membership.trial.badge')}</Badge>}{m.auto_renew && <Badge tone="primary">{t('membership.renewal.title')}</Badge>}</span></td>
                  <td className="px-4 py-3 text-right"><span className="flex justify-end gap-2">
                    <Button variant="outline" size="sm" onClick={() => setAdjusting(m)}>{t('admin.membership.member.adjust')}</Button>
                    <Button variant="ghost" size="sm" className="text-rose-600" loading={revoking === m.user.id} onClick={() => revoke(m)}>{t('admin.membership.member.revoke')}</Button>
                  </span></td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
      {data && data.total > data.page_size && <div className="mt-4"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
      {grantOpen && <GrantModal plans={plans} onClose={() => setGrantOpen(false)} onDone={() => { setGrantOpen(false); changed() }} />}
      {adjusting && <AdjustModal member={adjusting} plans={plans} onClose={() => setAdjusting(null)} onDone={() => { setAdjusting(null); changed() }} />}
    </>
  )
}

function GrantModal({ plans, onClose, onDone }: { plans: PlanItem[]; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const active = plans.filter(({ plan }) => plan.status === 'active')
  const [user, setUser] = useState<UserLite | null>(null)
  const [planID, setPlanID] = useState(active[0] ? String(active[0].plan.id) : '')
  const [days, setDays] = useState('30')
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)
  const plan = active.find(({ plan: p }) => String(p.id) === planID)?.plan

  async function submit() {
    if (!user || !planID || !(Number(days) > 0)) { showToast({ message: t('admin.membership.member.grantRequired'), tone: 'error' }); return }
    setSaving(true)
    try {
      const r = await api<{ action: string; expires_at: string }>('/admin/membership/grant', { method: 'POST', body: { user_id: user.id, plan_id: Number(planID), days: Number(days), reason } })
      showToast({ title: t(`admin.membership.action.${r.action}`), message: t('admin.membership.member.grantDone', { user: user.username, date: formatDate(r.expires_at) }), tone: 'success' })
      onDone()
    } catch (e) { showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setSaving(false) }
  }

  return (
    <Modal open onClose={onClose} title={t('admin.membership.member.grant')}
      footer={<><Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button><Button loading={saving} onClick={submit}>{t('admin.membership.member.grantSubmit')}</Button></>}>
      <div className="space-y-4">
        <p className="text-xs leading-5 text-slate-500">{t('admin.membership.member.grantHint')}</p>
        <Field label={t('admin.membership.member.col.user')}><UserSearchSelect value={user} onChange={setUser} /></Field>
        <Field label={t('admin.membership.member.col.plan')}><Select value={planID} onChange={setPlanID} options={active.map(({ plan: p }) => ({ value: String(p.id), label: p.name }))} /></Field>
        <Field label={t('admin.membership.member.days')}>
          <Input type="number" min={1} value={days} onChange={(e) => setDays(e.target.value)} trailing={<span className="text-xs text-slate-400">{t('admin.membership.plan.daysUnit')}</span>} />
        </Field>
        {plan && plan.prices.length > 0 && (
          <div className="flex flex-wrap gap-2">
            {plan.prices.map((pr) => (
              <Button key={pr.id} size="sm" variant={Number(days) === pr.duration_days ? 'primary' : 'outline'} onClick={() => setDays(String(pr.duration_days))}>{durationLabel(t, pr.duration_days)}</Button>
            ))}
          </div>
        )}
        <Field label={t('admin.membership.member.reason')}><Input value={reason} maxLength={255} onChange={(e) => setReason(e.target.value)} /></Field>
      </div>
    </Modal>
  )
}

function AdjustModal({ member, plans, onClose, onDone }: { member: MemberItem; plans: PlanItem[]; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [planID, setPlanID] = useState(String(member.plan?.id || plans[0]?.plan.id || ''))
  const [expires, setExpires] = useState(toLocalInput(member.expires_at))
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)

  async function submit() {
    const at = new Date(expires)
    if (!planID || Number.isNaN(at.getTime())) { showToast({ message: t('admin.membership.member.adjustRequired'), tone: 'error' }); return }
    setSaving(true)
    try {
      await api(`/admin/membership/members/${member.user.id}`, { method: 'PUT', body: { plan_id: Number(planID), expires_at: at.toISOString(), reason } })
      showToast({ message: t('admin.membership.member.adjusted'), tone: 'success' })
      onDone()
    } catch (e) { showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setSaving(false) }
  }

  return (
    <Modal open onClose={onClose} title={t('admin.membership.member.adjustTitle', { user: member.user.username })}
      footer={<><Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button><Button loading={saving} onClick={submit}>{t('common.actions.save')}</Button></>}>
      <div className="space-y-4">
        <Field label={t('admin.membership.member.col.plan')}><Select value={planID} onChange={setPlanID} options={plans.map(({ plan: p }) => ({ value: String(p.id), label: p.name }))} /></Field>
        <Field label={t('admin.membership.member.col.expires')}><DateTimePicker value={expires} onChange={setExpires} ariaLabel={t('admin.membership.member.col.expires')} /></Field>
        <Field label={t('admin.membership.member.reason')}><Input value={reason} maxLength={255} onChange={(e) => setReason(e.target.value)} /></Field>
      </div>
    </Modal>
  )
}

// —— 流水 ——

function RecordsPanel() {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [user, setUser] = useState<UserLite | null>(null)
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<{ items: RecordItem[]; total: number; page: number; page_size: number } | null>(null)
  useEffect(() => {
    const params = new URLSearchParams({ page: String(page), page_size: '20' })
    if (user) params.set('user_id', String(user.id))
    api<{ items: RecordItem[]; total: number; page: number; page_size: number }>(`/admin/membership/records?${params}`).then(setData)
      .catch((e) => showToast({ title: t('admin.membership.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [page, user, showToast, t])

  return (
    <>
      <div className="mb-4 w-full sm:w-72"><UserSearchSelect value={user} onChange={(u) => { setUser(u); setPage(1) }} /></div>
      {data === null ? <Loading className="py-16" /> : data.items.length === 0 ? <EmptyState>{t('admin.membership.record.empty')}</EmptyState> : (
        <Card className="overflow-x-auto">
          <table className="w-full min-w-[820px] text-sm">
            <thead className="bg-slate-50 text-left text-xs text-slate-500">
              <tr>
                <th className="px-4 py-3">{t('admin.membership.record.col.time')}</th><th className="px-4 py-3">{t('admin.membership.member.col.user')}</th>
                <th className="px-4 py-3">{t('admin.membership.record.col.action')}</th><th className="px-4 py-3">{t('admin.membership.member.col.plan')}</th>
                <th className="px-4 py-3">{t('admin.membership.member.col.expires')}</th><th className="px-4 py-3">{t('admin.membership.record.col.operator')}</th>
                <th className="px-4 py-3">{t('admin.membership.member.reason')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {data.items.map(({ record: r, user: u, operator }) => (
                <tr key={r.id}>
                  <td className="whitespace-nowrap px-4 py-3 text-slate-500">{formatDate(r.created_at)}</td>
                  <td className="px-4 py-3 text-slate-800">{displayName(u) || `#${r.user_id}`}</td>
                  <td className="px-4 py-3"><Badge tone={r.action === 'revoke' ? 'rose' : 'primary'}>{t(`admin.membership.action.${r.action}`)}</Badge>{r.days > 0 && <span className="ml-1.5 text-xs text-slate-400">+{r.days}</span>}</td>
                  <td className="px-4 py-3 text-slate-700">{r.plan_name}</td>
                  <td className="whitespace-nowrap px-4 py-3 text-xs text-slate-500">{r.prev_expires_at ? formatDate(r.prev_expires_at) : '—'} → {r.expires_at ? formatDate(r.expires_at) : '—'}</td>
                  <td className="px-4 py-3 text-slate-500">{operator?.username || (r.source !== 'admin' ? t('admin.membership.record.system') : '—')}</td>
                  <td className="max-w-xs truncate px-4 py-3 text-slate-500">{r.reason || r.source_ref || ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
      {data && data.total > data.page_size && <div className="mt-4"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
    </>
  )
}

// —— 设置 ——

function SettingsPanel({ onSaved }: { onSaved: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [form, setForm] = useState<{ currency: string; reminder_days: number; trial_verified_email: boolean } | null>(null)
  const [saving, setSaving] = useState(false)
  useEffect(() => {
    api<{ currency: string; reminder_days: number; trial_verified_email: boolean }>('/admin/membership/settings').then(setForm)
      .catch((e) => showToast({ title: t('admin.membership.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [showToast, t])

  async function save() {
    if (!form) return
    setSaving(true)
    try {
      setForm(await api('/admin/membership/settings', { method: 'PUT', body: form }))
      onSaved()
      showToast({ message: t('admin.membership.settings.saved'), tone: 'success' })
    } catch (e) { showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setSaving(false) }
  }

  if (!form) return <Loading className="py-16" />
  return (
    <Card className="max-w-xl space-y-4 p-6">
      <Field label={t('admin.membership.settings.currency')} hint={t('admin.membership.settings.currencyHint')}>
        <Input value={form.currency} maxLength={3} onChange={(e) => setForm({ ...form, currency: e.target.value.toUpperCase() })} />
      </Field>
      <Field label={t('admin.membership.settings.reminderDays')} hint={t('admin.membership.settings.reminderDaysHint')}>
        <Input type="number" min={0} max={30} value={form.reminder_days} onChange={(e) => setForm({ ...form, reminder_days: Math.max(0, Math.min(30, Number(e.target.value) || 0)) })}
          trailing={<span className="text-xs text-slate-400">{t('admin.membership.plan.daysUnit')}</span>} />
      </Field>
      <div className="flex items-center justify-between gap-4">
        <span className="text-sm"><span className="font-medium text-slate-800">{t('admin.membership.settings.trialVerified')}</span><span className="mt-0.5 block text-xs text-slate-400">{t('admin.membership.settings.trialVerifiedHint')}</span></span>
        <Switch checked={form.trial_verified_email} onChange={(v) => setForm({ ...form, trial_verified_email: v })} ariaLabel={t('admin.membership.settings.trialVerified')} />
      </div>
      <div className="flex justify-end"><Button loading={saving} onClick={save}>{t('common.actions.save')}</Button></div>
    </Card>
  )
}
