import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/router'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import ResourceIcon from '@/components/ResourceIcon'
import MyEntitlementsCard from '@/components/MyEntitlementsCard'
import GiftsCard from '@/components/membership/GiftsCard'
import ReferralCard from '@/components/membership/ReferralCard'
import Seo from '@/components/Seo'
import { api, formatDate } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, Card, EmptyState, Input, Loading, useFeedback } from '@/components/ui'
import { entitlementLabel, formatEntitlement, type EntitlementDef } from '@/lib/entitlements'
import type { MembershipPlan, MembershipRecord, MyMembership, TrialInfo } from '@/lib/membership'
import { checkoutAvailable, checkoutHref, durationLabel, formatPrice } from '@/lib/commerce'

export default function MyMembershipPage() {
  return <FeatureGate feature="membership"><MyMembershipInner /></FeatureGate>
}

// PlanEntitlements 方案包含的权益（只列方案配置了的项）。
function PlanEntitlements({ plan, defs }: { plan: MembershipPlan; defs: EntitlementDef[] }) {
  const { t } = useTranslation()
  const items = defs.filter((d) => plan.entitlements && plan.entitlements[d.key] !== undefined)
  if (items.length === 0) return null
  return (
    <ul className="mt-3 space-y-1.5 text-sm">
      {items.map((d) => (
        <li key={d.key} className="flex items-center justify-between gap-3">
          <span className="flex min-w-0 items-center gap-2 text-slate-600"><i className="fa-solid fa-check text-xs text-emerald-500" aria-hidden="true" /><span className="truncate">{entitlementLabel(t, d.key)}</span></span>
          <span className="shrink-0 font-medium tabular-nums text-slate-900">{formatEntitlement(t, d, plan.entitlements![d.key])}</span>
        </li>
      ))}
    </ul>
  )
}

