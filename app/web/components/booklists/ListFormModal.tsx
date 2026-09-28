import { useState } from 'react'
import { api } from '@/lib/api'
import type { BookList } from '@/lib/booklists'
import { useTranslation } from '@/lib/i18n'
import { Button, Field, Input, Modal, Switch, Textarea, useFeedback } from '@/components/ui'

// ListFormModal 新建或编辑书单；新建时可带 bookId 同时收录一本书。
export default function ListFormModal({ list, bookId, onSaved, onClose }: {
  list?: BookList
  bookId?: number
  onSaved: (list: BookList) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [title, setTitle] = useState(list?.title || '')
  const [description, setDescription] = useState(list?.description || '')
  const [isPublic, setIsPublic] = useState(list ? list.is_public : true)
  const [saving, setSaving] = useState(false)

  async function save() {
    if (!title.trim()) { showToast({ message: t('booklists.form.titleRequired'), tone: 'error' }); return }
    setSaving(true)
    try {
      const body = { title: title.trim(), description: description.trim(), is_public: isPublic, ...(bookId ? { book_id: bookId } : {}) }
      const saved = list
        ? await api<BookList>(`/book-lists/${list.id}`, { method: 'PUT', body })
        : await api<BookList>('/book-lists', { method: 'POST', body })
      showToast({ message: t(list ? 'booklists.form.saved' : 'booklists.form.created'), tone: 'success' })
      onSaved(saved)
    } catch (e) {
      showToast({ title: t('booklists.form.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal open onClose={onClose} elevated title={t(list ? 'booklists.form.editTitle' : 'booklists.form.createTitle')}
      footer={<>
        <Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button>
        <Button onClick={() => void save()} loading={saving}>{t(list ? 'common.actions.save' : 'booklists.form.create')}</Button>
      </>}>
      <div className="space-y-4">
        <Field label={t('booklists.form.name')}>
          <Input value={title} maxLength={60} autoFocus onChange={(e) => setTitle(e.target.value)} placeholder={t('booklists.form.namePlaceholder')} />
        </Field>
        <Field label={t('booklists.form.description')}>
          <Textarea value={description} maxLength={1000} rows={3} onChange={(e) => setDescription(e.target.value)} placeholder={t('booklists.form.descriptionPlaceholder')} />
        </Field>
        <div className="flex items-center justify-between gap-4 rounded-xl border border-slate-200 p-3">
          <div>
            <div className="text-sm font-medium text-slate-700">{t('booklists.form.public')}</div>
            <div className="text-xs text-slate-500">{t('booklists.form.publicHint')}</div>
          </div>
          <Switch checked={isPublic} onChange={setIsPublic} ariaLabel={t('booklists.form.public')} />
        </div>
      </div>
    </Modal>
  )
}
