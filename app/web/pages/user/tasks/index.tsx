import { useCallback, useEffect, useState } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/router'
import Container from '@/components/Container'
import Seo from '@/components/Seo'
import { api, formatDate } from '@/lib/api'
import { useApp, useRequireAuth } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { subscribeUserTasks, userTaskKey, userTaskTab, type UserTask, type UserTaskStatus, type UserTaskTab } from '@/lib/user-tasks'
import { Badge, Button, ButtonLink, Card, EmptyState, Loading, SegmentedTabs } from '@/components/ui'

const TABS: UserTaskTab[] = ['active', 'done', 'failed']

const KIND_ICONS: Record<string, string> = {
  zipImport: 'fa-file-zipper',
  pdfImport: 'fa-file-pdf',
  imageLocalize: 'fa-images',
  translate: 'fa-language',
  siteCrawl: 'fa-globe',
  aiWriter: 'fa-wand-magic-sparkles',
  chapterGuide: 'fa-map',
}

const STATUS_TONES: Record<UserTaskStatus, 'slate' | 'emerald' | 'amber' | 'sky' | 'rose' | 'primary'> = {
  queued: 'slate', running: 'primary', paused: 'amber', done: 'emerald', failed: 'rose', canceled: 'slate',
}

// 我的任务：自己发起的后台任务（导入、AI 翻译、整站采集、写作助手、导读生成等），按「进行中 / 已完成 / 失败」分组（?tab=），
// 进度经通知事件流实时更新，不轮询；每条任务可跳到对应的详情或结果页。
export default function MyTasksPage() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  const router = useRouter()
  const tab: UserTaskTab = TABS.includes(router.query.tab as UserTaskTab) ? (router.query.tab as UserTaskTab) : 'active'
  const [items, setItems] = useState<UserTask[] | null>(null)
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const d = await api<{ items: UserTask[] }>('/users/me/tasks', { params: { tab } })
      setItems(d.items || [])
    } catch {
      setItems([])
    }
    setLoading(false)
  }, [tab])

  useEffect(() => {
    if (!user || !router.isReady) return
    setItems(null)
    void load()
  }, [user, router.isReady, load])

  // 实时更新：已在列表中的任务原地更新（进行中的任务结束后仍留在本页，显示最终状态）；新任务进入所属分组时插到最前
  useEffect(() => subscribeUserTasks((task) => {
    if (!task) { void load(); return }
    setItems((list) => {
      if (!list) return list
      const key = userTaskKey(task)
      if (list.some((x) => userTaskKey(x) === key)) return list.map((x) => (userTaskKey(x) === key ? task : x))
      return userTaskTab(task.status) === tab ? [task, ...list] : list
    })
  }), [tab, load])

  if (!user) return <Loading className="min-h-[60vh]" />
  const siteName = site.site_name || 'KnowForge'

  return (
    <>
      <Seo siteName={siteName} title={t('userTasks.page.title')} noindex />
      <Container>
        <div className="flex flex-col justify-between gap-4 pb-6 sm:flex-row sm:items-end">
          <div>
            <h1 className="text-3xl font-bold text-ink">{t('userTasks.page.title')}</h1>
            <p className="mt-2 text-[15px] text-slate-500">{t('userTasks.page.description')}</p>
          </div>
          <Button variant="outline" onClick={() => void load()} loading={loading && items !== null}>
            <i className="fa-solid fa-rotate" aria-hidden="true" /> {t('userTasks.page.refresh')}
          </Button>
        </div>

        <SegmentedTabs className="mb-5" value={tab} ariaLabel={t('userTasks.page.title')} items={TABS.map((value) => ({
          value, label: t(`userTasks.tab.${value}`), href: value === 'active' ? '/user/tasks' : `/user/tasks?tab=${value}`,
        }))} />

        {items === null ? (
          <Loading className="py-20" label={t('userTasks.page.loading')} />
        ) : items.length === 0 ? (
          <EmptyState>
            <i className="fa-solid fa-list-check mb-3 block text-2xl text-slate-300" aria-hidden="true" />
            {t(`userTasks.empty.${tab}`)}
          </EmptyState>
        ) : (
          <div className="space-y-3">
            {items.map((task) => <TaskRow key={userTaskKey(task)} task={task} />)}
          </div>
        )}
      </Container>
    </>
  )
}

function TaskRow({ task }: { task: UserTask }) {
  const { t } = useTranslation()
  const processed = task.done + task.failed
  const percent = task.total > 0 ? Math.min(100, Math.round((processed / task.total) * 100)) : 0
  const active = userTaskTab(task.status) === 'active'
  return (
    <Card className="p-4 sm:p-5" data-testid="user-task-row">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center">
        <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-slate-100 text-slate-500">
          <i className={`fa-solid ${KIND_ICONS[task.kind] || 'fa-gears'}`} aria-hidden="true" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <Badge tone="slate">{t(`userTasks.kind.${task.kind}`)}</Badge>
            {task.link
              ? <Link href={task.link} className="truncate font-semibold text-slate-900 hover:text-primary-600">{task.title || '-'}</Link>
              : <span className="truncate font-semibold text-slate-900">{task.title || '-'}</span>}
            <Badge tone={STATUS_TONES[task.status] || 'slate'}>
              {task.status === 'running' && <i className="fa-solid fa-spinner fa-spin mr-1" aria-hidden="true" />}
              {t(`userTasks.status.${task.status}`)}
            </Badge>
          </div>
          {task.total > 0 ? (
            <div className="mt-2.5">
              <div className="h-1.5 overflow-hidden rounded-full bg-slate-100" role="progressbar" aria-valuemin={0} aria-valuemax={task.total} aria-valuenow={processed}>
                <div className={`h-full rounded-full transition-all ${task.failed > 0 && !active ? 'bg-amber-500' : 'bg-primary-500'}`} style={{ width: `${percent}%` }} />
              </div>
              <p className="mt-1 text-xs text-slate-500">
                {t('userTasks.row.progress', { done: task.done, total: task.total, percent })}
                {task.failed > 0 && <span className="ml-2 text-rose-600">{t('userTasks.row.failedCount', { n: task.failed })}</span>}
              </p>
            </div>
          ) : active && (
            <div className="mt-2.5">
              <div className="h-1.5 overflow-hidden rounded-full bg-slate-100">
                <div className={`h-full w-1/3 rounded-full bg-primary-500 ${task.status === 'running' ? 'animate-pulse' : 'opacity-40'}`} />
              </div>
              {task.remaining > 0 && <p className="mt-1 text-xs text-slate-500">{t('userTasks.row.remaining', { n: task.remaining })}</p>}
            </div>
          )}
          {task.error && task.status !== 'done' && <p className="mt-1.5 text-xs text-rose-600">{task.error}</p>}
          <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 text-xs text-slate-400">
            <span>{t('userTasks.row.createdAt', { time: formatDate(task.created_at) })}</span>
            {task.finished_at && !active && <span>{t('userTasks.row.finishedAt', { time: formatDate(task.finished_at) })}</span>}
          </div>
        </div>
        {task.link && (
          <ButtonLink size="sm" variant="outline" href={task.link} className="shrink-0">
            {t(active ? 'userTasks.row.viewProgress' : 'userTasks.row.view')}
          </ButtonLink>
        )}
      </div>
    </Card>
  )
}
