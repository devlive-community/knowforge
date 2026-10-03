import { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { api } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { Button, Input } from '@/components/ui'
import { insideFloatingLayer, usePopoverPosition } from '@/components/ui/usePopoverPosition'
import type { BookLite } from '@/components/BookSearchSelect'

interface DocNode { slug: string; title: string; external_url?: string; children?: DocNode[] }
interface Chapter { slug: string; title: string; depth: number }

// flattenDocs 把章节树压平为带层级的可选项（跳过外链章节：协议/帮助页需要站内可读的章节）。
function flattenDocs(nodes: DocNode[], depth = 0, out: Chapter[] = []): Chapter[] {
  for (const n of nodes) {
    if (!(n.external_url || '').trim()) out.push({ slug: n.slug, title: n.title, depth })
    if (n.children?.length) flattenDocs(n.children, depth + 1, out)
  }
  return out
}

const READER_LINK = /^\/book\/reader\/([^/?#]+)\/([^/?#]+)\/?$/

// ChapterLinkPicker 选择站内章节作为链接（如隐私政策、用户协议、帮助文档）：外观同下拉框，点击后弹出
// 「书籍（可搜索）→ 章节（可筛选）」两级选择，选中章节即得到阅读页地址 /book/reader/{书}/{章}；
// 底部仍可直接填写任意地址（含站外链接）。已选的站内章节显示为「书名 / 章节名」。
export default function ChapterLinkPicker({ value, onChange, placeholder }: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [bookQuery, setBookQuery] = useState('')
  const [books, setBooks] = useState<BookLite[]>([])
  const [booksLoading, setBooksLoading] = useState(false)
  const [active, setActive] = useState<BookLite | null>(null)
  const [chapters, setChapters] = useState<Chapter[] | null>(null)
  const [chapterQuery, setChapterQuery] = useState('')
  const [manual, setManual] = useState(value)
  const [label, setLabel] = useState<string | null>(null) // 已选章节的「书名 / 章节名」
  const triggerRef = useRef<HTMLButtonElement>(null)
  const popRef = useRef<HTMLDivElement>(null)
  const style = usePopoverPosition(open, triggerRef, popRef)

  // 已选的站内章节：解析书名与章节名用于显示
  useEffect(() => {
    setManual(value)
    const m = READER_LINK.exec(value.trim())
    if (!m) { setLabel(null); return }
    const [bookSlug, docSlug] = [decodeURIComponent(m[1]), decodeURIComponent(m[2])]
    let alive = true
    api<{ id: number; title: string; slug: string }>(`/books/slug/${encodeURIComponent(bookSlug)}`)
      .then(async (book) => {
        const tree = await api<DocNode[]>(`/books/${book.id}/documents`)
        const doc = flattenDocs(tree || []).find((d) => d.slug === docSlug)
        if (alive) setLabel(doc ? `${book.title} / ${doc.title}` : null)
      })
      .catch(() => { if (alive) setLabel(null) })
    return () => { alive = false }
  }, [value])

  // 书籍搜索（打开时按关键字实时查询）
  useEffect(() => {
    if (!open) return
    const h = setTimeout(() => {
      setBooksLoading(true)
      api<{ items: BookLite[] }>('/admin/books', { params: { q: bookQuery.trim(), page_size: 30 } })
        .then((r) => setBooks(r.items || []))
        .catch(() => setBooks([]))
        .finally(() => setBooksLoading(false))
    }, 200)
    return () => clearTimeout(h)
  }, [bookQuery, open])

  // 选中书籍后加载章节
  useEffect(() => {
    if (!active) { setChapters(null); return }
    setChapters(null)
    setChapterQuery('')
    let alive = true
    api<DocNode[]>(`/books/${active.id}/documents`)
      .then((tree) => { if (alive) setChapters(flattenDocs(tree || [])) })
      .catch(() => { if (alive) setChapters([]) })
    return () => { alive = false }
  }, [active])

  // 点击外部 / Esc 关闭
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (triggerRef.current?.contains(e.target as Node) || popRef.current?.contains(e.target as Node) || insideFloatingLayer(e.target)) return
      setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => { document.removeEventListener('mousedown', onDown); document.removeEventListener('keydown', onKey) }
  }, [open])

  const visibleChapters = useMemo(() => {
    const q = chapterQuery.trim().toLowerCase()
    return (chapters || []).filter((c) => !q || c.title.toLowerCase().includes(q) || c.slug.toLowerCase().includes(q))
  }, [chapters, chapterQuery])

  function pick(book: BookLite, docSlug: string) {
    onChange(`/book/reader/${book.slug}/${docSlug}`)
    setOpen(false)
  }

  const display = label || value
  return (
    <div className="relative">
      <button ref={triggerRef} type="button" onClick={() => setOpen((v) => !v)} aria-haspopup="dialog" aria-expanded={open}
        className="flex w-full items-center gap-2 rounded-lg border border-slate-200 bg-white px-3.5 text-left text-sm transition-colors hover:border-slate-300 focus:border-primary-500 focus:outline-none"
        style={{ height: 'var(--control-height)' }}>
        <i className={`fa-solid ${label ? 'fa-file-lines text-primary-500' : value ? 'fa-link text-slate-400' : 'fa-book text-slate-300'} text-xs`} aria-hidden="true" />
        <span className={`min-w-0 flex-1 truncate ${display ? 'text-slate-900' : 'text-slate-400'}`}>{display || placeholder || t('chapterPicker.placeholder')}</span>
        <i className="fa-solid fa-chevron-down text-[10px] text-slate-400" aria-hidden="true" />
      </button>
      {value && (
        <button type="button" onClick={() => onChange('')} aria-label={t('chapterPicker.clear')}
          className="absolute right-8 top-1/2 -translate-y-1/2 rounded p-1 text-slate-300 hover:text-slate-500">
          <i className="fa-solid fa-xmark text-xs" aria-hidden="true" />
        </button>
      )}
      {open && createPortal(
        <div ref={popRef} role="dialog" aria-label={t('chapterPicker.title')} data-floating-layer="" style={style}
          className="fixed z-[300] w-[min(40rem,calc(100vw-1rem))] overflow-hidden rounded-xl border border-slate-200 bg-white shadow-xl">
          <div className="grid h-80 grid-cols-2 divide-x divide-slate-100">
            {/* 书籍 */}
            <div className="flex min-h-0 flex-col">
              <div className="border-b border-slate-100 p-2">
                <Input size="sm" autoFocus value={bookQuery} onChange={(e) => setBookQuery(e.target.value)} placeholder={t('chapterPicker.searchBook')} />
              </div>
              <ul className="min-h-0 flex-1 overflow-y-auto py-1" role="listbox" aria-label={t('chapterPicker.books')}>
                {booksLoading && books.length === 0 ? (
                  <li className="px-3 py-6 text-center text-xs text-slate-400"><i className="fa-solid fa-spinner fa-spin mr-1" aria-hidden="true" />{t('chapterPicker.loading')}</li>
                ) : books.length === 0 ? (
                  <li className="px-3 py-6 text-center text-xs text-slate-400">{t('chapterPicker.noBooks')}</li>
                ) : books.map((b) => (
                  <li key={b.id}>
                    <button type="button" role="option" aria-selected={active?.id === b.id} onClick={() => setActive(b)}
                      className={`flex w-full items-center gap-2 px-3 py-2 text-left text-sm ${active?.id === b.id ? 'bg-primary-50 text-primary-700' : 'text-slate-700 hover:bg-slate-50'}`}>
                      <span className="min-w-0 flex-1">
                        <span className="block truncate">{b.title}</span>
                        <span className="block truncate text-[11px] text-slate-400">{b.slug}</span>
                      </span>
                      <i className="fa-solid fa-chevron-right text-[10px] text-slate-300" aria-hidden="true" />
                    </button>
                  </li>
                ))}
              </ul>
            </div>
            {/* 章节 */}
            <div className="flex min-h-0 flex-col">
              <div className="border-b border-slate-100 p-2">
                <Input size="sm" value={chapterQuery} disabled={!active} onChange={(e) => setChapterQuery(e.target.value)} placeholder={t('chapterPicker.filterChapter')} />
              </div>
              <ul className="min-h-0 flex-1 overflow-y-auto py-1" role="listbox" aria-label={t('chapterPicker.chapters')}>
                {!active ? (
                  <li className="px-3 py-6 text-center text-xs text-slate-400">{t('chapterPicker.pickBookFirst')}</li>
                ) : chapters === null ? (
                  <li className="px-3 py-6 text-center text-xs text-slate-400"><i className="fa-solid fa-spinner fa-spin mr-1" aria-hidden="true" />{t('chapterPicker.loading')}</li>
                ) : visibleChapters.length === 0 ? (
                  <li className="px-3 py-6 text-center text-xs text-slate-400">{t('chapterPicker.noChapters')}</li>
                ) : visibleChapters.map((c) => (
                  <li key={c.slug}>
                    <button type="button" role="option" aria-selected={value === `/book/reader/${active.slug}/${c.slug}`} onClick={() => pick(active, c.slug)}
                      className="flex w-full items-center gap-2 py-2 pr-3 text-left text-sm text-slate-700 hover:bg-slate-50"
                      style={{ paddingLeft: `${0.75 + (chapterQuery ? 0 : c.depth) * 1}rem` }}>
                      <i className="fa-regular fa-file-lines text-xs text-slate-300" aria-hidden="true" />
                      <span className="min-w-0 flex-1 truncate">{c.title}</span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          </div>
          {/* 其他地址 */}
          <form className="flex items-center gap-2 border-t border-slate-100 bg-slate-50/60 p-2" onSubmit={(e) => { e.preventDefault(); onChange(manual.trim()); setOpen(false) }}>
            <Input size="sm" className="flex-1" value={manual} onChange={(e) => setManual(e.target.value)} placeholder={t('chapterPicker.manualPlaceholder')} />
            <Button size="sm" type="submit" variant="outline">{t('chapterPicker.useLink')}</Button>
          </form>
        </div>,
        document.body,
      )}
    </div>
  )
}
