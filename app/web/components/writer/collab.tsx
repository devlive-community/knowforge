import { useEffect, useMemo, useRef, useState } from 'react'
import { api, setRequestHeader } from '@/lib/api'
import { openTicketedStream } from '@/lib/event-stream'
import { useTranslation } from '@/lib/i18n'
import { resolveMediaUrl } from '@/lib/media'
import type { ConflictChoice, MergeResult } from '@/lib/merge'
import { Button, Modal, SegmentedTabs, Tooltip } from '@/components/ui'

// —— 协作写作（写作台）：在线状态、他人保存与目录变化的实时事件、保存冲突的合并对话框 ——

export interface CollabUser { id: number; username: string; display_name: string; avatar: string }
export interface Presence { conn_id: string; user: CollabUser; doc_id: number; dirty: boolean; since: string }
export interface DocSavedEvent { doc_id: number; title: string; content_hash: string; by: CollabUser; at: string; origin: string }

interface Options {
  bookId: number | null
  docId: number | null
  dirty: boolean
  onDocSaved: (ev: DocSavedEvent) => void
  onTreeChanged: () => void
}

// useWriterCollab 连接本书的写作台事件流：登记在线、同步自己正在编辑的章节与是否有未保存修改，
// 接收他人的在线状态、章节保存与目录变化（自己发起的操作由连接 ID 识别后忽略）。
export function useWriterCollab({ bookId, docId, dirty, onDocSaved, onTreeChanged }: Options): { connId: string; others: Presence[] } {
  const [connId, setConnId] = useState('')
  const [presence, setPresence] = useState<Presence[]>([])
  const handlers = useRef({ onDocSaved, onTreeChanged })
  handlers.current = { onDocSaved, onTreeChanged }
  const connRef = useRef('')
  const docRef = useRef(docId)
  docRef.current = docId

  useEffect(() => {
    if (!bookId) return
    const stop = openTicketedStream(`/books/${bookId}/collab/stream?doc_id=${docRef.current || 0}`, (es) => {
      const parse = (e: MessageEvent) => { try { return JSON.parse(e.data) } catch { return null } }
      es.addEventListener('hello', (e) => {
        const d = parse(e as MessageEvent)
        if (!d) return
        connRef.current = d.conn_id
        setConnId(d.conn_id)
        setRequestHeader('X-Collab-Conn', d.conn_id)
        setPresence(d.presence || [])
      })
      es.addEventListener('presence', (e) => { const d = parse(e as MessageEvent); if (d) setPresence(d.presence || []) })
      es.addEventListener('doc.saved', (e) => {
        const d = parse(e as MessageEvent) as DocSavedEvent | null
        if (d && d.origin !== connRef.current) handlers.current.onDocSaved(d)
      })
      es.addEventListener('tree', (e) => {
        const d = parse(e as MessageEvent)
        if (d && d.origin !== connRef.current) handlers.current.onTreeChanged()
      })
    })
    return () => {
      stop()
      connRef.current = ''
      setConnId('')
      setRequestHeader('X-Collab-Conn', null)
    }
  }, [bookId])

  // 切换章节或修改状态变化时同步在线状态（短暂防抖，避免连续输入时频繁请求）
  useEffect(() => {
    if (!bookId || !connId) return
    const timer = setTimeout(() => {
      api(`/books/${bookId}/collab/presence`, { method: 'POST', body: { conn_id: connId, doc_id: docId || 0, dirty } }).catch(() => {})
    }, 300)
    return () => clearTimeout(timer)
  }, [bookId, connId, docId, dirty])

  const others = useMemo(() => presence.filter((p) => p.conn_id !== connId), [presence, connId])
  return { connId, others }
}

// uniqueUsers 同一用户多个窗口只显示一次（任一窗口有未保存修改即视为编辑中）。
export function uniqueByUser(list: Presence[]): Presence[] {
  const map = new Map<number, Presence>()
  for (const p of list) {
    const prev = map.get(p.user.id)
    map.set(p.user.id, prev ? { ...prev, dirty: prev.dirty || p.dirty } : p)
  }
  return Array.from(map.values())
}

