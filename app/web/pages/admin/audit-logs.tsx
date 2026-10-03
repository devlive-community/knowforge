import { FormEvent, useCallback, useEffect, useState } from 'react'
import AdminLayout from '@/components/AdminLayout'
import { ActivityIcon, SearchIcon, ShieldCheckIcon } from '@/components/icons'
import { Badge, Button, DatePicker, EmptyState, Input, Loading, Pagination, Select } from '@/components/ui'
import { api, formatDate } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import type { PageResult } from '@/lib/types'
import { useUrlPage } from '@/lib/use-url-page'

const PAGE_SIZE = 20

interface AuditLog {
  id: number
  actor_id: number
  actor_username: string
  action: string
  resource_type: string
  resource_id: string
  resource_label: string
  summary: Record<string, unknown> | null
  created_at: string
}

type TFn = (key: string, vars?: Record<string, string | number>) => string

// label 取可选的 i18n 文案：键不存在时返回 undefined（便于逐级回退）。
function label(t: TFn, key: string, vars?: Record<string, string | number>): string | undefined {
  const v = t(key, vars)
  return v === key ? undefined : v
}

// 审计词汇的多语言：操作 / 资源类型 / 摘要字段名（字段名还可复用权益名称），缺失时回退原始值。
const actionText = (t: TFn, action: string) => label(t, `admin.audit.actions.${action}`) || action
const resourceText = (t: TFn, type: string) => label(t, `admin.audit.resources.${type}`) || type
const fieldText = (t: TFn, field: string) => label(t, `admin.audit.fields.${field}`) || label(t, `entitlement.${field}.label`) || field

// targetText 资源名称：系统类目标（如「邮件配置」「基础权益」）的名称由服务端以中文存储，这里按类型 + ID 本地化；
// 用户内容（书名、用户名等）原样显示。
function targetText(t: TFn, item: AuditLog): string {
  if (item.action === 'i18n.messages_updated') return t('admin.audit.targets.languagePack', { code: item.resource_id })
  const known = label(t, `admin.audit.targets.${item.resource_type}.${item.resource_id.replace(/[^\w]/g, '_')}`)
  return known || item.resource_label || `${resourceText(t, item.resource_type)} ${item.resource_id}`
}

