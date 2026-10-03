import { useEffect, useState } from 'react'
import Link from 'next/link'
import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import Seo from '@/components/Seo'
import ModerationHits from '@/components/ModerationHits'
import { api, formatDate } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, Card, EmptyState, Loading, Pagination } from '@/components/ui'
import { MODERATION_STATUS_TONE, type ModerationCaseItem } from '@/lib/moderation'
import { useUrlPage } from '@/lib/use-url-page'

export default function MyModerationPage() {
  return <FeatureGate feature="moderation"><MyModerationInner /></FeatureGate>
}

// 我的审核：自己的章节/书籍的审核记录（待审核、已通过、已驳回），驳回时可看到命中位置与复审意见。
function MyModerationInner() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<{ items: ModerationCaseItem[]; total: number; page: number; page_size: number } | null>(null)
  useEffect(() => {
    if (!user) return
    api<{ items: ModerationCaseItem[]; total: number; page: number; page_size: number }>('/users/me/moderation-cases', { params: { page, page_size: 20 } }).then(setData).catch(() => {})
  }, [user, page])

  if (!user || !data) return <Loading className="min-h-[60vh]" />
  return (
    <>
      <Seo siteName={site.site_name || 'KnowForge'} title={t('moderation.mine.title')} noindex />
      <Container>
        <div className="py-8">
          <h1 className="text-2xl font-bold text-ink">{t('moderation.mine.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('moderation.mine.subtitle')}</p>
          {data.items.length === 0 ? <div className="mt-6"><EmptyState>{t('moderation.mine.empty')}</EmptyState></div> : (
            <div className="mt-6 space-y-3">
              {data.items.map((item) => {
                const c = item.case
                const edit = item.book ? `/book/writer/${encodeURIComponent(item.book.slug)}` : undefined
                return (
                  <Card key={c.id} className="p-4">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <div className="flex min-w-0 flex-wrap items-center gap-2">
                        <Badge tone={MODERATION_STATUS_TONE[c.status]}>{t(`moderation.status.${c.status}`)}</Badge>
                        <Badge>{t(`moderation.kind.${c.kind}`)}</Badge>
                        <span className="font-medium text-slate-900">{c.title}</span>
                        {item.book && c.kind === 'document' && <span className="text-xs text-slate-400">《{item.book.title}》</span>}
                      </div>
                      <span className="flex items-center gap-3 text-xs text-slate-400">
                        {formatDate(c.updated_at)}
                        {edit && c.status !== 'approved' && <Link href={edit} className="text-primary-600 hover:underline">{t('moderation.mine.edit')}</Link>}
                      </span>
                    </div>
                    {c.status === 'pending' && <p className="mt-2 text-xs text-slate-500">{t('moderation.mine.pendingHint')}</p>}
                    {c.review_note && <p className="mt-3 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-800">{t('moderation.reviewNote')}：{c.review_note}</p>}
                    {c.hits.length > 0 && c.status !== 'approved' && <div className="mt-3"><ModerationHits hits={c.hits} /></div>}
                  </Card>
                )
              })}
            </div>
          )}
          {data.total > data.page_size && <div className="mt-4"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
        </div>
      </Container>
    </>
  )
}