export function CollabAvatar({ user, size = 'md', ring }: { user: CollabUser; size?: 'sm' | 'md'; ring?: boolean }) {
  const cls = size === 'sm' ? 'h-4 w-4 text-[9px]' : 'h-7 w-7 text-xs'
  const ringCls = ring ? 'ring-2 ring-amber-400' : 'ring-2 ring-white'
  return user.avatar
    ? <img src={resolveMediaUrl(user.avatar)} alt="" className={`${cls} ${ringCls} shrink-0 rounded-full object-cover`} />
    : <span className={`${cls} ${ringCls} flex shrink-0 items-center justify-center rounded-full bg-primary-500 font-semibold text-white`}>{(user.display_name || user.username)[0]?.toUpperCase()}</span>
}

// CollabAvatars 写作台顶栏：同时在编辑本书的其他人（悬停显示各自所在章节）。
export function CollabAvatars({ others, docTitle }: { others: Presence[]; docTitle: (id: number) => string }) {
  const { t } = useTranslation()
  const users = uniqueByUser(others)
  if (users.length === 0) return null
  const tip = (
    <div className="space-y-1 text-left">
      {others.map((p) => (
        <div key={p.conn_id}>
          {t(p.dirty ? 'writer.collab.editingDirty' : 'writer.collab.editing', { name: p.user.display_name || p.user.username, chapter: p.doc_id ? docTitle(p.doc_id) : t('writer.collab.noChapter') })}
        </div>
      ))}
    </div>
  )
  return (
    <Tooltip content={tip}>
      <span className="flex items-center" data-testid="collab-avatars" aria-label={t('writer.collab.online', { n: users.length })}>
        <span className="flex -space-x-2">
          {users.slice(0, 4).map((p) => <CollabAvatar key={p.user.id} user={p.user} ring={p.dirty} />)}
        </span>
        {users.length > 4 && <span className="ml-1 text-xs text-slate-500">+{users.length - 4}</span>}
      </span>
    </Tooltip>
  )
}

// SameChapterBanner 当前章节还有其他人打开时的提示；他人在此期间保存过时提示保存时会自动合并。
export function SameChapterBanner({ others, remoteSave }: { others: Presence[]; remoteSave: DocSavedEvent | null }) {
  const { t } = useTranslation()
  const users = uniqueByUser(others)
  if (users.length === 0 && !remoteSave) return null
  const names = users.map((p) => p.user.display_name || p.user.username).join('、')
  const anyDirty = users.some((p) => p.dirty)
  return (
    <div className={`mt-3 flex items-center gap-2 rounded-lg border px-4 py-2 text-sm ${remoteSave ? 'border-amber-200 bg-amber-50 text-amber-800' : 'border-sky-200 bg-sky-50 text-sky-800'}`} data-testid="collab-banner">
      <i className={`fa-solid ${remoteSave ? 'fa-code-merge' : 'fa-user-pen'}`} aria-hidden="true" />
      <span className="min-w-0 flex-1">
        {remoteSave
          ? t('writer.collab.remoteSaved', { name: remoteSave.by.display_name || remoteSave.by.username })
          : t(anyDirty ? 'writer.collab.sameChapterDirty' : 'writer.collab.sameChapter', { names })}
      </span>
    </div>
  )
}

export interface ConflictState {
  result: MergeResult
  titles: { base: string; mine: string; theirs: string } | null // 标题冲突时
  savedBy?: CollabUser
  savedAt?: string
}

