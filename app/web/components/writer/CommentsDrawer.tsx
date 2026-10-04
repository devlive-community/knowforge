import { useCallback, useEffect, useState } from 'react'
import { api, formatDate } from '@/lib/api'
import { locateAnchor, type TextAnchor } from '@/lib/anchor'
import { useTranslation } from '@/lib/i18n'
import { Button, EmptyState, Loading, SegmentedTabs, Textarea, Tooltip, useFeedback } from '@/components/ui'
import { CollabAvatar, type CollabUser } from './collab'

export type CommentsTab = 'open' | 'resolved'

interface WriterComment {
  id: number
  document_id: number
  user_id: number
  parent_id: number | null
  quote: string
  prefix: string
  suffix: string
  quote_offset: number
  content: string
  resolved: boolean
  resolved_at: string | null
  created_at: string
  updated_at: string
  user: CollabUser
  replies?: WriterComment[]
}

interface Props {
  bookId: number
  docId: number | null
  tab: CommentsTab
  onNavigate: (tab: CommentsTab | null) => void
  pending: TextAnchor | null
  onClearPending: () => void
  content: string
  onSelect: (start: number, end: number) => void
  reloadKey: number
  onChanged: () => void
  currentUserId: number
  canManage: boolean
}

// CommentsDrawer 写作台右侧「批注」：当前章节的讨论串（未解决 / 已解决，?comments=open|resolved），
// 新建批注（锚定选中的原文）、回复、修改、标记解决与删除；点击原文在正文中选中对应位置。
export default function CommentsDrawer({ bookId, docId, tab, onNavigate, pending, onClearPending, content, onSelect, reloadKey, onChanged, currentUserId, canManage }: Props) {
  const { t } = useTranslation()
  const { confirmAction, showToast } = useFeedback()
  const [data, setData] = useState<{ items: WriterComment[]; open: number; resolved: number } | null>(null)
  const [draft, setDraft] = useState('')
  const [posting, setPosting] = useState(false)
  const [busy, setBusy] = useState<string | null>(null) // `${id}:${action}`
  const [replyFor, setReplyFor] = useState<number | null>(null)
  const [replyText, setReplyText] = useState('')
  const [editing, setEditing] = useState<{ id: number; text: string } | null>(null)

  const load = useCallback(async () => {
    if (!docId) { setData({ items: [], open: 0, resolved: 0 }); return }
    try {
      setData(await api(`/documents/${docId}/writer-comments`, { params: { status: tab } }))
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
  }, [docId, tab, showToast])

  useEffect(() => { setData(null); void load() }, [load])
  useEffect(() => { if (reloadKey) void load() }, [reloadKey]) // eslint-disable-line react-hooks/exhaustive-deps

  async function run(key: string, fn: () => Promise<unknown>) {
    setBusy(key)
    try {
      await fn()
      await load()
      onChanged()
      return true
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
      return false
    } finally {
      setBusy(null)
    }
  }

  async function post() {
    if (!docId || !pending || !draft.trim()) return
    setPosting(true)
    const done = await run('new', () => api(`/documents/${docId}/writer-comments`, { method: 'POST', body: { ...pending, content: draft } }))
    setPosting(false)
    if (done) {
      setDraft('')
      onClearPending()
      if (tab !== 'open') onNavigate('open')
    }
  }

  async function reply(root: WriterComment) {
    if (!replyText.trim()) return
    const done = await run(`${root.id}:reply`, () => api(`/documents/${root.document_id}/writer-comments`, { method: 'POST', body: { content: replyText, parent_id: root.id } }))
    if (done) { setReplyText(''); setReplyFor(null) }
  }

  async function saveEdit() {
    if (!editing || !editing.text.trim()) return
    const done = await run(`${editing.id}:edit`, () => api(`/writer-comments/${editing.id}`, { method: 'PUT', body: { content: editing.text } }))
    if (done) setEditing(null)
  }

  async function remove(cm: WriterComment) {
    if (!(await confirmAction({ title: t('writer.comments.deleteTitle'), message: t(cm.parent_id ? 'writer.comments.deleteReply' : 'writer.comments.deleteThread'), confirmLabel: t('writer.comments.delete'), danger: true }))) return
    await run(`${cm.id}:delete`, () => api(`/writer-comments/${cm.id}`, { method: 'DELETE' }))
  }

  const actions = (cm: WriterComment) => (
    <span className="ml-auto flex items-center gap-0.5">
      {cm.user_id === currentUserId && (
        <Tooltip content={t('writer.comments.edit')}>
          <Button size="sm" variant="ghost" className="!h-7 !px-2 text-slate-400" aria-label={t('writer.comments.edit')} onClick={() => setEditing({ id: cm.id, text: cm.content })}>
            <i className="fa-regular fa-pen-to-square" aria-hidden="true" />
          </Button>
        </Tooltip>
      )}
      {(cm.user_id === currentUserId || canManage) && (
        <Tooltip content={t('writer.comments.delete')}>
          <Button size="sm" variant="ghost" className="!h-7 !px-2 text-slate-400 hover:text-rose-600" aria-label={t('writer.comments.delete')} loading={busy === `${cm.id}:delete`} onClick={() => void remove(cm)}>
            <i className="fa-regular fa-trash-can" aria-hidden="true" />
          </Button>
        </Tooltip>
      )}
    </span>
  )

  const body = (cm: WriterComment) => editing?.id === cm.id ? (
    <div className="mt-1.5 space-y-2">
      <Textarea value={editing.text} onChange={(e) => setEditing({ id: cm.id, text: e.target.value })} className="min-h-[72px] text-sm" />
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={() => setEditing(null)}>{t('writer.comments.cancel')}</Button>
        <Button size="sm" loading={busy === `${cm.id}:edit`} onClick={() => void saveEdit()}>{t('writer.comments.save')}</Button>
      </div>
    </div>
  ) : <p className="mt-1 whitespace-pre-wrap break-words text-sm text-slate-700">{cm.content}</p>

  const header = (cm: WriterComment) => (
    <div className="flex items-center gap-2">
      <CollabAvatar user={cm.user} />
      <div className="min-w-0">
        <div className="truncate text-xs font-medium text-slate-800">{cm.user.display_name || cm.user.username}</div>
        <div className="text-[11px] text-slate-400">{formatDate(cm.created_at)}{cm.updated_at !== cm.created_at ? ` · ${t('writer.comments.edited')}` : ''}</div>
      </div>
      {actions(cm)}
    </div>
  )

  return (
    <>
      <div className="fixed inset-0 z-[60] bg-black/30 lg:hidden" onClick={() => onNavigate(null)} />
      <aside className="fixed inset-y-0 right-0 z-[61] flex w-full max-w-md flex-col border-l border-slate-200 bg-white shadow-2xl lg:w-96 2xl:w-[28rem] 2xl:max-w-none" aria-label={t('writer.comments.title')} data-testid="comments-drawer">
        <div className="flex shrink-0 items-center gap-3 border-b border-slate-200 px-4 py-3">
          <i className="fa-regular fa-comment-dots text-primary-500" aria-hidden="true" />
          <span className="font-semibold text-slate-900">{t('writer.comments.title')}</span>
          <Button size="sm" variant="ghost" className="ml-auto !px-2" aria-label={t('writer.comments.close')} onClick={() => onNavigate(null)}>
            <i className="fa-solid fa-xmark" aria-hidden="true" />
          </Button>
        </div>
        <div className="shrink-0 px-4 pt-3">
          <SegmentedTabs fullWidth size="sm" value={tab} ariaLabel={t('writer.comments.title')} onChange={(v) => onNavigate(v as CommentsTab)}
            items={[
              { value: 'open', label: `${t('writer.comments.open')}${data ? ` ${data.open}` : ''}` },
              { value: 'resolved', label: `${t('writer.comments.resolved')}${data ? ` ${data.resolved}` : ''}` },
            ]} />
        </div>

        {pending && docId && (
          <div className="shrink-0 border-b border-slate-100 px-4 py-3" data-testid="comment-composer">
            <div className="line-clamp-3 border-l-2 border-amber-300 bg-amber-50/60 px-2.5 py-1.5 text-xs text-slate-600">{pending.quote}</div>
            <Textarea autoFocus value={draft} onChange={(e) => setDraft(e.target.value)} placeholder={t('writer.comments.placeholder')} className="mt-2 min-h-[80px] text-sm"
              onKeyDown={(e) => { if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') void post() }} />
            <div className="mt-2 flex justify-end gap-2">
              <Button size="sm" variant="ghost" onClick={() => { setDraft(''); onClearPending() }}>{t('writer.comments.cancel')}</Button>
              <Button size="sm" loading={posting} disabled={!draft.trim()} onClick={() => void post()} data-testid="comment-post">{t('writer.comments.post')}</Button>
            </div>
          </div>
        )}

        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
          {!docId ? <EmptyState>{t('writer.comments.noChapter')}</EmptyState> : !data ? <Loading className="py-10" /> : data.items.length === 0 ? (
            <div className="py-6 text-center text-sm text-slate-400">
              <p>{t(tab === 'open' ? 'writer.comments.emptyOpen' : 'writer.comments.emptyResolved')}</p>
              {tab === 'open' && !pending && <p className="mt-2 text-xs">{t('writer.comments.howTo')}</p>}
            </div>
          ) : (
            <ul className="space-y-3">
              {data.items.map((root) => {
                const loc = locateAnchor(content, root)
                return (
                  <li key={root.id} className="rounded-xl border border-slate-200 p-3" data-testid="comment-thread">
                    {root.quote && (
                      <button type="button" onClick={() => loc && onSelect(loc.start, loc.end)} disabled={!loc}
                        className={`mb-2 block w-full border-l-2 px-2.5 py-1 text-left text-xs ${loc ? 'border-amber-300 bg-amber-50/60 text-slate-600 hover:bg-amber-100/60' : 'border-slate-200 bg-slate-50 text-slate-400'}`}>
                        <span className="line-clamp-2">{root.quote}</span>
                        {!loc && <span className="mt-0.5 block text-[11px]">{t('writer.comments.orphaned')}</span>}
                      </button>
                    )}
                    {header(root)}
                    {body(root)}
                    {(root.replies || []).map((r) => (
                      <div key={r.id} className="mt-3 border-l-2 border-slate-100 pl-3">
                        {header(r)}
                        {body(r)}
                      </div>
                    ))}
                    {replyFor === root.id ? (
                      <div className="mt-3 space-y-2">
                        <Textarea autoFocus value={replyText} onChange={(e) => setReplyText(e.target.value)} placeholder={t('writer.comments.replyPlaceholder')} className="min-h-[64px] text-sm"
                          onKeyDown={(e) => { if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') void reply(root) }} />
                        <div className="flex justify-end gap-2">
                          <Button size="sm" variant="ghost" onClick={() => { setReplyFor(null); setReplyText('') }}>{t('writer.comments.cancel')}</Button>
                          <Button size="sm" loading={busy === `${root.id}:reply`} disabled={!replyText.trim()} onClick={() => void reply(root)}>{t('writer.comments.reply')}</Button>
                        </div>
                      </div>
                    ) : (
                      <div className="mt-2 flex items-center gap-1 border-t border-slate-100 pt-2">
                        <Button size="sm" variant="ghost" className="!h-7" onClick={() => { setReplyFor(root.id); setReplyText('') }}>{t('writer.comments.reply')}</Button>
                        <Button size="sm" variant="ghost" className="!h-7" loading={busy === `${root.id}:resolve`} data-testid="comment-resolve"
                          onClick={() => void run(`${root.id}:resolve`, () => api(`/writer-comments/${root.id}/resolve`, { method: 'POST', body: { resolved: !root.resolved } }))}>
                          <i className={`fa-solid ${root.resolved ? 'fa-rotate-left' : 'fa-check'}`} aria-hidden="true" /> {t(root.resolved ? 'writer.comments.reopen' : 'writer.comments.resolve')}
                        </Button>
                      </div>
                    )}
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      </aside>
    </>
  )
}
