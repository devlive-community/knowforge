import { useCallback, useEffect, useState } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/router'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import Seo from '@/components/Seo'
import UserAvatar from '@/components/UserAvatar'
import { api, formatDate } from '@/lib/api'
import { useApp, useRequireAuth } from '@/lib/auth'
import { checkoutAvailable, checkoutHref, durationLabel, formatPrice } from '@/lib/commerce'
import { useTranslation } from '@/lib/i18n'
import { GROUP_PRODUCT_KIND, SEATS_PRODUCT_KIND, groupSKU, seatsSKU, type GroupSubscription, type MemberGroup, type MembershipPlan } from '@/lib/membership'
import { displayName } from '@/lib/users'
import { Badge, Button, Card, EmptyState, Field, Input, Loading, Select } from '@/components/ui'

interface GroupsData {
  items: { group: MemberGroup; subscription: GroupSubscription | null }[]
  plans: MembershipPlan[]
  currency: string
  max_seats: number
}

interface SeatMember { id: number; username: string; nickname?: string; name_display?: string; avatar?: string; covered: boolean }

export default function TeamMembershipPage() {
  return <FeatureGate feature="membership"><Inner /></FeatureGate>
}

// 团队会员：为我管理的团队（所有者 / 管理员）按席位开通会员。团队中按次序的前 N 名成员享有方案权益，
// 有效期内可以续期或增加席位（按剩余天数折算）。
function Inner() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  const [data, setData] = useState<GroupsData | null>(null)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api<GroupsData>('/membership/groups').then(setData).catch((e) => setError((e as Error).message))
  }, [])
  useEffect(() => { if (user) load() }, [user, load])

  if (!user) return <Loading className="min-h-[60vh]" />
  return (
    <>
      <Seo siteName={site.site_name || 'KnowForge'} title={t('membership.groupPage.title')} noindex />
      <Container>
        <div className="py-8">
          <nav className="mb-3 flex items-center gap-1.5 text-sm text-slate-500">
            <Link href="/user/membership" className="hover:text-primary-600">{t('membership.heading')}</Link>
            <span className="text-slate-300">/</span>
            <span className="text-slate-900">{t('membership.groupPage.title')}</span>
          </nav>
          <h1 className="text-2xl font-bold text-ink">{t('membership.groupPage.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('membership.groupPage.description')}</p>
          {error ? <div className="mt-6"><EmptyState>{error}</EmptyState></div> : !data ? <Loading className="py-16" /> : data.items.length === 0 ? (
            <div className="mt-6">
              <EmptyState>{t('membership.groupPage.noGroups')}</EmptyState>
              <div className="mt-3 text-center"><Link href="/teams" className="text-sm font-medium text-primary-600 hover:underline">{t('membership.groupPage.goTeams')}</Link></div>
            </div>
          ) : (
            <div className="mt-6 space-y-5">
              {!checkoutAvailable(site) && <p className="rounded-lg bg-amber-50 px-4 py-3 text-sm text-amber-700">{t('membership.groupPage.noCheckout')}</p>}
              {data.plans.length === 0 && <p className="rounded-lg bg-slate-50 px-4 py-3 text-sm text-slate-500">{t('membership.groupPage.noPlans')}</p>}
              {data.items.map((it) => <GroupCard key={`${it.group.kind}:${it.group.id}`} item={it} data={data} />)}
            </div>
          )}
        </div>
      </Container>
    </>
  )
}

function GroupCard({ item, data }: { item: GroupsData['items'][number]; data: GroupsData }) {
  const { t, locale } = useTranslation()
  const { site } = useApp()
  const router = useRouter()
  const { group, subscription: sub } = item
  const canBuy = checkoutAvailable(site)
  const active = !!sub?.active
  const plan = active ? data.plans.find((p) => p.id === sub!.plan?.id) || sub!.plan : null
  // 开通：所有方案的每档价格；续期：当前方案的价格
  const options = (active && plan ? [plan] : data.plans).flatMap((p) => p.prices.map((pr) => ({ plan: p, price: pr })))
  const [priceId, setPriceId] = useState(String(options[0]?.price.id || ''))
  const [seats, setSeats] = useState(String(active ? sub!.seats : Math.max(1, group.member_count)))
  const [add, setAdd] = useState(String(Math.max(1, group.member_count - (sub?.seats || 0))))
  const [showSeats, setShowSeats] = useState(false)
  const [members, setMembers] = useState<SeatMember[] | null>(null)
  const [navigating, setNavigating] = useState<'buy' | 'seats' | null>(null)

  const chosen = options.find((o) => String(o.price.id) === priceId)
  const seatCount = Math.min(data.max_seats, Math.max(1, Math.floor(Number(seats) || 0)))
  const addCount = Math.min(data.max_seats - (sub?.seats || 0), Math.max(1, Math.floor(Number(add) || 0)))
  const uncovered = active ? Math.max(0, group.member_count - sub!.seats) : 0

  async function toggleSeats() {
    setShowSeats((v) => !v)
    if (members) return
    try {
      const d = await api<{ members: SeatMember[] }>(`/membership/groups/${group.kind}/${group.id}`)
      setMembers(d.members)
    } catch { setMembers([]) }
  }

  function go(kind: 'buy' | 'seats') {
    setNavigating(kind)
    void router.push(kind === 'buy' ? checkoutHref(GROUP_PRODUCT_KIND, groupSKU(Number(priceId), group, seatCount)) : checkoutHref(SEATS_PRODUCT_KIND, seatsSKU(group, addCount)))
  }

  return (
    <Card className="p-6" data-testid="group-membership">
      <div className="flex flex-wrap items-center gap-3">
        <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-violet-50 text-violet-600"><i className="fa-solid fa-people-group" aria-hidden="true" /></span>
        <div className="min-w-0 flex-1">
          <Link href={group.link} className="font-semibold text-slate-900 hover:text-primary-600">{group.name}</Link>
          <div className="text-xs text-slate-400">{t('membership.groupPage.memberCount', { n: group.member_count })}</div>
        </div>
        {active ? <Badge tone="violet">{t('membership.groupPage.active')}</Badge> : sub ? <Badge tone="slate">{t('membership.groupPage.expired')}</Badge> : <Badge tone="slate">{t('membership.groupPage.none')}</Badge>}
      </div>

      {active && (
        <div className="mt-4 rounded-xl bg-slate-50 px-4 py-3 text-sm text-slate-600" data-testid="group-subscription">
          <div className="font-medium text-slate-900">{t('membership.groupPage.current', { plan: sub!.plan?.name || '', seats: sub!.seats })}</div>
          <div className="mt-1 text-xs">{t('membership.groupPage.expires', { date: formatDate(sub!.expires_at), days: sub!.days_left, covered: sub!.covered })}</div>
          {uncovered > 0 && <div className="mt-1 text-xs text-amber-700">{t('membership.groupPage.uncovered', { n: uncovered })}</div>}
          <button type="button" className="mt-2 text-xs font-medium text-primary-600 hover:underline" onClick={() => void toggleSeats()}>
            {showSeats ? t('membership.groupPage.hideSeats') : t('membership.groupPage.showSeats')}
          </button>
          {showSeats && (members === null ? <Loading className="py-4" /> : (
            <ul className="mt-2 grid gap-2 sm:grid-cols-2">
              {members.map((m) => (
                <li key={m.id} className="flex items-center gap-2 rounded-lg bg-white px-3 py-2">
                  <UserAvatar user={{ username: m.username || '?', avatar: m.avatar || '' }} size="h-6 w-6" />
                  <span className="min-w-0 flex-1 truncate text-xs text-slate-700">{displayName(m)}</span>
                  <Badge tone={m.covered ? 'emerald' : 'slate'}>{m.covered ? t('membership.groupPage.seatCovered') : t('membership.groupPage.seatNone')}</Badge>
                </li>
              ))}
            </ul>
          ))}
        </div>
      )}

      {canBuy && options.length > 0 && (
        <div className="mt-4 grid gap-4 border-t border-slate-100 pt-4 md:grid-cols-2">
          <div>
            <div className="text-sm font-medium text-slate-900">{active ? t('membership.groupPage.renewTitle') : t('membership.groupPage.buyTitle')}</div>
            <div className="mt-2 space-y-3">
              <Field label={t('membership.groupPage.planPrice')}>
                <Select value={priceId} onChange={setPriceId} options={options.map((o) => ({
                  value: String(o.price.id),
                  label: t('membership.groupPage.priceOption', { plan: o.plan.name, duration: durationLabel(t, o.price.duration_days), price: formatPrice(o.price.price_cents, data.currency, locale) }),
                }))} />
              </Field>
              <Field label={t('membership.groupPage.seats')} hint={active ? t('membership.groupPage.seatsFixed') : t('membership.groupPage.seatsHint')}>
                <Input type="number" min={1} max={data.max_seats} value={seats} disabled={active} onChange={(e) => setSeats(e.target.value)} />
              </Field>
              <div className="flex flex-wrap items-center gap-3">
                {chosen && <span className="text-sm text-slate-600">{t('membership.groupPage.total', { price: formatPrice(chosen.price.price_cents * seatCount, data.currency, locale) })}</span>}
                <Button className="ml-auto" loading={navigating === 'buy'} disabled={navigating !== null || !chosen} onClick={() => go('buy')} data-testid="group-buy">
                  {active ? t('membership.groupPage.renew') : t('membership.groupPage.buy')}
                </Button>
              </div>
            </div>
          </div>
          {active && sub!.seats < data.max_seats && (
            <div>
              <div className="text-sm font-medium text-slate-900">{t('membership.groupPage.addTitle')}</div>
              <div className="mt-2 space-y-3">
                <Field label={t('membership.groupPage.addSeats')} hint={t('membership.groupPage.addHint', { days: sub!.days_left })}>
                  <Input type="number" min={1} max={data.max_seats - sub!.seats} value={add} onChange={(e) => setAdd(e.target.value)} />
                </Field>
                <div className="flex justify-end">
                  <Button variant="outline" loading={navigating === 'seats'} disabled={navigating !== null} onClick={() => go('seats')} data-testid="group-add-seats">
                    {t('membership.groupPage.addButton', { n: addCount })}
                  </Button>
                </div>
              </div>
            </div>
          )}
        </div>
      )}
    </Card>
  )
}
