import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useRouter } from 'next/router'
import Seo from '@/components/Seo'
import Container from '@/components/Container'
import Link from 'next/link'
import { api, formatDate, formatNumber, API_BASE, getToken } from '@/lib/api'
import { isQueuedTask, waitForTask, type QueuedTask } from '@/lib/background-tasks'
import { useTranslation } from '@/lib/i18n'
import { useRequireAuth , useApp} from '@/lib/auth'
import { useGridPageSize } from '@/lib/useGridPageSize'
import { Button, ButtonLink, Badge, Checkbox, DropdownMenu, EmptyState, Field, Input, Pagination, SegmentedTabs, Select, Loading, Tooltip, useFeedback } from '@/components/ui'
import BookCard from '@/components/BookCard'
import MyLibraryTabs from '@/components/MyLibraryTabs'
import BookCopyDialog from '@/components/BookCopyDialog'
import PDFReimportPanel from '@/components/PDFReimportPanel'
import {
  CalendarIcon, CloseIcon, EyeIcon, FileTextIcon, GearIcon, GlobeIcon, GridIcon,
  ListIcon, PencilIcon, SearchIcon, UploadIcon,
} from '@/components/icons'
import type { Book, Document, PageResult } from '@/lib/types'
import { entitlementAllowed } from '@/lib/entitlements'

const statusTabs = [
  { key: '', labelKey: 'books.status.all' },
  { key: 'in_progress', labelKey: 'books.status.in_progress' },
  { key: 'published', labelKey: 'books.status.published' },
  { key: 'completed', labelKey: 'books.status.completed' },
  { key: 'draft', labelKey: 'books.status.draft' },
  { key: 'archived', labelKey: 'books.status.archived' },
]

type SortKey = 'updated' | 'created' | 'views' | 'title'

const sortOptions = [
  { value: 'updated', labelKey: 'books.sort.updated' },
  { value: 'created', labelKey: 'books.sort.created' },
  { value: 'views', labelKey: 'books.sort.views' },
  { value: 'title', labelKey: 'books.sort.title' },
]

function relativeUpdated(t: (key: string, vars?: Record<string, string | number>) => string, input: string | null | undefined): string {
  if (!input) return '-'
  const diff = Date.now() - new Date(input).getTime()
  const day = 86400000
  if (diff < 3600000) return t('books.updated.justNow')
  if (diff < day) return t('books.updated.hoursAgo', { count: Math.floor(diff / 3600000) })
  if (diff < day * 2) return t('books.updated.oneDayAgo')
  if (diff < day * 30) return t('books.updated.daysAgo', { count: Math.floor(diff / day) })
  return t('books.updated.on', { date: fmtDay(input) })
}

function fmtDay(input: string | null | undefined): string {
  return input ? input.slice(0, 10) : '-'
}

function chapterCount(book: Book): number {
  return (book.tags?.length ?? 0) + 0 || 0
}

