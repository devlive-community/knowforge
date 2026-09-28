import { useEffect, useState } from 'react'
import Link from 'next/link'
import { api } from '@/lib/api'
import type { Backlink } from '@/lib/backlinks'
import { useTranslation } from '@/lib/i18n'

// Backlinks 阅读页正文后的「被引用」：链接到本章的已发布章节及链接处的上下文（没有时不显示）。
export default function Backlinks({ docId, bookId }: { docId: number; bookId: number }) {
  const { t } = useTranslation()
  const [items, setItems] = useState<Backlink[]>([])
  useEffect(() => {
    let active = true
    setItems([])
    api<{ items: Backlink[] }>(`/backlinks/docs/${docId}`)
      .then((d) => { if (active) setItems(d.items) })
      .catch(() => { /* 忽略 */ })
    return () => { active = false }
  }, [docId])
  if (items.length === 0) return null
  return (
    <section className="mt-10 rounded-xl border border-slate-200 bg-white p-5" aria-label={t('backlinks.reader.title')}>
      <h2 className="flex items-center gap-2 text-base font-bold text-slate-900">
        <i className="fa-solid fa-link text-sm text-slate-400" aria-hidden="true" />
        {t('backlinks.reader.heading', { n: items.length })}
      </h2>
      <ul className="mt-3 divide-y divide-slate-100">
        {items.map((d) => (
          <li key={d.id} className="py-2.5 first:pt-0 last:pb-0">
            <Link href={`/book/reader/${encodeURIComponent(d.book_slug)}/${encodeURIComponent(d.slug)}`} className="group block text-sm">
              <span className="flex items-baseline gap-2">
                <span className="font-medium text-slate-700 group-hover:text-primary-600">{d.title}</span>
                {d.book_id !== bookId && <span className="truncate text-xs text-slate-400">{t('search.fromBook', { book: d.book_title })}</span>}
              </span>
              {d.excerpt && (
                <span className="mt-1 block text-xs leading-5 text-slate-500">
                  {d.excerpt.before}<mark className="rounded bg-primary-50 px-0.5 text-primary-700">{d.excerpt.text}</mark>{d.excerpt.after}
                </span>
              )}
            </Link>
          </li>
        ))}
      </ul>
    </section>
  )
}
