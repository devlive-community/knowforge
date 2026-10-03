import Link from 'next/link'
import { useCallback, useEffect, useState } from 'react'
import AdminLayout from '@/components/AdminLayout'
import UserAvatar from '@/components/UserAvatar'
import { api, formatDate, formatNumber } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { resolveMediaUrl } from '@/lib/media'
import { Badge, DropdownMenu, EmptyState, Input, Loading, Pagination, Select, useFeedback } from '@/components/ui'
import { EyeIcon, FileTextIcon, GearIcon, SearchIcon, TrashIcon } from '@/components/icons'
import { useTranslation } from '@/lib/i18n'
import type { Book, BookStatus, PageResult } from '@/lib/types'
import { useUrlPage } from '@/lib/use-url-page'

const PAGE_SIZE = 15

export default function AdminBooks() {
  const { user } = useApp()
  const { confirmAction } = useFeedback()
  const { t } = useTranslation()
  const isAdmin = user?.role === 'admin'
  const [items, setItems] = useState<Book[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useUrlPage()
  const [q, setQ] = useState('')
  const [status, setStatus] = useState('')
  const [visibility, setVisibility] = useState('')
  const [sort, setSort] = useState('created_at_desc')
  const [loading, setLoading] = useState(true)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [menuFor, setMenuFor] = useState<number | null>(null)
  const [notice, setNotice] = useState<{ message: string; error?: boolean } | null>(null)

  const statusOptions = [
    { value: '', label: t('admin.books.status.all') },
    { value: 'draft', label: t('book.status.draft') },
    { value: 'in_progress', label: t('book.status.in_progress') },
    { value: 'published', label: t('book.status.published') },
    { value: 'completed', label: t('book.status.completed') },
    { value: 'archived', label: t('book.status.archived') },
  ]

  const visibilityOptions = [
    { value: '', label: t('admin.books.visibility.all') },
    { value: 'public', label: t('admin.books.visibility.public') },
    { value: 'private', label: t('admin.books.visibility.private') },
  ]

  const sortOptions = [
    { value: 'created_at_desc', label: t('admin.books.sort.newest') },
    { value: 'created_at_asc', label: t('admin.books.sort.oldest') },
    { value: 'updated_at_desc', label: t('admin.books.sort.recently_updated') },
    { value: 'updated_at_asc', label: t('admin.books.sort.least_recently_updated') },
    { value: 'view_count_desc', label: t('admin.books.sort.views_high_to_low') },
    { value: 'view_count_asc', label: t('admin.books.sort.views_low_to_high') },
  ]

  const rowStatusOptions = statusOptions.filter((option) => option.value)
  const rowVisibilityOptions = visibilityOptions.filter((option) => option.value)

  const load = useCallback(async () => {
    setLoading(true)
    setNotice(null)
    try {
      const result = await api<PageResult<Book>>('/admin/books', {
        params: { page, page_size: PAGE_SIZE, q, status, visibility, sort },
      })
      setItems(result.items)
      setTotal(result.total)
    } catch (error) {
      setNotice({ message: (error as Error).message, error: true })
    } finally {
      setLoading(false)
    }
  }, [page, q, sort, status, visibility])

  useEffect(() => {
    if (isAdmin) load()
  }, [isAdmin, load])

  async function updateBook(book: Book, changes: Partial<Pick<Book, 'status' | 'is_public'>>) {
    setBusyId(book.id)
    setNotice(null)
    try {
      const updated = await api<Book>(`/books/${book.id}`, { method: 'PUT', body: changes })
      setItems((current) => current.map((item) => item.id === book.id
        ? { ...item, ...updated, chapter_count: item.chapter_count, tags: item.tags, user: item.user }
        : item))
      setNotice({ message: t('admin.books.updated', { title: book.title }) })
    } catch (error) {
      setNotice({ message: (error as Error).message, error: true })
    } finally {
      setBusyId(null)
    }
  }

  async function removeBook(book: Book) {
    if (!await confirmAction({
      title: t('admin.books.trash.title'),
      message: t('admin.books.trash.message', { title: book.title }),
      confirmLabel: t('admin.books.trash.confirm'),
      danger: true,
    })) return

    setBusyId(book.id)
    setMenuFor(null)
    setNotice(null)
    try {
      await api(`/books/${book.id}`, { method: 'DELETE' })
      if (items.length === 1 && page > 1) setPage((current) => current - 1)
      else load()
    } catch (error) {
      setNotice({ message: (error as Error).message, error: true })
    } finally {
      setBusyId(null)
    }
  }

  function statusLabel(status: BookStatus): string {
    const labels: Record<BookStatus, string> = {
      draft: t('book.status.draft'), in_progress: t('book.status.in_progress'), published: t('book.status.published'), completed: t('book.status.completed'), archived: t('book.status.archived'),
    }
    return labels[status]
  }

  function statusTone(status: BookStatus): 'slate' | 'primary' | 'emerald' | 'violet' | 'amber' {
    const tones: Record<BookStatus, 'slate' | 'primary' | 'emerald' | 'violet' | 'amber'> = {
      draft: 'slate', in_progress: 'primary', published: 'emerald', completed: 'violet', archived: 'amber',
    }
    return tones[status]
  }

  return (
    <AdminLayout current="books" breadcrumb={t('admin.nav.books')}>
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.books')}</h1>
        <p className="mt-1.5 text-sm text-slate-500">{t('admin.books.description')}</p>
      </div>

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <form onSubmit={(event) => { event.preventDefault(); setPage(1); load() }} className="w-full max-w-xs">
          <Input value={q} onChange={(event) => { setQ(event.target.value); setPage(1) }}
            leading={<SearchIcon className="h-4 w-4" />} placeholder={t('admin.books.searchPlaceholder')} />
        </form>
        <Select className="w-36" value={status} options={statusOptions}
          onChange={(value) => { setStatus(value); setPage(1) }} />
        <Select className="w-36" value={visibility} options={visibilityOptions}
          onChange={(value) => { setVisibility(value); setPage(1) }} />
        <Select className="w-48" value={sort} options={sortOptions}
          onChange={(value) => { setSort(value); setPage(1) }} />
        <span className="ml-auto text-sm text-slate-400">{t('admin.books.total', { total })}</span>
      </div>

      {notice && (
        <div className={`mb-4 rounded-lg px-4 py-3 text-sm ${notice.error ? 'bg-rose-50 text-rose-700' : 'bg-emerald-50 text-emerald-700'}`}>
          {notice.message}
        </div>
      )}

      {loading ? (
        <Loading className="py-24" label={t('admin.books.loading')} />
      ) : items.length === 0 ? (
        <EmptyState>{t('admin.books.empty')}</EmptyState>
      ) : (
        <>
          <div className="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
            <div className="overflow-x-auto">
              <table className="w-full min-w-[980px] text-sm">
                <thead>
                  <tr className="border-b border-slate-100 text-left text-xs font-medium text-slate-400">
                    <th className="px-5 py-3">{t('admin.books.column.book')}</th>
                    <th className="px-5 py-3">{t('admin.books.column.author')}</th>
                    <th className="px-5 py-3">{t('admin.books.column.status')}</th>
                    <th className="px-5 py-3">{t('admin.books.column.visibility')}</th>
                    <th className="px-5 py-3">{t('admin.books.column.data')}</th>
                    <th className="px-5 py-3">{t('admin.books.column.updatedAt')}</th>
                    <th className="px-5 py-3 text-right">{t('admin.books.column.actions')}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100">
                  {items.map((book) => (
                    <tr key={book.id} className="hover:bg-slate-50/60">
                      <td className="px-5 py-3">
                        <div className="flex min-w-0 items-center gap-3">
                          <div className="flex h-12 w-10 shrink-0 items-center justify-center overflow-hidden rounded-md bg-gradient-to-br from-primary-300 to-[#8B8DFF] text-sm font-bold text-white">
                            {book.cover_image
                              ? <img src={resolveMediaUrl(book.cover_image)} alt="" className="h-full w-full object-cover" />
                              : book.title.slice(0, 1)}
                          </div>
                          <div className="min-w-0">
                            <Link href={`/book/detail/${encodeURIComponent(book.slug)}`}
                              className="block max-w-xs truncate font-medium text-slate-800 hover:text-primary-600">
                              {book.title}
                            </Link>
                            <p className="mt-0.5 max-w-xs truncate text-xs text-slate-400">/{book.slug}</p>
                          </div>
                        </div>
                      </td>
                      <td className="px-5 py-3">
                        {book.user ? (
                          <div className="flex items-center gap-2">
                            <UserAvatar user={book.user} size="h-7 w-7" tooltip={false} />
                            <Link href={`/user/${encodeURIComponent(book.user.username)}`} className="text-slate-600 hover:text-primary-600">
                              {book.user.username}
                            </Link>
                          </div>
                        ) : <span className="text-slate-400">—</span>}
                      </td>
                      <td className="px-5 py-3">
                        <div className="flex items-center gap-2">
                          <Badge tone={statusTone(book.status)}>{statusLabel(book.status)}</Badge>
                          <Select className="w-28" value={book.status} disabled={busyId === book.id}
                            options={rowStatusOptions}
                            onChange={(value) => updateBook(book, { status: value as BookStatus })} />
                          {busyId === book.id && <span className="h-4 w-4 shrink-0 animate-spin rounded-full border-2 border-slate-200 border-t-primary-500" aria-label={t('common.status.processing')} />}
                        </div>
                      </td>
                      <td className="px-5 py-3">
                        <Select className="w-28" value={book.is_public ? 'public' : 'private'} disabled={busyId === book.id}
                          options={rowVisibilityOptions}
                          onChange={(value) => updateBook(book, { is_public: value === 'public' })} />
                      </td>
                      <td className="px-5 py-3 text-xs text-slate-500">
                        <p>{t('admin.books.chapters', { count: book.chapter_count || 0 })}</p>
                        <p className="mt-1">{t('admin.books.views', { count: formatNumber(book.view_count) })}</p>
                      </td>
                      <td className="whitespace-nowrap px-5 py-3 text-slate-500">{formatDate(book.updated_at)}</td>
                      <td className="px-5 py-3 text-right">
                        <DropdownMenu open={menuFor === book.id}
                          onOpenChange={(open) => setMenuFor(open ? book.id : null)} label={t('admin.books.bookActions')}>
                          <Link role="menuitem" href={`/book/detail/${encodeURIComponent(book.slug)}`}
                            onClick={() => setMenuFor(null)}
                            className="flex items-center gap-2.5 px-4 py-2.5 text-left text-sm text-slate-700 hover:bg-slate-50">
                            <EyeIcon className="h-4 w-4 text-slate-400" /> {t('admin.books.viewDetails')}
                          </Link>
                          <Link role="menuitem" href={`/book/settings/${encodeURIComponent(book.slug)}`}
                            onClick={() => setMenuFor(null)}
                            className="flex items-center gap-2.5 px-4 py-2.5 text-left text-sm text-slate-700 hover:bg-slate-50">
                            <GearIcon className="h-4 w-4 text-slate-400" /> {t('admin.books.bookSettings')}
                          </Link>
                          <Link role="menuitem" href={`/admin/documents?book_id=${book.id}`}
                            onClick={() => setMenuFor(null)}
                            className="flex items-center gap-2.5 px-4 py-2.5 text-left text-sm text-slate-700 hover:bg-slate-50">
                            <FileTextIcon className="h-4 w-4 text-slate-400" /> {t('admin.books.manageChapters')}
                          </Link>
                          <div className="my-1 border-t border-slate-100" />
                          <button role="menuitem" disabled={busyId === book.id} onClick={() => removeBook(book)}
                            className="flex w-full items-center gap-2.5 px-4 py-2.5 text-left text-sm text-rose-600 hover:bg-rose-50 disabled:opacity-50">
                            <TrashIcon className="h-4 w-4" /> {t('admin.books.moveToTrash')}
                          </button>
                        </DropdownMenu>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
          <Pagination page={page} pageSize={PAGE_SIZE} total={total} onChange={setPage} loading={loading} />
        </>
      )}
    </AdminLayout>
  )
}
