import { useEffect, useState } from 'react'
import { api } from './api'

// 我的任务：用户发起的后台任务（导入、AI 翻译、整站采集等）。任务变化经通知事件流（/notifications/stream）
// 以 {"task": {...}} 推送，由 NotificationBell 转发到这里的订阅者；事件流重连后通知订阅者重新拉取，不轮询。

export type UserTaskStatus = 'queued' | 'running' | 'paused' | 'done' | 'failed' | 'canceled'
export type UserTaskTab = 'active' | 'done' | 'failed'

export interface UserTask {
  kind: string // zipImport | pdfImport | imageLocalize | translate | siteCrawl | aiWriter | chapterGuide（插件可扩展）
  id: string
  title: string
  status: UserTaskStatus
  done: number
  failed: number
  total: number
  remaining: number
  link: string
  error: string
  created_at: string
  updated_at: string
  finished_at: string | null
}

export function userTaskKey(task: Pick<UserTask, 'kind' | 'id'>): string {
  return `${task.kind}:${task.id}`
}

export function userTaskTab(status: UserTaskStatus): UserTaskTab {
  if (status === 'done') return 'done'
  if (status === 'failed' || status === 'canceled') return 'failed'
  return 'active'
}

type Listener = (task: UserTask | null) => void // null 表示事件流已重连，需重新拉取
const listeners = new Set<Listener>()

// subscribeUserTasks 订阅任务变化，返回取消订阅函数。
export function subscribeUserTasks(fn: Listener): () => void {
  listeners.add(fn)
  return () => { listeners.delete(fn) }
}

// emitUserTask 由通知事件流调用：task 为变化的任务，null 表示重连后需要重新同步。
export function emitUserTask(task: UserTask | null) {
  listeners.forEach((fn) => fn(task))
}

// useActiveUserTasks 当前进行中的任务（导航栏的进行中标记使用）；enabled 为 false 时不拉取。
export function useActiveUserTasks(enabled: boolean): UserTask[] {
  const [tasks, setTasks] = useState<UserTask[]>([])
  useEffect(() => {
    if (!enabled) { setTasks([]); return }
    let alive = true
    const load = () => api<{ items: UserTask[] }>('/users/me/tasks', { params: { tab: 'active' } })
      .then((d) => { if (alive) setTasks(d.items || []) })
      .catch(() => {})
    load()
    const off = subscribeUserTasks((task) => {
      if (!task) { load(); return }
      setTasks((list) => {
        const rest = list.filter((x) => userTaskKey(x) !== userTaskKey(task))
        return userTaskTab(task.status) === 'active' ? [task, ...rest] : rest
      })
    })
    return () => { alive = false; off() }
  }, [enabled])
  return tasks
}
