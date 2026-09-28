import { useEffect, useState } from 'react'
import Link from 'next/link'
import { api } from '@/lib/api'
import { bookListPath, type BookList } from '@/lib/booklists'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, Checkbox, Loading, Modal, useFeedback } from '@/components/ui'
import ListFormModal from './ListFormModal'

// AddToListModal 书籍详情页「加入书单」：勾选即收录、取消即移出（每行独立的处理中状态），也可新建书单并收录。
export default function AddToListModal({ bookId, onClose }: { bookId: number; onClose: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [lists, setLists] = useState<BookList[] | null>(null)
  const [limit, setLimit] = useState(-1)
  const [busy, setBusy] = useState<number | null>(null)
  const [creating, setCreating] = useState(false)

  useEffect(() => {
    api<{ items: BookList[]; limit: number }>('/book-lists/mine', { params: { book_id: bookId } })
      .then((d) => { setLists(d.items); setLimit(d.limit) })
      .catch((e) => { setLists([]); showToast({ message: (e as Error).message, tone: 'error' }) })
  }, [bookId, showToast])

  async function toggle(list: BookList) {
    setBusy(list.id)
    try {
      if (list.contains) await api(`/book-lists/${list.id}/items/${bookId}`, { method: 'DELETE' })
      else await api(`/book-lists/${list.id}/items`, { method: 'POST', body: { book_id: bookId } })
      const next = !list.contains
      setLists((ls) => (ls || []).map((l) => (l.id === list.id ? { ...l, contains: next, item_count: l.item_count + (next ? 1 : -1) } : l)))
      showToast({ message: t(next ? 'booklists.add.added' : 'booklists.add.removed', { list: list.title }), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  const full = limit >= 0 && (lists?.length || 0) >= limit
  return (
    <>
      <Modal open onClose={onClose} title={t('booklists.add.title')}
        footer={<>
          <Button variant="outline" onClick={() => setCreating(true)} disabled={full || lists === null}>
            <i className="fa-solid fa-plus" aria-hidden="true" />{t('booklists.add.create')}
          </Button>
          <Button onClick={onClose}>{t('common.actions.close')}</Button>
        </>}>
        {lists === null ? <Loading className="py-8" /> : lists.length === 0 ? (
          <p className="py-6 text-center text-sm text-slate-500">{t('booklists.add.empty')}</p>
        ) : (
          <ul className="-mx-2 max-h-80 overflow-y-auto">
            {lists.map((l) => (
              <li key={l.id} className="flex items-center gap-3 rounded-lg px-2 py-2 hover:bg-slate-50">
                {busy === l.id
                  ? <span className="flex h-5 w-5 items-center justify-center text-primary-500"><i className="fa-solid fa-spinner fa-spin text-sm" aria-hidden="true" /></span>
                  : <Checkbox checked={!!l.contains} onChange={() => void toggle(l)} disabled={busy !== null} ariaLabel={l.title} />}
                <button type="button" className="min-w-0 flex-1 text-left" disabled={busy !== null} onClick={() => void toggle(l)}>
                  <span className="flex items-center gap-2">
                    <span className="truncate text-sm font-medium text-slate-800">{l.title}</span>
                    {!l.is_public && <Badge tone="slate">{t('booklists.card.private')}</Badge>}
                  </span>
                  <span className="text-xs text-slate-400">{t('booklists.card.books', { n: l.item_count })}</span>
                </button>
                <Link href={bookListPath(l.id)} className="shrink-0 text-xs text-slate-400 hover:text-primary-600">{t('booklists.add.view')}</Link>
              </li>
            ))}
          </ul>
        )}
        {full && <p className="mt-3 text-xs text-amber-600">{t('booklists.add.limitReached', { n: limit })}</p>}
      </Modal>
      {creating && (
        <ListFormModal bookId={bookId} onClose={() => setCreating(false)}
          onSaved={(list) => { setCreating(false); setLists((ls) => [{ ...list, contains: true }, ...(ls || [])]) }} />
      )}
    </>
  )
}
