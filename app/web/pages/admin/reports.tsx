import { FormEvent, useCallback, useEffect, useState } from 'react'
import AdminLayout from '@/components/AdminLayout'
import { api, formatDate } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import type { PageResult } from '@/lib/types'
import { Badge, Button, Card, EmptyState, Field, Input, Loading, Modal, Pagination, SegmentedTabs, Select, Textarea, useFeedback } from '@/components/ui'
import { useUrlPage } from '@/lib/use-url-page'

const PAGE_SIZE = 20

interface ContentReport {
  id: number
  reporter_id: number
  reporter_username: string
  reporter_email: string
  target_type: string // book | document | comment | 插件登记的用户内容类型
  target_id: number
  target_label: string
  reason: string
  description: string
  status: 'pending' | 'resolved' | 'rejected'
  resolution: '' | 'takedown' | 'reject'
  resolution_note: string
  handler_username: string
  resolved_at: string | null
  created_at: string
}

export default function AdminReports() {
  const { user } = useApp()
  const { showToast } = useFeedback()
  const { t } = useTranslation()
  const isAdmin = user?.role === 'admin'
  const [items, setItems] = useState<ContentReport[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useUrlPage()
  const [status, setStatus] = useState('pending')
  const [targetType, setTargetType] = useState('')
  const [targetTypes, setTargetTypes] = useState<string[]>(['book', 'document', 'comment'])
  const [reason, setReason] = useState('')
  const [queryInput, setQueryInput] = useState('')
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [active, setActive] = useState<ContentReport | null>(null)
  const [resolution, setResolution] = useState<'reject' | 'takedown'>('reject')
  const [note, setNote] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [modalError, setModalError] = useState('')

  const statusTabs = [
    { value: '', label: t('admin.reports.status.all') },
    { value: 'pending', label: t('admin.reports.status.pending') },
    { value: 'resolved', label: t('admin.reports.status.resolved') },
    { value: 'rejected', label: t('admin.reports.status.rejected') },
  ]
  // 可举报类型由服务端下发（含插件登记的用户内容）；文案 admin.reports.target.<类型>，缺失时回退类型名
  const targetLabel = (type: string) => { const k = `admin.reports.target.${type}`; const v = t(k); return v === k ? type : v }
  const targetOptions = [{ value: '', label: t('admin.reports.target.all') }, ...targetTypes.map((type) => ({ value: type, label: targetLabel(type) }))]
  const reasonOptions = [
    { value: '', label: t('admin.reports.reason.all') },
    { value: 'spam', label: t('admin.reports.reason.spam') },
    { value: 'harassment', label: t('admin.reports.reason.harassment') },
    { value: 'copyright', label: t('admin.reports.reason.copyright') },
    { value: 'illegal', label: t('admin.reports.reason.illegal') },
    { value: 'misleading', label: t('admin.reports.reason.misleading') },
    { value: 'other', label: t('admin.reports.reason.other') },
  ]
  const targetLabels = Object.fromEntries(targetOptions.map((item) => [item.value, item.label]))
  const reasonLabels = Object.fromEntries(reasonOptions.map((item) => [item.value, item.label]))

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const result = await api<PageResult<ContentReport> & { target_types?: string[] }>('/admin/reports', {
        params: { page, page_size: PAGE_SIZE, status, target_type: targetType, reason, q: query },
      })
      if (result.target_types?.length) setTargetTypes(result.target_types)
      setItems(result.items)
      setTotal(result.total)
    } catch (requestError) {
      setError((requestError as Error).message)
    } finally {
      setLoading(false)
    }
  }, [page, query, reason, status, targetType])

  useEffect(() => {
    if (isAdmin) load()
  }, [isAdmin, load])

  function applyQuery(event: FormEvent) {
    event.preventDefault()
    setPage(1)
    setQuery(queryInput.trim())
  }

  function openResolution(report: ContentReport, nextResolution: 'reject' | 'takedown') {
    setActive(report)
    setResolution(nextResolution)
    setNote('')
    setModalError('')
  }

  function closeResolution() {
    if (submitting) return
    setActive(null)
    setModalError('')
  }

  async function resolveReport() {
    if (!active) return
    setSubmitting(true)
    setModalError('')
    try {
      await api(`/admin/reports/${active.id}`, { method: 'PUT', body: { resolution, note: note.trim() } })
      showToast({
        title: resolution === 'takedown' ? t('admin.reports.takedownSuccess') : t('admin.reports.rejectSuccess'),
        message: t('admin.reports.notificationSent'),
        tone: 'success',
      })
      setActive(null)
      await load()
    } catch (requestError) {
      setModalError((requestError as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AdminLayout current="reports" breadcrumb={t('admin.nav.reports')}>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-bold text-slate-900">
            <i className="fa-solid fa-shield-halved text-primary-600" aria-hidden="true" />{t('admin.nav.reports')}
          </h1>
          <p className="mt-1.5 text-sm text-slate-500">{t('admin.reports.description')}</p>
        </div>
        <Badge tone="primary">{t('admin.reports.totalRecords', { total })}</Badge>
      </div>

      <Card className="mb-5 p-4">
        <SegmentedTabs size="sm" value={status} items={statusTabs} ariaLabel={t('admin.reports.statusFilterLabel')}
          onChange={(value) => { setStatus(value); setPage(1) }} />
        <div className="mt-3 flex flex-wrap items-center gap-3">
          <form onSubmit={applyQuery} className="w-full sm:w-72">
            <Input value={queryInput} onChange={(event) => setQueryInput(event.target.value)}
              leading={<i className="fa-solid fa-magnifying-glass" aria-hidden="true" />} placeholder={t('admin.reports.searchPlaceholder')} />
          </form>
          <Select className="w-36" value={targetType} options={targetOptions}
            onChange={(value) => { setTargetType(value); setPage(1) }} />
          <Select className="w-48" value={reason} options={reasonOptions}
            onChange={(value) => { setReason(value); setPage(1) }} />
          {(query || targetType || reason) && (
            <Button type="button" variant="ghost" onClick={() => {
              setQueryInput(''); setQuery(''); setTargetType(''); setReason(''); setPage(1)
            }}>{t('common.actions.clearFilter')}</Button>
          )}
        </div>
      </Card>

      {error && <p role="alert" className="mb-4 max-h-28 overflow-y-auto break-words rounded-xl border border-rose-100 bg-rose-50 px-4 py-3 text-sm text-rose-700">{error}</p>}

      {loading ? (
        <Loading className="py-24" label={t('admin.reports.loading')} />
      ) : items.length === 0 ? (
        <EmptyState>{t('admin.reports.empty')}</EmptyState>
      ) : (
        <>
          <div className="space-y-3">
            {items.map((item) => (
              <Card key={item.id} className="p-5">
                <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge tone={item.status === 'pending' ? 'amber' : item.status === 'resolved' ? 'rose' : 'slate'}>
                        {item.status === 'pending' ? t('admin.reports.status.pending') : item.status === 'resolved' ? t('admin.reports.status.resolved') : t('admin.reports.status.rejected')}
                      </Badge>
                      <Badge>{targetLabels[item.target_type] || item.target_type} #{item.target_id}</Badge>
                      <Badge tone="primary">{reasonLabels[item.reason] || item.reason}</Badge>
                      <span className="text-xs text-slate-400">{t('admin.reports.reportedAt', { time: formatDate(item.created_at) })}</span>
                    </div>
                    <h2 className="mt-3 break-words text-base font-semibold text-slate-900">{item.target_label || t('admin.reports.unnamedContent')}</h2>
                    <p className="mt-2 whitespace-pre-wrap break-words text-sm leading-6 text-slate-600">{item.description || t('admin.reports.noDescription')}</p>
                    <div className="mt-4 flex flex-wrap gap-x-5 gap-y-1 text-xs text-slate-500">
                      <span><i className="fa-solid fa-user-shield mr-1.5 text-slate-400" aria-hidden="true" />{t('admin.reports.reporter', { username: item.reporter_username, email: item.reporter_email })}</span>
                      {item.handler_username && <span>{t('admin.reports.handler', { username: item.handler_username })}</span>}
                      {item.resolved_at && <span>{t('admin.reports.resolvedAt', { time: formatDate(item.resolved_at) })}</span>}
                    </div>
                    {item.resolution_note && (
                      <p className="mt-3 rounded-lg bg-slate-50 px-3 py-2 text-sm text-slate-600">{t('admin.reports.resolutionNote', { note: item.resolution_note })}</p>
                    )}
                  </div>
                  {item.status === 'pending' && (
                    <div className="flex shrink-0 gap-2">
                      <Button type="button" size="sm" variant="outline" onClick={() => openResolution(item, 'reject')}>{t('admin.reports.rejectReport')}</Button>
                      <Button type="button" size="sm" variant="danger" onClick={() => openResolution(item, 'takedown')}>{t('admin.reports.takedownContent')}</Button>
                    </div>
                  )}
                </div>
              </Card>
            ))}
          </div>
          <Pagination page={page} total={total} pageSize={PAGE_SIZE} onChange={setPage} loading={loading} />
        </>
      )}

      <Modal open={Boolean(active)} onClose={closeResolution}
        title={resolution === 'takedown' ? t('admin.reports.confirmTakedown') : t('admin.reports.confirmReject')}
        footer={(
          <>
            <Button type="button" variant="ghost" onClick={closeResolution} disabled={submitting}>{t('common.actions.cancel')}</Button>
            <Button type="button" variant={resolution === 'takedown' ? 'danger' : 'primary'} loading={submitting} onClick={resolveReport}>
              {resolution === 'takedown' ? t('admin.reports.confirmTakedown') : t('admin.reports.confirmReject')}
            </Button>
          </>
        )}>
        <div className="space-y-4">
          <div className={`rounded-xl border px-4 py-3 text-sm leading-6 ${resolution === 'takedown' ? 'border-rose-100 bg-rose-50 text-rose-700' : 'border-primary-100 bg-primary-50 text-primary-700'}`}>
            {resolution === 'takedown'
              ? t('admin.reports.takedownWarning', { title: active?.target_label || t('admin.reports.thisContent') })
              : t('admin.reports.rejectInfo')}
          </div>
          <Field label={t('admin.reports.resolutionNoteLabel')} hint={t('admin.reports.resolutionNoteHint', { count: note.length })}>
            <Textarea rows={5} maxLength={1000} value={note} disabled={submitting}
              onChange={(event) => setNote(event.target.value)} placeholder={t('admin.reports.resolutionNotePlaceholder')} />
          </Field>
          {modalError && <p role="alert" className="max-h-24 overflow-y-auto break-words rounded-lg border border-rose-100 bg-rose-50 px-3 py-2 text-sm text-rose-700">{modalError}</p>}
        </div>
      </Modal>
    </AdminLayout>
  )
}