// RedeemCard 输入兑换码开通或续期会员（兑换码不区分大小写，可带连字符）。
function RedeemCard({ onRedeemed }: { onRedeemed: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  async function redeem() {
    setBusy(true)
    try {
      const r = await api<{ plan: { name: string }; expires_at: string }>('/membership/redeem', { method: 'POST', body: { code: code.trim() } })
      showToast({ message: t('membership.redeem.done', { plan: r.plan.name, date: formatDate(r.expires_at) }), tone: 'success' })
      setCode('')
      onRedeemed()
    } catch (e) {
      showToast({ title: t('membership.redeem.failed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(false)
    }
  }
  return (
    <Card className="mt-4 p-5">
      <div className="text-sm font-semibold text-slate-900"><i className="fa-solid fa-ticket mr-1.5 text-amber-500" aria-hidden="true" />{t('membership.redeem.title')}</div>
      <p className="mt-1 text-xs text-slate-500">{t('membership.redeem.hint')}</p>
      <div className="mt-3 flex max-w-md gap-2">
        <Input value={code} maxLength={64} placeholder="XXXX-XXXX-XXXX-XXXX" onChange={(e) => setCode(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter' && code.trim() && !busy) void redeem() }} />
        <Button className="shrink-0 whitespace-nowrap" loading={busy} disabled={!code.trim()} onClick={() => void redeem()}>{t('membership.redeem.submit')}</Button>
      </div>
    </Card>
  )
}

function MyMembershipInner() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t, locale } = useTranslation()
  const router = useRouter()
  const { confirmAction, showToast } = useFeedback()
  const siteName = site.site_name || 'KnowForge'
  const canBuy = checkoutAvailable(site)
  const [mine, setMine] = useState<{ membership: MyMembership | null; records: MembershipRecord[]; currency: string; trial: TrialInfo } | null>(null)
  const [startingTrial, setStartingTrial] = useState<number | null>(null)
  const [plans, setPlans] = useState<MembershipPlan[]>([])
  const [defs, setDefs] = useState<EntitlementDef[]>([])

  const loadMine = useCallback(() => {
    api<{ membership: MyMembership | null; records: MembershipRecord[]; currency: string; trial: TrialInfo }>('/users/me/membership').then(setMine).catch(() => {})
  }, [])
  useEffect(() => {
    if (!user) return
    loadMine()
    api<{ items: MembershipPlan[] }>('/membership/plans').then((r) => setPlans(r.items || [])).catch(() => {})
    api<{ items: EntitlementDef[] }>('/entitlements/definitions').then((r) => setDefs(r.items || [])).catch(() => {})
  }, [user, loadMine])

  if (!user || !mine) return <Loading className="min-h-[60vh]" />
  const m = mine.membership
  const currentPlanID = m?.active ? m.plan?.id : undefined

  // buy 前往结算页；有效期内购买其他方案会从现在起按新方案计算，先确认
  async function buy(plan: MembershipPlan, priceID: number) {
    if (currentPlanID && currentPlanID !== plan.id && m?.plan) {
      const ok = await confirmAction({ title: t('membership.switchTitle'), message: t('membership.switchMessage', { current: m.plan.name, next: plan.name, date: formatDate(m.expires_at) }), confirmLabel: t('membership.switchConfirm') })
      if (!ok) return
    }
    router.push(checkoutHref('membership', priceID))
  }

  // startTrial 领取方案的免费试用（每人一次）
  async function startTrial(plan: MembershipPlan) {
    const ok = await confirmAction({ title: t('membership.trial.confirmTitle', { plan: plan.name }), message: t('membership.trial.confirmMessage', { plan: plan.name, n: plan.trial_days }), confirmLabel: t('membership.trial.start') })
    if (!ok) return
    setStartingTrial(plan.id)
    try {
      const r = await api<{ expires_at: string }>(`/membership/plans/${plan.id}/trial`, { method: 'POST' })
      showToast({ message: t('membership.trial.started', { plan: plan.name, date: formatDate(r.expires_at) }), tone: 'success' })
      loadMine()
    } catch (e) {
      showToast({ title: t('membership.trial.failed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setStartingTrial(null)
    }
  }

  return (
    <>
      <Seo siteName={siteName} title={t('membership.seoTitle')} noindex />
      <Container>
        <div className="py-8">
          <h1 className="text-2xl font-bold text-ink">{t('membership.heading')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('membership.subtitle')}</p>

          {/* 当前会员 */}
          <Card className="mt-6 flex flex-col gap-5 p-6 sm:flex-row sm:items-center">
            <ResourceIcon iconType={m?.plan?.icon_type} iconValue={m?.plan?.icon_value} fallback="fa-crown"
              className={`flex h-16 w-16 shrink-0 items-center justify-center rounded-2xl border text-3xl ${m?.active ? 'border-amber-100 bg-amber-50 text-amber-500' : 'border-slate-100 bg-slate-50 text-slate-300'}`} />
            <div className="min-w-0 flex-1">
              {m && m.plan ? (
                <>
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-xl font-bold text-slate-900">{m.plan.name}</span>
                    <Badge tone={m.active ? 'amber' : 'slate'}>{m.active ? t('membership.active') : t('membership.expired')}</Badge>
                    {m.active && m.trial && <Badge tone="sky">{t('membership.trial.badge')}</Badge>}
                  </div>
                  <p className="mt-1.5 text-sm text-slate-500">
                    {m.active ? t('membership.expiresOn', { date: formatDate(m.expires_at), days: m.days_left }) : t('membership.expiredOn', { date: formatDate(m.expires_at) })}
                  </p>
                  {m.active && m.trial && <p className="mt-1 text-xs text-sky-700">{t('membership.trial.activeHint')}</p>}
                </>
              ) : (
                <>
                  <div className="text-xl font-bold text-slate-900">{t('membership.none')}</div>
                  <p className="mt-1.5 text-sm text-slate-500">{t('membership.noneHint')}</p>
                </>
              )}
            </div>
          </Card>

          <RedeemCard onRedeemed={loadMine} />

          {/* 可开通的方案 */}
          <h2 className="mt-8 font-bold text-slate-900">{t('membership.plans')}</h2>
          <p className="mt-1 text-xs text-slate-400">{canBuy ? t('membership.plansHintBuy') : t('membership.plansHint')}</p>
          {plans.length === 0 ? <div className="mt-3"><EmptyState>{t('membership.noPlans')}</EmptyState></div> : (
            <div className="mt-3 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              {plans.map((p) => (
                <Card key={p.id} className={`flex flex-col p-5 ${currentPlanID === p.id ? 'ring-2 ring-amber-300' : ''}`}>
                  <div className="flex items-center gap-3">
                    <ResourceIcon iconType={p.icon_type} iconValue={p.icon_value} fallback="fa-crown" className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-amber-100 bg-amber-50 text-amber-500" />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2"><span className="truncate font-bold text-slate-900">{p.name}</span>{currentPlanID === p.id && <Badge tone="amber">{t('membership.current')}</Badge>}</div>
                      {p.description && <p className="mt-0.5 line-clamp-2 text-xs text-slate-500">{p.description}</p>}
                    </div>
                  </div>
                  <PlanEntitlements plan={p} defs={defs} />
                  {p.trial_days > 0 && !m?.active && (mine.trial.eligible || mine.trial.needs_verified_email) && (
                    <div className="mt-4 rounded-lg border border-sky-100 bg-sky-50 px-3 py-2.5">
                      <div className="flex items-center justify-between gap-3">
                        <span className="text-sm font-medium text-sky-800"><i className="fa-solid fa-flask mr-1.5" aria-hidden="true" />{t('membership.trial.days', { n: p.trial_days })}</span>
                        <Button size="sm" className="shrink-0 whitespace-nowrap" disabled={!mine.trial.eligible} loading={startingTrial === p.id} onClick={() => void startTrial(p)}>{t('membership.trial.start')}</Button>
                      </div>
                      {mine.trial.needs_verified_email && <p className="mt-1 text-xs text-sky-700">{t('membership.trial.verifyEmail')}</p>}
                    </div>
                  )}
                  {p.prices.length > 0 && (
                    <div className="mt-4 space-y-1.5 border-t border-slate-100 pt-3">
                      {p.prices.map((pr) => (
                        <div key={pr.id} className="flex items-center justify-between gap-3 text-sm">
                          <span className="text-slate-600">{durationLabel(t, pr.duration_days)}</span>
                          <span className="flex items-center gap-2">
                            {pr.original_price_cents > pr.price_cents && <span className="text-xs text-slate-400 line-through">{formatPrice(pr.original_price_cents, mine.currency, locale)}</span>}
                            <span className="font-bold tabular-nums text-slate-900">{formatPrice(pr.price_cents, mine.currency, locale)}</span>
                            {canBuy && <Button size="sm" variant={currentPlanID === p.id ? 'outline' : 'primary'} onClick={() => buy(p, pr.id)}>{currentPlanID === p.id ? t('membership.renew') : t('membership.buy')}</Button>}
                            {canBuy && <Button size="sm" variant="outline" className="whitespace-nowrap" onClick={() => router.push(checkoutHref('membership_gift', pr.id))}><i className="fa-solid fa-gift mr-1 text-rose-500" aria-hidden="true" />{t('membership.gift.buy')}</Button>}
                          </span>
                        </div>
                      ))}
                    </div>
                  )}
                </Card>
              ))}
            </div>
          )}

          <GiftsCard />

          <ReferralCard />

          <MyEntitlementsCard />

          {/* 会员记录 */}
          {mine.records.length > 0 && (
            <Card className="mt-6 p-5">
              <h2 className="font-bold text-slate-900">{t('membership.records')}</h2>
              <ul className="mt-3 divide-y divide-slate-100">
                {mine.records.map((r) => (
                  <li key={r.id} className="flex flex-wrap items-center justify-between gap-2 py-2.5 text-sm">
                    <span className="flex min-w-0 items-center gap-2">
                      <Badge tone={r.action === 'revoke' ? 'rose' : 'amber'}>{t(`membership.action.${r.action}`)}</Badge>
                      {r.source === 'trial' && <Badge tone="sky">{t('membership.trial.badge')}</Badge>}
                      {r.source === 'referral' && <Badge tone="violet">{t('membership.referral.badge')}</Badge>}
                      <span className="truncate text-slate-700">{r.plan_name}{r.days > 0 ? ` · ${durationLabel(t, r.days)}` : ''}</span>
                    </span>
                    <span className="text-xs text-slate-400">
                      {formatDate(r.created_at)}{r.expires_at ? ` · ${t('membership.until', { date: formatDate(r.expires_at) })}` : ''}
                    </span>
                  </li>
                ))}
              </ul>
            </Card>
          )}
        </div>
      </Container>
    </>
  )
}
