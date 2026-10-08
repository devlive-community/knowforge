import { useEffect, useState } from 'react'
import Link from 'next/link'
import Container from '@/components/Container'
import Seo from '@/components/Seo'
import FeatureGate from '@/components/FeatureGate'
import ResourceIcon from '@/components/ResourceIcon'
import { api, formatNumber } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { EmptyState, Loading, Pagination } from '@/components/ui'
import type { Tag } from '@/lib/types'

interface TagPage {
  items: Tag[]
  total: number
  page: number
  page_size: number
}

const TAGS_PAGE_SIZE = 24

// 全部标签页：列出所有有公开书籍的标签（含图标与使用计数），点击进入按标签检索。
export default function AllTags() {
  return <FeatureGate feature="tags"><AllTagsInner /></FeatureGate>
}

function AllTagsInner() {
  const { t } = useTranslation()
  const { site } = useApp()
  const [tags, setTags] = useState<TagPage | null>(null)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setLoading(true)
    api<TagPage>('/tags', { params: { page, page_size: TAGS_PAGE_SIZE } })
      .then(setTags)
      .catch(() => setTags({ items: [], total: 0, page: 1, page_size: TAGS_PAGE_SIZE }))
      .finally(() => setLoading(false))
  }, [page])

  return (
    <>
      <Seo siteName={site.site_name || 'KnowForge'} title={t('tags.all.title')} description={t('tags.all.subtitle')} />
      <Container>
        <div className="py-8">
          <h1 className="text-2xl font-bold text-ink">{t('tags.all.title')}</h1>
          <p className="mt-1.5 text-sm text-slate-500">{t('tags.all.subtitle')}</p>

          <div className="mt-6">
            {tags === null ? (
              <Loading label={t('tags.all.loading')} />
            ) : tags.total === 0 ? (
              <EmptyState>{t('tags.all.empty')}</EmptyState>
            ) : (
              <>
                <div className="grid gap-3 grid-cols-[repeat(auto-fill,minmax(13rem,1fr))]">
                  {tags.items.map((tag) => (
                    <Link key={tag.id} href={`/explore?tag=${encodeURIComponent(tag.slug)}`}
                      className="flex items-center gap-3 rounded-xl border border-slate-200 bg-white p-3.5 transition-colors hover:border-primary-300 hover:bg-primary-50/40">
                      <ResourceIcon iconType={tag.icon_type} iconValue={tag.icon_value} name={tag.name} />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate font-medium text-slate-800">{tag.name}</span>
                        <span className="text-xs text-slate-400">{t('tags.all.bookCount', { count: formatNumber(tag.book_count || 0) })}</span>
                      </span>
                    </Link>
                  ))}
                </div>
                <Pagination page={tags.page} pageSize={tags.page_size} total={tags.total} onChange={setPage} loading={loading} />
              </>
            )}
          </div>
        </div>
      </Container>
    </>
  )
}
