import { useState } from 'react'
import { api } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { DatePicker, Select, Tooltip, useFeedback } from '@/components/ui'
import { CollabAvatar, type CollabUser } from './collab'

// —— 章节分工：负责人、阶段（未开始 / 写作中 / 待审阅 / 已定稿）与截止日期 ——

export type TaskStage = 'todo' | 'writing' | 'review' | 'done'
export const TASK_STAGES: TaskStage[] = ['todo', 'writing', 'review', 'done']

export interface WriterTask {
  document_id: number
  assignee: CollabUser | null
  stage: TaskStage
  due_at: string | null
  updated_at: string
}

export interface WriterMember extends CollabUser { role: string }

export const STAGE_DOT: Record<TaskStage, string> = {
  todo: 'bg-slate-300', writing: 'bg-sky-500', review: 'bg-amber-500', done: 'bg-emerald-500',
}

// dueKey 截止日期的 YYYY-MM-DD（本地时区）。
export function dueKey(due: string | null): string {
  if (!due) return ''
  const d = new Date(due)
  if (Number.isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

export function isOverdue(task: WriterTask): boolean {
  if (!task.due_at || task.stage === 'done') return false
  const today = dueKey(new Date().toISOString())
  return dueKey(task.due_at) < today
}

// TaskBadge 章节树中的分工标记：阶段色点（悬停显示阶段、负责人与截止日期）与负责人头像。
export function TaskBadge({ task }: { task: WriterTask }) {
  const { t } = useTranslation()
  const overdue = isOverdue(task)
  const tip = [
    t(`writer.task.stage.${task.stage}`),
    task.assignee ? t('writer.task.assigneeIs', { name: task.assignee.display_name || task.assignee.username }) : '',
    task.due_at ? t(overdue ? 'writer.task.overdue' : 'writer.task.dueIs', { date: dueKey(task.due_at) }) : '',
  ].filter(Boolean).join(' · ')
  return (
    <Tooltip content={tip}>
      <span className="ml-1 inline-flex items-center gap-1" data-testid="tree-task">
        <span className={`h-2 w-2 rounded-full ${STAGE_DOT[task.stage]} ${overdue ? 'ring-2 ring-rose-300' : ''}`} />
        {task.assignee && <CollabAvatar user={task.assignee} size="sm" />}
      </span>
    </Tooltip>
  )
}

// TaskPanel 章节设置中的「分工」：能编辑的人修改后立即保存（各控件单独显示保存中）；建议者只读。
export function TaskPanel({ docId, task, members, canEdit, onSaved }: {
  docId: number
  task: WriterTask | undefined
  members: WriterMember[]
  canEdit: boolean
  onSaved: (task: WriterTask) => void
}) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [saving, setSaving] = useState<'assignee' | 'stage' | 'due' | null>(null)
  const stage: TaskStage = task?.stage || 'todo'

  async function save(field: 'assignee' | 'stage' | 'due', body: Record<string, unknown>) {
    setSaving(field)
    try {
      onSaved(await api<WriterTask>(`/documents/${docId}/task`, { method: 'PUT', body }))
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setSaving(null)
  }

  const label = (text: string, field: 'assignee' | 'stage' | 'due') => (
    <span className="mb-1.5 flex items-center gap-1.5 text-sm font-medium text-slate-700">
      {text}
      {saving === field && <span className="h-3 w-3 animate-spin rounded-full border-2 border-slate-200 border-t-primary-500" aria-hidden="true" />}
    </span>
  )

  if (!canEdit) {
    return (
      <dl className="space-y-1.5 text-sm" data-testid="task-panel">
        <div className="flex justify-between gap-2"><dt className="text-slate-400">{t('writer.task.assignee')}</dt><dd className="text-slate-700">{task?.assignee ? (task.assignee.display_name || task.assignee.username) : t('writer.task.unassigned')}</dd></div>
        <div className="flex justify-between gap-2"><dt className="text-slate-400">{t('writer.task.stageLabel')}</dt><dd className="text-slate-700">{t(`writer.task.stage.${stage}`)}</dd></div>
        <div className="flex justify-between gap-2"><dt className="text-slate-400">{t('writer.task.due')}</dt><dd className="text-slate-700">{task?.due_at ? dueKey(task.due_at) : '-'}</dd></div>
      </dl>
    )
  }
  return (
    <div className="space-y-3" data-testid="task-panel">
      <div>
        {label(t('writer.task.assignee'), 'assignee')}
        <Select value={String(task?.assignee?.id || 0)} disabled={saving !== null} searchable={members.length > 6}
          options={[{ value: '0', label: t('writer.task.unassigned') }, ...members.map((m) => ({ value: String(m.id), label: m.display_name || m.username }))]}
          onChange={(v) => void save('assignee', { assignee_id: Number(v) })} />
      </div>
      <div>
        {label(t('writer.task.stageLabel'), 'stage')}
        <Select value={stage} disabled={saving !== null}
          leading={<span className={`h-2 w-2 shrink-0 rounded-full ${STAGE_DOT[stage]}`} />}
          options={TASK_STAGES.map((s) => ({ value: s, label: t(`writer.task.stage.${s}`) }))}
          onChange={(v) => void save('stage', { stage: v })} />
      </div>
      <div>
        {label(t('writer.task.due'), 'due')}
        <DatePicker value={task?.due_at ? dueKey(task.due_at) : ''} ariaLabel={t('writer.task.due')}
          onChange={(v) => void save('due', { due_at: v || null })} />
      </div>
    </div>
  )
}
