import { useCallback, useEffect, useState } from 'react'
import type { InferGetServerSidePropsType } from 'next'
import { useRouter } from 'next/router'
import BookSettingsLayout from '@/components/BookSettingsLayout'
import { requireBookSettingsFeature } from '@/lib/book-settings'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, ButtonLink, EmptyState, Loading, Pagination, SegmentedTabs, useFeedback } from '@/components/ui'
import { ChevronRightIcon } from '@/components/icons'
import { entitlementAllowed } from '@/lib/entitlements'

export const getServerSideProps = requireBookSettingsFeature('content-collect')

interface CrawlJob {
  id: number
  kind: string
  root_url: string
  status: string
  total: number
  success: number
  failed: number
  last_error: string
  created_at: string
}
interface CrawlPage {
  id: number
  url: string
  title: string
  depth: number
  status: string
  error: string
  doc_id: number
}

interface CrawlJobsPage {
  items: CrawlJob[]
  total: number
  page: number
  page_size: number
}

interface CrawlJobDetail {
  job: CrawlJob
  pages: CrawlPage[]
  pages_total: number
  pages_page: number
  pages_page_size: number
}

const CRAWL_JOBS_PAGE_SIZE = 10
const CRAWL_PAGES_PAGE_SIZE = 25

const JOB_TONE: Record<string, 'slate' | 'primary' | 'emerald' | 'amber' | 'rose'> = {
  preview: 'slate', pending: 'primary', running: 'primary', succeeded: 'emerald', partial: 'amber', failed: 'rose',
}
const PAGE_TONE: Record<string, 'slate' | 'primary' | 'emerald' | 'rose'> = {
  pending: 'slate', running: 'primary', success: 'emerald', failed: 'rose', skipped: 'slate',
}

