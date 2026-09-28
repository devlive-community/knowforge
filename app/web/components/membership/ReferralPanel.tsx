import { useCallback, useEffect, useState } from 'react'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import type { MembershipPlan } from '@/lib/membership'
import UserAvatar from '@/components/UserAvatar'
import type { UserLite } from '@/components/UserSearchSelect'
import { Badge, Button, Card, EmptyState, Field, Input, Loading, Pagination, Select, Switch, useFeedback } from '@/components/ui'
import { displayName } from '@/lib/users'

export interface ReferralSettings {
  enabled: boolean
  plan_id: number
  inviter_days: number
  invitee_days: number
  signup_days: number
  monthly_limit: number
}
interface RewardItem {
  reward: { id: number; kind: 'signup' | 'purchase'; order_no?: string; inviter_days: number; invitee_days: number; capped: boolean; created_at: string }
  inviter?: UserLite
  invitee?: UserLite
}

// ReferralPanel 会员邀请奖励：设置奖励规则并查看发放记录（邀请关系沿用站点的邀请码）。
export default function ReferralPanel({ plans }: { plans: { plan: MembershipPlan }[] }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [page, setPage] = useState(1)
  const [form, setForm] = useState<ReferralSettings | null>(null)
  const [data, setData] = useState<{ items: RewardItem[]; total: number; page: number; page_size: number } | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    api<{ settings: ReferralSettings; items: RewardItem[]; total: number; page: number; page_size: number }>('/admin/membership/referral', { params: { page } })
      .then((r) => { setForm((f) => f || r.settings); setData(r) })
      .catch((e) => showToast({ title: t('admin.membership.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [page, showToast, t])
  useEffect(() => { load() }, [load])

  async function save() {
    if (!form) return
    setSaving(true)
    try {
      const r = await api<{ settings: ReferralSettings }>('/admin/membership/referral', { method: 'PUT', body: form })
      setForm(r.settings)
      showToast({ message: t('admin.membership.referral.saved'), tone: 'success' })
    } catch (e) { showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setSaving(false) }
  }

  if (!form || !data) return <Loading className="py-16" />
  const num = (key: keyof ReferralSettings, max: number) => (
    <Input type="number" min={0} max={max} value={String(form[key])} onChange={(e) => setForm({ ...form, [key]: Math.max(0, Math.min(max, Number(e.target.value) || 0)) })}
      trailing={key === 'monthly_limit' ? undefined : <span className="text-xs text-slate-400">{t('admin.membership.plan.daysUnit')}</span>} />
  )
  return (
    <div className="space-y-6">
      <Card className="max-w-2xl space-y-4 p-6">
        <p className="text-sm text-slate-500">{t('admin.membership.referral.hint')}</p>
        <div className="flex items-center justify-between gap-4">
          <span className="text-sm font-medium text-slate-800">{t('admin.membership.referral.enabled')}</span>
          <Switch checked={form.enabled} onChange={(v) => setForm({ ...form, enabled: v })} ariaLabel={t('admin.membership.referral.enabled')} />
        </div>
        <Field label={t('admin.membership.referral.plan')} hint={t('admin.membership.referral.planHint')}>
          <Select value={form.plan_id ? String(form.plan_id) : ''} onChange={(v) => setForm({ ...form, plan_id: Number(v) })} placeholder={t('admin.membership.referral.planPlaceholder')}
            options={plans.map(({ plan: p }) => ({ value: String(p.id), label: p.name }))} />
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('admin.membership.referral.inviterDays')} hint={t('admin.membership.referral.inviterDaysHint')}>{num('inviter_days', 365)}</Field>
          <Field label={t('admin.membership.referral.inviteeDays')} hint={t('admin.membership.referral.inviteeDaysHint')}>{num('invitee_days', 365)}</Field>
          <Field label={t('admin.membership.referral.signupDays')} hint={t('admin.membership.referral.signupDaysHint')}>{num('signup_days', 365)}</Field>
          <Field label={t('admin.membership.referral.monthlyLimit')} hint={t('admin.membership.referral.monthlyLimitHint')}>{num('monthly_limit', 1000)}</Field>
        </div>
        <div className="flex justify-end"><Button loading={saving} onClick={() => void save()}>{t('common.actions.save')}</Button></div>
      </Card>

      <div>
        <h2 className="mb-3 font-bold text-slate-900">{t('admin.membership.referral.rewards')}</h2>
        {data.items.length === 0 ? <EmptyState>{t('admin.membership.referral.noRewards')}</EmptyState> : (
          <Card className="divide-y divide-slate-100">
            {data.items.map(({ reward: r, inviter, invitee }) => (
              <div key={r.id} className="flex flex-wrap items-center gap-3 px-4 py-3 text-sm">
                {inviter && <span className="flex items-center gap-2"><UserAvatar user={inviter} /><span className="text-slate-800">{displayName(inviter)}</span></span>}
                <i className="fa-solid fa-arrow-right text-xs text-slate-300" aria-hidden="true" />
                {invitee && <span className="flex items-center gap-2"><UserAvatar user={invitee} /><span className="text-slate-800">{displayName(invitee)}</span></span>}
                <Badge tone={r.kind === 'purchase' ? 'amber' : 'sky'}>{t(`membership.referral.kind.${r.kind}`)}</Badge>
                {r.capped && <Badge tone="slate">{t('membership.referral.capped')}</Badge>}
                <span className="ml-auto text-right text-xs text-slate-500">
                  <span className="block">{t('admin.membership.referral.daysSummary', { inviter: r.inviter_days, invitee: r.invitee_days })}</span>
                  <span className="block">{r.order_no ? `${r.order_no} · ` : ''}{formatDate(r.created_at)}</span>
                </span>
              </div>
            ))}
          </Card>
        )}
        {data.total > data.page_size && <div className="mt-4"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
      </div>
    </div>
  )
}
