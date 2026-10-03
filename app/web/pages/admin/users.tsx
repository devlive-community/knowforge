import { useCallback, useEffect, useState } from 'react'
import { api, API_BASE, formatDate } from '@/lib/api'
import { useApp } from '@/lib/auth'
import AdminLayout from '@/components/AdminLayout'
import { Badge, Button, Input, Select, Pagination, Loading, useFeedback } from '@/components/ui'
import { SearchIcon } from '@/components/icons'
import { useTranslation } from '@/lib/i18n'
import type { PageResult, User } from '@/lib/types'
import { useUrlPage } from '@/lib/use-url-page'

const PAGE_SIZE = 15

// 用户管理：分页检索用户并管理角色、启停与删除（仅管理员）
export default function AdminUsers() {
  const { confirmAction } = useFeedback()
  const { user } = useApp()
  const { t } = useTranslation()
  const isAdmin = user?.role === 'admin'
  const [items, setItems] = useState<User[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useUrlPage()
  const [q, setQ] = useState('')
  const [role, setRole] = useState('')
  const [status, setStatus] = useState('')
  const [sort, setSort] = useState('created_at_desc')
  const [loading, setLoading] = useState(true)
  const [message, setMessage] = useState('')
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const res = await api<PageResult<User>>('/admin/users', {
        params: { page, page_size: PAGE_SIZE, q, role, status, sort },
      })
      setItems(res.items)
      setTotal(res.total)
    } catch (e) {
      setMessage((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [page, q, role, status, sort])

  useEffect(() => {
    if (!isAdmin) return
    load()
  }, [isAdmin, load])

  function patch(updated: User) {
    setItems((list) => list.map((u) => (u.id === updated.id ? updated : u)))
  }

  async function changeRole(u: User, nextRole: string) {
    if (nextRole === u.role) return
    setBusyId(u.id)
    setMessage('')
    try {
      patch(await api<User>(`/admin/users/${u.id}/role`, { method: 'PUT', body: { role: nextRole } }))
    } catch (e) {
      setMessage((e as Error).message)
    } finally {
      setBusyId(null)
    }
  }

  async function toggleStatus(u: User) {
    setBusyId(u.id)
    setMessage('')
    try {
      patch(await api<User>(`/admin/users/${u.id}/status`, { method: 'PUT', body: { is_active: !u.is_active } }))
    } catch (e) {
      setMessage((e as Error).message)
    } finally {
      setBusyId(null)
    }
  }

  async function remove(u: User) {
    if (!await confirmAction({
      title: t('admin.users.delete.title'),
      message: t('admin.users.delete.message', { username: u.username }),
      confirmLabel: t('admin.users.delete.confirm'),
      danger: true,
    })) return
    setBusyId(u.id)
    setMessage('')
    try {
      await api(`/admin/users/${u.id}`, { method: 'DELETE' })
      setMessage(t('admin.users.deleted', { username: u.username }))
      load()
    } catch (e) {
      setMessage((e as Error).message)
    } finally {
      setBusyId(null)
    }
  }

  return (
    <AdminLayout current="users" breadcrumb={t('admin.nav.users')}>
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.users')}</h1>
        <p className="mt-1.5 text-sm text-slate-500">{t('admin.users.description')}</p>
      </div>

      {/* 筛选栏 */}
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <form onSubmit={(e) => { e.preventDefault(); setPage(1); load() }} className="w-full max-w-xs">
          <Input value={q} onChange={(e) => setQ(e.target.value)} leading={<SearchIcon className="h-4 w-4" />}
            placeholder={t('admin.users.searchPlaceholder')} />
        </form>
        <Select className="w-32" value={role} placeholder={t('admin.users.role.all')}
          options={[{ value: '', label: t('admin.users.role.all') }, { value: 'admin', label: t('admin.users.role.admin') }, { value: 'user', label: t('admin.users.role.user') }]}
          onChange={(v) => { setRole(v); setPage(1) }} />
        <Select className="w-32" value={status} placeholder={t('admin.users.status.all')}
          options={[{ value: '', label: t('admin.users.status.all') }, { value: 'active', label: t('admin.users.status.active') }, { value: 'inactive', label: t('admin.users.status.inactive') }]}
          onChange={(v) => { setStatus(v); setPage(1) }} />
        <Select className="w-44" value={sort}
          options={[
            { value: 'created_at_desc', label: t('admin.users.sort.newest') },
            { value: 'created_at_asc', label: t('admin.users.sort.oldest') },
            { value: 'last_login_at_desc', label: t('admin.users.sort.recentlyLoggedIn') },
            { value: 'last_login_at_asc', label: t('admin.users.sort.leastRecentlyLoggedIn') },
          ]}
          onChange={(v) => { setSort(v); setPage(1) }} />
        <span className="ml-auto text-sm text-slate-400">{t('admin.users.total', { total })}</span>
      </div>

      {message && <div className="mb-4 rounded-lg bg-slate-100 px-4 py-3 text-sm text-slate-600">{message}</div>}

      {loading ? (
        <Loading className="py-24" label={t('admin.users.loading')} />
      ) : (
      <>
      <div className="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] text-sm">
            <thead>
              <tr className="border-b border-slate-100 text-left text-xs font-medium text-slate-400">
                <th className="px-5 py-3">{t('admin.users.column.user')}</th>
                <th className="px-5 py-3">{t('admin.users.column.role')}</th>
                <th className="px-5 py-3">{t('admin.users.column.status')}</th>
                <th className="px-5 py-3">{t('admin.users.column.registeredAt')}</th>
                <th className="px-5 py-3">{t('admin.users.column.lastLogin')}</th>
                <th className="px-5 py-3 text-right">{t('admin.users.column.actions')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {items.length === 0 ? (
                <tr><td colSpan={6} className="px-5 py-10 text-center text-slate-400">{t('admin.users.empty')}</td></tr>
              ) : items.map((u) => {
                const self = u.id === user?.id
                return (
                  <tr key={u.id} className="hover:bg-slate-50/60">
                    <td className="px-5 py-3">
                      <div className="flex items-center gap-3">
                        {u.avatar
                          ? <img src={u.avatar.startsWith('/') ? API_BASE + u.avatar : u.avatar} alt="" className="h-9 w-9 rounded-full object-cover" />
                          : <span className="flex h-9 w-9 items-center justify-center rounded-full bg-primary-500 text-sm font-bold text-white">{u.username[0]?.toUpperCase()}</span>}
                        <div className="min-w-0">
                          <p className="flex items-center gap-1.5 font-medium text-slate-800">
                            {u.username}
                            {self && <span className="text-xs font-normal text-slate-400">{t('admin.users.self')}</span>}
                          </p>
                          <p className="truncate text-xs text-slate-400">{u.email}</p>
                        </div>
                      </div>
                    </td>
                    <td className="px-5 py-3">
                      {u.role === 'admin' ? <Badge tone="primary">{t('admin.users.role.admin')}</Badge> : <Badge tone="slate">{t('admin.users.role.user')}</Badge>}
                    </td>
                    <td className="px-5 py-3">
                      {u.is_active ? <Badge tone="emerald">{t('admin.users.status.active')}</Badge> : <Badge tone="rose">{t('admin.users.status.inactive')}</Badge>}
                    </td>
                    <td className="px-5 py-3 text-slate-500">{formatDate(u.created_at)}</td>
                    <td className="px-5 py-3 text-slate-500">{u.last_login_at ? formatDate(u.last_login_at) : '—'}</td>
                    <td className="px-5 py-3">
                      {self ? (
                        <span className="block text-right text-xs text-slate-300">{t('admin.users.cannotOperateSelf')}</span>
                      ) : (
                        <div className="flex items-center justify-end gap-2">
                          {busyId === u.id && <span className="h-4 w-4 animate-spin rounded-full border-2 border-slate-200 border-t-primary-500" aria-label={t('common.status.processing')} />}
                          <Select className="w-32" value={u.role} disabled={busyId === u.id}
                            options={[{ value: 'user', label: t('admin.users.role.user') }, { value: 'admin', label: t('admin.users.role.admin') }]}
                            onChange={(v) => changeRole(u, v)} />
                          <Button size="sm" variant="outline" disabled={busyId === u.id} onClick={() => toggleStatus(u)}>
                            {u.is_active ? t('admin.users.deactivate') : t('admin.users.activate')}
                          </Button>
                          <Button size="sm" variant="danger" disabled={busyId === u.id} onClick={() => remove(u)}>{t('admin.users.delete.confirm')}</Button>
                        </div>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>

      <Pagination page={page} pageSize={PAGE_SIZE} total={total} onChange={setPage} loading={loading} />
      </>
      )}
    </AdminLayout>
  )
}
