import { useCallback, useEffect, useState } from 'react'
import Link from 'next/link'
import Container from '@/components/Container'
import Seo from '@/components/Seo'
import { api, formatDate, API_BASE, getToken } from '@/lib/api'
import { useApp, useRequireAuth } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, Card, EmptyState, Loading, Pagination, SegmentedTabs, useFeedback } from '@/components/ui'
import type { ReadingAnnotation } from '@/lib/reading-annotations'
import { useUrlPage } from '@/lib/use-url-page'

type Filter = 'all' | 'note' | 'highlight' | 'bookmark'

interface MyAnnotation extends ReadingAnnotation {
  book_title: string
  book_slug: string
  document_title: string
  document_slug: string
}

interface AnnotationPage {
  items: MyAnnotation[]
  total: number
  page: number
  page_size: number
}

export default function MyNotesPage() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { showToast, confirmAction } = useFeedback()
  const { t } = useTranslation()
  const [filter, setFilter] = useState<Filter>('all')
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<AnnotationPage | null>(null)
  const [loading, setLoading] = useState(true)
  const [deleting, setDeleting] = useState<number | null>(null)
  const [exporting, setExporting] = useState(false)
  const siteName = site.site_name || 'KnowForge'

  const filters = [
    { value: 'all', label: t('user.notes.filterAll') },
    { value: 'note', label: t('user.notes.filterNote'), icon: <i className="fa-solid fa-note-sticky" aria-hidden="true" /> },
    { value: 'highlight', label: t('user.notes.filterHighlight'), icon: <i className="fa-solid fa-highlighter" aria-hidden="true" /> },
    { value: 'bookmark', label: t('user.notes.filterBookmark'), icon: <i className="fa-solid fa-bookmark" aria-hidden="true" /> },
  ]

  function kindLabel(kind: MyAnnotation['kind']) {
    return kind === 'note' ? t('user.notes.filterNote') : kind === 'highlight' ? t('user.notes.filterHighlight') : t('user.notes.filterBookmark')
  }

  async function exportNotes() {
    setExporting(true)
    try {
      const token = getToken()
      const res = await fetch(`${API_BASE}/api/v1/users/me/annotations/export`, {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      })
      if (!res.ok) {
        const msg = await res.json().then((p) => p.message).catch(() => '')
        throw new Error(msg || t('user.notes.exportFailed'))
      }
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `my-notes-${new Date().toISOString().slice(0, 10)}.md`
      a.click()
      URL.revokeObjectURL(url)
    } catch (error) {
      showToast({ title: t('user.notes.exportFailed'), message: (error as Error).message, tone: 'error' })
    } finally {
      setExporting(false)
    }
  }

  const load = useCallback(async () => {
    if (!user) return
    setLoading(true)
    try {
      const result = await api<AnnotationPage>('/users/me/annotations', {
        params: { kind: filter === 'all' ? undefined : filter, page, page_size: 12 },
      })
      setData({ ...result, items: result.items || [] })
    } catch (error) {
      setData({ items: [], total: 0, page: 1, page_size: 12 })
      showToast({ title: t('user.notes.loadFailed'), message: (error as Error).message, tone: 'error' })
    } finally {
      setLoading(false)
    }
  }, [filter, page, showToast, user, t])

  useEffect(() => { void load() }, [load])

  function switchFilter(next: string) {
    setFilter(next as Filter)
    setPage(1)
    setData(null)
  }

  async function remove(item: MyAnnotation) {
    const confirmed = await confirmAction({
      title: t('user.notes.deleteConfirmTitle', { kind: kindLabel(item.kind) }),
      message: t('user.notes.deleteConfirmMessage'),
      confirmLabel: t('user.notes.delete'),
      danger: true,
    })
    if (!confirmed) return
    setDeleting(item.id)
    try {
      await api(`/annotations/${item.id}`, { method: 'DELETE' })
      showToast({ message: t('user.notes.deleteSuccess', { kind: kindLabel(item.kind) }), tone: 'success' })
      if (data?.items.length === 1 && page > 1) setPage((current) => current - 1)
      else await load()
    } catch (error) {
      showToast({ title: t('user.notes.deleteFailed'), message: (error as Error).message, tone: 'error' })
    } finally {
      setDeleting(null)
    }
  }

  if (!user) return <Loading className="min-h-[60vh]" label={t('user.notes.verifyingAuth')} />

  return (
    <>
      <Seo siteName={siteName} title={t('user.notes.title')} noindex />
      <Container>
        <div className="flex flex-col justify-between gap-4 pb-6 sm:flex-row sm:items-end">
          <div>
            <h1 className="text-3xl font-bold text-ink">{t('user.notes.title')}</h1>
            <p className="mt-2 text-sm text-slate-500">{t('user.notes.description')}</p>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" disabled={loading} onClick={() => void load()}><i className="fa-solid fa-rotate" aria-hidden="true" />{t('user.notes.refresh')}</Button>
            <Button variant="outline" loading={exporting} disabled={loading || (data?.total ?? 0) === 0} onClick={() => void exportNotes()}><i className="fa-solid fa-file-arrow-down" aria-hidden="true" />{t('user.notes.export')}</Button>
          </div>
        </div>

        <SegmentedTabs className="mb-5" value={filter} items={filters} ariaLabel={t('user.notes.filterLabel')} onChange={switchFilter} />

        {loading || data === null ? (
          <Loading className="py-20" label={t('user.notes.loading')} />
        ) : data.items.length === 0 ? (
          <EmptyState>
            <i className="fa-regular fa-note-sticky mb-3 block text-2xl text-slate-300" aria-hidden="true" />
            {t('user.notes.empty', { kind: filter === 'all' ? t('user.notes.annotations') : kindLabel(filter as MyAnnotation['kind']) })}
          </EmptyState>
        ) : (
          <>
            <div className="grid gap-4 lg:grid-cols-2">
              {data.items.map((item) => (
                <Card key={item.id} className="flex min-w-0 flex-col p-5">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge tone={item.kind === 'note' ? 'primary' : item.kind === 'highlight' ? 'amber' : 'slate'}>{kindLabel(item.kind)}</Badge>
                        {item.anchor_status === 'orphaned' && <Badge tone="rose">{t('user.notes.orphaned')}</Badge>}
                        {item.anchor_status === 'relocated' && <Badge tone="slate">{t('user.notes.relocated')}</Badge>}
                      </div>
                      <Link href={`/book/reader/${encodeURIComponent(item.book_slug)}/${encodeURIComponent(item.document_slug)}`}
                        className="mt-3 block truncate font-semibold text-slate-900 hover:text-primary-600">
                        {item.document_title}
                      </Link>
                      <p className="mt-1 truncate text-xs text-slate-400">《{item.book_title}》</p>
                    </div>
                    <Button size="sm" variant="ghost" loading={deleting === item.id} onClick={() => void remove(item)} className="shrink-0 text-rose-600 hover:bg-rose-50">
                      <i className="fa-solid fa-trash" aria-hidden="true" />{t('user.notes.delete')}
                    </Button>
                  </div>
                  {item.quote && <blockquote className="mt-4 line-clamp-4 border-l-2 border-primary-300 pl-3 text-sm leading-6 text-slate-600">{item.quote}</blockquote>}
                  {item.note && <p className="mt-3 line-clamp-4 whitespace-pre-wrap text-sm leading-6 text-slate-800">{item.note}</p>}
                  <p className="mt-auto pt-4 text-xs text-slate-400">{t('user.notes.updatedAt')} {formatDate(item.updated_at)}</p>
                </Card>
              ))}
            </div>
            <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} />
          </>
        )}
      </Container>
    </>
  )
}
