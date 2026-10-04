import { API_BASE, api, requestHeaders, runStepUp } from '@/lib/api'

// openTicketedStream 连接需要登录的事件流：先换取短时事件流凭证（POST /stream-tickets）再建立 EventSource，
// 登录令牌不出现在 URL 中。连接被拒或断开后浏览器放弃重连时（如凭证过期），换新凭证按退避重连；调用 stop 后不再重连。
// setup 在每次（重新）建立连接后调用，用于注册事件处理；返回的函数用于关闭。
export function openTicketedStream(path: string, setup: (source: EventSource, stop: () => void) => void): () => void {
  let stopped = false
  let source: EventSource | null = null
  let retry = 0
  let timer: ReturnType<typeof setTimeout> | undefined

  const stop = () => {
    stopped = true
    if (timer) clearTimeout(timer)
    source?.close()
  }
  const schedule = () => {
    if (stopped) return
    source?.close()
    timer = setTimeout(() => void connect(), Math.min(30000, 1000 * 2 ** retry++))
  }
  const connect = async () => {
    if (stopped) return
    let ticket: string
    try {
      ticket = (await api<{ ticket: string }>('/stream-tickets', { method: 'POST' })).ticket
    } catch {
      schedule()
      return
    }
    if (stopped) return
    const es = new EventSource(`${API_BASE}/api/v1${path}${path.includes('?') ? '&' : '?'}ticket=${encodeURIComponent(ticket)}`)
    source = es
    es.addEventListener('open', () => { retry = 0 })
    es.addEventListener('error', () => { if (es.readyState === EventSource.CLOSED) schedule() })
    setup(es, stop)
  }
  void connect()
  return stop
}

// postEventStream 以 POST 提交并读取服务端在同一请求中推送的事件流（text/event-stream），逐条回调 onEvent；
// body 为 FormData 时按 multipart 上传（如备份文件），否则按 JSON 提交。
// 服务端在开始推送前校验失败时返回 JSON 错误并抛出；需要二次认证时交给全局处理器验证后重试一次。
export async function postEventStream(path: string, body: unknown, onEvent: (name: string, data: any) => void): Promise<void> {
  const form = typeof FormData !== 'undefined' && body instanceof FormData
  const send = () => fetch(`${API_BASE}/api/v1${path}`, { method: 'POST', headers: requestHeaders(!form), body: form ? body : JSON.stringify(body) })
  let res = await send()
  const isStream = (r: Response) => (r.headers.get('Content-Type') || '').startsWith('text/event-stream')
  if (!isStream(res)) {
    let payload = await res.json().catch(() => ({}))
    if (res.status === 403 && payload?.code === 'TWO_FACTOR_REQUIRED' && await runStepUp()) {
      res = await send()
      payload = isStream(res) ? null : await res.json().catch(() => ({}))
    }
    if (!isStream(res)) {
      const err = new Error(payload?.message || `请求失败 (${res.status})`) as Error & { status?: number; code?: string }
      err.status = res.status
      err.code = payload?.code
      throw err
    }
  }
  const reader = res.body!.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  const flush = (block: string) => {
    let name = 'message'
    const data: string[] = []
    for (const line of block.split('\n')) {
      if (line.startsWith('event:')) name = line.slice(6).trim()
      else if (line.startsWith('data:')) data.push(line.slice(5).trimStart())
    }
    if (data.length === 0) return // 心跳等注释行
    try { onEvent(name, JSON.parse(data.join('\n'))) } catch { onEvent(name, data.join('\n')) }
  }
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    let idx
    while ((idx = buffer.indexOf('\n\n')) >= 0) {
      flush(buffer.slice(0, idx))
      buffer = buffer.slice(idx + 2)
    }
  }
  if (buffer.trim()) flush(buffer)
}
