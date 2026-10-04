import { useCallback, useEffect, useState } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/router'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import Seo from '@/components/Seo'
import TeamBooks from '@/components/teams/TeamBooks'
import TeamMembers from '@/components/teams/TeamMembers'
import TeamSettings from '@/components/teams/TeamSettings'
import { api } from '@/lib/api'
import { useApp, useRequireAuth } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { TEAM_ROLE_KEYS, teamTab, type TeamDetail } from '@/lib/teams'
import { Badge, ButtonLink, EmptyState, Loading, SegmentedTabs } from '@/components/ui'

// 团队主页：团队书籍、成员与设置（?tab=books|members|settings）。只有团队成员可以查看。
export default function TeamPage() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  const router = useRouter()
  const slug = typeof router.query.slug === 'string' ? router.query.slug : ''
  if (!user || !slug) return <Loading className="min-h-[60vh]" />
  return (
    <FeatureGate feature="teams">
      <Seo siteName={site.site_name || 'KnowForge'} title={t('teams.page.title')} noindex />
      <Container>
        <TeamInner key={slug} slug={slug} />
      </Container>
    </FeatureGate>
  )
}

function TeamInner({ slug }: { slug: string }) {
  const { t } = useTranslation()
  const { site } = useApp()
  const membershipOn = (site.feature_plugins || []).includes('membership')
  const router = useRouter()
  const [data, setData] = useState<TeamDetail | null>(null)
  const [error, setError] = useState('')
  const tab = teamTab(router.query.tab)

  const load = useCallback(async () => {
    try {
      setData(await api<TeamDetail>(`/teams/${encodeURIComponent(slug)}`))
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }, [slug])
  useEffect(() => { void load() }, [load])

  if (error && !data) {
    return (
      <div className="py-16">
        <EmptyState>{error}</EmptyState>
        <div className="mt-4 text-center"><Link href="/teams" className="text-sm text-primary-600 hover:underline">{t('teams.page.back')}</Link></div>
      </div>
    )
  }
  if (!data) return <Loading className="py-16" />
  const { team } = data
  const base = `/teams/${encodeURIComponent(team.slug)}`
  return (
    <>
      <nav className="flex items-center gap-1.5 py-4 text-sm text-slate-500">
        <Link href="/teams" className="hover:text-primary-600">{t('teams.page.title')}</Link>
        <span className="text-slate-300">/</span>
        <span className="truncate text-slate-900">{team.name}</span>
      </nav>
      <div className="flex flex-wrap items-start gap-4 pb-6">
        <span className="flex h-14 w-14 shrink-0 items-center justify-center rounded-xl bg-primary-50 text-2xl font-bold text-primary-600">{team.name.slice(0, 1).toUpperCase()}</span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-2xl font-bold text-ink" data-testid="team-name">{team.name}</h1>
            {team.my_role && <Badge tone="primary">{t(TEAM_ROLE_KEYS[team.my_role])}</Badge>}
          </div>
          {team.description && <p className="mt-1 text-[15px] text-slate-500">{team.description}</p>}
          <div className="mt-2 flex gap-4 text-xs text-slate-500">
            <span>{t('teams.stats.members', { n: team.member_count })}</span>
            <span>{t('teams.stats.books', { n: team.book_count })}</span>
          </div>
        </div>
        {membershipOn && data.can_manage && team.my_role && (
          <ButtonLink href="/user/membership/teams" variant="outline" size="sm" data-testid="team-membership-link">
            <i className="fa-solid fa-crown text-amber-500" aria-hidden="true" /> {t('teams.page.membership')}
          </ButtonLink>
        )}
      </div>
      <SegmentedTabs className="mb-6" value={tab} ariaLabel={t('teams.page.tabs')} items={[
        { value: 'books', label: t('teams.tab.books'), href: base },
        { value: 'members', label: t('teams.tab.members'), href: `${base}?tab=members` },
        { value: 'settings', label: t('teams.tab.settings'), href: `${base}?tab=settings` },
      ]} />
      {tab === 'books' && <TeamBooks data={data} reload={load} />}
      {tab === 'members' && <TeamMembers data={data} reload={load} />}
      {tab === 'settings' && <TeamSettings data={data} reload={load} />}
    </>
  )
}