// ConflictDialog 保存冲突：逐块选择保留自己的、采用对方的或两者都保留；也可以放弃自己的修改改用对方版本。
export function ConflictDialog({ state, onResolve, onUseTheirs, onCancel }: {
  state: ConflictState
  onResolve: (choices: ConflictChoice[], title: 'mine' | 'theirs') => void
  onUseTheirs: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const conflicts = state.result.chunks.filter((c) => c.type === 'conflict')
  const [choices, setChoices] = useState<ConflictChoice[]>(() => conflicts.map(() => 'mine'))
  const [titleChoice, setTitleChoice] = useState<'mine' | 'theirs'>('mine')
  const name = state.savedBy ? (state.savedBy.display_name || state.savedBy.username) : t('writer.collab.someone')
  const lines = (list: string[]) => list.length === 0
    ? <span className="italic text-slate-400">{t('writer.collab.emptyBlock')}</span>
    : list.join('\n')

  return (
    <Modal open onClose={onCancel} className="max-w-4xl" title={t('writer.collab.conflictTitle')}
      footer={(
        <div className="flex w-full flex-wrap items-center justify-between gap-2">
          <Button variant="ghost" className="text-rose-600 hover:bg-rose-50" onClick={onUseTheirs} data-testid="conflict-use-theirs">{t('writer.collab.useTheirs')}</Button>
          <div className="flex gap-2">
            <Button variant="outline" onClick={onCancel}>{t('writer.collab.cancel')}</Button>
            <Button onClick={() => onResolve(choices, titleChoice)} data-testid="conflict-save">{t('writer.collab.saveMerged')}</Button>
          </div>
        </div>
      )}>
      <div className="space-y-4" data-testid="conflict-dialog">
        <p className="text-sm text-slate-600">{t('writer.collab.conflictHint', { name, n: conflicts.length + (state.titles ? 1 : 0) })}</p>
        {state.titles && (
          <div className="rounded-xl border border-slate-200 p-3">
            <div className="mb-2 flex items-center justify-between gap-2">
              <span className="text-xs font-medium text-slate-500">{t('writer.collab.titleConflict')}</span>
              <SegmentedTabs size="sm" value={titleChoice} ariaLabel={t('writer.collab.titleConflict')} onChange={(v) => setTitleChoice(v as 'mine' | 'theirs')}
                items={[{ value: 'mine', label: t('writer.collab.keepMine') }, { value: 'theirs', label: t('writer.collab.takeTheirs') }]} />
            </div>
            <div className="grid gap-2 text-sm sm:grid-cols-2">
              <div className="rounded-lg bg-emerald-50 px-3 py-2 text-emerald-900">{state.titles.mine}</div>
              <div className="rounded-lg bg-sky-50 px-3 py-2 text-sky-900">{state.titles.theirs}</div>
            </div>
          </div>
        )}
        {conflicts.map((c, i) => c.type === 'conflict' && (
          <div key={i} className="rounded-xl border border-slate-200 p-3" data-testid="conflict-block">
            <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
              <span className="text-xs font-medium text-slate-500">{t('writer.collab.block', { n: i + 1 })}</span>
              <SegmentedTabs size="sm" value={choices[i]} ariaLabel={t('writer.collab.block', { n: i + 1 })}
                onChange={(v) => setChoices((list) => list.map((x, j) => (j === i ? v as ConflictChoice : x)))}
                items={(['mine', 'theirs', 'both'] as const).map((choice) => ({ value: choice, label: t(`writer.collab.choice.${choice}`) }))} />
            </div>
            <div className="grid gap-2 sm:grid-cols-2">
              <div>
                <div className="mb-1 text-[11px] font-medium text-emerald-700">{t('writer.collab.mine')}</div>
                <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-emerald-50 px-3 py-2 text-xs text-emerald-900">{lines(c.mine)}</pre>
              </div>
              <div>
                <div className="mb-1 text-[11px] font-medium text-sky-700">{t('writer.collab.theirs', { name })}</div>
                <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-sky-50 px-3 py-2 text-xs text-sky-900">{lines(c.theirs)}</pre>
              </div>
            </div>
          </div>
        ))}
        <p className="text-xs text-slate-400">{t('writer.collab.conflictNote')}</p>
      </div>
    </Modal>
  )
}