export default function MyBooks() {
  const { confirmAction, requestInput, showToast } = useFeedback()
  const user = useRequireAuth()
  const router = useRouter()
  const { site } = useApp()
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const [status, setStatus] = useState('')
  const scope: 'owned' | 'collaborating' = router.query.scope === 'collaborating' ? 'collaborating' : 'owned'
  const { ref: gridRef, pageSize, ready } = useGridPageSize({ minItemRem: 15, rows: 3, fallback: 9 })
  const [page, setPage] = useState(1)
  const [keyword, setKeyword] = useState('')
  const [sort, setSort] = useState<SortKey>('updated')
  const [view, setView] = useState<'grid' | 'list'>('grid')
  const [menuFor, setMenuFor] = useState<number | null>(null)
  const [data, setData] = useState<PageResult<Book>>({ items: [], total: 0, page: 1, page_size: 10 })
  const [counts, setCounts] = useState<Record<string, number>>({
    '': 0, draft: 0, in_progress: 0, published: 0, completed: 0, archived: 0,
  })
  const [loading, setLoading] = useState(true)
  const [importOpen, setImportOpen] = useState(false)
  const [pdfImportBook, setPDFImportBook] = useState<Book | null>(null)
  const [copyBook, setCopyBook] = useState<Book | null>(null)

  async function load() {
    if (!user) return
    setLoading(true)
    try {
      setData(await api<PageResult<Book>>('/books', { params: { scope, page, page_size: pageSize, status, title: keyword, sort } }))
      const summary = await api<Record<string, number>>('/books/status-counts', { params: { scope } }).catch(() => null)
      if (summary) setCounts(summary)
    } catch (e) {
      showToast({ title: t('books.error.load'), message: (e as Error).message, tone: 'error' })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { if (user && router.isReady && ready) load() /* eslint-disable-line react-hooks/exhaustive-deps */ }, [user, router.isReady, ready, page, scope, status, keyword, sort, pageSize])

  function changeScope(next: 'owned' | 'collaborating') {
    setStatus('')
    setPage(1)
    setMenuFor(null)
    router.replace({ pathname: '/books', query: next === 'collaborating' ? { scope: next } : {} }, undefined, { shallow: true })
  }

  // 客户端排序（API 分页内排序字段当前仅支持基础列；数量小时在当前页排序即可）
  const items = [...(data.items || [])].sort((a, b) => {
    if (sort === 'views') return b.view_count - a.view_count
    if (sort === 'title') return a.title.localeCompare(b.title, 'zh-CN')
    if (sort === 'created') return a.created_at < b.created_at ? 1 : -1
    return a.updated_at < b.updated_at ? 1 : -1
  })

  async function remove(book: Book) {
    if (!await confirmAction({
      title: t('books.delete.title'),
      message: t('books.delete.message', { title: book.title }),
      confirmLabel: t('books.delete.confirm'),
      danger: true,
    })) return
    try {
      await api(`/books/${book.id}`, { method: 'DELETE' })
      load()
    } catch (e) {
      showToast({ title: t('books.error.delete'), message: (e as Error).message, tone: 'error' })
    }
  }

  async function copyLink(book: Book) {
    const url = `${window.location.origin}/book/detail/${encodeURIComponent(book.slug)}`
    try {
      await navigator.clipboard.writeText(url)
      showToast({ message: t('books.link.copied'), tone: 'success' })
    } catch {
      await requestInput({ title: t('books.link.copyTitle'), label: t('books.link.linkLabel'), defaultValue: url, confirmLabel: t('books.link.close') })
    }
    setMenuFor(null)
  }

  async function leaveCollaboration(book: Book) {
    if (!user || !await confirmAction({
      title: t('books.leave.title'),
      message: t('books.leave.message', { title: book.title }),
      confirmLabel: t('books.leave.confirm'),
      danger: true,
    })) return
    try {
      await api(`/books/${book.id}/collaborators/${user.id}`, { method: 'DELETE' })
      showToast({ message: t('books.leave.success'), tone: 'success' })
      await load()
    } catch (e) {
      showToast({ title: t('books.error.leave'), message: (e as Error).message, tone: 'error' })
    }
  }

  if (!user) return <Loading className="min-h-[60vh]" label={t('books.authChecking')} />

  const hasBooks = (data.items || []).length > 0

  return (
    <>
      <Seo siteName={siteName} title={t('books.title')} noindex />
      <Container>
      {/* 页头 */}
      <div className="pb-8 pt-2">
        <p className="text-sm text-slate-400">{t('books.kicker')}</p>
        <div className="mt-2 flex flex-wrap items-end justify-between gap-4">
          <div>
            <h1 className="text-3xl font-bold text-ink md:text-4xl">{t('books.title')}</h1>
            <p className="mt-2 text-[15px] text-slate-500">{t('books.subtitle')}</p>
          </div>
          <div className="grid w-full grid-cols-2 gap-2 sm:flex sm:w-auto sm:flex-wrap sm:items-center sm:gap-3">
            <ButtonLink href="/user/trash" variant="ghost" className="px-3 text-sm sm:px-4 sm:text-base">
              <i className="fa-regular fa-trash-can" aria-hidden="true" /> {t('books.action.trash')}
            </ButtonLink>
            <Button variant="outline" className="px-3 text-sm sm:px-5 sm:text-base" onClick={() => setImportOpen(true)}>
              <UploadIcon className="h-5 w-5" /> {t('books.action.import')}
            </Button>
            {entitlementAllowed(user, 'collect.site', site.collect_site_enabled !== false) && (
              <ButtonLink href="/books/collect" variant="outline" className="px-3 text-sm sm:px-5 sm:text-base">
                <GlobeIcon className="h-5 w-5" /> {t('collect.title')}
              </ButtonLink>
            )}
            <ButtonLink href="/books/create" className="col-span-2 px-3 text-sm sm:px-5 sm:text-base">
              <PlusIcon className="h-5 w-5" /> {t('books.action.create')}
            </ButtonLink>
          </div>
        </div>
      </div>

      <MyLibraryTabs active="books" />

      <div className="mb-5">
        <SegmentedTabs value={scope} ariaLabel={t('books.scopeAria')}
          onChange={(value) => changeScope(value as 'owned' | 'collaborating')} items={[
            { value: 'owned', label: t('books.scope.owned') },
            { value: 'collaborating', label: t('books.scope.collaborating') },
          ]} />
      </div>

      {/* 筛选行 */}
      <div className="flex flex-col gap-3 border-y border-slate-200 py-3 lg:flex-row lg:items-center lg:justify-between lg:gap-4">
        <div className="w-full overflow-x-auto pb-1 lg:w-auto lg:pb-0">
          <SegmentedTabs className="min-w-max" value={status} ariaLabel={t('books.statusAria')} onChange={(value) => { setStatus(value); setPage(1) }}
            items={statusTabs.map((tab) => ({
              value: tab.key,
              label: <>{t(tab.labelKey)}{counts[tab.key] !== undefined && <span className="ml-1 text-xs text-slate-400">{counts[tab.key]}</span>}</>,
            }))} />
        </div>
        <div className="flex w-full flex-wrap items-center gap-2 lg:w-auto">
          <Input className="w-full sm:w-56" value={keyword} onChange={(e) => { setKeyword(e.target.value); setPage(1) }}
            leading={<SearchIcon className="h-4 w-4" />} placeholder={t('books.searchPlaceholder')} />
          <Select className="min-w-0 flex-1 sm:w-36 sm:flex-none" value={sort} onChange={(v) => { setSort(v as SortKey); setPage(1) }}
            options={sortOptions.map((o) => ({ value: o.value, label: t(o.labelKey) }))} />
          <SegmentedTabs iconOnly value={view} ariaLabel={t('books.viewAria')}
            onChange={(value) => setView(value as 'grid' | 'list')} items={[
              { value: 'grid', label: t('books.view.grid'), icon: <GridIcon className="h-4 w-4" /> },
              { value: 'list', label: t('books.view.list'), icon: <ListIcon className="h-4 w-4" /> },
            ]} />
        </div>
      </div>

      <p className="py-4 text-sm text-slate-400">{t('books.total', { count: data.total })}</p>

      <div ref={gridRef}>
      {loading ? (
        <Loading />
      ) : hasBooks ? (
        <div className={view === 'grid' ? 'grid gap-5 grid-cols-[repeat(auto-fill,minmax(15rem,1fr))]' : 'space-y-4'}>
          {items.map((book) => (
            <BookCardMine key={book.id} book={book} view={view} collaborating={scope === 'collaborating'}
              menuOpen={menuFor === book.id} setMenuOpen={(open) => setMenuFor(open ? book.id : null)}
              onCopy={() => copyLink(book)} onImportPDF={() => setPDFImportBook(book)} onDelete={() => remove(book)}
              onCopyBook={() => setCopyBook(book)} onLeave={() => leaveCollaboration(book)} />
          ))}
        </div>
      ) : (
        <EmptyState>
          {scope === 'owned' ? <>
            {t('books.empty.owned')}<Link href="/books/create" className="text-primary-600 hover:underline">{t('books.empty.createLink')}</Link>
          </> : t('books.empty.collaborating')}
        </EmptyState>
      )}
      </div>

      {/* 数据加载完成且有书籍时才显示分页（加载中不显示旧的分页） */}
      {!loading && hasBooks && <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} />}

      {importOpen && <BookImportDialog onClose={() => setImportOpen(false)} onImported={load} />}
      {pdfImportBook && (
        <PDFImportDialog book={pdfImportBook} onClose={() => setPDFImportBook(null)} onImported={load} />
      )}
      {copyBook && (
        <BookCopyDialog book={copyBook} open onClose={() => setCopyBook(null)} />
      )}

    </Container>
  </>
  )
}

