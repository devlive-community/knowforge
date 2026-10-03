import { useCallback, useEffect, useState } from 'react'
import Link from 'next/link'
import Container from '@/components/Container'
import Seo from '@/components/Seo'
import { api, formatDate } from '@/lib/api'
import { useApp, useRequireAuth } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import type { PageResult, TrashItem } from '@/lib/types'
import { Badge, Button, Card, EmptyState, Loading, Pagination, SegmentedTabs, useFeedback } from '@/components/ui'
import { useUrlPage } from '@/lib/use-url-page'

type TrashType = 'book' | 'document'

function remainingDays(expiresAt: string): number {
  return Math.max(0, Math.ceil((new Date(expiresAt).getTime() - Date.now()) / 86_400_000))
}

export default function TrashPage() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { showToast, confirmAction } = useFeedback()
  const { t } = useTranslation()
  const [type, setType] = useState<TrashType>('book')
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<PageResult<TrashItem> | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState<string | null>(null)
  const siteName = site.site_name || 'KnowForge'

  const load = useCallback(async () => {
    if (!user) return
    setLoading(true)
    try {
      const result = await api<PageResult<TrashItem>>('/trash', { params: { type, page, page_size: 10 } })
      setData({ ...result, items: result.items || [] })
    } catch (error) {
      setData({ items: [], total: 0, page: 1, page_size: 10 })
      showToast({ title: t('user.trash.loadFailed'), message: (error as Error).message, tone: 'error' })
    } finally {
      setLoading(false)
    }
  }, [page, showToast, type, user, t])

  useEffect(() => { load() }, [load])

  function switchType(next: TrashType) {
    if (next === type) return
    setType(next)
    setPage(1)
    setData(null)
  }

  async function restore(item: TrashItem) {
    const key = `restore-${item.type}-${item.id}`
    setBusy(key)
    try {
      await api(`/trash/${item.type === 'book' ? 'books' : 'documents'}/${item.id}/restore`, { method: 'POST' })
      showToast({ title: t('user.trash.restoreSuccess'), message: item.type === 'book' ? t('user.trash.restoreBookSuccess', { title: item.title }) : t('user.trash.restoreDocumentSuccess', { title: item.title }), tone: 'success' })
      await load()
    } catch (error) {
      showToast({ title: t('user.trash.restoreFailed'), message: (error as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  async function removePermanently(item: TrashItem) {
    const label = item.type === 'book' ? t('user.trash.bookAndContent', { title: item.title }) : t('user.trash.documentAndChildren', { title: item.title })
    const confirmed = await confirmAction({
      title: t('user.trash.permanentDelete'),
      message: t('user.trash.permanentDeleteConfirm', { label }),
      confirmLabel: t('user.trash.permanentDelete'),
      danger: true,
    })
    if (!confirmed) return
    const key = `delete-${item.type}-${item.id}`
    setBusy(key)
    try {
      await api(`/trash/${item.type === 'book' ? 'books' : 'documents'}/${item.id}`, { method: 'DELETE' })
      showToast({ title: t('user.trash.permanentDeleteSuccess'), message: t('user.trash.removedFromTrash', { title: item.title }), tone: 'success' })
      if (data && data.items.length === 1 && page > 1) setPage(page - 1)
      else await load()
    } catch (error) {
      showToast({ title: t('user.trash.permanentDeleteFailed'), message: (error as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  if (!user) return <Loading className="min-h-[60vh]" label={t('user.trash.verifyingAuth')} />

  return (
    <>
      <Seo siteName={siteName} title={t('user.trash.title')} noindex />
      <Container>
        <nav className="flex items-center gap-1.5 pb-4 text-sm text-slate-500">
          <Link href="/books" className="hover:text-primary-600">{t('user.trash.myBooks')}</Link>
          <span className="text-slate-300">/</span>
          <span className="text-slate-900">{t('user.trash.title')}</span>
        </nav>

        <div className="flex flex-col justify-between gap-4 pb-6 sm:flex-row sm:items-end">
          <div>
            <h1 className="text-3xl font-bold text-ink">{t('user.trash.title')}</h1>
            <p className="mt-2 text-[15px] text-slate-500">{t('user.trash.description')}</p>
          </div>
          <Button variant="outline" onClick={load} disabled={loading}>
            <i className="fa-solid fa-rotate" aria-hidden="true" /> {t('user.trash.refresh')}
          </Button>
        </div>

        <SegmentedTabs className="mb-5" value={type} ariaLabel={t('user.trash.contentType')} onChange={(value) => switchType(value as TrashType)} items={[
          { value: 'book', label: t('user.trash.books'), icon: <i className="fa-solid fa-book" aria-hidden="true" /> },
          { value: 'document', label: t('user.trash.chapters'), icon: <i className="fa-regular fa-file-lines" aria-hidden="true" /> },
        ]} />

        {loading || data === null ? (
          <Loading className="py-20" label={t('user.trash.loading')} />
        ) : data.items.length === 0 ? (
          <EmptyState>
            <i className="fa-regular fa-trash-can mb-3 block text-2xl text-slate-300" aria-hidden="true" />
            {t('user.trash.empty', { type: type === 'book' ? t('user.trash.books') : t('user.trash.chapters') })}
          </EmptyState>
        ) : (
          <div className="space-y-3">
            {data.items.map((item) => {
              const days = remainingDays(item.expires_at)
              const restoreKey = `restore-${item.type}-${item.id}`
              const deleteKey = `delete-${item.type}-${item.id}`
              return (
                <Card key={`${item.type}-${item.id}`} className="p-4 sm:p-5">
                  <div className="flex flex-col gap-4 sm:flex-row sm:items-center">
                    <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-slate-100 text-slate-500">
                      <i className={`fa-solid ${item.type === 'book' ? 'fa-book' : 'fa-file-lines'}`} aria-hidden="true" />
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <h2 className="truncate font-semibold text-slate-900">{item.title}</h2>
                        <Badge tone={days <= 3 ? 'rose' : days <= 7 ? 'amber' : 'slate'}>{t('user.trash.daysRemaining', { days })}</Badge>
                      </div>
                      <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 text-xs text-slate-500">
                        {item.book_title && <span>{t('user.trash.belongsTo', { title: item.book_title })}</span>}
                        <span>{item.type === 'book' ? t('user.trash.chapterCount', { count: item.descendant_count }) : t('user.trash.childCount', { count: item.descendant_count })}</span>
                        <span>{t('user.trash.deletedAt', { date: formatDate(item.deleted_at) })}</span>
                        {user.role === 'admin' && item.owner_username && <span>{t('user.trash.owner', { username: item.owner_username })}</span>}
                      </div>
                    </div>
                    <div className="flex shrink-0 gap-2 sm:justify-end">
                      <Button variant="outline" loading={busy === restoreKey} disabled={busy !== null} onClick={() => restore(item)}>
                        <i className="fa-solid fa-rotate-left" aria-hidden="true" /> {t('user.trash.restore')}
                      </Button>
                      <Button variant="danger" loading={busy === deleteKey} disabled={busy !== null} onClick={() => removePermanently(item)}>
                        <i className="fa-solid fa-trash" aria-hidden="true" /> {t('user.trash.permanentDelete')}
                      </Button>
                    </div>
                  </div>
                </Card>
              )
            })}
            <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} />
          </div>
        )}
      </Container>
    </>
  )
}
