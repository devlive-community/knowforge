import { useEffect, useState } from 'react'
import Link from 'next/link'
import Container from '@/components/Container'
import { api } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { ButtonLink, EmptyState, Loading, Pagination, Badge } from '@/components/ui'
import MyLibraryTabs from '@/components/MyLibraryTabs'
import Seo from '@/components/Seo'
import { DownloadIcon } from '@/components/icons'
import type { Book } from '@/lib/types'
import { useUrlPage } from '@/lib/use-url-page'

interface ExportRecord {
  id: number
  format: string
  created_at: string
  book_title: string
  book_slug: string
  book: Book | null
}

interface ExportPage {
  items: ExportRecord[]
  total: number
  page: number
  page_size: number
}

const FORMAT_TONE: Record<string, 'primary' | 'slate'> = { pdf: 'primary', docx: 'primary', epub: 'primary', markdown: 'slate', zip: 'slate' }

export default function MyExports() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<ExportPage | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!user) return
    setLoading(true)
    api<ExportPage>('/users/me/exports', { params: { page, page_size: 20 } })
      .then(setData)
      .catch(() => setData({ items: [], total: 0, page: 1, page_size: 20 }))
      .finally(() => setLoading(false))
  }, [user, page])

  if (!user) return <Loading className="min-h-[60vh]" label={t('account.common.verifying')} />

  return (
    <>
      <Seo siteName={siteName} title={t('exports.title')} noindex />
      <Container>
        <div className="flex flex-wrap items-start justify-between gap-3 pb-6">
          <div>
            <h1 className="text-2xl font-bold text-ink">{t('exports.title')}</h1>
            <p className="mt-1 text-sm text-slate-500">{t('exports.subtitle')}</p>
          </div>
          <ButtonLink href="/user/export" variant="outline" className="shrink-0">
            <DownloadIcon className="h-4 w-4" /> {t('exports.batchButton')}
          </ButtonLink>
        </div>
        <MyLibraryTabs active="export" />

        {loading || data === null ? (
          <Loading label={t('exports.loading')} />
        ) : data.total === 0 ? (
          <EmptyState>{t('exports.empty')}</EmptyState>
        ) : (
          <>
            <ul className="divide-y divide-slate-100 overflow-hidden rounded-xl border border-slate-200 bg-white">
              {data.items.map((r) => (
                <li key={r.id} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3.5">
                  <span className="min-w-0 flex-1">
                    {r.book ? (
                      <Link href={`/book/detail/${encodeURIComponent(r.book.slug || r.book_slug)}`}
                        className="truncate font-medium text-slate-900 hover:text-primary-600">{r.book.title || r.book_title}</Link>
                    ) : (
                      <span className="truncate font-medium text-slate-500">{r.book_title}<span className="ml-1 text-xs text-slate-400">（{t('exports.deletedBook')}）</span></span>
                    )}
                  </span>
                  <Badge tone={FORMAT_TONE[r.format] || 'slate'}>{r.format.toUpperCase()}</Badge>
                  <span className="shrink-0 text-xs tabular-nums text-slate-400">{r.created_at}</span>
                </li>
              ))}
            </ul>
            <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} />
          </>
        )}
      </Container>
    </>
  )
}