export default function CrawlHistoryPage({ book }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const { site, user: me } = useApp()
  const { showToast } = useFeedback()
  const router = useRouter()
  const kind = router.query.kind === 'chapter' ? 'chapter' : 'site'
  const jobId = router.query.job ? Number(router.query.job) : 0

  const [jobs, setJobs] = useState<CrawlJobsPage | null>(null)
  const [page, setPage] = useState(1)
  const [detail, setDetail] = useState<CrawlJobDetail | null>(null)
  const [detailPage, setDetailPage] = useState(1)
  const [detailLoadFailed, setDetailLoadFailed] = useState(false)
  const [busy, setBusy] = useState<number | 'all' | null>(null)

  const loadJobs = useCallback(() => {
    setJobs(null)
    api<CrawlJobsPage>(`/books/${book.id}/collect/jobs`, { params: { kind, page, page_size: CRAWL_JOBS_PAGE_SIZE } })
      .then(setJobs)
      .catch(() => setJobs({ items: [], total: 0, page: 1, page_size: CRAWL_JOBS_PAGE_SIZE }))
  }, [book.id, kind, page])

  const loadDetail = useCallback(() => {
    if (!jobId) { setDetail(null); setDetailLoadFailed(false); return }
    setDetail(null)
    setDetailLoadFailed(false)
    api<CrawlJobDetail>(`/collect/jobs/${jobId}`, { params: { page: detailPage, page_size: CRAWL_PAGES_PAGE_SIZE } })
      .then((result) => {
        setDetail(result)
        if (result.pages_page !== detailPage) setDetailPage(result.pages_page)
      })
      .catch(() => { setDetail(null); setDetailLoadFailed(true) })
  }, [jobId, detailPage])

  useEffect(() => { if (!jobId) loadJobs() }, [jobId, loadJobs])
  useEffect(() => { loadDetail() }, [loadDetail])

  const goKind = (k: string) => {
    setPage(1)
    return router.push({ pathname: router.pathname, query: { slug: book.slug, kind: k } }, undefined, { shallow: false })
  }
  const openJob = (id: number) => {
    setDetailPage(1)
    return router.push({ pathname: router.pathname, query: { slug: book.slug, job: id } })
  }
  const backToList = () => router.push({ pathname: router.pathname, query: { slug: book.slug, kind } })

  async function retryJob(id: number) {
    setBusy('all')
    try { const r = await api<{ retried: number }>(`/collect/jobs/${id}/retry`, { method: 'POST' }); showToast({ message: t('crawlHistory.retried', { n: r.retried }), tone: 'success' }); loadDetail() }
    catch (e) { showToast({ title: t('crawlHistory.retryFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setBusy(null) }
  }
  async function retryPage(id: number) {
    setBusy(id)
    try { await api(`/collect/pages/${id}/retry`, { method: 'POST' }); showToast({ message: t('crawlHistory.retriedOne'), tone: 'success' }); loadDetail() }
    catch (e) { showToast({ title: t('crawlHistory.retryFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setBusy(null) }
  }

  return (
    <BookSettingsLayout book={book} active="crawl-history">
      <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-lg font-bold text-slate-900">{t('crawlHistory.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('crawlHistory.subtitle')}</p>
        </div>
        {entitlementAllowed(me, 'collect.site', site.collect_site_enabled !== false) && (
          <ButtonLink href={`/books/collect?book_id=${book.id}&book=${encodeURIComponent(book.slug)}`} variant="outline" className="shrink-0">
            <i className="fa-solid fa-spider" aria-hidden="true" /> {t('crawlHistory.newCrawl')}
          </ButtonLink>
        )}
      </div>

      {detail ? (
        <div>
          <button onClick={backToList} className="mb-4 text-sm text-primary-600 hover:underline">← {t('crawlHistory.back')}</button>
          <div className="mb-4 flex flex-wrap items-center gap-2 rounded-xl border border-slate-200 bg-white p-4">
            <Badge tone={JOB_TONE[detail.job.status] || 'slate'}>{t(`crawlHistory.status.${detail.job.status}`)}</Badge>
            <span className="truncate text-sm text-slate-600">{detail.job.root_url}</span>
            <span className="ml-auto text-xs text-slate-400">{t('crawlHistory.counts', { success: detail.job.success, failed: detail.job.failed, total: detail.job.total })}</span>
            {detail.job.failed > 0 && (
              <Button size="sm" variant="outline" loading={busy === 'all'} onClick={() => retryJob(detail.job.id)}>{t('crawlHistory.retryAllFailed')}</Button>
            )}
          </div>
          <ul className="divide-y divide-slate-100 overflow-hidden rounded-xl border border-slate-200 bg-white">
            {detail.pages.map((p) => (
              <li key={p.id} className="flex items-center gap-3 px-4 py-3" style={{ paddingLeft: `${p.depth * 16 + 16}px` }}>
                <Badge tone={PAGE_TONE[p.status] || 'slate'}>{t(`crawlHistory.pageStatus.${p.status}`)}</Badge>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm text-slate-800">{p.title || p.url}</span>
                  {p.status === 'failed' && p.error && <span className="block truncate text-xs text-rose-500">{p.error}</span>}
                </span>
                {p.doc_id > 0 && (
                  <a href={`/book/writer/${encodeURIComponent(book.slug)}`} className="shrink-0 text-slate-300 hover:text-primary-500"><ChevronRightIcon className="h-4 w-4" /></a>
                )}
                {p.status === 'failed' && (
                  <Button size="sm" variant="ghost" loading={busy === p.id} onClick={() => retryPage(p.id)}>{t('crawlHistory.retry')}</Button>
                )}
              </li>
            ))}
          </ul>
          <Pagination page={detail.pages_page} pageSize={detail.pages_page_size} total={detail.pages_total} onChange={setDetailPage} />
        </div>
      ) : jobId && !detailLoadFailed ? (
        <Loading label={t('crawlHistory.loading')} />
      ) : (
        <>
          <SegmentedTabs className="mb-4 max-w-sm" value={kind} ariaLabel={t('crawlHistory.title')} onChange={goKind}
            items={[{ value: 'site', label: t('crawlHistory.kindSite') }, { value: 'chapter', label: t('crawlHistory.kindChapter') }]} />
          {jobs === null ? (
            <Loading label={t('crawlHistory.loading')} />
          ) : jobs.total === 0 ? (
            <EmptyState>{t('crawlHistory.empty')}</EmptyState>
          ) : (
            <>
              <ul className="divide-y divide-slate-100 overflow-hidden rounded-xl border border-slate-200 bg-white">
                {jobs.items.map((j) => (
                  <li key={j.id}>
                    <button onClick={() => openJob(j.id)} className="flex w-full items-center gap-3 px-4 py-3.5 text-left hover:bg-slate-50">
                      <Badge tone={JOB_TONE[j.status] || 'slate'}>{t(`crawlHistory.status.${j.status}`)}</Badge>
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm font-medium text-slate-800">{j.root_url}</span>
                        <span className="text-xs text-slate-400">{t('crawlHistory.counts', { success: j.success, failed: j.failed, total: j.total })} · {j.created_at}</span>
                      </span>
                      <ChevronRightIcon className="h-4 w-4 shrink-0 text-slate-300" />
                    </button>
                  </li>
                ))}
              </ul>
              <Pagination page={jobs.page} pageSize={jobs.page_size} total={jobs.total} onChange={setPage} />
            </>
          )}
        </>
      )}
    </BookSettingsLayout>
  )
}
