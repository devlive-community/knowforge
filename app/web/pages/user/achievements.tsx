import { useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/router'
import AchievementIcon from '@/components/AchievementIcon'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import Seo from '@/components/Seo'
import { api } from '@/lib/api'
import { useApp, useRequireAuth } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, Card, EmptyState, Loading, Pagination, SegmentedTabs, Switch, useFeedback } from '@/components/ui'
import type { AchievementGrant, UserAchievementItem } from '@/lib/types'

type Tab = 'unlocked' | 'progress' | 'all'

const PAGE_SIZE = 12

interface AchievementPageData {
  enabled: boolean
  items: UserAchievementItem[]
  unlocked_count: number
  total: number
  allow_user_hide: boolean
}

const categoryLabels: Record<string, string> = { reading: 'myAch.cat.reading', creation: 'myAch.cat.creation', community: 'myAch.cat.community', account: 'myAch.cat.account', special: 'myAch.cat.special' }
const rarityLabels: Record<string, string> = { common: 'myAch.rarity.common', rare: 'myAch.rarity.rare', epic: 'myAch.rarity.epic', legendary: 'myAch.rarity.legendary' }
const rarityTone: Record<string, 'slate' | 'sky' | 'violet' | 'amber'> = { common: 'slate', rare: 'sky', epic: 'violet', legendary: 'amber' }

export default function MyAchievementsPage() {
  return <FeatureGate feature="achievements"><MyAchievementsInner /></FeatureGate>
}