type ImportKind = 'zip' | 'pdf' | 'web' | 'markdown'
type WebRenderMode = 'auto' | 'static' | 'browser'
type ImportResult = { book: Book; message?: string; imported_doc?: number; render_mode?: string }

function BookImportDialog({ onClose, onImported }: { onClose: () => void; onImported: () => Promise<void> }) {
  const { site, user: me } = useApp()
  const webCollectEnabled = entitlementAllowed(me, 'collect.page', site.collect_page_enabled !== false) // 单页采集权益（含插件启用）：无权限则不显示「网页」导入
  const [kind, setKind] = useState<ImportKind>('pdf')
  const [file, setFile] = useState<File | null>(null)
  const [mdFiles, setMdFiles] = useState<File[]>([]) // Markdown：多个 .md（可附带图片）或一个 ZIP
  const [publishChapters, setPublishChapters] = useState(false)
  const [title, setTitle] = useState('')
  const [url, setURL] = useState('')
  const [renderMode, setRenderMode] = useState<WebRenderMode>('auto')
  const [browserAvailable, setBrowserAvailable] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<ImportResult | null>(null)
  const { t } = useTranslation()

  useEffect(() => {
    api<{ available: boolean }>('/import/browser-available')
      .then((r) => { setBrowserAvailable(r.available); if (!r.available) setRenderMode((m) => m === 'browser' ? 'auto' : m) })
      .catch(() => {})
  }, [])

  function switchKind(next: ImportKind) {
    if (submitting) return
    setKind(next)
    setFile(null)
    setMdFiles([])
    setError('')
    setResult(null)
  }

  async function uploadFile(endpoint: string, selectedFile: File): Promise<ImportResult | QueuedTask<ImportResult>> {
    const form = new FormData()
    form.append('file', selectedFile)
    if (title.trim()) form.append('title', title.trim())
    const token = getToken()
    const response = await fetch(`${API_BASE}/api/v1${endpoint}`, {
      method: 'POST',
      headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      body: form,
    })
    const payload = await response.json().catch(() => ({}))
    if (!response.ok || payload.success === false) throw new Error(payload.message || t('books.import.failed', { status: response.status }))
    return payload.data as ImportResult | QueuedTask<ImportResult>
  }

  async function uploadMarkdown(files: File[]): Promise<ImportResult> {
    const form = new FormData()
    files.forEach((f) => form.append('files', f))
    if (title.trim()) form.append('title', title.trim())
    if (publishChapters) form.append('publish', 'true')
    const token = getToken()
    const response = await fetch(`${API_BASE}/api/v1/import/markdown`, { method: 'POST', headers: token ? { Authorization: `Bearer ${token}` } : undefined, body: form })
    const payload = await response.json().catch(() => ({}))
    if (!response.ok || payload.success === false) throw new Error(payload.message || t('books.import.failed', { status: response.status }))
    return payload.data as ImportResult
  }

  async function submit() {
    if (kind === 'markdown' && mdFiles.length === 0) {
      setError(t('books.import.needMarkdown'))
      return
    }
    if (kind !== 'web' && kind !== 'markdown' && !file) {
      setError(t('books.import.needFile', { type: kind === 'pdf' ? 'PDF' : 'ZIP' }))
      return
    }
    if (kind === 'web' && !url.trim()) {
      setError(t('books.import.needUrl'))
      return
    }
    setSubmitting(true)
    setError('')
    try {
      const response = kind === 'web'
        ? await api<ImportResult>('/import/web', { method: 'POST', body: { url: url.trim(), title: title.trim(), render_mode: renderMode } })
        : kind === 'markdown'
          ? await uploadMarkdown(mdFiles)
          : await uploadFile(kind === 'pdf' ? '/import/pdf' : '/import', file as File)
      const imported = isQueuedTask(response) ? await waitForTask<ImportResult>(response.task.id) : response
      setResult(imported)
      await onImported()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  const loadingLabel = kind === 'pdf'
    ? t('books.import.loading.pdf')
    : kind === 'web'
      ? renderMode === 'static' ? t('books.import.loading.static') : t('books.import.loading.browser')
      : kind === 'markdown' ? t('books.import.loading.markdown') : t('books.import.loading.zip')

  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-slate-950/30 p-0 backdrop-blur-[2px] sm:items-center sm:p-6"
      role="dialog" aria-modal="true" aria-label={t('books.import.aria')} onMouseDown={(event) => { if (!submitting && event.target === event.currentTarget) onClose() }}>
      <div className="max-h-[94vh] w-full overflow-y-auto rounded-t-3xl border border-slate-200 bg-white shadow-2xl sm:max-w-2xl sm:rounded-2xl">
        <div className="flex items-start justify-between border-b border-slate-100 px-6 py-5 sm:px-7">
          <div>
            <h2 className="text-xl font-bold text-ink">{t('books.import.title')}</h2>
            <p className="mt-1 text-sm text-slate-500">{t('books.import.desc')}</p>
          </div>
          <button type="button" aria-label={t('books.import.close')} disabled={submitting} onClick={onClose}
            className="flex shrink-0 items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700 disabled:cursor-not-allowed disabled:opacity-50"
            style={{ width: 'var(--control-height-sm)', height: 'var(--control-height-sm)' }}>
            <CloseIcon className="h-5 w-5" />
          </button>
        </div>

        <div className="px-6 py-6 sm:px-7">
          <SegmentedTabs fullWidth value={kind} ariaLabel={t('books.import.typeAria')}
            onChange={(value) => switchKind(value as ImportKind)} items={[
              { value: 'pdf', label: t('books.import.kind.pdf'), icon: <FileTextIcon className="h-4 w-4" /> },
              ...(webCollectEnabled ? [{ value: 'web', label: t('books.import.kind.web'), icon: <GlobeIcon className="h-4 w-4" /> }] : []),
              { value: 'markdown', label: t('books.import.kind.markdown'), icon: <i className="fa-brands fa-markdown text-sm" aria-hidden="true" /> },
              { value: 'zip', label: t('books.import.kind.zip'), icon: <UploadIcon className="h-4 w-4" /> },
            ]} />

          {result ? (
            <div className="py-10 text-center">
              <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-emerald-50 text-emerald-600">
                <CheckIcon className="h-6 w-6" />
              </div>
              <h3 className="mt-4 text-lg font-semibold text-slate-900">{t('books.import.doneTitle')}</h3>
              <p className="mt-2 text-sm text-slate-500">{result.message || t('books.import.doneMessage', { title: result.book.title })}</p>
              <div className="mt-6 flex justify-center gap-3">
                <Button variant="outline" onClick={onClose}>{t('books.import.done')}</Button>
                <ButtonLink href={`/book/writer/${encodeURIComponent(result.book.slug)}`}>{t('books.import.goEdit')}</ButtonLink>
              </div>
            </div>
          ) : (
            <div className="mt-6 space-y-5">
              {kind === 'web' ? (
                <>
                  <Field label={t('books.import.urlLabel')}>
                    <Input type="url" value={url} onChange={(event) => setURL(event.target.value)} placeholder="https://example.com/article"
                      leading={<GlobeIcon className="h-4 w-4" />} />
                  </Field>
                  <fieldset>
                    <legend className="text-sm font-medium text-slate-700">{t('books.import.renderMode')}</legend>
                    <div className="mt-2 grid gap-2 sm:grid-cols-3">
                      {([
                        ['auto', t('books.import.mode.auto'), t('books.import.mode.autoHelp')],
                        ['static', t('books.import.mode.static'), t('books.import.mode.staticHelp')],
                        ...(browserAvailable ? [['browser', t('books.import.mode.browser'), t('books.import.mode.browserHelp')] as [WebRenderMode, string, string]] : []),
                      ] as [WebRenderMode, string, string][]).map(([value, label, help]) => (
                        <label key={value} className={`cursor-pointer rounded-xl border p-3 transition-colors ${renderMode === value ? 'border-primary-400 bg-primary-50/60' : 'border-slate-200 hover:border-slate-300'}`}>
                          <input className="sr-only" type="radio" name="render-mode" value={value} checked={renderMode === value} onChange={() => setRenderMode(value)} />
                          <span className="block text-sm font-medium text-slate-800">{label}</span>
                          <span className="mt-1 block text-xs leading-5 text-slate-500">{help}</span>
                        </label>
                      ))}
                    </div>
                    {!browserAvailable && <p className="mt-2 text-xs leading-5 text-amber-600">{t('books.import.noBrowser')}</p>}
                    {browserAvailable && renderMode !== 'static' && <p className="mt-2 text-xs leading-5 text-slate-400">{t('books.import.browserHint')}</p>}
                  </fieldset>
                </>
              ) : kind === 'markdown' ? (
                <>
                  <label className="block">
                    <span className="text-sm font-medium text-slate-700">{t('books.import.fileLabel')}</span>
                    <span className="mt-2 flex min-h-32 cursor-pointer flex-col items-center justify-center rounded-2xl border border-dashed border-slate-300 bg-slate-50/60 px-5 py-6 text-center transition-colors hover:border-primary-400 hover:bg-primary-50/40">
                      <UploadIcon className="h-7 w-7 text-primary-500" />
                      <span className="mt-3 text-sm font-medium text-slate-700 [overflow-wrap:anywhere]">
                        {mdFiles.length === 0 ? t('books.import.chooseMarkdown') : mdFiles.length === 1 ? mdFiles[0].name : t('books.import.markdownSelected', { count: mdFiles.length })}
                      </span>
                      <span className="mt-1 text-xs leading-5 text-slate-400">{t('books.import.markdownHint')}</span>
                      <input type="file" multiple accept=".md,.markdown,.zip,application/zip,image/*" className="sr-only"
                        onChange={(event) => { setMdFiles(Array.from(event.target.files || [])); setError('') }} />
                    </span>
                  </label>
                  <label className="flex cursor-pointer items-center gap-2 text-sm text-slate-600">
                    <Checkbox checked={publishChapters} onChange={setPublishChapters} ariaLabel={t('books.import.publishChapters')} />
                    {t('books.import.publishChapters')}
                  </label>
                </>
              ) : (
                <label className="block">
                  <span className="text-sm font-medium text-slate-700">{t('books.import.fileLabel')}</span>
                  <span className="mt-2 flex min-h-32 cursor-pointer flex-col items-center justify-center rounded-2xl border border-dashed border-slate-300 bg-slate-50/60 px-5 py-6 text-center transition-colors hover:border-primary-400 hover:bg-primary-50/40">
                    <UploadIcon className="h-7 w-7 text-primary-500" />
                    <span className="mt-3 text-sm font-medium text-slate-700">{file ? file.name : kind === 'pdf' ? t('books.import.choosePdf') : t('books.import.chooseZip')}</span>
                    <span className="mt-1 text-xs text-slate-400">{kind === 'pdf' ? t('books.import.pdfHint') : t('books.import.zipHint')}</span>
                    <input type="file" accept={kind === 'pdf' ? '.pdf,application/pdf' : '.zip,application/zip'} className="sr-only"
                      onChange={(event) => { setFile(event.target.files?.[0] || null); setError('') }} />
                  </span>
                </label>
              )}

              <Field label={<>{t('books.import.nameLabel')} <span className="font-normal text-slate-400">{t('books.import.optional')}</span></>}>
                <Input value={title} onChange={(event) => setTitle(event.target.value)} placeholder={kind === 'web' ? t('books.import.namePlaceholderWeb') : t('books.import.namePlaceholderFile')} />
              </Field>

              {error && <div role="alert" className="max-h-32 overflow-y-auto whitespace-pre-wrap break-words rounded-xl border border-rose-100 bg-rose-50 px-4 py-3 text-sm leading-6 text-rose-600 [overflow-wrap:anywhere]">{error}</div>}
              {submitting && <div className="rounded-xl border border-primary-100 bg-primary-50/50 px-4 py-4"><Loading className="py-1" label={loadingLabel} /></div>}

              <div className="flex justify-end gap-3 border-t border-slate-100 pt-5">
                <Button variant="outline" disabled={submitting} onClick={onClose}>{t('common.actions.cancel')}</Button>
                <Button loading={submitting} onClick={submit}>{t('books.import.start')}</Button>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

/* ── 单本书卡片（网格 / 列表两种视图） ── */

function BookCardMine({ book, view, collaborating, menuOpen, setMenuOpen, onCopy, onImportPDF, onDelete, onCopyBook, onLeave }: {
  book: Book
  view: 'grid' | 'list'
  collaborating: boolean
  menuOpen: boolean
  setMenuOpen: (open: boolean) => void
  onCopy: () => void
  onImportPDF: () => void
  onDelete: () => void
  onCopyBook: () => void
  onLeave: () => void
}) {
  const { t } = useTranslation()
  const detailUrl = `/book/detail/${encodeURIComponent(book.slug)}`

  // 章节计数与相对更新时间作为自定义元信息槽（含浏览量由统一卡片接管）
  const metaSlot = (
    <>
      <span className="flex items-center gap-1"><FileTextIcon className="h-3.5 w-3.5" /> {t('books.meta.chapters', { count: book.chapter_count ?? 0 })}</span>
      <span className="flex items-center gap-1"><CalendarIcon className="h-3.5 w-3.5" /> {relativeUpdated(t, book.updated_at)}</span>
    </>
  )

  const menu = (
    <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
      <Link role="menuitem" href={detailUrl} onClick={() => setMenuOpen(false)}
        className="flex items-center gap-2.5 px-4 py-2.5 text-sm text-slate-700 hover:bg-slate-50">
        <EyeIcon className="h-4 w-4 text-slate-400" /> {t('books.menu.viewDetail')}
      </Link>
      {!collaborating && <>
        <Link role="menuitem" href={`/book/settings/${encodeURIComponent(book.slug)}`} onClick={() => setMenuOpen(false)}
          className="flex items-center gap-2.5 px-4 py-2.5 text-sm text-slate-700 hover:bg-slate-50">
          <GearIcon className="h-4 w-4 text-slate-400" /> {t('books.menu.settings')}
        </Link>
        <button role="menuitem" onClick={() => { setMenuOpen(false); onImportPDF() }}
          className="flex w-full items-center gap-2.5 px-4 py-2.5 text-left text-sm text-slate-700 hover:bg-slate-50">
          <i className="fa-solid fa-file-pdf w-4 text-center text-slate-400" aria-hidden="true" /> {t('books.menu.importPdf')}
        </button>
      </>}
      <button role="menuitem" onClick={onCopy}
        className="flex w-full items-center gap-2.5 px-4 py-2.5 text-left text-sm text-slate-700 hover:bg-slate-50">
        <LinkIcon2 className="h-4 w-4 text-slate-400" /> {t('books.menu.copyLink')}
      </button>
      <button role="menuitem" onClick={() => { setMenuOpen(false); onCopyBook() }}
        className="flex w-full items-center gap-2.5 px-4 py-2.5 text-left text-sm text-slate-700 hover:bg-slate-50">
        <i className="fa-regular fa-copy w-4 text-center text-slate-400" aria-hidden="true" /> {t('books.menu.copyBook')}
      </button>
      <div className="my-1 border-t border-slate-100" />
      <button role="menuitem" onClick={() => { setMenuOpen(false); collaborating ? onLeave() : onDelete() }}
        className="flex w-full items-center gap-2.5 px-4 py-2.5 text-left text-sm text-rose-600 hover:bg-rose-50">
        {collaborating ? <i className="fa-solid fa-arrow-right-from-bracket w-4 text-center" aria-hidden="true" /> : <TrashIcon className="h-4 w-4" />}
        {collaborating ? t('books.menu.leave') : t('books.menu.trash')}
      </button>
    </DropdownMenu>
  )

  // 写作操作行：继续/开始写作 + 章节列表 + 书籍设置。
  // grid 走底部 actions 槽（统一卡片自带分隔线）；list 走右侧栏（含 menu）。
  const actions = view === 'grid'
    ? <ActionRow book={book} menu={null} canManage={!collaborating} canEdit={!collaborating || book.collaborator_role === 'editor'} />
    : <ActionRow book={book} menu={menu} canManage={!collaborating} canEdit={!collaborating || book.collaborator_role === 'editor'} />

  return (
    <BookCard
      book={book}
      view={view}
      showAuthor={collaborating}
      showStatus
      showVisibility={!collaborating}
      badge={(book.crawling || collaborating) ? <>
        {book.crawling && <Badge tone="primary"><i className="fa-solid fa-spinner fa-spin mr-1 text-[10px]" aria-hidden="true" />{t('collect.crawling')}</Badge>}
        {collaborating && <>
          <Badge tone="slate">{t('books.badge.collabVisible')}</Badge>
          <Badge tone={book.collaborator_role === 'editor' ? 'emerald' : 'sky'}>{book.collaborator_role === 'editor' ? t('books.role.editor') : t('books.role.viewer')}</Badge>
        </>}
      </> : undefined}
      tagsMax={3}
      tagsLink={false}
      meta={metaSlot}
      topActions={view === 'grid' ? menu : undefined}
      actions={actions}
    />
  )
}

function PDFImportDialog({ book, onClose, onImported }: { book: Book; onClose: () => void; onImported: () => Promise<void> }) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    function onKey(event: KeyboardEvent) { if (event.key === 'Escape' && !busy) onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [busy, onClose])

  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-slate-950/30 p-0 backdrop-blur-[2px] sm:items-center sm:p-6"
      role="dialog" aria-modal="true" aria-labelledby="pdf-import-dialog-title"
      onMouseDown={(event) => { if (!busy && event.target === event.currentTarget) onClose() }}>
      <section className="flex max-h-[92vh] w-full flex-col overflow-hidden rounded-t-3xl border border-slate-200 bg-white shadow-2xl sm:max-w-2xl sm:rounded-2xl">
        <header className="flex shrink-0 items-start justify-between gap-4 border-b border-slate-100 px-5 py-5 sm:px-6">
          <div className="min-w-0">
            <div className="flex items-center gap-2.5">
              <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-primary-50 text-primary-600">
                <i className="fa-solid fa-file-pdf" aria-hidden="true" />
              </span>
              <h2 id="pdf-import-dialog-title" className="truncate text-xl font-bold text-ink">{t('books.pdf.title', { title: book.title })}</h2>
            </div>
            <p className="mt-2 text-sm leading-6 text-slate-500">{t('books.pdf.desc')}</p>
          </div>
          <button type="button" aria-label={t('books.pdf.close')} disabled={busy} onClick={onClose}
            className="flex shrink-0 items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700 disabled:cursor-not-allowed disabled:opacity-40"
            style={{ width: 'var(--control-height-sm)', height: 'var(--control-height-sm)' }}>
            <CloseIcon className="h-5 w-5" />
          </button>
        </header>
        <div className="min-h-0 overflow-y-auto">
          <PDFReimportPanel bookId={book.id} embedded onImported={onImported} onBusyChange={setBusy} />
        </div>
      </section>
    </div>
  )
}

function ActionRow({ book, menu, canManage, canEdit }: { book: Book; menu: React.ReactNode; canManage: boolean; canEdit: boolean }) {
  const { t } = useTranslation()
  const isNew = !book.description
  // 已完成/已归档的书不再提示「继续写作」，左下角改为查看书籍（仍可从章节面板的编辑按钮或书籍设置进入编辑）
  const finished = book.status === 'completed' || book.status === 'archived'
  const writeLink = canEdit && !finished
  const [chaptersOpen, setChaptersOpen] = useState(false)
  const chaptersBtnRef = useRef<HTMLButtonElement>(null)
  return (
    <div className="flex items-center justify-between">
      <Link href={writeLink ? `/book/writer/${encodeURIComponent(book.slug)}` : `/book/detail/${encodeURIComponent(book.slug)}`} className="text-sm font-medium text-primary-600 hover:underline">
        {writeLink ? (isNew ? t('books.row.start') : t('books.row.continue')) : t('books.row.view')}
      </Link>
      <div className="relative flex items-center gap-1">
        <Tooltip content={t('books.tooltip.chapters')}><button ref={chaptersBtnRef} type="button" onClick={() => setChaptersOpen(!chaptersOpen)}
          className={`flex items-center justify-center rounded-lg transition-colors ${chaptersOpen ? 'bg-primary-50 text-primary-600' : 'text-slate-400 hover:bg-slate-100 hover:text-slate-700'}`}
          style={{ width: 'var(--control-height-sm)', height: 'var(--control-height-sm)' }}>
            <FileTextIcon className="h-4 w-4" />
          </button></Tooltip>
        {canManage && <Tooltip content={t('books.tooltip.settings')}><Link href={`/book/settings/${encodeURIComponent(book.slug)}`}
            className="flex items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700"
            style={{ width: 'var(--control-height-sm)', height: 'var(--control-height-sm)' }}>
            <GearIcon className="h-4 w-4" />
          </Link></Tooltip>}
        {menu}
        {chaptersOpen && <ChapterPanel book={book} canEdit={canEdit} triggerRef={chaptersBtnRef} onClose={() => setChaptersOpen(false)} />}
      </div>
    </div>
  )
}

// ChapterPanel 书籍章节弹出列表：懒加载文档树，点击进阅读，铅笔进编辑。
// 用 Portal + fixed 定位渲染到 body，避免被卡片的 overflow-hidden 裁切（导致左侧内容显示不全）。
function ChapterPanel({ book, canEdit, triggerRef, onClose }: { book: Book; canEdit: boolean; triggerRef: React.RefObject<HTMLButtonElement>; onClose: () => void }) {
  const { t } = useTranslation()
  const [docs, setDocs] = useState<Document[] | null>(null)
  const [error, setError] = useState('')
  const [style, setStyle] = useState<React.CSSProperties | null>(null)
  useEffect(() => {
    api<Document[]>(`/books/${book.id}/documents`)
      .then((d) => setDocs(d || []))
      .catch((e) => setError((e as Error).message))
  }, [book.id])

  useLayoutEffect(() => {
    const place = () => {
      const rect = triggerRef.current?.getBoundingClientRect()
      if (!rect) return
      const width = Math.min(320, window.innerWidth - 16)
      const left = Math.max(8, Math.min(rect.right - width, window.innerWidth - width - 8))
      const spaceAbove = rect.top
      const spaceBelow = window.innerHeight - rect.bottom
      const above = spaceAbove > spaceBelow
      setStyle(above
        ? { left, width, bottom: window.innerHeight - rect.top + 8, maxHeight: spaceAbove - 16 }
        : { left, width, top: rect.bottom + 8, maxHeight: spaceBelow - 16 })
    }
    place()
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, true)
    return () => { window.removeEventListener('resize', place); window.removeEventListener('scroll', place, true) }
  }, [triggerRef])

  const rows = flattenDocs(docs || [])
  if (typeof document === 'undefined') return null
  return createPortal(
    <>
      <div className="fixed inset-0 z-[110]" onClick={onClose} />
      <div className="fixed z-[120] flex flex-col overflow-hidden rounded-xl border border-slate-200 bg-white shadow-xl"
        style={{ visibility: style ? 'visible' : 'hidden', ...style }}>
        <div className="flex shrink-0 items-center justify-between border-b border-slate-100 px-4 py-2.5">
          <span className="text-sm font-semibold text-slate-900">{t('books.chapters.title')}</span>
          <span className="text-xs text-slate-400">{docs ? t('books.chapters.count', { count: rows.length }) : ''}</span>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {error && <p className="px-4 py-3 text-sm text-rose-500">{error}</p>}
          {!error && docs === null && <Loading className="py-6" label={t('books.chapters.loading')} />}
          {docs !== null && rows.length === 0 && <p className="px-4 py-6 text-center text-sm text-slate-400">{t('books.chapters.empty')}</p>}
          {rows.map((row) => (
            <div key={row.doc.id} className="flex items-center gap-2 px-4 py-2 hover:bg-slate-50">
              <Link href={`/book/reader/${encodeURIComponent(book.slug)}/${row.doc.slug}`}
                onClick={onClose}
                className="flex min-w-0 flex-1 items-center gap-1.5 text-sm text-slate-700 hover:text-primary-600"
                style={{ paddingLeft: row.level * 12 }}>
                <FileTextIcon className="h-3.5 w-3.5 shrink-0 text-slate-400" />
                <span className="truncate">{book.chapter_prefix}{row.doc.title}</span>
              </Link>
              {canEdit && <Link href={`/book/writer/${encodeURIComponent(book.slug)}/${row.doc.slug}`} onClick={onClose}
                className="shrink-0 text-slate-300 transition-colors hover:text-primary-600">
                <PencilIcon className="h-3.5 w-3.5" />
              </Link>}
            </div>
          ))}
        </div>
        {canEdit && <div className="shrink-0 border-t border-slate-100 px-4 py-2">
          <Link href={`/book/writer/${encodeURIComponent(book.slug)}`} onClick={onClose}
            className="text-sm font-medium text-primary-600 hover:underline">{t('books.chapters.manageAll')}</Link>
        </div>}
      </div>
    </>,
    document.body,
  )
}

function flattenDocs(docs: Document[], level = 0): { doc: Document; level: number }[] {
  return docs.flatMap((d) => [{ doc: d, level }, ...flattenDocs(d.children || [], level + 1)])
}

function LinkIcon2({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
      <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
    </svg>
  )
}

function PlusIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" className={className}>
      <path d="M12 5v14M5 12h14" />
    </svg>
  )
}

function CheckIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d="m5 12 4 4L19 6" />
    </svg>
  )
}

function TrashIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d="M3 6h18" /><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" /><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
    </svg>
  )
}
