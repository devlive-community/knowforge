import { useEffect, useState } from 'react'
import { api } from '@/lib/api'
import type { BookList } from '@/lib/booklists'
import { useTranslation } from '@/lib/i18n'
import type { PageResult } from '@/lib/types'
import { Pagination } from '@/components/ui'
import BookListCard from './BookListCard'

const PAGE_SIZE = 6

// UserBookLists 个人主页「书单」：该用户的公开书单（本人可见全部；没有时不显示）。
export default function UserBookLists({ username }: { username: string }) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [data, setData] = useState<PageResult<BookList> | null>(null)
  useEffect(() => {
    let active = true
    api<PageResult<BookList>>(`/users/${encodeURIComponent(username)}/book-lists`, { params: { page, page_size: PAGE_SIZE } })
      .then((d) => { if (active) setData(d) })
      .catch(() => { /* 忽略 */ })
    return () => { active = false }
  }, [username, page])
  if (!data || data.total === 0) return null
  return (
    <section className="mt-12">
      <div className="mb-5 flex items-baseline gap-3">
        <h2 className="text-2xl font-bold text-ink">{t('booklists.user.title')}</h2>
        <span className="text-sm text-slate-400">{t('booklists.user.count', { n: data.total })}</span>
      </div>
      <div className="grid gap-5 grid-cols-[repeat(auto-fill,minmax(16rem,1fr))]">
        {data.items.map((l) => <BookListCard key={l.id} list={l} showOwner={false} />)}
      </div>
      <Pagination page={page} pageSize={PAGE_SIZE} total={data.total} onChange={setPage} />
    </section>
  )
}
