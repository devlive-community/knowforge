import { useEffect, useState } from 'react'
import Link from 'next/link'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import Seo from '@/components/Seo'
import { api, formatDate } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, Card, EmptyState, Loading, Pagination } from '@/components/ui'
import { formatPrice } from '@/lib/commerce'
import { useUrlPage } from '@/lib/use-url-page'

interface Item {
  purchase: { id: number; doc_id: number; amount_cents: number; currency: string; created_at: string }
  book: { id: number; title: string; slug: string }
  doc?: { id: number; title: string; slug: string }
}

export default function MyPurchasesPage() {
  return <FeatureGate feature="paid-content"><MyPurchasesInner /></FeatureGate>
}

// 我的购买：已解锁的整本书与章节，可直接前往阅读。
function MyPurchasesInner() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t, locale } = useTranslation()
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<{ items: Item[]; total: number; page: number; page_size: number } | null>(null)
  useEffect(() => {
    if (!user) return
    api<{ items: Item[]; total: number; page: number; page_size: number }>('/users/me/purchases', { params: { page, page_size: 20 } }).then(setData).catch(() => {})
  }, [user, page])
  if (!user || !data) return <Loading className="min-h-[60vh]" />
  return (
    <>
      <Seo siteName={site.site_name || 'KnowForge'} title={t('paid.purchases.title')} noindex />
      <Container>
        <div className="py-8">
          <h1 className="text-2xl font-bold text-ink">{t('paid.purchases.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('paid.purchases.subtitle')}</p>
          {data.items.length === 0 ? <div className="mt-6"><EmptyState>{t('paid.purchases.empty')}</EmptyState></div> : (
            <Card className="mt-6 divide-y divide-slate-100">
              {data.items.map(({ purchase: p, book, doc }) => (
                <Link key={p.id} href={doc ? `/book/reader/${encodeURIComponent(book.slug)}/${encodeURIComponent(doc.slug)}` : `/book/detail/${encodeURIComponent(book.slug)}`}
                  className="flex flex-wrap items-center justify-between gap-3 px-5 py-4 hover:bg-slate-50">
                  <span className="flex min-w-0 items-center gap-2">
                    <Badge tone={doc ? 'slate' : 'amber'}>{doc ? t('paid.purchases.chapter') : t('paid.purchases.book')}</Badge>
                    <span className="truncate font-medium text-slate-900">{book.title}{doc ? ` · ${doc.title}` : ''}</span>
                  </span>
                  <span className="text-xs text-slate-400">{formatPrice(p.amount_cents, p.currency, locale)} · {formatDate(p.created_at)}</span>
                </Link>
              ))}
            </Card>
          )}
          {data.total > data.page_size && <div className="mt-4"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
        </div>
      </Container>
    </>
  )
}
