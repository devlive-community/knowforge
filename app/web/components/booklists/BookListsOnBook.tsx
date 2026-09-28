import { useEffect, useState } from 'react'
import { api } from '@/lib/api'
import type { BookList } from '@/lib/booklists'
import { useTranslation } from '@/lib/i18n'
import BookListCard from './BookListCard'

// BookListsOnBook 书籍详情页「收录于书单」：收录这本书的公开书单（没有时不显示）。
export default function BookListsOnBook({ bookId }: { bookId: number }) {
  const { t } = useTranslation()
  const [data, setData] = useState<{ items: BookList[]; total: number } | null>(null)
  useEffect(() => {
    let active = true
    setData(null)
    api<{ items: BookList[]; total: number }>(`/books/${bookId}/book-lists`)
      .then((d) => { if (active) setData(d) })
      .catch(() => { /* 忽略 */ })
    return () => { active = false }
  }, [bookId])
  if (!data || data.items.length === 0) return null
  return (
    <section className="border-t border-slate-200 py-10">
      <h2 className="mb-6 text-xl font-bold text-slate-900">{t('booklists.onBook.title', { n: data.total })}</h2>
      <div className="grid gap-4 grid-cols-[repeat(auto-fill,minmax(15rem,1fr))]">
        {data.items.map((l) => <BookListCard key={l.id} list={l} />)}
      </div>
    </section>
  )
}