function displayValue(value: unknown, t: TFn): string {
  if (typeof value === 'boolean') return value ? t('common.boolean.yes') : t('common.boolean.no')
  if (value === null || value === undefined || value === '') return t('common.unset')
  if (Array.isArray(value)) return value.map((v) => displayValue(v, t)).join(t('admin.audit.format.listSep'))
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

// summaryText 变更摘要：字段名本地化，标点按语言（format 键）拼接。
function summaryText(summary: Record<string, unknown> | null, t: TFn): string {
  if (!summary) return t('admin.audit.completed')
  const fields = summary.changed_fields
  if (Array.isArray(fields)) return `${t('admin.audit.changedFields')}${fields.map((f) => fieldText(t, String(f))).join(t('admin.audit.format.listSep'))}`
  const parts = Object.entries(summary).map(([key, value]) => {
    if (value && typeof value === 'object' && !Array.isArray(value)) {
      const change = value as { from?: unknown; to?: unknown }
      if ('from' in change || 'to' in change) {
        return t('admin.audit.format.change', { field: fieldText(t, key), from: displayValue(change.from, t), to: displayValue(change.to, t) })
      }
    }
    return t('admin.audit.format.field', { field: fieldText(t, key), value: displayValue(value, t) })
  })
  return parts.join(t('admin.audit.format.partSep')) || t('admin.audit.completed')
}

export default function AdminAuditLogs() {
  const { user } = useApp()
  const { t } = useTranslation()
  const isAdmin = user?.role === 'admin'

  // 筛选项：审计日志中实际出现过的操作与资源类型（服务端去重），标签按当前语言
  const [facets, setFacets] = useState<{ actions: string[]; resource_types: string[] }>({ actions: [], resource_types: [] })
  useEffect(() => {
    if (!isAdmin) return
    api<{ actions: string[]; resource_types: string[] }>('/admin/audit-logs/facets').then(setFacets).catch(() => { /* 筛选项加载失败不影响列表 */ })
  }, [isAdmin])
  const actionOptions = [{ value: '', label: t('admin.audit.action.all') }, ...facets.actions.map((a) => ({ value: a, label: actionText(t, a) }))]
  const resourceOptions = [{ value: '', label: t('admin.audit.resource.all') }, ...facets.resource_types.map((r) => ({ value: r, label: resourceText(t, r) }))]
  const [items, setItems] = useState<AuditLog[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useUrlPage()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [operatorInput, setOperatorInput] = useState('')
  const [operator, setOperator] = useState('')
  const [action, setAction] = useState('')
  const [resourceType, setResourceType] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const result = await api<PageResult<AuditLog>>('/admin/audit-logs', {
        params: { page, page_size: PAGE_SIZE, actor: operator, action, resource_type: resourceType, from, to },
      })
      setItems(result.items)
      setTotal(result.total)
    } catch (requestError) {
      setError((requestError as Error).message)
    } finally {
      setLoading(false)
    }
  }, [action, from, operator, page, resourceType, to])

  useEffect(() => {
    if (isAdmin) load()
  }, [isAdmin, load])

  function applyOperator(event: FormEvent) {
    event.preventDefault()
    setPage(1)
    setOperator(operatorInput.trim())
  }

  function clearFilters() {
    setOperatorInput('')
    setOperator('')
    setAction('')
    setResourceType('')
    setFrom('')
    setTo('')
    setPage(1)
  }

  const hasFilters = Boolean(operator || action || resourceType || from || to)

  return (
    <AdminLayout current="audit" breadcrumb={t('admin.nav.audit')}>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-bold text-slate-900">
            <ActivityIcon className="h-6 w-6 text-primary-600" />{t('admin.nav.audit')}
          </h1>
          <p className="mt-1.5 text-sm text-slate-500">{t('admin.audit.description')}</p>
        </div>
        <div className="flex items-center gap-2 rounded-xl border border-emerald-100 bg-emerald-50 px-3 py-2 text-xs font-medium text-emerald-700">
          <ShieldCheckIcon className="h-4 w-4" />{t('admin.audit.sensitiveNote')}
        </div>
      </div>

      <div className="mb-5 rounded-2xl border border-slate-200 bg-white p-4 shadow-sm">
        <div className="flex flex-wrap items-center gap-3">
          <form onSubmit={applyOperator} className="w-full sm:w-56">
            <Input value={operatorInput} onChange={(event) => setOperatorInput(event.target.value)}
              leading={<SearchIcon className="h-4 w-4" />} placeholder={t('admin.audit.searchPlaceholder')} />
          </form>
          <Select className="w-48" value={action} options={actionOptions}
            onChange={(value) => { setAction(value); setPage(1) }} />
          <Select className="w-36" value={resourceType} options={resourceOptions}
            onChange={(value) => { setResourceType(value); setPage(1) }} />
          <DatePicker className="w-40" value={from} onChange={(value) => { setFrom(value); setPage(1) }} placeholder={t('common.date.from')} max={to || undefined} ariaLabel={t('common.date.from')} />
          <DatePicker className="w-40" value={to} onChange={(value) => { setTo(value); setPage(1) }} placeholder={t('common.date.to')} min={from || undefined} ariaLabel={t('common.date.to')} />
          {hasFilters && <Button variant="ghost" onClick={clearFilters}>{t('common.actions.clearFilter')}</Button>}
          <span className="ml-auto text-sm text-slate-400">{t('admin.audit.totalRecords', { total })}</span>
        </div>
      </div>

      {error && <div className="mb-4 rounded-xl border border-rose-100 bg-rose-50 px-4 py-3 text-sm text-rose-700">{error}</div>}

      {loading ? (
        <Loading className="py-24" label={t('admin.audit.loading')} />
      ) : items.length === 0 ? (
        <EmptyState>{t('admin.audit.empty')}</EmptyState>
      ) : (
        <>
          <div className="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
            <div className="overflow-x-auto">
              <table className="w-full min-w-[980px] text-sm">
                <thead>
                  <tr className="border-b border-slate-100 bg-slate-50/70 text-left text-xs font-medium text-slate-400">
                    <th className="px-5 py-3">{t('admin.audit.column.time')}</th>
                    <th className="px-5 py-3">{t('admin.audit.column.actor')}</th>
                    <th className="px-5 py-3">{t('admin.audit.column.action')}</th>
                    <th className="px-5 py-3">{t('admin.audit.column.resource')}</th>
                    <th className="px-5 py-3">{t('admin.audit.column.summary')}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100">
                  {items.map((item) => (
                    <tr key={item.id} className="align-top hover:bg-slate-50/60">
                      <td className="whitespace-nowrap px-5 py-4 text-slate-500">{formatDate(item.created_at)}</td>
                      <td className="px-5 py-4">
                        <p className="font-medium text-slate-800">{item.actor_username}</p>
                        <p className="mt-0.5 text-xs text-slate-400">ID {item.actor_id}</p>
                      </td>
                      <td className="px-5 py-4"><Badge tone="slate">{actionText(t, item.action)}</Badge></td>
                      <td className="px-5 py-4">
                        <p className="font-medium text-slate-700">{targetText(t, item)}</p>
                        <p className="mt-0.5 text-xs text-slate-400">{resourceText(t, item.resource_type)} · {item.resource_id}</p>
                      </td>
                      <td className="max-w-lg px-5 py-4 leading-6 text-slate-600">{summaryText(item.summary, t)}</td>
                    </tr>
                  ))}
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
