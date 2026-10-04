import { useCallback, useEffect, useState, type FormEvent } from 'react'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import type { Team, TeamUser } from '@/lib/teams'
import type { PageResult } from '@/lib/types'
import { useUrlPage } from '@/lib/use-url-page'
import { displayName } from '@/lib/users'
import { Button, Card, EmptyState, Input, Loading, Pagination, useFeedback } from '@/components/ui'

interface Row { team: Team; owner: TeamUser }

export default function AdminTeams() {
  return <FeatureGate feature="teams"><Inner /></FeatureGate>
}

// 团队空间管理：站内全部团队（名称搜索），可解散团队（收回团队授予的书籍权限，书籍仍归各自作者）；
// 每人可创建的团队数与每个团队的成员数在「权益」中配置。
function Inner() {
  const { t } = useTranslation()
  const { showToast, confirmAction } = useFeedback()
  const [data, setData] = useState<PageResult<Row> | null>(null)
  const [page, setPage] = useUrlPage()
  const [q, setQ] = useState('')
  const [search, setSearch] = useState('')
  const [loading, setLoading] = useState(true)
  const [deleting, setDeleting] = useState<number | null>(null)

  const load = useCallback(() => {
    setLoading(true)
    api<PageResult<Row>>('/admin/teams', { params: { page, page_size: 20, q: search } })
      .then(setData)
      .catch((e) => showToast({ title: t('admin.teams.loadFailed'), message: (e as Error).message, tone: 'error' }))
      .finally(() => setLoading(false))
  }, [page, search, showToast, t])
  useEffect(() => { load() }, [load])

  function submit(e: FormEvent) {
    e.preventDefault()
    setSearch(q.trim())
    setPage(1)
  }

  async function remove(team: Team) {
    if (!await confirmAction({ title: t('admin.teams.deleteTitle'), message: t('admin.teams.deleteConfirm', { name: team.name }), confirmLabel: t('admin.teams.delete'), danger: true })) return
    setDeleting(team.id)
    try {
      await api(`/admin/teams/${team.id}`, { method: 'DELETE' })
      showToast({ message: t('admin.teams.deleted'), tone: 'success' })
      load()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setDeleting(null)
    }
  }

  return (
    <AdminLayout current="teams" breadcrumb={t('admin.nav.teams')}>
      <div>
        <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.teams')}</h1>
        <p className="mt-1.5 text-sm text-slate-500">{t('admin.teams.description')}</p>
      </div>
      <form onSubmit={submit} className="mt-6 flex max-w-md gap-2">
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('admin.teams.search')} />
        <Button type="submit" variant="outline" loading={loading && search !== ''}>{t('admin.teams.searchButton')}</Button>
      </form>
      <div className="mt-4">
        {!data ? <Loading className="py-12" /> : data.items.length === 0 ? <EmptyState>{t('admin.teams.empty')}</EmptyState> : (
          <>
            <Card className="overflow-x-auto">
              <table className="w-full min-w-[640px] text-sm">
                <thead className="border-b border-slate-100 text-left text-xs text-slate-500">
                  <tr>
                    <th className="px-4 py-3 font-medium">{t('admin.teams.colName')}</th>
                    <th className="px-4 py-3 font-medium">{t('admin.teams.colOwner')}</th>
                    <th className="px-4 py-3 font-medium">{t('admin.teams.colMembers')}</th>
                    <th className="px-4 py-3 font-medium">{t('admin.teams.colBooks')}</th>
                    <th className="px-4 py-3 font-medium">{t('admin.teams.colCreated')}</th>
                    <th className="px-4 py-3" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100">
                  {data.items.map(({ team, owner }) => (
                    <tr key={team.id}>
                      <td className="px-4 py-3">
                        <div className="font-medium text-slate-900">{team.name}</div>
                        <div className="text-xs text-slate-400">{team.slug}</div>
                      </td>
                      <td className="px-4 py-3 text-slate-600">{owner?.username ? `${displayName(owner)} (@${owner.username})` : '-'}</td>
                      <td className="px-4 py-3 tabular-nums text-slate-600">{team.member_count}</td>
                      <td className="px-4 py-3 tabular-nums text-slate-600">{team.book_count}</td>
                      <td className="px-4 py-3 text-slate-500">{formatDate(team.created_at).slice(0, 10)}</td>
                      <td className="px-4 py-3 text-right">
                        <Button variant="ghost" size="sm" className="text-rose-600" loading={deleting === team.id} disabled={deleting !== null} onClick={() => void remove(team)}>
                          {t('admin.teams.delete')}
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </Card>
            <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} loading={loading} />
          </>
        )}
      </div>
    </AdminLayout>
  )
}
