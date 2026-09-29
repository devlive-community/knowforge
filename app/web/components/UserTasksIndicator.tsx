import Link from 'next/link'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { useActiveUserTasks } from '@/lib/user-tasks'
import { Tooltip } from '@/components/ui'

// UserTasksIndicator 导航栏的进行中任务标记：有任务在进行时显示转圈图标与数量，点击进入「我的任务」；没有时不显示。
export default function UserTasksIndicator() {
  const { t } = useTranslation()
  const { user } = useApp()
  const tasks = useActiveUserTasks(!!user)
  if (!user || tasks.length === 0) return null
  const running = tasks.some((task) => task.status === 'running')
  return (
    <Tooltip content={t('userTasks.indicator.tooltip', { n: tasks.length })} placement="bottom">
      <Link href="/user/tasks" aria-label={t('userTasks.indicator.tooltip', { n: tasks.length })} data-testid="user-tasks-indicator"
        className="relative flex items-center justify-center rounded-lg text-primary-600 hover:bg-slate-100"
        style={{ width: 'var(--control-height)', height: 'var(--control-height)' }}>
        <i className={`fa-solid ${running ? 'fa-spinner fa-spin' : 'fa-hourglass-half'} text-base`} aria-hidden="true" />
        <span className="absolute right-0.5 top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-primary-500 px-1 text-[10px] font-semibold leading-none text-white">
          {tasks.length > 99 ? '99+' : tasks.length}
        </span>
      </Link>
    </Tooltip>
  )
}
