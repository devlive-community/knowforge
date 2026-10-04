import { useMemo, useState } from 'react'
import { formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { applyHunks, diffHunks } from '@/lib/merge'
import { Button, Checkbox, Modal } from '@/components/ui'
import type { CollabUser } from './collab'

export interface WriterSuggestion {
  id: number
  document_id: number
  user_id: number
  base_title: string
  base_content: string
  title: string
  content: string
  note: string
  status: 'pending' | 'accepted' | 'partial' | 'rejected'
  decided_at: string | null
  created_at: string
  user: CollabUser
}

export interface AcceptedSuggestion { title: string; content: string; partial: boolean }

const CONTEXT_LINES = 2

// suggestionStats 建议改动的行数（新增 / 删除）与是否改了标题。
export function suggestionStats(s: Pick<WriterSuggestion, 'base_content' | 'content' | 'base_title' | 'title'>) {
  const a = s.base_content.split('\n')
  const b = s.content.split('\n')
  let added = 0
  let removed = 0
  for (const h of diffHunks(a, b)) {
    added += h.bEnd - h.bStart
    removed += h.aEnd - h.aStart
  }
  return { added, removed, title: s.base_title !== s.title }
}

// SuggestionReview 审阅修改建议：按改动块列出原文（删除）与建议（新增），逐块选择是否采纳；
// 采纳的部分由写作台合并到当前版本后保存。
export default function SuggestionReview({ suggestion, onClose, onApply, onReject }: {
  suggestion: WriterSuggestion
  onClose: () => void
  onApply: (accepted: AcceptedSuggestion) => Promise<boolean>
  onReject: () => Promise<boolean>
}) {
  const { t } = useTranslation()
  const a = useMemo(() => suggestion.base_content.split('\n'), [suggestion])
  const b = useMemo(() => suggestion.content.split('\n'), [suggestion])
  const hunks = useMemo(() => diffHunks(a, b), [a, b])
  const titleChanged = suggestion.base_title !== suggestion.title
  const [accept, setAccept] = useState<boolean[]>(() => hunks.map(() => true))
  const [acceptTitle, setAcceptTitle] = useState(titleChanged)
  const [busy, setBusy] = useState<'apply' | 'reject' | null>(null)
  const selected = accept.filter(Boolean).length + (acceptTitle ? 1 : 0)
  const total = hunks.length + (titleChanged ? 1 : 0)
  const name = suggestion.user.display_name || suggestion.user.username

  async function apply() {
    setBusy('apply')
    const content = applyHunks(a, b, hunks, accept).join('\n')
    const done = await onApply({ title: acceptTitle ? suggestion.title : suggestion.base_title, content, partial: selected < total })
    setBusy(null)
    if (done) onClose()
  }

  async function reject() {
    setBusy('reject')
    const done = await onReject()
    setBusy(null)
    if (done) onClose()
  }

  return (
    <Modal open elevated onClose={() => { if (!busy) onClose() }} className="max-w-4xl" title={t('writer.suggest.reviewTitle', { name })}
      footer={(
        <div className="flex w-full flex-wrap items-center justify-between gap-2">
          <Button variant="ghost" className="text-rose-600 hover:bg-rose-50" loading={busy === 'reject'} disabled={!!busy} onClick={() => void reject()} data-testid="suggest-reject">{t('writer.suggest.rejectAll')}</Button>
          <div className="flex gap-2">
            <Button variant="outline" disabled={!!busy} onClick={onClose}>{t('writer.comments.cancel')}</Button>
            <Button loading={busy === 'apply'} disabled={!!busy || selected === 0} onClick={() => void apply()} data-testid="suggest-apply">
              {t('writer.suggest.applySelected', { n: selected, total })}
            </Button>
          </div>
        </div>
      )}>
      <div className="space-y-4" data-testid="suggest-review">
        <div className="text-sm text-slate-600">
          <p>{t('writer.suggest.reviewHint', { time: formatDate(suggestion.created_at) })}</p>
          {suggestion.note && <p className="mt-2 rounded-lg bg-slate-50 px-3 py-2 text-slate-700">{suggestion.note}</p>}
        </div>
        {titleChanged && (
          <label className="flex items-start gap-3 rounded-xl border border-slate-200 p-3 text-sm">
            <Checkbox checked={acceptTitle} onChange={setAcceptTitle} ariaLabel={t('writer.suggest.acceptTitle')} />
            <span className="min-w-0 flex-1">
              <span className="block text-xs font-medium text-slate-500">{t('writer.suggest.titleChange')}</span>
              <span className="mt-1 block text-rose-700 line-through">{suggestion.base_title}</span>
              <span className="block text-emerald-700">{suggestion.title}</span>
            </span>
          </label>
        )}
        {hunks.map((h, i) => (
          <div key={i} className={`rounded-xl border p-3 ${accept[i] ? 'border-emerald-200' : 'border-slate-200 opacity-70'}`} data-testid="suggest-hunk">
            <label className="mb-2 flex items-center gap-2 text-xs font-medium text-slate-500">
              <Checkbox checked={accept[i]} onChange={(v) => setAccept((list) => list.map((x, j) => (j === i ? v : x)))} ariaLabel={t('writer.suggest.acceptBlock', { n: i + 1 })} />
              {t('writer.suggest.block', { n: i + 1 })}
            </label>
            <pre className="overflow-x-auto whitespace-pre-wrap break-words rounded-lg bg-slate-50 px-3 py-2 font-mono text-xs leading-5">
              {a.slice(Math.max(0, h.aStart - CONTEXT_LINES), h.aStart).map((line, k) => <span key={`c${k}`} className="block text-slate-400">{'  '}{line}</span>)}
              {a.slice(h.aStart, h.aEnd).map((line, k) => <span key={`r${k}`} className="block bg-rose-50 text-rose-700">{'- '}{line}</span>)}
              {b.slice(h.bStart, h.bEnd).map((line, k) => <span key={`a${k}`} className="block bg-emerald-50 text-emerald-700">{'+ '}{line}</span>)}
              {a.slice(h.aEnd, h.aEnd + CONTEXT_LINES).map((line, k) => <span key={`d${k}`} className="block text-slate-400">{'  '}{line}</span>)}
            </pre>
          </div>
        ))}
        <p className="text-xs text-slate-400">{t('writer.suggest.reviewNote')}</p>
      </div>
    </Modal>
  )
}
