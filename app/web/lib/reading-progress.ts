// 阅读进度：优先服务端存储（跨设备），未登录时回退 localStorage
import { api } from './api'

interface ProgressEntry {
  docId?: number
  docSlug: string
  docTitle: string
  chapterPrefix?: string
  /** 章节滚动百分比（0-100），用于精确续读定位 */
  scrollPercent?: number
  /** 本次活跃阅读秒数增量（服务端累加到该书 read_seconds） */
  secondsDelta?: number
  /** 服务端返回的累计阅读秒数（读取时可用） */
  readSeconds?: number
}

const LOCAL_KEY = 'knowforge_reading_progress'

function localRead(): Record<string, ProgressEntry> {
  if (typeof window === 'undefined') return {}
  try {
    return JSON.parse(localStorage.getItem(LOCAL_KEY) || '{}')
  } catch {
    return {}
  }
}

// save 记录阅读进度：登录用户写服务端（fire-and-forget），未登录写本地
export function saveReadingProgress(username: string, bookId: number, entry: ProgressEntry): void {
  if (typeof window === 'undefined') return
  if (!username) {
    // 游客仅本地记录最近章节与滚动位置，不累计阅读时长
    const map = localRead()
    const prev = map[String(bookId)]
    map[String(bookId)] = {
      docId: entry.docId,
      docSlug: entry.docSlug,
      docTitle: entry.docTitle,
      chapterPrefix: entry.chapterPrefix,
      scrollPercent: entry.scrollPercent ?? prev?.scrollPercent,
    }
    localStorage.setItem(LOCAL_KEY, JSON.stringify(map))
    return
  }
  const body: Record<string, unknown> = { doc_id: entry.docId, doc_slug: entry.docSlug, doc_title: entry.docTitle }
  if (entry.scrollPercent != null) body.scroll_percent = Math.round(entry.scrollPercent)
  if (entry.secondsDelta != null && entry.secondsDelta > 0) body.read_seconds_delta = Math.round(entry.secondsDelta)
  api(`/reading-progress/${bookId}`, { method: 'PUT', body }).catch((e) => {
    if (!(e as { status?: number })?.status) enqueueProgress(bookId, body) // 离线等网络错误：联网后补传
  })
}

// —— 离线时的进度：按书保存最后一次进度（阅读秒数累加），联网后（online 事件或下次打开页面）补传 ——

const QUEUE_KEY = 'knowforge_progress_queue'

function readQueue(): Record<string, Record<string, unknown>> {
  try {
    return JSON.parse(localStorage.getItem(QUEUE_KEY) || '{}')
  } catch {
    return {}
  }
}

function enqueueProgress(bookId: number, body: Record<string, unknown>) {
  try {
    const queue = readQueue()
    const prev = queue[String(bookId)]
    const seconds = Number(prev?.read_seconds_delta || 0) + Number(body.read_seconds_delta || 0)
    queue[String(bookId)] = { ...prev, ...body, ...(seconds > 0 ? { read_seconds_delta: seconds } : {}) }
    localStorage.setItem(QUEUE_KEY, JSON.stringify(queue))
  } catch {
    // 存储不可用时放弃
  }
}

let flushing = false

// flushProgressQueue 补传离线期间的阅读进度（成功或被服务端拒绝的条目移出队列，网络仍不可用时保留）。
export async function flushProgressQueue(): Promise<void> {
  if (typeof window === 'undefined' || flushing || !navigator.onLine) return
  flushing = true
  try {
    for (const [bookId, body] of Object.entries(readQueue())) {
      try {
        await api(`/reading-progress/${bookId}`, { method: 'PUT', body })
      } catch (e) {
        if (!(e as { status?: number })?.status) break // 仍然离线
      }
      const queue = readQueue()
      delete queue[bookId]
      localStorage.setItem(QUEUE_KEY, JSON.stringify(queue))
    }
  } finally {
    flushing = false
  }
}

// get 读取进度：登录走服务端（null 视为无），未登录读本地
export async function getReadingProgress(username: string, bookId: number): Promise<ProgressEntry | null> {
  if (!username) {
    return localRead()[String(bookId)] || null
  }
  try {
    const data = await api<Record<string, any> | null>(`/reading-progress/${bookId}`)
    if (data && data.doc_slug) {
      return {
        docId: data.doc_id,
        docSlug: data.doc_slug,
        docTitle: data.doc_title,
        scrollPercent: data.scroll_percent ?? 0,
        readSeconds: data.read_seconds ?? 0,
      }
    }
    return null
  } catch {
    return null
  }
}
