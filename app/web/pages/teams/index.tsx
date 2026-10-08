import { useCallback, useEffect, useState } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/router'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import Seo from '@/components/Seo'
import UserAvatar from '@/components/UserAvatar'
import { api } from '@/lib/api'
import { useApp, useRequireAuth } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { TEAM_ROLE_KEYS, type Team, type TeamInvitation } from '@/lib/teams'
import { Badge, Button, Card, EmptyState, Field, Input, Loading, Modal, Pagination, Textarea, useFeedback } from '@/components/ui'

interface Mine {
  teams: Team[]
  teams_total: number
  teams_page: number
  teams_page_size: number
  invitations: TeamInvitation[]
  invitations_total: number
  invitations_page: number
  invitations_page_size: number
  owned: number
  owned_limit: number
}

// 我的团队：加入的团队、待接受的邀请与创建团队。
export default function TeamsPage() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  if (!user) return <Loading className="min-h-[60vh]" />
  return (
    <FeatureGate feature="teams">
      <Seo siteName={site.site_name || 'KnowForge'} title={t('teams.page.title')} noindex />
      <Container>
        <TeamsInner />
      </Container>
    </FeatureGate>
  )
}

function TeamsInner() {
  const { t } = useTranslation()
  const router = useRouter()
  const { showToast } = useFeedback()
  const [data, setData] = useState<Mine | null>(null)
  const [teamPage, setTeamPage] = useState(1)
  const [invitationPage, setInvitationPage] = useState(1)
  const [responding, setResponding] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [desc, setDesc] = useState('')
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      const result = await api<Mine>('/teams', { params: {
        teams_page: teamPage, teams_page_size: 12,
        invitations_page: invitationPage, invitations_page_size: 12,
      } })
      setData(result)
      if (result.teams_page !== teamPage) setTeamPage(result.teams_page)
      if (result.invitations_page !== invitationPage) setInvitationPage(result.invitations_page)
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
  }, [teamPage, invitationPage, showToast])
  useEffect(() => { void load() }, [load])

  async function respond(inv: TeamInvitation, accept: boolean) {
    setResponding(`${inv.id}:${accept}`)
    try {
      await api(`/team-invitations/${inv.id}/${accept ? 'accept' : 'decline'}`, { method: 'POST' })
      showToast({ message: accept ? t('teams.invite.joined', { team: inv.team.name }) : t('teams.invite.declined'), tone: 'success' })
      await load()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setResponding(null)
    }
  }

  async function create() {
    setSaving(true)
    try {
      const team = await api<Team>('/teams', { method: 'POST', body: { name: name.trim(), description: desc.trim() } })
      setCreating(false)
      setName('')
      setDesc('')
      await router.push(`/teams/${team.slug}`)
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  const canCreate = data ? data.owned_limit < 0 || data.owned < data.owned_limit : false
  return (
    <>
      <div className="flex flex-wrap items-end gap-3 pb-6">
        <div className="min-w-0 flex-1">
          <h1 className="text-3xl font-bold text-ink">{t('teams.page.title')}</h1>
          <p className="mt-2 text-[15px] text-slate-500">{t('teams.page.description')}</p>
        </div>
        {data && (
          <Button disabled={!canCreate} onClick={() => setCreating(true)} data-testid="team-create">
            <i className="fa-solid fa-plus" aria-hidden="true" /> {t('teams.create.button')}
          </Button>
        )}
      </div>
      {!data ? <Loading className="py-16" /> : (
        <div className="space-y-6">
          {data.owned_limit >= 0 && (
            <p className="text-xs text-slate-400">{t('teams.create.quota', { used: data.owned, limit: data.owned_limit })}</p>
          )}
          {data.invitations_total > 0 && (
            <Card className="p-5" data-testid="team-invitations">
              <h2 className="font-semibold text-slate-900">{t('teams.invite.pendingTitle')}</h2>
              <ul className="mt-3 divide-y divide-slate-100">
                {data.invitations.map((inv) => (
                  <li key={inv.id} className="flex flex-wrap items-center gap-3 py-3">
                    <UserAvatar user={{ username: inv.inviter.username || '?', avatar: inv.inviter.avatar || '' }} size="h-8 w-8" />
                    <div className="min-w-0 flex-1 text-sm text-slate-700">
                      {t('teams.invite.from', { user: inv.inviter.username, team: inv.team.name })}
                      <span className="ml-2"><Badge tone="sky">{t(TEAM_ROLE_KEYS[inv.role])}</Badge></span>
                    </div>
                    <Button size="sm" variant="outline" loading={responding === `${inv.id}:false`} disabled={responding !== null} onClick={() => void respond(inv, false)}>{t('teams.invite.decline')}</Button>
                    <Button size="sm" loading={responding === `${inv.id}:true`} disabled={responding !== null} onClick={() => void respond(inv, true)}>{t('teams.invite.accept')}</Button>
                  </li>
                ))}
              </ul>
              <Pagination page={data.invitations_page} pageSize={data.invitations_page_size} total={data.invitations_total} onChange={setInvitationPage} />
            </Card>
          )}
          {data.teams_total === 0 ? (
            <EmptyState>{t('teams.page.empty')}</EmptyState>
          ) : (
            <>
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3" data-testid="team-list">
                {data.teams.map((team) => (
                  <Link key={team.id} href={`/teams/${team.slug}`} className="group rounded-xl border border-slate-200 bg-white p-5 shadow-sm transition hover:border-primary-300 hover:shadow">
                    <div className="flex items-center gap-3">
                      <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-lg font-bold text-primary-600">{team.name.slice(0, 1).toUpperCase()}</span>
                      <div className="min-w-0 flex-1">
                        <div className="truncate font-semibold text-slate-900 group-hover:text-primary-600">{team.name}</div>
                        {team.my_role && <div className="text-xs text-slate-400">{t(TEAM_ROLE_KEYS[team.my_role])}</div>}
                      </div>
                    </div>
                    {team.description && <p className="mt-3 line-clamp-2 text-sm text-slate-500">{team.description}</p>}
                    <div className="mt-3 flex gap-4 text-xs text-slate-500">
                      <span><i className="fa-solid fa-user-group mr-1" aria-hidden="true" />{t('teams.stats.members', { n: team.member_count })}</span>
                      <span><i className="fa-solid fa-book mr-1" aria-hidden="true" />{t('teams.stats.books', { n: team.book_count })}</span>
                    </div>
                  </Link>
                ))}
              </div>
              <Pagination page={data.teams_page} pageSize={data.teams_page_size} total={data.teams_total} onChange={setTeamPage} />
            </>
          )}
        </div>
      )}
      <Modal open={creating} onClose={() => setCreating(false)} title={t('teams.create.title')}
        footer={<>
          <Button variant="ghost" onClick={() => setCreating(false)}>{t('common.actions.cancel')}</Button>
          <Button loading={saving} disabled={!name.trim()} onClick={() => void create()} data-testid="team-create-submit">{t('teams.create.submit')}</Button>
        </>}>
        <div className="space-y-4">
          <Field label={t('teams.field.name')}>
            <Input value={name} maxLength={60} onChange={(e) => setName(e.target.value)} placeholder={t('teams.field.namePlaceholder')} />
          </Field>
          <Field label={t('teams.field.description')}>
            <Textarea value={desc} maxLength={500} rows={3} onChange={(e) => setDesc(e.target.value)} />
          </Field>
        </div>
      </Modal>
    </>
  )
}
