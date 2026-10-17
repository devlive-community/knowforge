import { useEffect, useState } from 'react'
import { useRouter } from 'next/router'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import MyLibraryTabs from '@/components/MyLibraryTabs'
import { api } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { useGridPageSize } from '@/lib/useGridPageSize'
import { EmptyState, Loading, Pagination, SegmentedTabs } from '@/components/ui'
import BookCard from '@/components/BookCard'
import Seo from '@/components/Seo'
import { FollowList } from '@/components/user-follow/FollowListDialog'
import type { Book } from '@/lib/types'
import { useUrlPage } from '@/lib/use-url-page'

interface FollowItem {
  book: Book
  followed_at: string
}

type FollowTab = 'books' | 'authors' | 'updates'

// 「我的关注」：关注的书籍（书籍关注插件）、关注的作者与作者最近更新的书籍（用户关注插件），按 ?tab= 切换。
export default function MyFollows() {
  const { site } = useApp()
  const plugins = site.feature_plugins || []
  if (!plugins.includes('book-follow') && !plugins.includes('user-follow')) return <FeatureGate feature="book-follow"><></></FeatureGate>
  return <MyFollowsInner />
}

function MyFollowsInner() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  const router = useRouter()
  const siteName = site.site_name || 'KnowForge'
  const bookFollow = (site.feature_plugins || []).includes('book-follow')
  const userFollow = (site.feature_plugins || []).includes('user-follow')
  const tabs: { value: FollowTab; label: string }[] = [
    ...(bookFollow ? [{ value: 'books' as const, label: t('follows.tab.books') }] : []),
    ...(userFollow ? [{ value: 'authors' as const, label: t('follows.tab.authors') }, { value: 'updates' as const, label: t('follows.tab.updates') }] : []),
  ]
  const requested = router.query.tab as FollowTab | undefined
  const tab: FollowTab = tabs.some((x) => x.value === requested) ? requested! : tabs[0].value
  const goTab = (v: string) => router.push({ pathname: router.pathname, query: v === tabs[0].value ? {} : { tab: v } }, undefined, { shallow: true })

  if (!user) return <Loading className="min-h-[60vh]" label={t('account.common.verifying')} />

  return (
    <>
      <Seo siteName={siteName} title={t('follows.seoTitle')} noindex />
      <Container>
        <div className="pb-6">
          <h1 className="text-2xl font-bold text-ink">{t('follows.heading')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t(userFollow ? 'follows.subtitleWithAuthors' : 'follows.subtitle')}</p>
        </div>
        <MyLibraryTabs active="follows" />
        {tabs.length > 1 && <SegmentedTabs className="mb-5" size="sm" value={tab} items={tabs} ariaLabel={t('follows.heading')} onChange={goTab} />}
        {tab === 'books' && <BookGrid key="books" endpoint="/users/me/follows" pick={(it: FollowItem) => it.book} empty={t('follows.empty')} loadingLabel={t('follows.loading')} />}
        {tab === 'authors' && (
          <div className="max-w-2xl rounded-xl border border-slate-200 bg-white px-3 py-1">
            <FollowList username={user.username} kind="following" />
          </div>
        )}
        {tab === 'updates' && <BookGrid key="updates" endpoint="/users/me/followed-authors/books" pick={(b: Book) => b} empty={t('follows.updatesEmpty')} loadingLabel={t('follows.loading')} />}
      </Container>
    </>
  )
}

// BookGrid 分页书籍卡片网格；pick 从列表项中取出书籍。
function BookGrid<T>({ endpoint, pick, empty, loadingLabel }: { endpoint: string; pick: (item: T) => Book; empty: string; loadingLabel: string }) {
  const { ref: gridRef, pageSize, ready } = useGridPageSize({ minItemRem: 15, rows: 3, fallback: 9 })
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<{ items: T[]; total: number; page: number; page_size: number } | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!ready) return
    setLoading(true)
    api<{ items: T[]; total: number; page: number; page_size: number }>(endpoint, { params: { page, page_size: pageSize } })
      .then(setData)
      .catch(() => setData({ items: [], total: 0, page: 1, page_size: pageSize }))
      .finally(() => setLoading(false))
  }, [endpoint, page, pageSize, ready])

  return (
    <div ref={gridRef}>
      {loading || data === null ? (
        <Loading label={loadingLabel} />
      ) : data.total === 0 ? (
        <EmptyState>{empty}</EmptyState>
      ) : (
        <>
          <div className="grid gap-5 grid-cols-[repeat(auto-fill,minmax(15rem,1fr))]">
            {data.items.map((it) => { const book = pick(it); return <BookCard key={book.id} book={book} /> })}
          </div>
          <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} />
        </>
      )}
    </div>
  )
}
