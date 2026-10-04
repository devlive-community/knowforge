import { useCallback, useEffect, useMemo, useState } from 'react'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { cachedCount, pwaSupported, readerPath, removeOffline, saveOffline } from '@/lib/pwa'
import type { Document } from '@/lib/types'
import { Button, Modal, Tooltip } from '@/components/ui'

interface Props {
  bookSlug: string
  tree: Document[]
  className?: string
}

function chapterPaths(bookSlug: string, nodes: Document[]): string[] {
  const out: string[] = []
  const walk = (list: Document[]) => {
    for (const d of list) {
      if (!d.external_url) out.push(readerPath(bookSlug, d.slug))
      if (d.children?.length) walk(d.children)
    }
  }
  walk(nodes)
  return out
}

// OfflineBookButton 书籍详情页「离线阅读」：把全部章节（含正文中的图片）保存到浏览器，断网时也能阅读；可更新或移除。
// 浏览器不支持或站点关闭了离线阅读时不显示。
export default function OfflineBookButton({ bookSlug, tree, className }: Props) {
  const { t } = useTranslation()
  const { site } = useApp()
  const [supported, setSupported] = useState(false)
  const [open, setOpen] = useState(false)
  const [cached, setCached] = useState<number | null>(null)
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null)
  const [removing, setRemoving] = useState(false)
  const [message, setMessage] = useState('')
  const paths = useMemo(() => chapterPaths(bookSlug, tree), [bookSlug, tree])

  useEffect(() => { setSupported(pwaSupported() && site.pwa_enabled !== false) }, [site.pwa_enabled])
  const refresh = useCallback(async () => setCached(await cachedCount(paths)), [paths])
  useEffect(() => { if (open) void refresh() }, [open, refresh])

  if (!supported || paths.length === 0) return null

  async function download() {
    setMessage('')
    setProgress({ done: 0, total: paths.length })
    const saved = await saveOffline(paths, (done, total) => setProgress({ done, total }))
    setProgress(null)
    await refresh()
    setMessage(saved === paths.length ? t('pwa.book.saved', { n: saved }) : t('pwa.book.partial', { n: saved, total: paths.length }))
  }

  async function remove() {
    setRemoving(true)
    await removeOffline(paths)
    await refresh()
    setRemoving(false)
    setMessage(t('pwa.book.removed'))
  }

  const busy = progress !== null || removing
  return (
    <>
      <Tooltip content={t('pwa.book.button')} className={className}>
        <Button type="button" variant="outline" onClick={() => setOpen(true)} aria-label={t('pwa.book.button')} data-testid="offline-book"
          className="w-full !px-0 text-slate-500 sm:w-[var(--control-height)]">
          <i className="fa-solid fa-cloud-arrow-down" aria-hidden="true" />
        </Button>
      </Tooltip>
      <Modal open={open} onClose={() => { if (!busy) setOpen(false) }} title={t('pwa.book.title')}
        footer={(
          <>
            {cached !== null && cached > 0 && (
              <Button variant="ghost" className="text-rose-600 hover:bg-rose-50" loading={removing} disabled={busy} onClick={() => void remove()}>{t('pwa.book.remove')}</Button>
            )}
            <Button loading={progress !== null} disabled={busy} onClick={() => void download()} data-testid="offline-book-download">
              {progress ? t('pwa.book.downloading', { done: progress.done, total: progress.total }) : t(cached ? 'pwa.book.update' : 'pwa.book.download')}
            </Button>
          </>
        )}>
        <div className="space-y-3 text-sm text-slate-600" data-testid="offline-book-dialog">
          <p>{t('pwa.book.description')}</p>
          <p className="font-medium text-slate-800" data-testid="offline-book-status">
            {cached === null ? t('pwa.book.checking') : t('pwa.book.status', { n: cached, total: paths.length })}
          </p>
          {progress && (
            <div className="h-1.5 overflow-hidden rounded-full bg-slate-100">
              <div className="h-full rounded-full bg-primary-500 transition-all" style={{ width: `${Math.round((progress.done / progress.total) * 100)}%` }} />
            </div>
          )}
          {message && <p className="text-xs text-emerald-700">{message}</p>}
          <p className="text-xs text-slate-400">{t('pwa.book.note')}</p>
        </div>
      </Modal>
    </>
  )
}
