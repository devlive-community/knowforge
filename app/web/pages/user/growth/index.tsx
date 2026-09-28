import { useEffect, useState } from 'react'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import ResourceIcon from '@/components/ResourceIcon'
import CheckinCard from '@/components/CheckinCard'
import MyEntitlementsCard from '@/components/MyEntitlementsCard'
import Seo from '@/components/Seo'
import { api, formatNumber } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, ButtonLink, Card, EmptyState, Loading, Pagination, Switch, useFeedback } from '@/components/ui'
import { growthReasonLabel, growthRuleLabel } from '@/lib/growth'

interface Level { id: number; level: number; name: string; icon_type?: string; icon_value?: string; color?: string; min_xp: number }
interface Growth {
  lifetime_xp: number
  current_level: number
  highest_level: number
  public: boolean
  level: Level
  next_level?: Level
  xp_to_next?: number
  progress_percent: number
}
interface XPEvent { id: number; rule_key: string; base_xp: number; final_xp: number; reason?: string; created_at: string }

export default function MyGrowthPage() {
  return <FeatureGate feature="growth"><MyGrowthInner /></FeatureGate>
}

function MyGrowthInner() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const siteName = site.site_name || 'KnowForge'
  const [growth, setGrowth] = useState<Growth | null>(null)
  const [levels, setLevels] = useState<Level[]>([])
  const [events, setEvents] = useState<{ items: XPEvent[]; total: number; page: number; page_size: number } | null>(null)
  const [page, setPage] = useState(1)
  const [savingPublic, setSavingPublic] = useState(false)
  const [reloadKey, setReloadKey] = useState(0) // 签到后刷新经验与流水

  useEffect(() => {
    if (!user) return
    api<Growth>('/users/me/growth').then(setGrowth).catch(() => {})
    api<{ items: Level[] }>('/growth/levels').then((r) => setLevels(r.items || [])).catch(() => {})
  }, [user, reloadKey])

  useEffect(() => {
    if (!user) return
    api<{ items: XPEvent[]; total: number; page: number; page_size: number }>('/users/me/experience-events', { params: { page, page_size: 10 } })
      .then(setEvents).catch(() => {})
  }, [user, page, reloadKey])

  async function togglePublic(next: boolean) {
    if (!growth) return
    setSavingPublic(true)
    setGrowth({ ...growth, public: next })
    try {
      await api('/users/me/growth/display', { method: 'PUT', body: { public: next } })
    } catch (e) {
      setGrowth({ ...growth, public: !next })
      showToast({ title: t('growth.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSavingPublic(false)
    }
  }

  if (!user || !growth) return <Loading className="min-h-[60vh]" label={t('growth.loading')} />

  return (
    <>
      <Seo siteName={siteName} title={t('growth.seoTitle')} noindex />
      <Container>
        <div className="py-8">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h1 className="text-2xl font-bold text-ink">{t('growth.heading')}</h1>
              <p className="mt-1 text-sm text-slate-500">{t('growth.subtitle')}</p>
            </div>
            {site.growth_leaderboard_enabled && <ButtonLink href="/growth/leaderboard" variant="outline"><i className="fa-solid fa-ranking-star" aria-hidden="true" /> {t('growth.leaderboard.link')}</ButtonLink>}
          </div>

          {/* 当前等级 + 进度 */}
          <Card className="mt-6 flex flex-col gap-5 p-6 sm:flex-row sm:items-center">
            <ResourceIcon iconType={growth.level.icon_type} iconValue={growth.level.icon_value} fallback="fa-star"
              className="flex h-16 w-16 shrink-0 items-center justify-center rounded-2xl border border-primary-100 bg-primary-50 text-3xl text-primary-600" />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-xl font-bold text-slate-900">{growth.level.name || `Lv.${growth.current_level}`}</span>
                <Badge tone="primary">{t('growth.totalXp', { xp: formatNumber(growth.lifetime_xp) })}</Badge>
              </div>
              <div className="mt-3 h-2.5 w-full overflow-hidden rounded-full bg-slate-100">
                <div className="h-full rounded-full bg-primary-500" style={{ width: `${growth.progress_percent}%` }} />
              </div>
              <p className="mt-1.5 text-xs text-slate-400">
                {growth.next_level
                  ? t('growth.toNext', { xp: formatNumber(Math.max(0, growth.xp_to_next || 0)), level: growth.next_level.name || `Lv.${growth.next_level.level}` })
                  : t('growth.maxLevel')}
              </p>
            </div>
            <label className="flex shrink-0 items-center gap-2 text-sm text-slate-600">
              <Switch ariaLabel={t('growth.publicToggle')} checked={growth.public} disabled={savingPublic} onChange={togglePublic} />
              {t('growth.publicToggle')}
            </label>
          </Card>

          <CheckinCard onCheckedIn={() => { setPage(1); setReloadKey((k) => k + 1) }} />
          <MyEntitlementsCard reloadKey={reloadKey} />

          <div className="mt-6 grid gap-6 lg:grid-cols-[1fr_1.2fr]">
            {/* 等级路线 */}
            <Card className="p-5">
              <h2 className="font-bold text-slate-900">{t('growth.ladder')}</h2>
              <ul className="mt-3 space-y-1.5">
                {levels.map((lv) => {
                  const reached = growth.highest_level >= lv.level
                  const current = growth.current_level === lv.level
                  return (
                    <li key={lv.id} className={`flex items-center gap-3 rounded-lg px-3 py-2 ${current ? 'bg-primary-50' : ''}`}>
                      <ResourceIcon iconType={lv.icon_type} iconValue={lv.icon_value} fallback="fa-star"
                        className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border ${reached ? 'border-primary-100 bg-primary-50 text-primary-600' : 'border-slate-200 bg-slate-50 text-slate-300'}`} />
                      <span className={`min-w-0 flex-1 truncate text-sm ${reached ? 'font-medium text-slate-800' : 'text-slate-400'}`}>{lv.name || `Lv.${lv.level}`}</span>
                      <span className="shrink-0 text-xs text-slate-400">{formatNumber(lv.min_xp)} XP</span>
                    </li>
                  )
                })}
              </ul>
            </Card>

            {/* 经验流水 */}
            <Card className="p-5">
              <h2 className="font-bold text-slate-900">{t('growth.ledger')}</h2>
              {!events || events.total === 0 ? (
                <div className="mt-3"><EmptyState>{t('growth.ledgerEmpty')}</EmptyState></div>
              ) : (
                <>
                  <ul className="mt-3 divide-y divide-slate-100">
                    {events.items.map((e) => (
                      <li key={e.id} className="flex items-center justify-between gap-3 py-2.5">
                        <span className="min-w-0">
                          <span className="block truncate text-sm text-slate-700">{growthRuleLabel(t, e.rule_key)}</span>
                          <span className="text-xs text-slate-400">{new Date(e.created_at).toLocaleString()}{e.reason ? ` · ${growthReasonLabel(t, e.reason)}` : ''}{e.base_xp > 0 && e.final_xp > e.base_xp ? ` · ${t('growth.ledgerBonus', { n: e.final_xp - e.base_xp })}` : ''}</span>
                        </span>
                        <span className={`shrink-0 text-sm font-semibold ${e.final_xp >= 0 ? 'text-emerald-600' : 'text-rose-600'}`}>{e.final_xp >= 0 ? '+' : ''}{e.final_xp}</span>
                      </li>
                    ))}
                  </ul>
                  <Pagination page={events.page} pageSize={events.page_size} total={events.total} onChange={setPage} />
                </>
              )}
            </Card>
          </div>
        </div>
      </Container>
    </>
  )
}
