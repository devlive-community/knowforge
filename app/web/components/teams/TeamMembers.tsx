import { useState, type FormEvent } from 'react'
import UserAvatar from '@/components/UserAvatar'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { displayName } from '@/lib/users'
import { TEAM_ROLE_KEYS, type TeamDetail, type TeamMember, type TeamRole } from '@/lib/teams'
import { Badge, Button, Input, Select, useFeedback } from '@/components/ui'

// TeamMembers 团队成员：所有者与管理员邀请成员（只有所有者能邀请管理员、调整角色），移除成员或撤回邀请。
export default function TeamMembers({ data, reload }: { data: TeamDetail; reload: () => Promise<void> }) {
  const { t } = useTranslation()
  const { user } = useApp()
  const { showToast, confirmAction } = useFeedback()
  const [username, setUsername] = useState('')
  const [role, setRole] = useState<TeamRole>('member')
  const [inviting, setInviting] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const teamId = data.team.id
  const myRole = data.team.my_role
  const isOwner = myRole === 'owner'
  const { used, limit } = data.seats
  const full = limit >= 0 && used >= limit

  async function invite(e: FormEvent) {
    e.preventDefault()
    if (!username.trim()) return
    setInviting(true)
    try {
      await api(`/teams/${teamId}/members`, { method: 'POST', body: { username: username.trim(), role } })
      showToast({ message: t('teams.members.invited', { name: username.trim() }), tone: 'success' })
      setUsername('')
      await reload()
    } catch (err) {
      showToast({ message: (err as Error).message, tone: 'error' })
    } finally {
      setInviting(false)
    }
  }

  async function changeRole(m: TeamMember, next: string) {
    setBusy(`role:${m.user.id}`)
    try {
      await api(`/teams/${teamId}/members/${m.user.id}`, { method: 'PUT', body: { role: next } })
      await reload()
    } catch (err) {
      showToast({ message: (err as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  async function remove(m: TeamMember) {
    const name = displayName(m.user)
    const pending = m.status === 'pending'
    if (!await confirmAction({
      title: pending ? t('teams.members.revokeTitle') : t('teams.members.removeTitle'),
      message: pending ? t('teams.members.revokeConfirm', { name }) : t('teams.members.removeConfirm', { name }),
      confirmLabel: pending ? t('teams.members.revoke') : t('teams.members.remove'),
      danger: true,
    })) return
    setBusy(`remove:${m.user.id}`)
    try {
      await api(`/teams/${teamId}/members/${m.user.id}`, { method: 'DELETE' })
      await reload()
    } catch (err) {
      showToast({ message: (err as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  const canRemove = (m: TeamMember) => m.role !== 'owner' && m.user.id !== user?.id && (isOwner || (myRole === 'admin' && m.role === 'member'))

  return (
    <div className="space-y-5" data-testid="team-members">
      {data.can_manage && (
        <form onSubmit={invite} className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
          <div className="flex flex-col gap-2 sm:flex-row">
            <Input className="flex-1" value={username} onChange={(e) => setUsername(e.target.value)} placeholder={t('teams.members.usernamePlaceholder')} disabled={full} />
            <Select className="sm:w-32" value={role} onChange={(v) => setRole(v as TeamRole)} disabled={full}
              options={[{ value: 'member', label: t('teams.role.member') }, ...(isOwner ? [{ value: 'admin', label: t('teams.role.admin') }] : [])]} />
            <Button type="submit" loading={inviting} disabled={full || !username.trim()} data-testid="team-invite">{t('teams.members.invite')}</Button>
          </div>
          <p className="mt-2 text-xs text-slate-400">
            {limit < 0 ? t('teams.members.seatsUnlimited', { used }) : t('teams.members.seats', { used, limit })}
            {full && <span className="ml-1 text-amber-600">{t('teams.members.full')}</span>}
          </p>
        </form>
      )}
      <ul className="divide-y divide-slate-100 rounded-xl border border-slate-200 bg-white shadow-sm">
        {data.members.map((m) => (
          <li key={m.id} className="flex flex-wrap items-center gap-3 px-4 py-3" data-testid="team-member">
            <UserAvatar user={{ username: m.user.username || '?', avatar: m.user.avatar || '' }} size="h-9 w-9" />
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium text-slate-900">{displayName(m.user)}</div>
              <div className="text-xs text-slate-400">@{m.user.username}</div>
            </div>
            {m.status === 'pending' && <Badge tone="amber">{t('teams.members.pending')}</Badge>}
            {isOwner && m.role !== 'owner' && m.status === 'accepted' ? (
              <Select size="sm" className="w-28" value={m.role} disabled={busy !== null} onChange={(v) => void changeRole(m, v)}
                options={[{ value: 'admin', label: t('teams.role.admin') }, { value: 'member', label: t('teams.role.member') }]} />
            ) : (
              <Badge tone={m.role === 'owner' ? 'primary' : m.role === 'admin' ? 'violet' : 'slate'}>{t(TEAM_ROLE_KEYS[m.role])}</Badge>
            )}
            {canRemove(m) && (
              <Button size="sm" variant="ghost" loading={busy === `remove:${m.user.id}`} disabled={busy !== null} onClick={() => void remove(m)}>
                {m.status === 'pending' ? t('teams.members.revoke') : t('teams.members.remove')}
              </Button>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}
