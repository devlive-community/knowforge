import { useCallback, useEffect, useState, FormEvent } from 'react'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { Button, Input, Select, Badge, EmptyState, Loading, useFeedback } from '@/components/ui'
import UserAvatar from '@/components/UserAvatar'
import { useTranslation } from '@/lib/i18n'
import type { Book, User } from '@/lib/types'

interface Collaborator {
  id: number
  user_id: number
  role: 'editor' | 'viewer' | 'suggester'
  status: 'pending' | 'accepted' | 'rejected'
  team_id?: number // 非 0：权限来自书籍所属团队，在团队中调整
  created_at: string
  user?: Pick<User, 'id' | 'username' | 'avatar'>
}

// CollaboratorManager 书籍设置页的协作者管理：所有者可增删，协作者可查看与自己退出
export default function CollaboratorManager({ book }: { book: Book }) {
  const { t } = useTranslation()
  const ROLE_LABELS: Record<string, string> = { editor: t('collab.roleEditor'), suggester: t('collab.roleSuggester'), viewer: t('collab.roleViewer') }
  const STATUS_LABELS: Record<string, string> = { pending: t('collab.statusPending'), accepted: t('collab.statusAccepted'), rejected: t('collab.statusRejected') }
  const { confirmAction } = useFeedback()
  const { user } = useApp()
  const [collaborators, setCollaborators] = useState<Collaborator[]>([])
  const [loaded, setLoaded] = useState(false)
  const [username, setUsername] = useState('')
  const [role, setRole] = useState('editor')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [adding, setAdding] = useState(false)
  const [removingId, setRemovingId] = useState<number | null>(null)

  const isOwner = user?.id === book.user_id || user?.role === 'admin'

  const load = useCallback(async () => {
    try {
      const data = await api<{ collaborators: Collaborator[] }>(`/books/${book.id}/collaborators`)
      setCollaborators(data.collaborators || [])
    } catch { /* 无权查看时静默 */ }
    setLoaded(true)
  }, [book.id])

  useEffect(() => { load() }, [load])

  async function add(e: FormEvent) {
    e.preventDefault()
    if (!username.trim()) return
    setAdding(true)
    setMessage('')
    setError('')
    try {
      await api(`/books/${book.id}/collaborators`, { method: 'POST', body: { username: username.trim(), role } })
      setMessage(t('collab.inviteSent', { name: username.trim(), role: ROLE_LABELS[role] }))
      setUsername('')
      await load()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setAdding(false)
    }
  }

  async function remove(userId: number, name: string) {
    const self = user?.id === userId
    if (!await confirmAction({
      title: self ? t('collab.leaveTitle') : t('collab.removeTitle'),
      message: self ? t('collab.leaveConfirm', { title: book.title }) : t('collab.removeConfirm', { name }),
      confirmLabel: self ? t('collab.leaveBtn') : t('collab.removeBtn'),
      danger: true,
    })) return
    setRemovingId(userId)
    setMessage('')
    setError('')
    try {
      await api(`/books/${book.id}/collaborators/${userId}`, { method: 'DELETE' })
      setMessage(self ? t('collab.left') : t('collab.removed', { name }))
      await load()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setRemovingId(null)
    }
  }

  return (
    <div className="rounded-xl border border-slate-200 bg-white shadow-sm p-6">
      <h2 className="font-semibold text-slate-900">{t('collab.heading')}</h2>
      <p className="mt-1 text-sm text-slate-500">
        {isOwner
          ? t('collab.descOwner')
          : t('collab.descOther')}
      </p>
      {message && <div className="mt-3 rounded-lg bg-emerald-50 px-4 py-3 text-sm text-emerald-600">{message}</div>}
      {error && <div className="mt-3 rounded-lg bg-rose-50 px-4 py-3 text-sm text-rose-600">{error}</div>}

      {!loaded ? (
        <Loading className="py-8" label={t('collab.loading')} />
      ) : collaborators.length === 0 ? (
        <div className="mt-4">
          <EmptyState>{t('collab.empty')}</EmptyState>
        </div>
      ) : (
        <ul className="mt-4 divide-y divide-slate-100">
          {collaborators.map((c) => (
            <li key={c.id} className="flex items-center justify-between py-3">
              <div className="flex min-w-0 items-center gap-3">
                <UserAvatar user={{ username: c.user?.username || '?', avatar: c.user?.avatar || '' }} size="h-9 w-9" />
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium text-slate-900">{c.user?.username}</div>
                  <span className="mt-0.5 inline-block">
                    <Badge tone={c.role === 'editor' ? 'emerald' : c.role === 'suggester' ? 'sky' : 'slate'}>{ROLE_LABELS[c.role]}</Badge>
                    <span className="ml-1.5">
                      <Badge tone={c.status === 'accepted' ? 'emerald' : c.status === 'pending' ? 'amber' : 'slate'}>
                        {STATUS_LABELS[c.status] || c.status}
                      </Badge>
                    </span>
                    {!!c.team_id && <span className="ml-1.5"><Badge tone="violet">{t('collab.fromTeam')}</Badge></span>}
                  </span>
                </div>
              </div>
              {!c.team_id && (
                <Button variant="ghost" size="sm" loading={removingId === c.user_id} disabled={removingId !== null}
                  onClick={() => remove(c.user_id, c.user?.username || '')}>
                  {user?.id === c.user_id ? t('collab.leave') : t('collab.remove')}
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}

      {isOwner && (
        <form onSubmit={add} className="mt-4 flex flex-col gap-2 border-t border-slate-100 pt-4 sm:flex-row">
          <Input className="flex-1" value={username} onChange={(e) => setUsername(e.target.value)}
            placeholder={t('collab.usernamePlaceholder')} />
          <Select
            className="sm:w-36"
            options={[{ value: 'editor', label: t('collab.roleEditor') }, { value: 'suggester', label: t('collab.roleSuggester') }, { value: 'viewer', label: t('collab.roleViewer') }]}
            value={role} onChange={setRole} />
          <Button type="submit" loading={adding}>{t('collab.sendInvite')}</Button>
        </form>
      )}
    </div>
  )
}
