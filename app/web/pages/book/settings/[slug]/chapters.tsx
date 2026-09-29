import { useCallback, useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import type { InferGetServerSidePropsType } from 'next'
import { postEventStream } from '@/lib/event-stream'
import { api } from '@/lib/api'
import { Badge, Button, ButtonLink, Checkbox, DropdownMenu, Field, Loading, EmptyState, Modal, Select, Tooltip, useFeedback } from '@/components/ui'
import { ChevronDownIcon, ChevronRightIcon, GripIcon, HistoryIcon, LinkIcon, PencilIcon, TrashIcon } from '@/components/icons'
import BookSettingsLayout from '@/components/BookSettingsLayout'
import BookSearchSelect, { BookLite } from '@/components/BookSearchSelect'
import DocTreeIcon from '@/components/DocTreeIcon'
import { getBookSettingsProps } from '@/lib/book-settings'
import { useTranslation } from '@/lib/i18n'
import type { Book, Document, DocumentStatus, PageResult } from '@/lib/types'

export const getServerSideProps = getBookSettingsProps

const STATUS: Record<string, { labelKey: string; tone: 'emerald' | 'slate' | 'amber' }> = {
  published: { labelKey: 'books.status.published', tone: 'emerald' },
  draft: { labelKey: 'books.status.draft', tone: 'amber' },
  archived: { labelKey: 'books.status.archived', tone: 'slate' },
}

// 可切换的发布状态（供“更多操作”菜单，排除当前状态）
const STATUS_ACTIONS: { value: DocumentStatus; labelKey: string }[] = [
  { value: 'published', labelKey: 'bookSettings.chapters.action.setPublished' },
  { value: 'draft', labelKey: 'bookSettings.chapters.action.setDraft' },
  { value: 'archived', labelKey: 'bookSettings.chapters.action.archive' },
]

type DropPos = 'before' | 'inside' | 'after'

function flatten(docs: Document[]): Document[] {
  return docs.flatMap((d) => [d, ...flatten(d.children || [])])
}

// 书籍设置 · 章节管理：查看全部章节，支持拖拽排序（含跨层级），快速跳转编辑或删除（仅可管理者）
export default function BookSettingsChapters({ book }: InferGetServerSidePropsType<typeof getBookSettingsProps>) {
  const { showToast, confirmAction } = useFeedback()
  const { t } = useTranslation()
  const [docs, setDocs] = useState<Document[] | null>(null)
  const [busy, setBusy] = useState<number | null>(null)
  // 批量操作进度：逐个处理所选章节，current 为正在处理的章节，failed 为失败数
  const [bulk, setBulk] = useState<{ kind: 'status' | 'delete'; status?: DocumentStatus; done: number; total: number; current: number | null; failed: number } | null>(null)
  const bulkBusy = bulk !== null
  const [expanded, setExpanded] = useState<Set<number>>(new Set())
  const [dragId, setDragId] = useState<number | null>(null)
  const [dropTarget, setDropTarget] = useState<{ id: number; pos: DropPos } | null>(null)
  const [reordering, setReordering] = useState(false)
  const [menuFor, setMenuFor] = useState<number | null>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [copyIds, setCopyIds] = useState<number[] | null>(null) // 打开复制弹框的目标章节 id（多选或单条）
  const [copyMode, setCopyMode] = useState<'copy' | 'move'>('copy') // 复制到书籍 / 移动到书籍
  const [bulkMenuOpen, setBulkMenuOpen] = useState(false)
  const openTransfer = (ids: number[], mode: 'copy' | 'move') => { setCopyMode(mode); setCopyIds(ids) }

  const load = useCallback(() => {
    api<Document[]>(`/books/${book.id}/documents`).then((d) => setDocs(d || [])).catch((e) => showToast({ title: t('bookSettings.chapters.error.load'), message: (e as Error).message, tone: 'error' }))
  }, [book.id, showToast, t])
  useEffect(() => { load() }, [load])

  const flat = useMemo(() => (docs ? flatten(docs) : []), [docs])

  // 拖拽起点自身子树内的节点不可作为放置目标（避免把父级拖进自己的孩子）
  const blocked = useMemo(() => {
    if (dragId == null) return null
    const set = new Set<number>()
    const walk = (d: Document) => { set.add(d.id); (d.children || []).forEach(walk) }
    const root = flat.find((d) => d.id === dragId)
    if (root) walk(root)
    return set
  }, [dragId, flat])

  async function remove(doc: Document) {
    if (!(await confirmAction({ title: t('books.delete.title'), message: t('bookSettings.chapters.delete.message', { title: doc.title }), confirmLabel: t('books.delete.confirm'), danger: true }))) return
    setBusy(doc.id)
    try {
      await api(`/documents/${doc.id}`, { method: 'DELETE' })
      load()
    } catch (e) {
      showToast({ title: t('books.error.delete'), message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  async function changeStatus(doc: Document, status: DocumentStatus) {
    // 含子章节时询问：仅本章 or 本章及子章节
    let cascade = false
    const childCount = flatten(doc.children || []).length
    if (childCount > 0) {
      cascade = await confirmAction({
        title: t('bookSettings.chapters.cascade.title', { status: STATUS[status] ? t(STATUS[status].labelKey) : t('bookSettings.chapters.status.new') }),
        message: t('bookSettings.chapters.cascade.message', { title: doc.title, count: childCount }),
        confirmLabel: t('bookSettings.chapters.cascade.confirm'), cancelLabel: t('bookSettings.chapters.cascade.cancel'),
      })
    }
    setBusy(doc.id)
    try {
      await api(`/documents/${doc.id}`, { method: 'PUT', body: { status, cascade_status: cascade } })
      showToast({ message: STATUS[status] ? t('bookSettings.chapters.status.changed', { status: t(STATUS[status].labelKey) }) : t('bookSettings.chapters.status.updated'), tone: 'success' })
      load()
    } catch (e) {
      showToast({ title: t('bookSettings.chapters.error.status'), message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  // setStatusLocal 本地更新章节（及其子章节，与服务端级联一致）的状态，批量处理时逐行即时切换显示。
  function setStatusLocal(id: number, status: DocumentStatus) {
    const apply = (list: Document[], inside: boolean): Document[] => list.map((d) => {
      const hit = inside || d.id === id
      return { ...d, status: hit ? status : d.status, children: d.children ? apply(d.children, hit) : d.children }
    })
    setDocs((ds) => (ds ? apply(ds, false) : ds))
  }

  // 批量改状态：一次提交所选章节（按目录顺序），服务端逐个处理并以事件流推送进度（含子章节级联）；
  // 工具栏显示进度，正在处理的章节显示处理中，完成的章节即时切换状态；失败的章节保持选中，便于重试。
  async function bulkStatus(status: DocumentStatus) {
    const ids = flat.filter((d) => selected.has(d.id)).map((d) => d.id)
    if (ids.length === 0) return
    const failed: number[] = []
    setBulk({ kind: 'status', status, done: 0, total: ids.length, current: ids[0], failed: 0 })
    try {
      await postEventStream(`/books/${book.id}/documents/batch-status`, { ids, status }, (name, data) => {
        if (name !== 'item') return
        const r = data.result as { id: number; ok: boolean; status?: DocumentStatus }
        if (r.ok && r.status) setStatusLocal(r.id, r.status)
        if (!r.ok) failed.push(r.id)
        setBulk((b) => (b ? { ...b, done: data.done, failed: failed.length, current: ids[data.done] ?? null } : b))
      })
      if (failed.length === 0) {
        showToast({ message: t('bookSettings.chapters.bulk.statusDone', { count: ids.length }), tone: 'success' })
      } else {
        showToast({ title: t('bookSettings.chapters.error.status'), message: t('bookSettings.chapters.bulk.partialFailed', { done: ids.length - failed.length, failed: failed.length }), tone: 'error' })
      }
      setSelected(new Set(failed))
    } catch (e) {
      showToast({ title: t('bookSettings.chapters.error.status'), message: (e as Error).message, tone: 'error' })
    } finally {
      setBulk(null)
      load()
    }
  }

  // 批量删除：删除父章节会连带其子树，逐个删除并忽略已随父级删除的项。
  async function bulkDelete() {
    const ids = Array.from(selected)
    if (ids.length === 0) return
    if (!(await confirmAction({ title: t('bookSettings.chapters.bulk.deleteTitle'), message: t('bookSettings.chapters.bulk.deleteMessage', { count: ids.length }), confirmLabel: t('books.delete.confirm'), danger: true }))) return
    const ordered = flat.filter((d) => selected.has(d.id)).map((d) => d.id)
    const failed: number[] = []
    setBulk({ kind: 'delete', done: 0, total: ordered.length, current: ordered[0], failed: 0 })
    try {
      // 服务端逐个移入回收站并推送进度；已随父章节删除的子章节记为跳过
      await postEventStream(`/books/${book.id}/documents/batch-delete`, { ids: ordered }, (name, data) => {
        if (name !== 'item') return
        if (!data.result.ok) failed.push(data.result.id)
        setBulk((b) => (b ? { ...b, done: data.done, failed: failed.length, current: ordered[data.done] ?? null } : b))
      })
      if (failed.length === 0) showToast({ message: t('bookSettings.chapters.bulk.deleteDone', { count: ordered.length }), tone: 'success' })
      else showToast({ title: t('books.error.delete'), message: t('bookSettings.chapters.bulk.partialFailed', { done: ordered.length - failed.length, failed: failed.length }), tone: 'error' })
      setSelected(new Set(failed))
    } catch (e) {
      showToast({ title: t('books.error.delete'), message: (e as Error).message, tone: 'error' })
    } finally {
      setBulk(null)
      load()
    }
  }

  async function copyLink(doc: Document) {
    const url = `${window.location.origin}/book/reader/${encodeURIComponent(book.slug)}/${encodeURIComponent(doc.slug)}`
    try {
      await navigator.clipboard.writeText(url)
      showToast({ message: t('bookSettings.chapters.linkCopied'), tone: 'success' })
    } catch {
      showToast({ title: t('bookSettings.chapters.error.copy'), message: url, tone: 'error' })
    }
  }

  // 拖拽移动：pos=before/after 挂到目标同级，inside 作为目标的子章节；批量写入受影响同级的 sort_order
  async function moveNode(dragDocId: number, targetId: number, pos: DropPos) {
    if (!docs || dragDocId === targetId) return
    const drag = flat.find((d) => d.id === dragDocId)
    const target = flat.find((d) => d.id === targetId)
    if (!drag || !target) return
    if (blocked?.has(targetId)) return

    let newParent: number | null
    let siblings: Document[]
    if (pos === 'inside') {
      newParent = target.id
      siblings = (target.children || []).slice()
    } else {
      newParent = target.parent_id ?? null
      siblings = (newParent === null ? docs : flat.find((d) => d.id === newParent)?.children || []).slice()
    }
    const without = siblings.filter((d) => d.id !== dragDocId)
    let insertAt = without.length
    if (pos !== 'inside') {
      const idx = without.findIndex((d) => d.id === targetId)
      if (idx < 0) return
      insertAt = pos === 'before' ? idx : idx + 1
    }
    without.splice(insertAt, 0, drag)

    const parentChanged = (drag.parent_id ?? null) !== newParent
    const writes: Promise<unknown>[] = []
    without.forEach((d, i) => {
      if (d.id === dragDocId) {
        if (parentChanged || d.sort_order !== i) writes.push(api(`/documents/${d.id}`, { method: 'PUT', body: { parent_id: newParent, sort_order: i } }))
      } else if (d.sort_order !== i) {
        writes.push(api(`/documents/${d.id}`, { method: 'PUT', body: { sort_order: i } }))
      }
    })
    if (!writes.length) return
    setReordering(true)
    try {
      await Promise.all(writes)
      if (pos === 'inside') setExpanded((s) => new Set(s).add(target.id))
      load()
    } catch (e) {
      showToast({ title: t('bookSettings.chapters.error.sort'), message: (e as Error).message, tone: 'error' })
    } finally {
      setReordering(false)
    }
  }

  function endDrag() { setDragId(null); setDropTarget(null) }

  function renderNode(doc: Document, level: number) {
    const meta = STATUS[doc.status] || STATUS.draft
    const hasChildren = !!doc.children?.length
    const isExpanded = expanded.has(doc.id)
    const dragging = dragId === doc.id
    const dropHere = dropTarget?.id === doc.id
    return (
      <li key={doc.id}>
        <div
          draggable={!reordering}
          onDragStart={(e) => { e.dataTransfer.effectAllowed = 'move'; setDragId(doc.id) }}
          onDragEnd={endDrag}
          onDragOver={(e) => {
            if (dragId == null || dragId === doc.id || blocked?.has(doc.id)) return
            e.preventDefault()
            const rect = e.currentTarget.getBoundingClientRect()
            const y = e.clientY - rect.top
            const pos: DropPos = y < rect.height * 0.3 ? 'before' : y > rect.height * 0.7 ? 'after' : 'inside'
            setDropTarget({ id: doc.id, pos })
          }}
          onDrop={(e) => { e.preventDefault(); if (dragId != null && dropTarget) moveNode(dragId, doc.id, dropTarget.pos); endDrag() }}
          className={`group relative flex items-center gap-3 rounded-lg px-3 py-3 hover:bg-slate-50/60 ${dragging ? 'opacity-40' : ''}`}
          style={{ marginLeft: level * 20 }}>
          {dropHere && dropTarget!.pos !== 'inside' && <span className={`pointer-events-none absolute inset-x-2 z-10 h-0.5 rounded-full bg-primary-500 ${dropTarget!.pos === 'before' ? 'top-0' : 'bottom-0'}`} />}
          {dropHere && dropTarget!.pos === 'inside' && <span className="pointer-events-none absolute inset-0 z-10 rounded-lg ring-2 ring-inset ring-primary-400" />}
          <span className="shrink-0" onClick={(e) => e.stopPropagation()}>
            <Checkbox ariaLabel={t('bookSettings.chapters.copy.select', { title: doc.title })}
              checked={selected.has(doc.id)}
              onChange={(v) => setSelected((s) => { const n = new Set(s); v ? n.add(doc.id) : n.delete(doc.id); return n })} />
          </span>
          <Tooltip content={t('bookSettings.chapters.tooltip.drag')}>
            <span className="flex h-6 w-5 shrink-0 cursor-grab items-center justify-center text-slate-300 group-hover:text-slate-500"><GripIcon className="h-4 w-4" /></span>
          </Tooltip>
          {hasChildren ? (
            <button type="button" aria-label={isExpanded ? t('bookSettings.chapters.aria.collapse') : t('bookSettings.chapters.aria.expand')}
              onClick={() => setExpanded((s) => { const n = new Set(s); n.has(doc.id) ? n.delete(doc.id) : n.add(doc.id); return n })}
              className="flex h-6 w-5 shrink-0 items-center justify-center rounded text-slate-400 hover:bg-slate-200 hover:text-slate-600">
              {isExpanded ? <ChevronDownIcon className="h-3.5 w-3.5" /> : <ChevronRightIcon className="h-3.5 w-3.5" />}
            </button>
          ) : (
            <span className="w-5 shrink-0" />
          )}
          <DocTreeIcon icon={doc.icon} hasChildren={hasChildren} colorClass={hasChildren ? 'text-slate-400' : 'text-slate-300'} />
          <span className="min-w-0 flex-1 truncate font-medium text-slate-800">{book.chapter_prefix}{doc.title}</span>
          <Badge tone={meta.tone}>{t(meta.labelKey)}</Badge>
          {(busy === doc.id || bulk?.current === doc.id) && (
            <span className="flex shrink-0 items-center gap-1.5 text-xs text-slate-400">
              <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-slate-200 border-t-primary-500" />
              {t('bookSettings.chapters.processing')}
            </span>
          )}
          <div className={`flex shrink-0 items-center gap-0.5 ${busy === doc.id ? 'pointer-events-none opacity-40' : ''}`}>
            <Tooltip content={t('common.actions.edit')}>
              <Link href={`/book/writer/${encodeURIComponent(book.slug)}/${encodeURIComponent(doc.slug)}`} aria-label={t('common.actions.edit')}
                className="flex items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-slate-100 hover:text-primary-600"
                style={{ width: 'var(--control-height-sm)', height: 'var(--control-height-sm)' }}>
                <PencilIcon className="h-4 w-4" />
              </Link>
            </Tooltip>
            <Tooltip content={t('bookSettings.chapters.tooltip.copyLink')}>
              <button type="button" aria-label={t('bookSettings.chapters.tooltip.copyLink')} onClick={() => copyLink(doc)}
                className="flex items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-slate-100 hover:text-primary-600"
                style={{ width: 'var(--control-height-sm)', height: 'var(--control-height-sm)' }}>
                <LinkIcon className="h-4 w-4" />
              </button>
            </Tooltip>
            <DropdownMenu open={menuFor === doc.id} onOpenChange={(o) => setMenuFor(o ? doc.id : null)}>
              <Link role="menuitem" href={`/book/writer/${encodeURIComponent(book.slug)}/${encodeURIComponent(doc.slug)}`} onClick={() => setMenuFor(null)}
                className="flex items-center gap-2.5 px-4 py-2.5 text-sm text-slate-700 hover:bg-slate-50">
                <HistoryIcon className="h-4 w-4 text-slate-400" /> {t('bookSettings.chapters.menu.history')}
              </Link>
              <button role="menuitem" onClick={() => { setMenuFor(null); openTransfer([doc.id], 'copy') }}
                className="flex w-full items-center gap-2.5 px-4 py-2.5 text-left text-sm text-slate-700 hover:bg-slate-50">
                <i className="fa-solid fa-copy w-4 text-center text-slate-400" aria-hidden="true" /> {t('bookSettings.chapters.menu.copyTo')}
              </button>
              <button role="menuitem" onClick={() => { setMenuFor(null); openTransfer([doc.id], 'move') }}
                className="flex w-full items-center gap-2.5 px-4 py-2.5 text-left text-sm text-slate-700 hover:bg-slate-50">
                <i className="fa-solid fa-arrow-right-arrow-left w-4 text-center text-slate-400" aria-hidden="true" /> {t('bookSettings.chapters.menu.moveTo')}
              </button>
              <div className="my-1 border-t border-slate-100" />
              {STATUS_ACTIONS.filter((s) => s.value !== doc.status).map((s) => (
                <button key={s.value} role="menuitem" disabled={busy === doc.id}
                  onClick={() => { setMenuFor(null); changeStatus(doc, s.value) }}
                  className="flex w-full items-center gap-2.5 px-4 py-2.5 text-left text-sm text-slate-700 hover:bg-slate-50 disabled:opacity-50">
                  <span className={`h-2 w-2 rounded-full ${s.value === 'published' ? 'bg-emerald-500' : s.value === 'draft' ? 'bg-amber-500' : 'bg-slate-400'}`} /> {t(s.labelKey)}
                </button>
              ))}
              <div className="my-1 border-t border-slate-100" />
              <button role="menuitem" disabled={busy === doc.id} onClick={() => { setMenuFor(null); remove(doc) }}
                className="flex w-full items-center gap-2.5 px-4 py-2.5 text-left text-sm text-rose-600 hover:bg-rose-50 disabled:opacity-50">
                <TrashIcon className="h-4 w-4" /> {t('books.delete.confirm')}
              </button>
            </DropdownMenu>
          </div>
        </div>
        {hasChildren && isExpanded && (
          <ul>{doc.children!.map((child) => renderNode(child, level + 1))}</ul>
        )}
      </li>
    )
  }

  return (
    <BookSettingsLayout book={book} active="chapters">
      <div className="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 p-6">
          <div>
            <h2 className="text-lg font-bold text-slate-900">{t('bookSettings.chapters.title')}</h2>
            <p className="mt-1 text-sm text-slate-500">{t('bookSettings.chapters.desc')}</p>
          </div>
          <ButtonLink href={`/book/writer/${encodeURIComponent(book.slug)}`}>{t('bookSettings.chapters.goWriter')}</ButtonLink>
        </div>

        {selected.size > 0 && (
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 bg-primary-50/50 px-6 py-3">
            <div className="flex items-center gap-3 text-sm text-slate-600">
              {/* 全选：勾选后选中全部章节，再点取消全选 */}
              <label className="flex cursor-pointer items-center gap-1.5">
                <Checkbox ariaLabel={t('bookSettings.chapters.copy.selectAll')}
                  checked={flat.length > 0 && flat.every((d) => selected.has(d.id))}
                  onChange={(all) => setSelected(all ? new Set(flat.map((d) => d.id)) : new Set())} />
                {t('bookSettings.chapters.copy.selectAll')}
              </label>
              <span>{t('bookSettings.chapters.copy.selectedCount', { count: selected.size })}</span>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <DropdownMenu open={bulkMenuOpen} onOpenChange={setBulkMenuOpen} label={t('bookSettings.chapters.copy.action')}>
                <button role="menuitem" onClick={() => { setBulkMenuOpen(false); openTransfer(Array.from(selected), 'copy') }}
                  className="flex w-full items-center gap-2.5 px-4 py-2.5 text-left text-sm text-slate-700 hover:bg-slate-50">
                  <i className="fa-solid fa-copy w-4 text-center text-slate-400" aria-hidden="true" /> {t('bookSettings.chapters.menu.copyTo')}
                </button>
                <button role="menuitem" onClick={() => { setBulkMenuOpen(false); openTransfer(Array.from(selected), 'move') }}
                  className="flex w-full items-center gap-2.5 px-4 py-2.5 text-left text-sm text-slate-700 hover:bg-slate-50">
                  <i className="fa-solid fa-arrow-right-arrow-left w-4 text-center text-slate-400" aria-hidden="true" /> {t('bookSettings.chapters.menu.moveTo')}
                </button>
              </DropdownMenu>
              <span className="mx-1 hidden h-5 w-px bg-slate-200 sm:block" />
              <span className="text-xs text-slate-400">{t('bookSettings.chapters.bulk.setStatus')}</span>
              {STATUS_ACTIONS.map((s) => (
                <Button key={s.value} size="sm" variant="outline" disabled={bulkBusy} loading={bulk?.kind === 'status' && bulk.status === s.value} onClick={() => bulkStatus(s.value)}>{t(s.labelKey)}</Button>
              ))}
              <Button size="sm" variant="outline" disabled={bulkBusy} loading={bulk?.kind === 'delete'} className="border-rose-300 text-rose-600 hover:bg-rose-50" onClick={bulkDelete}>
                <TrashIcon className="h-4 w-4" /> {t('bookSettings.chapters.bulk.delete')}
              </Button>
              <Button size="sm" variant="ghost" disabled={bulkBusy} onClick={() => setSelected(new Set())}>{t('bookSettings.chapters.copy.clear')}</Button>
            </div>
            {bulk && (
              <div className="w-full" role="status" aria-live="polite">
                <div className="flex items-center justify-between text-xs text-slate-500">
                  <span>{bulk.kind === 'delete'
                    ? t('bookSettings.chapters.bulk.deleting', { done: bulk.done, total: bulk.total })
                    : t('bookSettings.chapters.bulk.updating', { status: t(STATUS[bulk.status!]?.labelKey || 'bookSettings.chapters.status.new'), done: bulk.done, total: bulk.total })}</span>
                  {bulk.failed > 0 && <span className="text-rose-600">{t('bookSettings.chapters.bulk.failedCount', { n: bulk.failed })}</span>}
                </div>
                <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-primary-100">
                  <div className="h-full rounded-full bg-primary-500 transition-all duration-300" style={{ width: `${Math.round((bulk.done / bulk.total) * 100)}%` }} />
                </div>
              </div>
            )}
          </div>
        )}

        {docs === null ? (
          <Loading className="py-16" label={t('bookSettings.chapters.loading')} />
        ) : docs.length === 0 ? (
          <div className="p-6"><EmptyState>{t('bookSettings.chapters.empty')}<Link href={`/book/writer/${encodeURIComponent(book.slug)}`} className="text-primary-600 hover:underline">{t('bookSettings.chapters.emptyLink')}</Link></EmptyState></div>
        ) : (
          <ul className="p-3">{docs.map((doc) => renderNode(doc, 0))}</ul>
        )}
      </div>

      {copyIds !== null && (
        <CopyToBookDialog sourceBookId={book.id} sourceBookSlug={book.slug} docIds={copyIds} mode={copyMode}
          onClose={() => setCopyIds(null)}
          onDone={() => { setCopyIds(null); setSelected(new Set()) }} />
      )}
    </BookSettingsLayout>
  )
}

// CopyToBookDialog 将选定章节（含子章节）复制或移动到目标书籍。
function CopyToBookDialog({ sourceBookId, docIds, mode, onClose, onDone }: {
  sourceBookId: number
  sourceBookSlug: string
  docIds: number[]
  mode: 'copy' | 'move'
  onClose: () => void
  onDone: () => void
}) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [target, setTarget] = useState<BookLite | null>(null)
  const [saving, setSaving] = useState(false)
  const isMove = mode === 'move'

  async function submit() {
    if (!target) return
    setSaving(true)
    try {
      const r = await api<{ copied_documents: number; target_slug: string }>(`/books/${sourceBookId}/documents/copy`, {
        method: 'POST', body: { target_book_id: target.id, doc_ids: docIds, move: isMove },
      })
      showToast({ message: t(isMove ? 'bookSettings.chapters.move.done' : 'bookSettings.chapters.copy.done', { count: r.copied_documents }), tone: 'success' })
      onDone()
    } catch (e) {
      showToast({ title: t(isMove ? 'bookSettings.chapters.move.failed' : 'bookSettings.chapters.copy.failed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal open onClose={onClose} title={t(isMove ? 'bookSettings.chapters.move.title' : 'bookSettings.chapters.copy.title')}
      footer={<><Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button><Button loading={saving} disabled={!target} onClick={submit}>{t(isMove ? 'bookSettings.chapters.move.confirm' : 'bookSettings.chapters.copy.confirm')}</Button></>}>
      <div className="space-y-4">
        <p className="text-sm text-slate-500">{t(isMove ? 'bookSettings.chapters.move.desc' : 'bookSettings.chapters.copy.desc', { count: docIds.length })}</p>
        <Field label={t('bookSettings.chapters.copy.target')}>
          {/* 通用可搜索书籍选择器：查自己的书（含协作），排除源书自身 */}
          <BookSearchSelect value={target} onChange={setTarget} endpoint="/books" searchParam="title"
            baseParams={{ mine: true }} excludeId={sourceBookId} placeholder={t('bookSettings.chapters.copy.targetPlaceholder')} />
        </Field>
        <p className="text-xs text-slate-400">{t('bookSettings.chapters.copy.hint')}</p>
      </div>
    </Modal>
  )
}
