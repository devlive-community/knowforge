import { useCallback, useEffect, useState } from 'react'
import AdminLayout from '@/components/AdminLayout'
import { ClockIcon } from '@/components/icons'
import { Badge, Button, EmptyState, Loading, Pagination, Select, useFeedback } from '@/components/ui'
import { api, formatDate } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import type { PageResult } from '@/lib/types'
import { useUrlPage } from '@/lib/use-url-page'

const PAGE_SIZE = 20

type TaskStatus = 'pending' | 'running' | 'retrying' | 'succeeded' | 'failed'

interface BackgroundTask {
  id: number
  type: string
  status: TaskStatus
  attempts: number
  max_attempts: number
  available_at: string
  started_at: string | null
  finished_at: string | null
  last_error: string
  created_at: string
  updated_at: string
}

export default function AdminTasks() {
  const { user } = useApp()
  const { showToast } = useFeedback()
  const { t } = useTranslation()
  const isAdmin = user?.role === 'admin'
  const [items, setItems] = useState<BackgroundTask[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useUrlPage()
  const [status, setStatus] = useState('')
  const [jobType, setJobType] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retryingID, setRetryingID] = useState<number | null>(null)

  const statusOptions = [
    { value: '', label: t('admin.tasks.status.all') },
    { value: 'pending', label: t('admin.tasks.status.pending') },
    { value: 'running', label: t('admin.tasks.status.running') },
    { value: 'retrying', label: t('admin.tasks.status.retrying') },
    { value: 'succeeded', label: t('admin.tasks.status.succeeded') },
    { value: 'failed', label: t('admin.tasks.status.failed') },
  ]

  const statusMeta: Record<TaskStatus, { label: string; tone: 'slate' | 'primary' | 'amber' | 'emerald' | 'rose' }> = {
    pending: { label: t('admin.tasks.status.pending'), tone: 'slate' },
    running: { label: t('admin.tasks.status.running'), tone: 'primary' },
    retrying: { label: t('admin.tasks.status.retrying'), tone: 'amber' },
    succeeded: { label: t('admin.tasks.status.succeeded'), tone: 'emerald' },
    failed: { label: t('admin.tasks.status.failed'), tone: 'rose' },
  }

  // 任务类型名称：admin.tasks.types.<类型>（核心与插件登记的类型），缺失时回退原始类型
  const typeText = (type: string) => { const k = `admin.tasks.types.${type}`; const v = t(k); return v === k ? type : v }
  // 类型筛选项：服务端已注册的任务类型
  const [types, setTypes] = useState<string[]>([])
  useEffect(() => {
    if (!isAdmin) return
    api<{ items: string[] }>('/admin/tasks/types').then((r) => setTypes(r.items || [])).catch(() => { /* 不影响列表 */ })
  }, [isAdmin])
  const typeOptions = [{ value: '', label: t('admin.tasks.typeAll') }, ...types.map((ty) => ({ value: ty, label: typeText(ty) }))]

  const load = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true)
    setError('')
    try {
      const result = await api<PageResult<BackgroundTask>>('/admin/tasks', {
        params: { page, page_size: PAGE_SIZE, status, type: jobType },
      })
      setItems(result.items || [])
      setTotal(result.total)
    } catch (requestError) {
      setError((requestError as Error).message)
    } finally {
      if (!quiet) setLoading(false)
    }
  }, [jobType, page, status])

  useEffect(() => {
    if (isAdmin) void load()
  }, [isAdmin, load])

  useEffect(() => {
    if (!items.some((item) => ['pending', 'running', 'retrying'].includes(item.status))) return
    const timer = window.setInterval(() => void load(true), 3000)
    return () => window.clearInterval(timer)
  }, [items, load])

  async function retry(task: BackgroundTask) {
    setRetryingID(task.id)
    try {
      await api(`/admin/tasks/${task.id}/retry`, { method: 'POST' })
      showToast(t('admin.tasks.requeued'))
      await load(true)
    } catch (requestError) {
      showToast({ title: t('admin.tasks.requeueFailed'), message: (requestError as Error).message, tone: 'error' })
    } finally {
      setRetryingID(null)
    }
  }

  return (
    <AdminLayout current="tasks" breadcrumb={t('admin.nav.tasks')}>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-bold text-slate-900">
            <ClockIcon className="h-6 w-6 text-primary-600" />{t('admin.nav.tasks')}
          </h1>
          <p className="mt-1.5 text-sm text-slate-500">{t('admin.tasks.description')}</p>
        </div>
        <span className="rounded-xl border border-slate-200 bg-white px-3 py-2 text-sm text-slate-500">{t('admin.tasks.total', { total })}</span>
      </div>

      <div className="mb-5 rounded-2xl border border-slate-200 bg-white p-4 shadow-sm">
        <div className="flex flex-wrap items-center gap-3">
          <Select className="w-full sm:w-56" value={jobType} options={typeOptions} searchable
            onChange={(value) => { setJobType(value); setPage(1) }} />
          <Select className="w-40" value={status} options={statusOptions}
            onChange={(value) => { setStatus(value); setPage(1) }} />
          {(jobType || status) && <Button variant="ghost" onClick={() => { setJobType(''); setStatus(''); setPage(1) }}>{t('common.actions.clearFilter')}</Button>}
        </div>
      </div>

      {error && <div className="mb-4 max-h-32 overflow-auto break-words rounded-xl border border-rose-100 bg-rose-50 px-4 py-3 text-sm text-rose-700">{error}</div>}

      {loading ? (
        <Loading className="py-24" label={t('admin.tasks.loading')} />
      ) : items.length === 0 ? (
        <EmptyState>{t('admin.tasks.empty')}</EmptyState>
      ) : (
        <>
          <div className="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
            <div className="overflow-x-auto">
              <table className="w-full min-w-[920px] text-sm">
                <thead>
                  <tr className="border-b border-slate-100 bg-slate-50/70 text-left text-xs font-medium text-slate-400">
                    <th className="px-5 py-3">{t('admin.tasks.column.task')}</th>
                    <th className="px-5 py-3">{t('admin.tasks.column.status')}</th>
                    <th className="px-5 py-3">{t('admin.tasks.column.attempts')}</th>
                    <th className="px-5 py-3">{t('admin.tasks.column.createdAt')}</th>
                    <th className="px-5 py-3">{t('admin.tasks.column.lastResult')}</th>
                    <th className="px-5 py-3 text-right">{t('admin.tasks.column.actions')}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100">
                  {items.map((task) => {
                    const meta = statusMeta[task.status] || statusMeta.pending
                    return (
                      <tr key={task.id} className="align-top hover:bg-slate-50/60">
                        <td className="px-5 py-4">
                          <p className="font-medium text-slate-800">{typeText(task.type)}</p>
                          <p className="mt-0.5 text-xs text-slate-400">#{task.id} · {task.type}</p>
                        </td>
                        <td className="px-5 py-4"><Badge tone={meta.tone}>{meta.label}</Badge></td>
                        <td className="px-5 py-4 text-slate-600">{task.attempts} / {task.max_attempts}</td>
                        <td className="whitespace-nowrap px-5 py-4 text-slate-500">{formatDate(task.created_at)}</td>
                        <td className="max-w-md px-5 py-4">
                          {task.last_error ? (
                            <p className="max-h-24 overflow-auto break-words rounded-lg bg-rose-50 px-3 py-2 text-xs leading-5 text-rose-700">{task.last_error}</p>
                          ) : (
                            <span className="text-slate-400">{task.finished_at ? formatDate(task.finished_at) : t('admin.tasks.status.pending')}</span>
                          )}
                        </td>
                        <td className="px-5 py-4 text-right">
                          {task.status === 'failed' && <Button size="sm" variant="outline" loading={retryingID === task.id} onClick={() => retry(task)}>{t('admin.tasks.retry')}</Button>}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </div>
          <Pagination page={page} total={total} pageSize={PAGE_SIZE} onChange={setPage} loading={loading} />
        </>
      )}
    </AdminLayout>
  )
}
