import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import type { Document } from '@/lib/types'
import { Button, EmptyState, Loading, SegmentedTabs } from '@/components/ui'
import { CollabAvatar, type CollabUser } from './collab'
import { STAGE_DOT, TASK_STAGES, dueKey, isOverdue, type WriterTask } from './tasks'

export type TeamTab = 'activity' | 'tasks'

interface Activity {
  id: number
  kind: string
  document_id: number
  user: CollabUser
  detail: Record<string, any>
  created_at: string
  updated_at: string
}

const PAGE_SIZE = 30

interface Props {
  bookId: number
  tab: TeamTab
  onNavigate: (tab: TeamTab | null) => void
  docs: Document[] // 目录顺序的全部章节
  tasks: Record<number, WriterTask>
  onOpenDoc: (doc: Document) => void
  reloadKey: number
}

function dayKey(iso: string): string {
  return dueKey(iso)
}

// TeamDrawer 写作台右侧「协作」：动态（谁在什么时候修改、批注、处理建议、调整分工）与分工总览（各章节负责人、阶段与截止日期）。
export default function TeamDrawer({ bookId, tab, onNavigate, docs, tasks, onOpenDoc, reloadKey }: Props) {
  const { t } = useTranslation()
  return (
    <>
      <div className="fixed inset-0 z-[60] bg-black/30 lg:hidden" onClick={() => onNavigate(null)} />
      <aside className="fixed inset-y-0 right-0 z-[61] flex w-full max-w-md flex-col border-l border-slate-200 bg-white shadow-2xl lg:w-96 2xl:w-[28rem] 2xl:max-w-none" aria-label={t('writer.team.title')} data-testid="team-drawer">
        <div className="flex shrink-0 items-center gap-3 border-b border-slate-200 px-4 py-3">
          <i className="fa-solid fa-users text-primary-500" aria-hidden="true" />
          <span className="font-semibold text-slate-900">{t('writer.team.title')}</span>
          <Button size="sm" variant="ghost" className="ml-auto !px-2" aria-label={t('writer.team.close')} onClick={() => onNavigate(null)}>
            <i className="fa-solid fa-xmark" aria-hidden="true" />
          </Button>
        </div>
        <div className="shrink-0 px-4 pt-3">
          <SegmentedTabs fullWidth size="sm" value={tab} ariaLabel={t('writer.team.title')} onChange={(v) => onNavigate(v as TeamTab)}
            items={[{ value: 'activity', label: t('writer.team.activity') }, { value: 'tasks', label: t('writer.team.tasks') }]} />
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
          {tab === 'activity'
            ? <ActivityFeed bookId={bookId} docs={docs} onOpenDoc={onOpenDoc} reloadKey={reloadKey} />
            : <TaskOverview docs={docs} tasks={tasks} onOpenDoc={onOpenDoc} />}
        </div>
      </aside>
    </>
  )
}

