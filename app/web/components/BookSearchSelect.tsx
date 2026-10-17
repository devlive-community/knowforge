import { ReactNode, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { api } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { Input } from '@/components/ui'

export interface BookLite { id: number; title: string; slug: string }

// BookSearchSelect 通用「可搜索书籍选择器」：输入关键字实时查后端并下拉候选，点选即回调。
// 各处（复制章节到书籍、协议/帮助章节选择、后台等）统一复用，避免各自实现固定下拉。
// endpoint/searchParam/baseParams 用于适配不同列表接口（/books 用 title 搜、/admin/books 用 q 搜）。
// pinned 为固定排在最前的书（如「本书」），按关键字在本地过滤，不与搜索结果重复。
export default function BookSearchSelect({
  value,
  onChange,
  endpoint = '/admin/books',
  searchParam = 'q',
  baseParams,
  placeholder,
  excludeId,
  labelFor,
  pinned,
}: {
  value: BookLite | null
  onChange: (book: BookLite | null) => void
  endpoint?: string
  searchParam?: string
  baseParams?: Record<string, string | number | boolean>
  placeholder?: string
  excludeId?: number
  labelFor?: (book: BookLite) => ReactNode
  pinned?: BookLite[]
}) {
  const { t } = useTranslation()
  const [query, setQuery] = useState(value?.title || '')
  const [results, setResults] = useState<BookLite[]>([])
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const boxRef = useRef<HTMLDivElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ top: number; left: number; width: number } | null>(null)

  useEffect(() => { setQuery(value?.title || '') }, [value])

  // 下拉用 Portal 挂到 body 并按输入框位置固定定位，避免被 Modal/overflow 容器裁剪。
  useLayoutEffect(() => {
    if (!open) { setPos(null); return }
    const update = () => {
      const r = boxRef.current?.getBoundingClientRect()
      if (r) setPos({ top: r.bottom + 4, left: r.left, width: r.width })
    }
    update()
    window.addEventListener('resize', update)
    window.addEventListener('scroll', update, true)
    return () => { window.removeEventListener('resize', update); window.removeEventListener('scroll', update, true) }
  }, [open, results.length, loading])

  useEffect(() => {
    if (!open) return
    const h = setTimeout(() => {
      setLoading(true)
      api<{ items: BookLite[] }>(endpoint, { params: { ...(baseParams || {}), [searchParam]: query.trim(), page_size: 20 } })
        .then((r) => setResults((r.items || []).filter((b) => b.id !== excludeId && !pinned?.some((p) => p.id === b.id))))
        .catch(() => setResults([]))
        .finally(() => setLoading(false))
    }, 250)
    return () => clearTimeout(h)
  }, [query, open, endpoint, searchParam, excludeId]) // eslint-disable-line react-hooks/exhaustive-deps

  const keyword = query.trim().toLowerCase()
  const pinnedShown = (pinned || []).filter((b) => !keyword || b.title.toLowerCase().includes(keyword) || b.slug.toLowerCase().includes(keyword) || b.title === value?.title)
  const options = [...pinnedShown, ...results]

  useEffect(() => {
    function onDoc(e: MouseEvent) {
      const target = e.target as Node
      if (!boxRef.current?.contains(target) && !menuRef.current?.contains(target)) setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [])

  return (
    <div ref={boxRef} className="relative">
      <Input value={query}
        onChange={(e) => { setQuery(e.target.value); if (value) onChange(null); setOpen(true) }}
        onFocus={() => setOpen(true)}
        placeholder={placeholder || t('common.book.searchPlaceholder')} />
      {open && pos && typeof document !== 'undefined' && createPortal(
        <div ref={menuRef} className="fixed z-[200] max-h-64 overflow-auto rounded-lg border border-slate-200 bg-white shadow-lg"
          style={{ top: pos.top, left: pos.left, width: pos.width }}>
          {loading && options.length === 0 ? (
            <div className="px-3 py-2 text-sm text-slate-400">{t('common.book.searching')}</div>
          ) : options.length === 0 ? (
            <div className="px-3 py-2 text-sm text-slate-400">{t('common.book.noResult')}</div>
          ) : (
            options.map((b) => (
              <button key={b.id} type="button" onMouseDown={(e) => { e.preventDefault(); onChange(b); setQuery(b.title); setOpen(false) }}
                className="block w-full truncate px-3 py-2 text-left text-sm text-slate-700 hover:bg-slate-50">
                {labelFor ? labelFor(b) : <>{b.title} <span className="text-xs text-slate-400">/{b.slug}</span></>}
              </button>
            ))
          )}
        </div>,
        document.body,
      )}
    </div>
  )
}