function MyAchievementsInner() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { showToast } = useFeedback()
  const { locale, t } = useTranslation()
  const router = useRouter()
  // Tab（筛选）用查询参数承载，不用本地 state
  const tab: Tab = router.query.tab === 'progress' || router.query.tab === 'all' ? router.query.tab : 'unlocked'
  const [page, setPage] = useState(1)
  const setTab = (next: Tab) => {
    setPage(1)
    return router.push({ query: next === 'unlocked' ? {} : { tab: next } }, undefined, { shallow: true })
  }
  const [data, setData] = useState<AchievementPageData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [updating, setUpdating] = useState<number | null>(null)

  useEffect(() => {
    if (!user) return
    api<AchievementPageData>('/users/me/achievements', { params: { locale } })
      .then(setData)
      .catch((cause) => { const message = (cause as Error).message || t('myAch.loadFailedMsg'); setError(message); showToast({ title: t('myAch.loadFailed'), message, tone: 'error' }) })
      .finally(() => setLoading(false))
  }, [showToast, user, t, locale])

  const items = useMemo(() => {
    const source = data?.items || []
    if (tab === 'unlocked') return source.filter((item) => item.unlocked)
    if (tab === 'progress') {
      // 进行中：按进度从高到低排序，最接近解锁的排在前面
      return source
        .filter((item) => !item.unlocked && (item.progress?.percent || 0) > 0)
        .sort((a, b) => (b.progress?.percent || 0) - (a.progress?.percent || 0))
    }
    return source
  }, [data, tab])

  const visibleItems = items.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE)

  async function updateDisplay(grant: AchievementGrant, patch: { is_public?: boolean; showcase_order?: number }) {
    setUpdating(grant.id)
    try {
      const updated = await api<AchievementGrant>(`/users/me/achievements/${grant.id}/display`, { method: 'PUT', body: patch })
      setData((current) => current ? { ...current, items: current.items.map((item) => item.grant?.id === grant.id ? { ...item, grant: updated } : item) } : current)
      showToast({ message: t('myAch.displayUpdated'), tone: 'success' })
    } catch (error) {
      showToast({ title: t('myAch.saveFailed'), message: (error as Error).message, tone: 'error' })
    } finally {
      setUpdating(null)
    }
  }

  if (!user || loading) return <Loading className="min-h-[60vh]" label={t('myAch.loading')} />

  const siteName = site.site_name || 'KnowForge'
  const percentage = data?.total ? Math.round((data.unlocked_count / data.total) * 100) : 0

  return (
    <>
      <Seo siteName={siteName} title={t('myAch.seoTitle')} noindex />
      <Container>
        <div className="py-8">
          <div className="flex flex-col gap-5 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm md:flex-row md:items-center md:justify-between">
            <div><p className="text-sm font-medium text-primary-600">{t('myAch.growth')}</p><h1 className="mt-1 text-3xl font-bold text-slate-900">{t('myAch.title')}</h1><p className="mt-2 text-sm text-slate-500">{t('myAch.subtitle')}</p></div>
            <div className="grid grid-cols-3 gap-3 text-center md:w-[360px]">
              <div className="rounded-xl bg-slate-50 px-3 py-4"><strong className="block text-2xl text-slate-900">{data?.unlocked_count || 0}</strong><span className="text-xs text-slate-400">{t('myAch.unlockedStat')}</span></div>
              <div className="rounded-xl bg-slate-50 px-3 py-4"><strong className="block text-2xl text-slate-900">{data?.total || 0}</strong><span className="text-xs text-slate-400">{t('myAch.totalStat')}</span></div>
              <div className="rounded-xl bg-primary-50 px-3 py-4"><strong className="block text-2xl text-primary-700">{percentage}%</strong><span className="text-xs text-primary-500">{t('myAch.completion')}</span></div>
            </div>
          </div>

          {error ? (
            <Card className="mt-6 border-rose-200 bg-rose-50 p-5 text-sm text-rose-700"><i className="fa-solid fa-circle-exclamation mr-2" aria-hidden="true" />{error}</Card>
          ) : !data?.enabled ? (
            <EmptyState><i className="fa-solid fa-trophy mb-3 block text-3xl text-slate-300" aria-hidden="true" />{t('myAch.notEnabled')}</EmptyState>
          ) : (
            <>
              <div className="mt-6"><SegmentedTabs value={tab} onChange={(value) => setTab(value as Tab)} ariaLabel={t('myAch.statusAria')} items={[{ value: 'unlocked', label: t('myAch.tabUnlocked', { n: data.unlocked_count }) }, { value: 'progress', label: t('myAch.tabProgress') }, { value: 'all', label: t('myAch.tabAll', { n: data.total }) }]} /></div>
              {items.length === 0 ? <div className="mt-6"><EmptyState>{tab === 'unlocked' ? t('myAch.emptyUnlocked') : tab === 'progress' ? t('myAch.emptyProgress') : t('myAch.emptyAll')}</EmptyState></div> : (
                <>
                  <div className="mt-6 grid gap-4 grid-cols-[repeat(auto-fill,minmax(18rem,1fr))]">
                    {visibleItems.map((item) => {
                    const definition = item.definition
                    const progress = item.progress?.percent || 0
                    const name = definition.name
                    const description = definition.description
                    return (
                      <Card key={definition.id} className={`p-5 ${item.unlocked ? '' : 'bg-white/70'}`}>
                        <div className="flex items-start gap-4">
                          <AchievementIcon achievement={definition} muted={!item.unlocked} />
                          <div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-2"><h2 className="font-semibold text-slate-900">{name}</h2><Badge tone={rarityTone[definition.rarity]}>{t(rarityLabels[definition.rarity])}</Badge></div><p className="mt-1 text-xs text-slate-400">{t(categoryLabels[definition.category])}{definition.series_key ? ` · ${t('myAch.tier', { n: definition.tier })}` : ''}</p></div>
                        </div>
                        <p className="mt-4 min-h-[40px] text-sm leading-5 text-slate-500">{description || definition.locked_hint || t('myAch.lockedHint')}</p>
                        {definition.progress_mode !== 'hidden' && <div className="mt-4"><div className="mb-1.5 flex items-center justify-between text-xs text-slate-400"><span>{item.unlocked ? t('myAch.doneLabel') : t('myAch.currentProgress')}</span><span>{item.unlocked ? 100 : progress}%</span></div><div className="h-2 overflow-hidden rounded-full bg-slate-100"><span className="block h-full rounded-full bg-primary-500 transition-all" style={{ width: `${item.unlocked ? 100 : progress}%` }} /></div></div>}
                        {item.grant && <div className="mt-4 border-t border-slate-100 pt-4"><div className="flex items-center justify-between gap-3"><span className="text-xs text-slate-400">{t('myAch.unlockedAt', { date: new Date(item.grant.unlocked_at).toLocaleDateString(locale) })}</span><div className="flex items-center gap-3"><label className="flex items-center gap-2 text-xs text-slate-500"><span>{definition.visibility !== 'private' ? t('myAch.public') : t('myAch.selfOnly')}</span><Switch ariaLabel={t('myAch.publicAria')} checked={item.grant.is_public} disabled={definition.visibility === 'private' || (!data.allow_user_hide && item.grant.is_public) || updating === item.grant.id} onChange={(value) => updateDisplay(item.grant as AchievementGrant, { is_public: value })} /></label><Button variant={item.grant.showcase_order > 0 ? 'primary' : 'outline'} disabled={definition.visibility === 'private'} loading={updating === item.grant.id} onClick={() => updateDisplay(item.grant as AchievementGrant, { showcase_order: item.grant?.showcase_order ? 0 : 1 })}>{item.grant.showcase_order > 0 ? t('myAch.pinned') : t('myAch.pin')}</Button></div></div></div>}
                      </Card>
                    )
                    })}
                  </div>
                  <Pagination page={page} pageSize={PAGE_SIZE} total={items.length} onChange={setPage} />
                </>
              )}
            </>
          )}
        </div>
      </Container>
    </>
  )
}
