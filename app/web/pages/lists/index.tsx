import { useEffect, useState } from 'react'
import { useRouter } from 'next/router'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import Seo from '@/components/Seo'
import BookListCard from '@/components/booklists/BookListCard'
import ListFormModal from '@/components/booklists/ListFormModal'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { BOOK_LISTS_PLUGIN_KEY, bookListPath, type BookList } from '@/lib/booklists'
import { useTranslation } from '@/lib/i18n'
import type { PageResult } from '@/lib/types'
import { Button, EmptyState, Loading, Pagination, SegmentedTabs } from '@/components/ui'

const TABS = ['popular', 'latest', 'mine', 'followed'] as const
type Tab = typeof TABS[number]
const PAGE_SIZE = 12

export default function BookListsPage() {
  return <FeatureGate feature={BOOK_LISTS_PLUGIN_KEY}><BookListsInner /></FeatureGate>
}

// 书单广场：热门（收藏多）/ 最新，登录后可看「我的书单」「我收藏的」。Tab 与页码由 URL 承载。
function BookListsInner() {
  const { t } = useTranslation()
  const router = useRouter()
  const { site, user, authReady } = useApp()
  const requested = String(router.query.tab || 'popular') as Tab
  const tab: Tab = TABS.includes(requested) && (user || (requested !== 'mine' && requested !== 'followed')) ? requested : 'popular'
  const page = Math.max(1, Number(router.query.page) || 1)
  const [data, setData] = useState<{ items: BookList[]; total: number; limit?: number } | null>(null)
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)

  useEffect(() => {
    if (!router.isReady || !authReady) return
    setData(null)
    setError('')
    const req = tab === 'mine'
      ? api<{ items: BookList[]; limit: number }>('/book-lists/mine').then((d) => ({ items: d.items, total: d.items.length, limit: d.limit }))
      : api<PageResult<BookList>>(tab === 'followed' ? '/book-lists/followed' : '/book-lists', { params: { sort: tab === 'latest' ? 'latest' : undefined, page, page_size: PAGE_SIZE } })
    req.then(setData).catch((e) => setError((e as Error).message))
  }, [router.isReady, authReady, tab, page])

  const href = (next: Tab, p = 1) => `/lists?tab=${next}${p > 1 ? `&page=${p}` : ''}`
  const full = tab === 'mine' && data?.limit !== undefined && data.limit >= 0 && data.items.length >= data.limit
  const emptyKey = { popular: 'booklists.index.empty', latest: 'booklists.index.empty', mine: 'booklists.index.emptyMine', followed: 'booklists.index.emptyFollowed' }[tab]
  return (
    <>
      <Seo siteName={site.site_name || 'KnowForge'} title={t('booklists.index.title')} description={t('booklists.index.subtitle')} />
      <Container>
        <div className="flex flex-wrap items-end gap-4 pb-6 pt-2">
          <div className="min-w-0 flex-1">
            <h1 className="text-2xl font-bold text-ink">{t('booklists.index.title')}</h1>
            <p className="mt-1 text-sm text-slate-500">{t('booklists.index.subtitle')}</p>
          </div>
          {user && <Button onClick={() => setCreating(true)} disabled={full}><i className="fa-solid fa-plus" aria-hidden="true" />{t('booklists.index.create')}</Button>}
        </div>
        <SegmentedTabs className="mb-6 max-w-lg" value={tab} ariaLabel={t('booklists.index.title')}
          items={[
            { value: 'popular', label: t('booklists.index.popular'), href: href('popular') },
            { value: 'latest', label: t('booklists.index.latest'), href: href('latest') },
            ...(user ? [
              { value: 'mine', label: t('booklists.index.mine'), href: href('mine') },
              { value: 'followed', label: t('booklists.index.followed'), href: href('followed') },
            ] : []),
          ]} />
        {full && <p className="mb-4 text-sm text-amber-600">{t('booklists.add.limitReached', { n: data?.limit ?? 0 })}</p>}
        {error ? <p className="text-sm text-rose-600">{error}</p> : !data ? <Loading className="py-16" /> : data.items.length === 0 ? (
          <EmptyState>{t(emptyKey)}</EmptyState>
        ) : (
          <>
            <div className="grid gap-5 grid-cols-[repeat(auto-fill,minmax(16rem,1fr))]">
              {data.items.map((l) => <BookListCard key={l.id} list={l} showOwner={tab !== 'mine'} />)}
            </div>
            {tab !== 'mine' && <Pagination page={page} pageSize={PAGE_SIZE} total={data.total} onChange={(p) => void router.push(href(tab, p))} />}
          </>
        )}
      </Container>
      {creating && <ListFormModal onClose={() => setCreating(false)} onSaved={(l) => { setCreating(false); void router.push(bookListPath(l.id)) }} />}
    </>
  )
}
