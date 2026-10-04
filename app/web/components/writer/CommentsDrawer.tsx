import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent, type TextareaHTMLAttributes } from 'react'
import { api, formatDate } from '@/lib/api'
import { locateAnchor, type TextAnchor } from '@/lib/anchor'
import { useTranslation } from '@/lib/i18n'
import { Button, EmptyState, Loading, SegmentedTabs, Textarea, Tooltip, useFeedback } from '@/components/ui'
import { CollabAvatar, type CollabUser } from './collab'
import { suggestionStats, type WriterSuggestion } from './SuggestionReview'

export type CommentsTab = 'open' | 'resolved' | 'suggestions'

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
  canReview: boolean // 能编辑的人可以审阅修改建议
  suggestionCount: number
  suggestionsReload: number
  onReview: (s: WriterSuggestion) => void
}

// CommentsDrawer 写作台右侧「批注」：当前章节的讨论串（未解决 / 已解决，?comments=open|resolved），
// 新建批注（锚定选中的原文）、回复、修改、标记解决与删除；点击原文在正文中选中对应位置。
export default function CommentsDrawer({ bookId, docId, tab, onNavigate, pending, onClearPending, content, onSelect, reloadKey, onChanged, currentUserId, canManage, canReview, suggestionCount, suggestionsReload, onReview }: Props) {
  const { t } = useTranslation()
  const { confirmAction, showToast } = useFeedback()
  const [data, setData] = useState<{ items: WriterComment[]; open: number; resolved: number } | null>(null)
  const [draft, setDraft] = useState('')
  const [posting, setPosting] = useState(false)
  const [busy, setBusy] = useState<string | null>(null) // `${id}:${action}`
  const [replyFor, setReplyFor] = useState<number | null>(null)
  const [replyText, setReplyText] = useState('')
  const [editing, setEditing] = useState<{ id: number; text: string } | null>(null)
  const [members, setMembers] = useState<Member[]>([])
  useEffect(() => {
    api<{ items: Member[] }>(`/books/${bookId}/writer-members`).then((d) => setMembers(d.items || [])).catch(() => {})
  }, [bookId])
  const usernames = useMemo(() => new Set(members.map((m) => m.username)), [members])

  const load = useCallback(async () => {
    if (tab === 'suggestions') return
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
      <MentionTextarea members={members} value={editing.text} onValue={(v) => setEditing({ id: cm.id, text: v })} className="min-h-[72px] text-sm" />
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={() => setEditing(null)}>{t('writer.comments.cancel')}</Button>
        <Button size="sm" loading={busy === `${cm.id}:edit`} onClick={() => void saveEdit()}>{t('writer.comments.save')}</Button>
      </div>
    </div>
  ) : <p className="mt-1 whitespace-pre-wrap break-words text-sm text-slate-700">{withMentions(cm.content, usernames)}</p>

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
              { value: 'suggestions', label: `${t('writer.suggest.tab')}${suggestionCount ? ` ${suggestionCount}` : ''}` },
            ]} />
        </div>

        {pending && docId && tab !== 'suggestions' && (
          <div className="shrink-0 border-b border-slate-100 px-4 py-3" data-testid="comment-composer">
            <div className="line-clamp-3 border-l-2 border-amber-300 bg-amber-50/60 px-2.5 py-1.5 text-xs text-slate-600">{pending.quote}</div>
            <MentionTextarea autoFocus members={members} value={draft} onValue={setDraft} placeholder={t('writer.comments.placeholder')} className="mt-2 min-h-[80px] text-sm"
              onSubmit={() => void post()} />
            <div className="mt-2 flex justify-end gap-2">
              <Button size="sm" variant="ghost" onClick={() => { setDraft(''); onClearPending() }}>{t('writer.comments.cancel')}</Button>
              <Button size="sm" loading={posting} disabled={!draft.trim()} onClick={() => void post()} data-testid="comment-post">{t('writer.comments.post')}</Button>
            </div>
          </div>
        )}

        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
          {tab === 'suggestions' ? (
            <SuggestionsPanel docId={docId} reloadKey={suggestionsReload} currentUserId={currentUserId} canReview={canReview} onReview={onReview} onChanged={onChanged} />
          ) : !docId ? <EmptyState>{t('writer.comments.noChapter')}</EmptyState> : !data ? <Loading className="py-10" /> : data.items.length === 0 ? (
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
                        <MentionTextarea autoFocus members={members} value={replyText} onValue={setReplyText} placeholder={t('writer.comments.replyPlaceholder')} className="min-h-[64px] text-sm"
                          onSubmit={() => void reply(root)} />
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

interface Member extends CollabUser { role: string }

// withMentions 把 @用户名（写作成员）显示为高亮。
function withMentions(text: string, usernames: Set<string>) {
  const parts = text.split(/(@[A-Za-z0-9_-]{3,50})/g)
  return parts.map((part, i) => (part.startsWith('@') && usernames.has(part.slice(1))
    ? <span key={i} className="rounded bg-primary-50 px-0.5 font-medium text-primary-700">{part}</span>
    : part))
}

// MentionTextarea 批注输入框：输入 @ 时列出写作成员（作者与协作者），↑↓ 选择、Enter / Tab 插入；⌘/Ctrl + Enter 提交。
function MentionTextarea({ members, value, onValue, onSubmit, ...rest }: {
  members: Member[]
  value: string
  onValue: (v: string) => void
  onSubmit?: () => void
} & Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'value' | 'onChange' | 'onSubmit'>) {
  const { t } = useTranslation()
  const ref = useRef<HTMLTextAreaElement>(null)
  const [query, setQuery] = useState<{ text: string; start: number } | null>(null)
  const [index, setIndex] = useState(0)
  const options = useMemo(() => {
    if (!query) return []
    const q = query.text.toLowerCase()
    return members.filter((m) => m.username.toLowerCase().includes(q) || (m.display_name || '').toLowerCase().includes(q)).slice(0, 6)
  }, [members, query])

  function detect(text: string, caret: number) {
    const m = /(^|\s)@([A-Za-z0-9_-]*)$/.exec(text.slice(0, caret))
    setQuery(m ? { text: m[2], start: caret - m[2].length - 1 } : null)
    setIndex(0)
  }

  function pick(m: Member) {
    if (!query) return
    const el = ref.current
    const caret = el ? el.selectionStart : value.length
    const insert = `@${m.username} `
    const next = value.slice(0, query.start) + insert + value.slice(caret)
    onValue(next)
    setQuery(null)
    const pos = query.start + insert.length
    requestAnimationFrame(() => { el?.focus(); el?.setSelectionRange(pos, pos) })
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (query && options.length > 0) {
      if (e.key === 'ArrowDown') { e.preventDefault(); setIndex((i) => (i + 1) % options.length); return }
      if (e.key === 'ArrowUp') { e.preventDefault(); setIndex((i) => (i - 1 + options.length) % options.length); return }
      if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); pick(options[index]); return }
      if (e.key === 'Escape') { e.preventDefault(); setQuery(null); return }
    }
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') { e.preventDefault(); onSubmit?.() }
  }

  return (
    <div className="relative">
      <Textarea ref={ref} {...rest} value={value} onKeyDown={onKeyDown}
        onChange={(e) => { onValue(e.target.value); detect(e.target.value, e.target.selectionStart) }}
        onBlur={() => setTimeout(() => setQuery(null), 150)} />
      {query && options.length > 0 && (
        <ul role="listbox" aria-label={t('writer.comments.mention')} className="absolute left-0 right-0 top-full z-10 mt-1 overflow-hidden rounded-lg border border-slate-200 bg-white py-1 shadow-lg" data-testid="mention-menu">
          {options.map((m, i) => (
            <li key={m.id} role="option" aria-selected={i === index}>
              <button type="button" onMouseDown={(e) => { e.preventDefault(); pick(m) }}
                className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm ${i === index ? 'bg-primary-50 text-primary-700' : 'text-slate-700 hover:bg-slate-50'}`}>
                <CollabAvatar user={m} size="sm" />
                <span className="truncate">{m.display_name || m.username}</span>
                <span className="ml-auto text-xs text-slate-400">@{m.username}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// SuggestionsPanel 当前章节的修改建议：待处理（能编辑的人可审阅；建议者可撤回自己的）与最近已处理的。
function SuggestionsPanel({ docId, reloadKey, currentUserId, canReview, onReview, onChanged }: {
  docId: number | null
  reloadKey: number
  currentUserId: number
  canReview: boolean
  onReview: (s: WriterSuggestion) => void
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [items, setItems] = useState<WriterSuggestion[] | null>(null)
  const [decided, setDecided] = useState<WriterSuggestion[] | null>(null)
  const [showDecided, setShowDecided] = useState(false)
  const [withdrawing, setWithdrawing] = useState<number | null>(null)

  const load = useCallback(async () => {
    if (!docId) { setItems([]); return }
    try {
      setItems((await api<{ items: WriterSuggestion[] }>(`/documents/${docId}/suggestions`)).items || [])
      if (showDecided) setDecided((await api<{ items: WriterSuggestion[] }>(`/documents/${docId}/suggestions`, { params: { status: 'decided' } })).items || [])
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
  }, [docId, showDecided, showToast])
  useEffect(() => { void load() }, [load, reloadKey])

  async function withdraw(s: WriterSuggestion) {
    setWithdrawing(s.id)
    try {
      await api(`/suggestions/${s.id}`, { method: 'DELETE' })
      await load()
      onChanged()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setWithdrawing(null)
  }

  const card = (s: WriterSuggestion) => {
    const st = suggestionStats(s)
    return (
      <li key={s.id} className="rounded-xl border border-slate-200 p-3" data-testid="suggestion-item">
        <div className="flex items-center gap-2">
          <CollabAvatar user={s.user} />
          <div className="min-w-0">
            <div className="truncate text-xs font-medium text-slate-800">{s.user.display_name || s.user.username}</div>
            <div className="text-[11px] text-slate-400">{formatDate(s.created_at)}</div>
          </div>
          {s.status !== 'pending' && <span className="ml-auto text-[11px] text-slate-500">{t(`writer.suggest.status.${s.status}`)}</span>}
        </div>
        {s.note && <p className="mt-2 whitespace-pre-wrap break-words text-sm text-slate-700">{s.note}</p>}
        <p className="mt-2 text-xs">
          <span className="text-emerald-600">+{st.added}</span> <span className="text-rose-600">−{st.removed}</span>
          {st.title && <span className="ml-2 text-slate-500">{t('writer.suggest.titleChanged')}</span>}
        </p>
        {s.status === 'pending' && (canReview || s.user_id === currentUserId) && (
          <div className="mt-2 flex gap-1 border-t border-slate-100 pt-2">
            {canReview && <Button size="sm" onClick={() => onReview(s)} data-testid="suggestion-review">{t('writer.suggest.review')}</Button>}
            {s.user_id === currentUserId && (
              <Button size="sm" variant="ghost" loading={withdrawing === s.id} onClick={() => void withdraw(s)}>{t('writer.suggest.withdraw')}</Button>
            )}
          </div>
        )}
      </li>
    )
  }

  if (!docId) return <EmptyState>{t('writer.comments.noChapter')}</EmptyState>
  if (!items) return <Loading className="py-10" />
  return (
    <div className="space-y-4">
      {items.length === 0
        ? <p className="py-6 text-center text-sm text-slate-400">{t('writer.suggest.empty')}</p>
        : <ul className="space-y-3">{items.map(card)}</ul>}
      <Button size="sm" variant="ghost" className="w-full" onClick={() => setShowDecided((v) => !v)}>
        {t(showDecided ? 'writer.suggest.hideDecided' : 'writer.suggest.showDecided')}
      </Button>
      {showDecided && (decided === null ? <Loading className="py-6" /> : decided.length === 0
        ? <p className="text-center text-xs text-slate-400">{t('writer.suggest.noDecided')}</p>
        : <ul className="space-y-3">{decided.map(card)}</ul>)}
    </div>
  )
}
