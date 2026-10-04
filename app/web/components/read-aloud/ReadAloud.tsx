import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'
import { useRouter } from 'next/router'
import { API_BASE, api, requestHeaders, type ApiError } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { collectSegments, type ReadAloudSegment } from '@/lib/read-aloud'
import { Button, Select, Switch, Tooltip, useFeedback } from '@/components/ui'

interface Status {
  available: boolean
  voices?: string[]
  default_voice?: string
  used?: number
  limit?: number
}

interface Props {
  docId: number
  title: string
  contentRef: RefObject<HTMLDivElement>
  loggedIn: boolean
  nextHref: string | null // 读完后自动进入的下一章（已带 listen=1）；没有下一章或下一章未解锁为 null
}

type PlayerStatus = 'loading' | 'playing' | 'paused' | 'ended' | 'error'

const RATES = ['0.75', '1', '1.25', '1.5', '2']
const ACTIVE = ['bg-primary-50', 'ring-4', 'ring-primary-50', 'rounded-sm']

function readPref(key: string): string | null {
  try { return localStorage.getItem(key) } catch { return null }
}
function writePref(key: string, value: string) {
  try { localStorage.setItem(key, value) } catch { /* 忽略 */ }
}

// fetchAudio 合成（或取缓存的）一段朗读音频，返回可播放的地址与计入后的本月用量。
async function fetchAudio(docId: number, text: string, voice: string): Promise<{ url: string; used: number; limit: number }> {
  const res = await fetch(`${API_BASE}/api/v1/read-aloud/docs/${docId}/speech`, {
    method: 'POST', headers: requestHeaders(true), body: JSON.stringify({ text, voice }),
  })
  if (!res.ok) {
    const payload = await res.json().catch(() => ({}))
    const err = new Error(payload.message || `${res.status}`) as ApiError
    err.status = res.status
    throw err
  }
  const blob = await res.blob()
  return { url: URL.createObjectURL(blob), used: Number(res.headers.get('X-Quota-Used') || 0), limit: Number(res.headers.get('X-Quota-Limit') || -1) }
}

