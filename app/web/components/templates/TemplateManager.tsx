import { useCallback, useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/router'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { TEMPLATE_VARIABLES, type TemplateDetail, type TemplateKind, type TemplateLists, type TemplateSummary } from '@/lib/templates'
import type { Book, PageResult } from '@/lib/types'
import { Badge, Button, Card, Checkbox, EmptyState, Field, Input, Loading, Modal, SegmentedTabs, Select, Textarea, useFeedback } from '@/components/ui'
import { Outline } from './TemplatePicker'

interface Props {
  /** mine：我的个人模板；official：站点模板（管理员） */
  scope: 'mine' | 'official'
  /** 当前页面路径，用于 ?kind= Tab 链接 */
  basePath: string
}

interface EditorState {
  id?: number
  kind: TemplateKind
  title: string
  description: string
  content: string
  detail?: TemplateDetail
}

// TemplateManager 模板管理（「我的模板」与后台「站点模板」共用）：按章节模板 / 书籍模板分 Tab（?kind=），
// 章节模板可新建与编辑正文，书籍模板从书籍保存（可选是否包含正文），两类都可改名称、说明与删除。
export default function TemplateManager({ scope, basePath }: Props) {
  const { t } = useTranslation()
  const router = useRouter()
  const { confirmAction, showToast } = useFeedback()
  const kind: TemplateKind = router.query.kind === 'book' ? 'book' : 'chapter'
  const base = scope === 'official' ? '/admin/templates' : '/templates'
  const [items, setItems] = useState<TemplateSummary[] | null>(null)
  const [quota, setQuota] = useState<{ used: number; limit: number } | null>(null)
  const [editor, setEditor] = useState<EditorState | null>(null)
  const [fromBook, setFromBook] = useState(false)
  const [deleting, setDeleting] = useState<number | null>(null)
  const [opening, setOpening] = useState<number | null>(null)

  const load = useCallback(async () => {
    try {
      if (scope === 'official') {
        const d = await api<{ items: TemplateSummary[] }>('/admin/templates', { params: { kind } })
        setItems(d.items || [])
      } else {
        const d = await api<TemplateLists>('/templates', { params: { kind } })
        setItems(d.mine || [])
        setQuota({ used: d.used, limit: d.limit })
      }
    } catch (e) {
      setItems([])
      showToast({ message: (e as Error).message, tone: 'error' })
    }
  }, [scope, kind, showToast])

  useEffect(() => {
    if (!router.isReady) return
    setItems(null)
    void load()
  }, [router.isReady, load])

  async function openEditor(item: TemplateSummary) {
    setOpening(item.id)
    try {
      const d = await api<TemplateDetail>(`/templates/${item.id}`)
      setEditor({ id: d.id, kind: d.kind, title: d.title, description: d.description, content: d.content || '', detail: d })
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setOpening(null)
  }

  async function remove(item: TemplateSummary) {
    if (!(await confirmAction({ title: t('templates.manage.deleteTitle'), message: t('templates.manage.deleteMessage', { title: item.title }), confirmLabel: t('templates.manage.delete'), danger: true }))) return
    setDeleting(item.id)
    try {
      await api(`${base}/${item.id}`, { method: 'DELETE' })
      setItems((list) => (list || []).filter((x) => x.id !== item.id))
      setQuota((q) => (q ? { ...q, used: Math.max(0, q.used - 1) } : q))
      showToast({ message: t('templates.manage.deleted'), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setDeleting(null)
  }

  const full = quota !== null && quota.limit >= 0 && quota.used >= quota.limit

  return (
    <div>
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
        <SegmentedTabs value={kind} ariaLabel={t('templates.manage.kindAria')} items={(['chapter', 'book'] as const).map((k) => ({
          value: k, label: t(`templates.kind.${k}`), href: k === 'chapter' ? basePath : `${basePath}?kind=book`,
        }))} />
        <div className="flex items-center gap-3">
          {quota && (
            <span className="text-xs text-slate-500" data-testid="template-quota">
              {quota.limit < 0 ? t('templates.manage.quotaUnlimited', { used: quota.used }) : t('templates.manage.quota', { used: quota.used, limit: quota.limit })}
            </span>
          )}
          {kind === 'chapter' ? (
            <Button disabled={full} onClick={() => setEditor({ kind: 'chapter', title: '', description: '', content: '' })} data-testid="template-create">
              <i className="fa-solid fa-plus" aria-hidden="true" /> {t('templates.manage.createChapter')}
            </Button>
          ) : (
            <Button disabled={full} onClick={() => setFromBook(true)} data-testid="template-from-book">
              <i className="fa-solid fa-book" aria-hidden="true" /> {t('templates.manage.fromBook')}
            </Button>
          )}
        </div>
      </div>
      {full && <p className="mb-4 rounded-lg bg-amber-50 px-4 py-2.5 text-sm text-amber-700">{t('templates.manage.full')}</p>}

      {items === null ? <Loading className="py-16" /> : items.length === 0 ? (
        <EmptyState>
          <i className="fa-regular fa-clone mb-3 block text-2xl text-slate-300" aria-hidden="true" />
          {t(`templates.manage.empty.${scope}.${kind}`)}
        </EmptyState>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {items.map((item) => (
            <Card key={item.id} className="flex flex-col p-5" data-testid="template-row">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <h3 className="truncate font-semibold text-slate-900">{item.title}</h3>
                  {item.description && <p className="mt-1 line-clamp-2 text-sm text-slate-500">{item.description}</p>}
                </div>
                {kind === 'book' && <Badge tone="slate">{t('templates.picker.chapterCount', { n: item.chapter_count })}</Badge>}
              </div>
              <p className="mt-3 line-clamp-3 flex-1 text-xs leading-5 text-slate-400">
                {kind === 'book' ? (item.outline || []).join(' · ') : item.preview || t('templates.manage.noContent')}
              </p>
              <div className="mt-4 flex items-center justify-between gap-2 border-t border-slate-100 pt-3">
                <span className="text-xs text-slate-400">{t('templates.manage.meta', { n: item.use_count, time: formatDate(item.updated_at) })}</span>
                <div className="flex gap-1">
                  <Button size="sm" variant="ghost" loading={opening === item.id} onClick={() => void openEditor(item)}>{t('templates.manage.edit')}</Button>
                  <Button size="sm" variant="ghost" className="text-rose-600 hover:bg-rose-50" loading={deleting === item.id} onClick={() => void remove(item)}>{t('templates.manage.delete')}</Button>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}

      {editor && <EditorModal base={base} state={editor} onClose={() => setEditor(null)} onSaved={() => { setEditor(null); void load() }} />}
      {fromBook && <FromBookModal base={base} onClose={() => setFromBook(false)} onSaved={() => { setFromBook(false); void load() }} />}
    </div>
  )
}

// EditorModal 新建或编辑模板：名称、说明；章节模板编辑正文（可点变量插入到光标处），书籍模板显示章节目录。
function EditorModal({ base, state, onClose, onSaved }: { base: string; state: EditorState; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [form, setForm] = useState(state)
  const [saving, setSaving] = useState(false)
  const contentRef = useRef<HTMLTextAreaElement>(null)

  function insertVariable(name: string) {
    const el = contentRef.current
    const token = `{{${name}}}`
    if (!el) { setForm((f) => ({ ...f, content: f.content + token })); return }
    const s = el.selectionStart, e = el.selectionEnd
    const next = form.content.slice(0, s) + token + form.content.slice(e)
    setForm((f) => ({ ...f, content: next }))
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(s + token.length, s + token.length) })
  }

  async function save() {
    if (!form.title.trim()) {
      showToast({ message: t('templates.manage.titleRequired'), tone: 'error' })
      return
    }
    setSaving(true)
    try {
      const body: Record<string, unknown> = { title: form.title, description: form.description }
      if (form.kind === 'chapter') body.content = form.content
      if (form.id) await api(`${base}/${form.id}`, { method: 'PUT', body })
      else await api(base, { method: 'POST', body: { ...body, kind: form.kind } })
      showToast({ message: t('templates.manage.saved'), tone: 'success' })
      onSaved()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setSaving(false)
  }

  return (
    <Modal open onClose={onClose} className="max-w-3xl" title={t(form.id ? 'templates.manage.editTitle' : 'templates.manage.createTitle')}
      footer={(
        <>
          <Button variant="outline" onClick={onClose}>{t('templates.picker.cancel')}</Button>
          <Button onClick={() => void save()} loading={saving} data-testid="template-editor-save">{t('templates.manage.save')}</Button>
        </>
      )}>
      <div className="space-y-4" data-testid="template-editor">
        <Field label={t('templates.field.title')}>
          <Input value={form.title} maxLength={100} onChange={(e) => setForm({ ...form, title: e.target.value })} />
        </Field>
        <Field label={t('templates.field.description')}>
          <Input value={form.description} maxLength={500} onChange={(e) => setForm({ ...form, description: e.target.value })} placeholder={t('templates.field.descriptionPlaceholder')} />
        </Field>
        {form.kind === 'chapter' ? (
          <Field label={t('templates.field.content')}>
            <Textarea ref={contentRef} value={form.content} onChange={(e) => setForm({ ...form, content: e.target.value })}
              className="min-h-[280px] font-mono text-[13px]" placeholder={t('templates.field.contentPlaceholder')} />
            <div className="mt-2 flex flex-wrap items-center gap-1.5 text-xs text-slate-500">
              <span>{t('templates.field.variables')}</span>
              {TEMPLATE_VARIABLES.map((v) => (
                <button key={v} type="button" onClick={() => insertVariable(v)}
                  className="rounded-md bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] text-slate-600 hover:bg-primary-50 hover:text-primary-700">
                  {`{{${v}}}`}
                </button>
              ))}
            </div>
            <p className="mt-1 text-xs text-slate-400">{t('templates.field.variablesHint')}</p>
          </Field>
        ) : (
          <div>
            <div className="mb-2 text-sm font-medium text-slate-700">{t('templates.field.outline')}</div>
            <div className="max-h-72 overflow-y-auto rounded-lg border border-slate-200 p-3"><Outline nodes={form.detail?.chapters || []} /></div>
            <p className="mt-2 text-xs text-slate-400">{t('templates.field.outlineHint')}</p>
          </div>
        )}
      </div>
    </Modal>
  )
}

// FromBookModal 把自己的一本书保存为书籍模板。
function FromBookModal({ base, onClose, onSaved }: { base: string; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [books, setBooks] = useState<Book[] | null>(null)
  const [bookId, setBookId] = useState('')
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [withContent, setWithContent] = useState(true)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    api<PageResult<Book>>('/books', { params: { scope: 'owned', page: 1, page_size: 100 } })
      .then((d) => setBooks(d.items || []))
      .catch(() => setBooks([]))
  }, [])

  async function save() {
    if (!bookId) {
      showToast({ message: t('templates.fromBook.bookRequired'), tone: 'error' })
      return
    }
    setSaving(true)
    try {
      await api(`${base}/from-book`, { method: 'POST', body: { book_id: Number(bookId), title: title.trim() || undefined, description, with_content: withContent } })
      showToast({ message: t('templates.manage.saved'), tone: 'success' })
      onSaved()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setSaving(false)
  }

  return (
    <Modal open onClose={onClose} title={t('templates.fromBook.title')}
      footer={(
        <>
          <Button variant="outline" onClick={onClose}>{t('templates.picker.cancel')}</Button>
          <Button onClick={() => void save()} loading={saving} disabled={!books?.length} data-testid="template-from-book-save">{t('templates.manage.save')}</Button>
        </>
      )}>
      {books === null ? <Loading className="py-10" /> : books.length === 0 ? <EmptyState>{t('templates.fromBook.noBooks')}</EmptyState> : (
        <div className="space-y-4" data-testid="template-from-book-form">
          <Field label={t('templates.fromBook.book')}>
            <Select value={bookId} searchable searchPlaceholder={t('templates.fromBook.search')} placeholder={t('templates.fromBook.bookPlaceholder')}
              options={books.map((b) => ({ value: String(b.id), label: b.title }))}
              onChange={(v) => { setBookId(v); if (!title) setTitle(books.find((b) => String(b.id) === v)?.title || '') }} />
          </Field>
          <Field label={t('templates.field.title')}>
            <Input value={title} maxLength={100} onChange={(e) => setTitle(e.target.value)} />
          </Field>
          <Field label={t('templates.field.description')}>
            <Input value={description} maxLength={500} onChange={(e) => setDescription(e.target.value)} placeholder={t('templates.field.descriptionPlaceholder')} />
          </Field>
          <label className="flex items-start gap-2 text-sm text-slate-700">
            <Checkbox checked={withContent} onChange={setWithContent} />
            <span>
              {t('templates.fromBook.withContent')}
              <span className="block text-xs text-slate-400">{t('templates.fromBook.withContentHint')}</span>
            </span>
          </label>
        </div>
      )}
    </Modal>
  )
}
