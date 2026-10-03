import { useEffect, useState } from 'react'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import MyLibraryTabs from '@/components/MyLibraryTabs'
import { api } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { useGridPageSize } from '@/lib/useGridPageSize'
import { EmptyState, Loading, Pagination } from '@/components/ui'
import BookCard from '@/components/BookCard'
import Seo from '@/components/Seo'
import type { Book } from '@/lib/types'
import { useUrlPage } from '@/lib/use-url-page'

interface FollowItem {
  book: Book
  followed_at: string
}

export default function MyFollows() {
  return <FeatureGate feature="book-follow"><MyFollowsInner /></FeatureGate>
}

function MyFollowsInner() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const { ref: gridRef, pageSize, ready } = useGridPageSize({ minItemRem: 15, rows: 3, fallback: 9 })
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<{ items: FollowItem[]; total: number; page: number; page_size: number } | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!user || !ready) return
    setLoading(true)
    api<{ items: FollowItem[]; total: number; page: number; page_size: number }>('/users/me/follows', { params: { page, page_size: pageSize } })
      .then(setData)
      .catch(() => setData({ items: [], total: 0, page: 1, page_size: pageSize }))
      .finally(() => setLoading(false))
  }, [user, page, pageSize, ready])

  if (!user) return <Loading className="min-h-[60vh]" label={t('account.common.verifying')} />

  return (
    <>
      <Seo siteName={siteName} title={t('follows.seoTitle')} noindex />
      <Container>
        <div className="pb-6">
          <h1 className="text-2xl font-bold text-ink">{t('follows.heading')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('follows.subtitle')}</p>
        </div>
        <MyLibraryTabs active="follows" />

        <div ref={gridRef}>
          {loading || data === null ? (
            <Loading label={t('follows.loading')} />
          ) : data.total === 0 ? (
            <EmptyState>{t('follows.empty')}</EmptyState>
          ) : (
            <>
              <div className="grid gap-5 grid-cols-[repeat(auto-fill,minmax(15rem,1fr))]">
                {data.items.map(({ book }) => (
                  <BookCard key={book.id} book={book} />
                ))}
              </div>
              <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} />
            </>
          )}
        </div>
      </Container>
    </>
  )
}
