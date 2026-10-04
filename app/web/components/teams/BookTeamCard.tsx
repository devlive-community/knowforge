import { useCallback, useEffect, useState } from 'react'
import Link from 'next/link'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { TEAM_BOOK_ROLE_KEYS, teamsEnabled, type Team, type TeamBookRole } from '@/lib/teams'
import type { Book } from '@/lib/types'
import { Button, Select, useFeedback } from '@/components/ui'

interface BookTeam {
  team: { id: number; name: string; slug: string } | null
  member_role?: TeamBookRole
  can_change?: boolean
}

const BOOK_ROLES: TeamBookRole[] = ['editor', 'suggester', 'viewer']

// BookTeamCard 书籍设置 · 协作者页的「团队」：书籍所属团队与成员权限；作者可以把书加入自己所在的团队或移出。
export default function BookTeamCard({ book }: { book: Book }) {
  const { t } = useTranslation()
  const { site, user } = useApp()
  const { showToast, confirmAction } = useFeedback()
  const [data, setData] = useState<BookTeam | null>(null)
  const [teams, setTeams] = useState<Team[]>([])
  const [teamId, setTeamId] = useState('')
  const [role, setRole] = useState<TeamBookRole>('editor')
  const [busy, setBusy] = useState<'add' | 'role' | 'remove' | null>(null)
  const enabled = teamsEnabled(site)
  const isAuthor = user?.id === book.user_id

  const load = useCallback(async () => {
    try {
      const d = await api<BookTeam>(`/team-books/${book.id}`)
      setData(d)
      if (!d.team && isAuthor) setTeams((await api<{ teams: Team[] }>('/teams')).teams)
    } catch { /* 无权查看时不显示 */ }
  }, [book.id, isAuthor])
  useEffect(() => { if (enabled) void load() }, [enabled, load])

  if (!enabled || !data || (!data.team && !isAuthor)) return null
  const roleOptions = BOOK_ROLES.map((r) => ({ value: r, label: t(TEAM_BOOK_ROLE_KEYS[r]) }))

  async function run(kind: NonNullable<typeof busy>, fn: () => Promise<unknown>) {
    setBusy(kind)
    try {
      await fn()
      await load()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  const team = data.team
  return (
    <div className="mb-5 rounded-xl border border-slate-200 bg-white p-6 shadow-sm" data-testid="book-team">
      <h2 className="font-semibold text-slate-900">{t('teams.bookCard.title')}</h2>
      {team ? (
        <>
          <p className="mt-1 text-sm text-slate-500">
            {t('teams.bookCard.inTeam')}{' '}
            <Link href={`/teams/${encodeURIComponent(team.slug)}`} className="font-medium text-primary-600 hover:underline">{team.name}</Link>
            {t('teams.bookCard.inTeamSuffix', { role: t(TEAM_BOOK_ROLE_KEYS[data.member_role || 'editor']) })}
          </p>
          {data.can_change && (
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <Select size="sm" className="w-32" value={data.member_role || 'editor'} options={roleOptions} disabled={busy !== null}
                onChange={(v) => void run('role', () => api(`/teams/${team.id}/books/${book.id}`, { method: 'PUT', body: { member_role: v } }))} />
              {busy === 'role' && <i className="fa-solid fa-spinner fa-spin text-xs text-slate-400" aria-hidden="true" />}
              <Button size="sm" variant="ghost" loading={busy === 'remove'} disabled={busy !== null} onClick={async () => {
                if (!await confirmAction({ title: t('teams.books.removeTitle'), message: t('teams.books.removeConfirm', { title: book.title }), confirmLabel: t('teams.books.remove'), danger: true })) return
                await run('remove', () => api(`/teams/${team.id}/books/${book.id}`, { method: 'DELETE' }))
              }}>{t('teams.books.remove')}</Button>
            </div>
          )}
        </>
      ) : (
        <>
          <p className="mt-1 text-sm text-slate-500">{teams.length ? t('teams.bookCard.addHint') : t('teams.bookCard.noTeams')}</p>
          {teams.length > 0 ? (
            <div className="mt-3 flex flex-col gap-2 sm:flex-row">
              <Select className="flex-1" value={teamId} onChange={setTeamId} placeholder={t('teams.bookCard.pickTeam')}
                options={teams.map((tm) => ({ value: String(tm.id), label: tm.name }))} />
              <Select className="sm:w-32" value={role} onChange={(v) => setRole(v as TeamBookRole)} options={roleOptions} />
              <Button loading={busy === 'add'} disabled={busy !== null || !teamId}
                onClick={() => void run('add', () => api(`/teams/${teamId}/books`, { method: 'POST', body: { book_id: book.id, member_role: role } }))}>
                {t('teams.bookCard.add')}
              </Button>
            </div>
          ) : (
            <Link href="/teams" className="mt-2 inline-block text-sm font-medium text-primary-600 hover:underline">{t('teams.bookCard.goTeams')}</Link>
          )}
        </>
      )}
    </div>
  )
}
