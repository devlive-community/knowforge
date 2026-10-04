import { useState } from 'react'
import BookCard from '@/components/BookCard'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { TEAM_BOOK_ROLE_KEYS, type TeamBookRole, type TeamDetail } from '@/lib/teams'
import type { Book, PageResult } from '@/lib/types'
import { Badge, Button, EmptyState, Field, Modal, Select, useFeedback } from '@/components/ui'

const BOOK_ROLES: TeamBookRole[] = ['editor', 'suggester', 'viewer']

// TeamBooks 团队书籍：成员把自己的书加入团队；团队所有者、管理员与书籍作者可以调整成员权限或把书移出团队。
export default function TeamBooks({ data, reload }: { data: TeamDetail; reload: () => Promise<void> }) {
  const { t } = useTranslation()
  const { user } = useApp()
  const { showToast, confirmAction } = useFeedback()
  const [adding, setAdding] = useState(false)
  const [myBooks, setMyBooks] = useState<Book[] | null>(null)
  const [bookId, setBookId] = useState('')
  const [role, setRole] = useState<TeamBookRole>('editor')
  const [saving, setSaving] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const teamId = data.team.id
  const roleOptions = BOOK_ROLES.map((r) => ({ value: r, label: t(TEAM_BOOK_ROLE_KEYS[r]) }))

  async function openAdd() {
    setAdding(true)
    if (myBooks) return
    try {
      const r = await api<PageResult<Book>>('/books', { params: { scope: 'owned', page_size: 100 } })
      const inTeam = new Set(data.books.map((b) => b.id))
      setMyBooks((r.items || []).filter((b) => !inTeam.has(b.id)))
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
  }

  async function add() {
    setSaving(true)
    try {
      await api(`/teams/${teamId}/books`, { method: 'POST', body: { book_id: Number(bookId), member_role: role } })
      showToast({ message: t('teams.books.added'), tone: 'success' })
      setAdding(false)
      setBookId('')
      setMyBooks(null)
      await reload()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  async function changeRole(id: number, next: string) {
    setBusy(`role:${id}`)
    try {
      await api(`/teams/${teamId}/books/${id}`, { method: 'PUT', body: { member_role: next } })
      await reload()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  async function remove(id: number, title: string) {
    if (!await confirmAction({ title: t('teams.books.removeTitle'), message: t('teams.books.removeConfirm', { title }), confirmLabel: t('teams.books.remove'), danger: true })) return
    setBusy(`remove:${id}`)
    try {
      await api(`/teams/${teamId}/books/${id}`, { method: 'DELETE' })
      showToast({ message: t('teams.books.removed'), tone: 'success' })
      setMyBooks(null)
      await reload()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  return (
    <div data-testid="team-books">
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <p className="min-w-0 flex-1 text-sm text-slate-500">{t('teams.books.hint')}</p>
        <Button size="sm" onClick={() => void openAdd()} data-testid="team-add-book">
          <i className="fa-solid fa-plus" aria-hidden="true" /> {t('teams.books.add')}
        </Button>
      </div>
      {data.books.length === 0 ? <EmptyState>{t('teams.books.empty')}</EmptyState> : (
        <div className="space-y-3">
          {data.books.map((b) => {
            const canChange = data.can_manage || b.user_id === user?.id
            return (
              <BookCard key={b.id} book={b} view="list" showStatus showVisibility
                badge={<Badge tone="sky">{t('teams.books.memberRole', { role: t(TEAM_BOOK_ROLE_KEYS[b.member_role]) })}</Badge>}
                actions={canChange ? (
                  <div className="flex flex-wrap items-center gap-2 text-xs text-slate-500">
                    <span>{t('teams.books.memberRoleLabel')}</span>
                    <Select size="sm" className="w-32" value={b.member_role} disabled={busy !== null} options={roleOptions} onChange={(v) => void changeRole(b.id, v)} />
                    {busy === `role:${b.id}` && <i className="fa-solid fa-spinner fa-spin" aria-hidden="true" />}
                    <Button size="sm" variant="ghost" className="ml-auto" loading={busy === `remove:${b.id}`} disabled={busy !== null} onClick={() => void remove(b.id, b.title)}>
                      {t('teams.books.remove')}
                    </Button>
                  </div>
                ) : undefined} />
            )
          })}
        </div>
      )}
      <Modal open={adding} onClose={() => setAdding(false)} title={t('teams.books.addTitle')}
        footer={<>
          <Button variant="ghost" onClick={() => setAdding(false)}>{t('common.actions.cancel')}</Button>
          <Button loading={saving} disabled={!bookId} onClick={() => void add()} data-testid="team-add-book-submit">{t('teams.books.addSubmit')}</Button>
        </>}>
        <div className="space-y-4">
          <p className="text-sm text-slate-500">{t('teams.books.addHint')}</p>
          <Field label={t('teams.books.book')}>
            <Select searchable value={bookId} onChange={setBookId} placeholder={myBooks === null ? t('teams.books.loading') : myBooks.length ? t('teams.books.pick') : t('teams.books.noBooks')}
              disabled={!myBooks?.length} options={(myBooks || []).map((b) => ({ value: String(b.id), label: b.title }))} />
          </Field>
          <Field label={t('teams.books.memberRoleLabel')} hint={t('teams.books.memberRoleHint')}>
            <Select value={role} onChange={(v) => setRole(v as TeamBookRole)} options={roleOptions} />
          </Field>
        </div>
      </Modal>
    </div>
  )
}
