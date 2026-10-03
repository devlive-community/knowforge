import { useEffect, useState } from 'react'
import Link from 'next/link'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import Seo from '@/components/Seo'
import { api, formatDate } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, Card, EmptyState, Loading, Pagination } from '@/components/ui'
import { durationLabel, formatPrice } from '@/lib/commerce'
import { CHANNEL_ICONS, STATUS_TONE, type PaymentOrder } from '@/lib/payment'
import { useUrlPage } from '@/lib/use-url-page'

export default function MyOrdersPage() {
  return <FeatureGate feature="payment"><MyOrdersInner /></FeatureGate>
}

function MyOrdersInner() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t, locale } = useTranslation()
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<{ items: PaymentOrder[]; total: number; page: number; page_size: number } | null>(null)

  useEffect(() => {
    if (!user) return
    api<{ items: PaymentOrder[]; total: number; page: number; page_size: number }>('/users/me/orders', { params: { page, page_size: 20 } }).then(setData).catch(() => {})
  }, [user, page])

  if (!user || !data) return <Loading className="min-h-[60vh]" />
  return (
    <>
      <Seo siteName={site.site_name || 'KnowForge'} title={t('payment.orders.title')} noindex />
      <Container>
        <div className="py-8">
          <h1 className="text-2xl font-bold text-ink">{t('payment.orders.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('payment.orders.subtitle')}</p>
          {data.items.length === 0 ? <div className="mt-6"><EmptyState>{t('payment.orders.empty')}</EmptyState></div> : (
            <Card className="mt-6 divide-y divide-slate-100">
              {data.items.map((o) => (
                <Link key={o.id} href={`/pay/orders/${o.order_no}`} className="flex flex-wrap items-center justify-between gap-3 px-5 py-4 hover:bg-slate-50">
                  <span className="flex min-w-0 items-center gap-3">
                    <i className={`${CHANNEL_ICONS[o.channel]} w-5 text-center text-lg text-slate-400`} aria-hidden="true" />
                    <span className="min-w-0">
                      <span className="block truncate font-medium text-slate-900">{o.title}{o.duration_days > 0 ? ` · ${durationLabel(t, o.duration_days)}` : ''}</span>
                      <span className="block text-xs text-slate-400">{o.order_no} · {formatDate(o.created_at)}</span>
                    </span>
                  </span>
                  <span className="flex items-center gap-3">
                    {o.discount_cents > 0 && <span className="text-xs tabular-nums text-slate-400 line-through">{formatPrice(o.original_cents, o.currency, locale)}</span>}
                    <span className="font-bold tabular-nums text-slate-900">{formatPrice(o.amount_cents, o.currency, locale)}</span>
                    <Badge tone={STATUS_TONE[o.status]}>{t(`payment.status.${o.status}`)}</Badge>
                    {o.status === 'paid' && o.refunded_cents > 0 && <Badge tone="slate">{t('payment.refund.partial')}</Badge>}
                  </span>
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