// ReadAloud 阅读页「朗读」：按正文段落逐段合成并播放，高亮当前段落；可调语速、切换音色，读完自动进入下一章。
// 由 ?listen=1 进入的章节自动开始播放（上一章读完后跳转过来）。
export default function ReadAloud({ docId, title, contentRef, loggedIn, nextHref }: Props) {
  const { t } = useTranslation()
  const router = useRouter()
  const { showToast } = useFeedback()
  const [open, setOpen] = useState(false)
  const [starting, setStarting] = useState(false)
  const [status, setStatus] = useState<PlayerStatus>('loading')
  const [error, setError] = useState('')
  const [index, setIndex] = useState(0)
  const [total, setTotal] = useState(0)
  const [voices, setVoices] = useState<string[]>([])
  const [voice, setVoice] = useState('')
  const [rate, setRate] = useState('1')
  const [continueNext, setContinueNext] = useState(true)
  const [usage, setUsage] = useState<{ used: number; limit: number } | null>(null)

  const audioRef = useRef<HTMLAudioElement | null>(null)
  const segsRef = useRef<ReadAloudSegment[]>([])
  const idxRef = useRef(0)
  const genRef = useRef(0) // 每次跳转段落递增，丢弃过期的异步结果
  const cacheRef = useRef(new Map<string, Promise<string | null>>())
  const urlsRef = useRef<string[]>([])
  const voiceRef = useRef('')
  const rateRef = useRef(1)
  const continueRef = useRef(true)
  const nextRef = useRef(nextHref)
  nextRef.current = nextHref

  const highlight = useCallback((i: number) => {
    segsRef.current.forEach((s) => s.el?.classList.remove(...ACTIVE))
    const el = segsRef.current[i]?.el
    if (!el) return
    el.classList.add(...ACTIVE)
    const r = el.getBoundingClientRect()
    if (r.top < 80 || r.bottom > window.innerHeight - 160) el.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }, [])

  // load 取第 i 段的音频（同一音色只请求一次）；不属于本章的段落（排版与原文差异过大）返回 null 跳过
  const load = useCallback((i: number): Promise<string | null> => {
    const seg = segsRef.current[i]
    if (!seg) return Promise.resolve(null)
    const key = `${voiceRef.current}|${i}`
    let p = cacheRef.current.get(key)
    if (!p) {
      p = fetchAudio(docId, seg.text, voiceRef.current).then((r) => {
        urlsRef.current.push(r.url)
        setUsage({ used: r.used, limit: r.limit })
        return r.url
      }).catch((e: ApiError) => {
        cacheRef.current.delete(key)
        if (e.status === 400) return null
        throw e
      })
      cacheRef.current.set(key, p)
    }
    return p
  }, [docId])

  const finish = useCallback(() => {
    highlight(-1)
    const next = nextRef.current
    if (continueRef.current && next) {
      void router.push(next)
      return
    }
    setStatus('ended')
  }, [highlight, router])

  const playAt = useCallback(async (i: number) => {
    const gen = ++genRef.current
    const audio = audioRef.current
    if (!audio) return
    audio.pause()
    if (i >= segsRef.current.length) { finish(); return }
    idxRef.current = i
    setIndex(i)
    setError('')
    highlight(i)
    setStatus('loading')
    try {
      const url = await load(i)
      if (gen !== genRef.current) return
      if (!url) { void playAt(i + 1); return }
      audio.src = url
      audio.playbackRate = rateRef.current
      void load(i + 1).catch(() => {}) // 预取下一段，减少段落间的停顿
      try {
        await audio.play()
        if (gen === genRef.current) setStatus('playing')
      } catch {
        if (gen === genRef.current) setStatus('paused') // 浏览器拦截了自动播放：等读者点播放
      }
    } catch (e) {
      if (gen !== genRef.current) return
      setError((e as Error).message)
      setStatus('error')
    }
  }, [finish, highlight, load])

  const playRef = useRef(playAt)
  playRef.current = playAt

  const stop = useCallback(() => {
    genRef.current++
    audioRef.current?.pause()
    highlight(-1)
    urlsRef.current.forEach((u) => URL.revokeObjectURL(u))
    urlsRef.current = []
    cacheRef.current.clear()
  }, [highlight])

  useEffect(() => stop, [stop])

  const start = useCallback(async () => {
    if (!loggedIn) {
      void router.push(`/login?next=${encodeURIComponent(router.asPath)}`)
      return
    }
    if (!contentRef.current) return
    setStarting(true)
    try {
      const s = await api<Status>('/read-aloud/status')
      if (!s.available || !s.voices?.length) {
        showToast({ message: t('reader.readAloud.unavailable'), tone: 'info' })
        return
      }
      const saved = readPref('reader:read-aloud-voice')
      const v = saved && s.voices.includes(saved) ? saved : s.default_voice || s.voices[0]
      const savedRate = readPref('reader:read-aloud-rate')
      const r = savedRate && RATES.includes(savedRate) ? savedRate : '1'
      const cont = readPref('reader:read-aloud-continue') !== 'false'
      setVoices(s.voices)
      setVoice(v)
      setRate(r)
      setContinueNext(cont)
      voiceRef.current = v
      rateRef.current = Number(r)
      continueRef.current = cont
      setUsage({ used: s.used ?? 0, limit: s.limit ?? -1 })
      segsRef.current = collectSegments(contentRef.current, title)
      setTotal(segsRef.current.length)
      if (!audioRef.current) {
        const audio = new Audio()
        audio.addEventListener('ended', () => { void playRef.current(idxRef.current + 1) })
        audioRef.current = audio
      }
      setOpen(true)
      if (segsRef.current.length === 0) { setStatus('ended'); return }
      void playAt(0)
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setStarting(false)
    }
  }, [contentRef, loggedIn, playAt, router, showToast, t, title])

  // 上一章读完跳转过来（?listen=1）：自动开始，并去掉参数
  const autoStarted = useRef(false)
  useEffect(() => {
    if (!router.isReady || router.query.listen !== '1' || autoStarted.current) return
    autoStarted.current = true
    const { listen: _listen, ...rest } = router.query
    void router.replace({ pathname: router.pathname, query: rest }, undefined, { shallow: true, scroll: false })
    void start()
  }, [router, start])

  // 系统媒体控制（锁屏、耳机按键）
  useEffect(() => {
    if (!open || typeof navigator === 'undefined' || !('mediaSession' in navigator)) return
    const ms = navigator.mediaSession
    try {
      ms.metadata = new MediaMetadata({ title })
      ms.setActionHandler('play', () => { void audioRef.current?.play().then(() => setStatus('playing')) })
      ms.setActionHandler('pause', () => { audioRef.current?.pause(); setStatus('paused') })
      ms.setActionHandler('previoustrack', () => { void playAt(Math.max(0, idxRef.current - 1)) })
      ms.setActionHandler('nexttrack', () => { void playAt(idxRef.current + 1) })
    } catch { /* 部分浏览器不支持某些操作 */ }
    return () => {
      try {
        ms.metadata = null
        for (const a of ['play', 'pause', 'previoustrack', 'nexttrack'] as MediaSessionAction[]) ms.setActionHandler(a, null)
      } catch { /* 忽略 */ }
    }
  }, [open, playAt, title])

  function togglePlay() {
    const audio = audioRef.current
    if (!audio) return
    if (status === 'playing') {
      audio.pause()
      setStatus('paused')
    } else if (status === 'paused' && audio.src) {
      void audio.play().then(() => setStatus('playing')).catch(() => setStatus('paused'))
    } else {
      void playAt(status === 'ended' ? 0 : idxRef.current)
    }
  }

  function changeVoice(v: string) {
    setVoice(v)
    voiceRef.current = v
    writePref('reader:read-aloud-voice', v)
    if (status !== 'ended') void playAt(idxRef.current)
  }

  function changeRate(r: string) {
    setRate(r)
    rateRef.current = Number(r)
    writePref('reader:read-aloud-rate', r)
    if (audioRef.current) audioRef.current.playbackRate = Number(r)
  }

  function changeContinue(v: boolean) {
    setContinueNext(v)
    continueRef.current = v
    writePref('reader:read-aloud-continue', String(v))
  }

  function close() {
    stop()
    setOpen(false)
    setStatus('loading')
  }

  const current = segsRef.current[index]?.text || ''
  const busy = status === 'loading'

  return (
    <>
      <button type="button" onClick={() => (open ? close() : void start())} disabled={starting} data-testid="read-aloud-toggle"
        className="flex items-center gap-1 text-slate-500 transition-colors hover:text-primary-600 disabled:opacity-60">
        <i className={`fa-solid ${starting ? 'fa-spinner fa-spin' : open ? 'fa-circle-stop' : 'fa-headphones'} text-xs`} aria-hidden="true" />
        {open ? t('reader.readAloud.stop') : t('reader.readAloud.start')}
      </button>

      {open && (
        <div className="fixed inset-x-3 bottom-20 z-40 mx-auto max-w-xl rounded-2xl border border-slate-200 bg-white/95 p-3 shadow-xl backdrop-blur sm:inset-x-6"
          role="region" aria-label={t('reader.readAloud.player')} data-testid="read-aloud-player">
          <div className="flex items-center gap-2">
            <Tooltip content={t('reader.readAloud.prev')}>
              <Button size="sm" variant="ghost" className="!px-2" aria-label={t('reader.readAloud.prev')} disabled={index === 0 || busy} onClick={() => void playAt(index - 1)}>
                <i className="fa-solid fa-backward-step" aria-hidden="true" />
              </Button>
            </Tooltip>
            <Button size="sm" className="!h-9 !w-9 !rounded-full !px-0" loading={busy} aria-label={status === 'playing' ? t('reader.readAloud.pause') : t('reader.readAloud.play')}
              onClick={togglePlay} data-testid="read-aloud-play">
              {!busy && <i className={`fa-solid ${status === 'playing' ? 'fa-pause' : 'fa-play'}`} aria-hidden="true" />}
            </Button>
            <Tooltip content={t('reader.readAloud.next')}>
              <Button size="sm" variant="ghost" className="!px-2" aria-label={t('reader.readAloud.next')} disabled={index >= total - 1 || busy} onClick={() => void playAt(index + 1)}>
                <i className="fa-solid fa-forward-step" aria-hidden="true" />
              </Button>
            </Tooltip>
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm text-slate-700" data-testid="read-aloud-current">
                {status === 'ended' ? t('reader.readAloud.ended') : current}
              </div>
              <div className="text-[11px] tabular-nums text-slate-400" data-testid="read-aloud-progress">
                {t('reader.readAloud.progress', { current: Math.min(index + 1, total), total })}
              </div>
            </div>
            <Tooltip content={t('reader.readAloud.close')}>
              <Button size="sm" variant="ghost" className="!px-2" aria-label={t('reader.readAloud.close')} onClick={close}>
                <i className="fa-solid fa-xmark" aria-hidden="true" />
              </Button>
            </Tooltip>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-2 border-t border-slate-100 pt-2 text-xs text-slate-500">
            <Select size="sm" className="w-24" value={rate} onChange={changeRate} menuPlacement="top"
              options={RATES.map((r) => ({ value: r, label: `${r}×` }))} />
            {voices.length > 1 && (
              <Select size="sm" className="w-28" value={voice} onChange={changeVoice} menuPlacement="top"
                options={voices.map((v) => ({ value: v, label: v }))} />
            )}
            <label className="flex items-center gap-1.5">
              <Switch checked={continueNext} onChange={changeContinue} ariaLabel={t('reader.readAloud.continue')} />
              {t('reader.readAloud.continue')}
            </label>
            {usage && (
              <span className="ml-auto tabular-nums" data-testid="read-aloud-usage">
                {usage.limit < 0
                  ? t('reader.readAloud.usedUnlimited', { used: usage.used.toLocaleString() })
                  : t('reader.readAloud.used', { used: usage.used.toLocaleString(), limit: usage.limit.toLocaleString() })}
              </span>
            )}
          </div>
          {error && (
            <div className="mt-2 flex items-center gap-2 rounded-lg bg-rose-50 px-3 py-2 text-xs text-rose-600" data-testid="read-aloud-error">
              <span className="min-w-0 flex-1">{error}</span>
              <Button size="sm" variant="ghost" onClick={() => void playAt(index)}>{t('reader.readAloud.retry')}</Button>
            </div>
          )}
        </div>
      )}
    </>
  )
}
