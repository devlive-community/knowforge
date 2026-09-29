import { api } from './api'
import { subscribeUserTasks } from './user-tasks'

export type BackgroundTaskStatus = 'pending' | 'running' | 'retrying' | 'succeeded' | 'failed'

export interface BackgroundTask<TResult = unknown> {
  id: number
  type: string
  status: BackgroundTaskStatus
  attempts: number
  max_attempts: number
  last_error: string
  result?: TResult
}

export interface QueuedTask<TResult> {
  task: BackgroundTask<TResult>
  message: string
}

export function isQueuedTask<TResult>(value: TResult | QueuedTask<TResult>): value is QueuedTask<TResult> {
  return typeof value === 'object' && value !== null && 'task' in value
}

const CORE_TASK_KINDS = new Set(['zipImport', 'pdfImport', 'imageLocalize'])

// waitForTask 等待后台任务结束并返回结果：任务状态经通知事件流推送（见 lib/user-tasks），收到结束推送（或重连）后读取一次，不轮询。
export function waitForTask<TResult>(taskID: number): Promise<TResult> {
  return new Promise<TResult>((resolve, reject) => {
    let settled = false
    const check = async () => {
      if (settled) return
      try {
        const task = await api<BackgroundTask<TResult>>(`/tasks/${taskID}`)
        if (settled) return
        if (task.status === 'succeeded' && task.result !== undefined) { settled = true; off(); resolve(task.result) }
        else if (task.status === 'failed') { settled = true; off(); reject(new Error(task.last_error || '后台任务执行失败')) }
      } catch (err) {
        settled = true
        off()
        reject(err)
      }
    }
    const off = subscribeUserTasks((task) => {
      if (!task) { void check(); return }
      if (CORE_TASK_KINDS.has(task.kind) && task.id === String(taskID) && (task.status === 'done' || task.status === 'failed')) void check()
    })
    void check() // 订阅后先读一次，覆盖订阅前已结束的情况
  })
}