function ActivityFeed({ bookId, docs, onOpenDoc, reloadKey }: { bookId: number; docs: Document[]; onOpenDoc: (doc: Document) => void; reloadKey: number }) {
  const { t } = useTranslation()
  const [items, setItems] = useState<Activity[] | null>(null)
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')
  const byId = useMemo(() => new Map(docs.map((d) => [d.id, d])), [docs])

  const load = useCallback(async (upto: number) => {
    try {
      const d = await api<{ items: Activity[]; total: number }>(`/books/${bookId}/writer-activity`, { params: { page: 1, page_size: PAGE_SIZE * upto } })
      setItems(d.items || [])
      setTotal(d.total)
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }, [bookId])

  useEffect(() => { void load(page) }, [load, reloadKey]) // eslint-disable-line react-hooks/exhaustive-deps

  async function more() {
    setLoadingMore(true)
    await load(page + 1)
    setPage((p) => p + 1)
    setLoadingMore(false)
  }

  if (error) return <p className="text-sm text-rose-600">{error}</p>
  if (!items) return <Loading className="py-10" />
  if (items.length === 0) return <EmptyState>{t('writer.team.noActivity')}</EmptyState>

  const today = dayKey(new Date().toISOString())
  const yesterday = dayKey(new Date(Date.now() - 86400000).toISOString())
  let lastDay = ''
  return (
    <div data-testid="activity-feed">
      <ul className="space-y-3">
        {items.map((it) => {
          const day = dayKey(it.updated_at)
          const header = day !== lastDay ? (day === today ? t('writer.team.today') : day === yesterday ? t('writer.team.yesterday') : day) : null
          lastDay = day
          const doc = byId.get(it.document_id)
          const chapter = it.detail.title || doc?.title || '-'
          return (
            <li key={it.id}>
              {header && <div className="mb-2 mt-1 text-xs font-medium text-slate-400">{header}</div>}
              <div className="flex gap-2.5" data-testid="activity-item">
                <CollabAvatar user={it.user} />
                <div className="min-w-0 flex-1 text-sm">
                  <p className="text-slate-700">
                    <span className="font-medium text-slate-900">{it.user.display_name || it.user.username}</span>{' '}
                    {t(`writer.team.kind.${it.kind}`, { chapter, suggester: it.detail.suggester || '', count: it.detail.count || 1, status: it.detail.status || 'other' })}
                  </p>
                  <ActivityDetail item={it} />
                  <div className="mt-0.5 flex items-center gap-2 text-[11px] text-slate-400">
                    <span>{formatDate(it.updated_at).slice(11)}</span>
                    {doc && it.kind !== 'doc.deleted' && (
                      <button type="button" className="text-primary-600 hover:underline" onClick={() => onOpenDoc(doc)}>{t('writer.team.open')}</button>
                    )}
                  </div>
                </div>
              </div>
            </li>
          )
        })}
      </ul>
      {items.length < total && (
        <Button size="sm" variant="ghost" className="mt-3 w-full" loading={loadingMore} onClick={() => void more()}>{t('writer.team.more')}</Button>
      )}
    </div>
  )
}

function ActivityDetail({ item }: { item: Activity }) {
  const { t } = useTranslation()
  const d = item.detail
  if (item.kind === 'doc.saved') {
    return (
      <p className="mt-0.5 text-xs">
        <span className="text-emerald-600">+{d.added || 0}</span> <span className="text-rose-600">−{d.removed || 0}</span>
        {d.old_title && <span className="ml-2 text-slate-400">{t('writer.team.renamed', { title: d.old_title })}</span>}
      </p>
    )
  }
  // 批注带摘录，修改建议带说明
  const quote = d.excerpt || d.note
  if (quote) {
    return <p className="mt-1 line-clamp-2 border-l-2 border-slate-200 pl-2 text-xs text-slate-500">{quote}</p>
  }
  if (item.kind === 'task.updated') {
    const parts: string[] = []
    if ('assignee' in d) parts.push(d.assignee ? t('writer.task.assigneeIs', { name: d.assignee }) : t('writer.team.unassigned'))
    if (d.stage) parts.push(t(`writer.task.stage.${d.stage}`))
    if ('due_at' in d) parts.push(d.due_at ? t('writer.task.dueIs', { date: d.due_at }) : t('writer.team.noDue'))
    return <p className="mt-0.5 text-xs text-slate-500">{parts.join(' · ')}</p>
  }
  return null
}

function TaskOverview({ docs, tasks, onOpenDoc }: { docs: Document[]; tasks: Record<number, WriterTask>; onOpenDoc: (doc: Document) => void }) {
  const { t } = useTranslation()
  const counts = useMemo(() => {
    const c: Record<string, number> = { todo: 0, writing: 0, review: 0, done: 0, overdue: 0, unassigned: 0 }
    for (const d of docs) {
      const task = tasks[d.id]
      c[task?.stage || 'todo']++
      if (task && isOverdue(task)) c.overdue++
      if (!task?.assignee) c.unassigned++
    }
    return c
  }, [docs, tasks])
  if (docs.length === 0) return <EmptyState>{t('writer.team.noChapters')}</EmptyState>
  return (
    <div data-testid="task-overview">
      <div className="grid grid-cols-2 gap-2 text-xs">
        {TASK_STAGES.map((s) => (
          <div key={s} className="flex items-center gap-2 rounded-lg border border-slate-200 px-3 py-2">
            <span className={`h-2 w-2 rounded-full ${STAGE_DOT[s]}`} />
            <span className="text-slate-600">{t(`writer.task.stage.${s}`)}</span>
            <span className="ml-auto font-semibold tabular-nums text-slate-900">{counts[s]}</span>
          </div>
        ))}
      </div>
      <p className="mt-2 text-xs text-slate-500">
        {t('writer.team.summary', { overdue: counts.overdue, unassigned: counts.unassigned })}
      </p>
      <ul className="mt-3 divide-y divide-slate-100 rounded-xl border border-slate-200">
        {docs.map((d) => {
          const task = tasks[d.id]
          const overdue = task ? isOverdue(task) : false
          return (
            <li key={d.id}>
              <button type="button" onClick={() => onOpenDoc(d)} className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-slate-50" data-testid="overview-row">
                <span className={`h-2 w-2 shrink-0 rounded-full ${STAGE_DOT[task?.stage || 'todo']}`} />
                <span className="min-w-0 flex-1 truncate text-slate-700">{d.title}</span>
                {task?.assignee ? <CollabAvatar user={task.assignee} size="sm" /> : <span className="text-[11px] text-slate-300">{t('writer.task.unassigned')}</span>}
                {task?.due_at && <span className={`shrink-0 text-[11px] tabular-nums ${overdue ? 'font-medium text-rose-600' : 'text-slate-400'}`}>{dueKey(task.due_at).slice(5)}</span>}
              </button>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
