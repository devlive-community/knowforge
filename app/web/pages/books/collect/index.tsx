import { useMemo, useState, type DragEvent } from 'react'
import { useRouter } from 'next/router'
import Container from '@/components/Container'
import Seo from '@/components/Seo'
import { api } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { renderMarkdown } from '@/lib/markdown'
import { Button, Input, Select, Loading, EmptyState, Checkbox, useFeedback } from '@/components/ui'
import FeatureGate from '@/components/FeatureGate'
import { entitlementAllowed } from '@/lib/entitlements'
import { GripIcon } from '@/components/icons'
import { crawlSubtreeURLs, moveCrawlNode, type CrawlDropPos, type CrawlNode } from '@/lib/crawl-tree'

interface PreviewSample { url: string; ok: boolean; title?: string; markdown?: string; error?: string }
interface PreviewResult { root_url: string; tree: CrawlNode[]; sample: PreviewSample; limit: number }
type RenderMode = 'auto' | 'static' | 'browser'

function CollectWizard() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const router = useRouter()
  const siteName = site.site_name || 'KnowForge'

  const targetBookId = router.query.book_id ? Number(router.query.book_id) : 0
  const targetBookSlug = typeof router.query.book === 'string' ? router.query.book : ''
  const [url, setURL] = useState('https://')
  const [renderMode, setRenderMode] = useState<RenderMode>('auto')
  const [previewing, setPreviewing] = useState(false)
  const [preview, setPreview] = useState<PreviewResult | null>(null)
  const [title, setTitle] = useState('')
  const [excluded, setExcluded] = useState<Set<string>>(new Set())
  const [submitting, setSubmitting] = useState(false)
  // 目录可拖拽调整：nodes 为当前顺序与层级（先序扁平），提交时按它建章节目录
  const [nodes, setNodes] = useState<CrawlNode[]>([])
  const [dragURL, setDragURL] = useState<string | null>(null)
  const [dropTarget, setDropTarget] = useState<{ url: string; pos: CrawlDropPos } | null>(null)
  const blocked = useMemo(() => (dragURL ? crawlSubtreeURLs(nodes, dragURL) : null), [dragURL, nodes])
  const reordered = !!preview && nodes.some((n, i) => n.url !== preview.tree[i]?.url || n.parent_url !== preview.tree[i]?.parent_url)

  async function runPreview() {
    if (!/^https?:\/\/.+/.test(url.trim())) { showToast({ message: t('collect.invalidUrl'), tone: 'error' }); return }
    setPreviewing(true)
    setPreview(null)
    try {
      const d = await api<PreviewResult>('/collect/site/preview', { method: 'POST', body: { url: url.trim(), render_mode: renderMode } })
      setPreview(d)
      setNodes(d.tree)
      setExcluded(new Set())
      try { setTitle(new URL(d.root_url).hostname) } catch { setTitle('') }
    } catch (e) {
      showToast({ title: t('collect.previewFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setPreviewing(false)
    }
  }

  function toggle(u: string) {
    setExcluded((prev) => { const n = new Set(prev); n.has(u) ? n.delete(u) : n.add(u); return n })
  }

  async function start() {
    if (!preview) return
    const pages = nodes.filter((n) => !excluded.has(n.url))
    if (pages.length === 0) { showToast({ message: t('collect.noPages'), tone: 'error' }); return }
    setSubmitting(true)
    try {
      const d = await api<{ book: { slug: string } }>('/collect/site', { method: 'POST', body: {
        root_url: preview.root_url, title: title.trim(), render_mode: renderMode, pages,
        ...(targetBookId ? { book_id: targetBookId } : {}),
      } })
      showToast({ message: t('collect.started', { n: pages.length }), tone: 'success' })
      router.push(`/book/settings/${encodeURIComponent(d.book.slug)}/crawl-history`)
    } catch (e) {
      showToast({ title: t('collect.startFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSubmitting(false)
    }
  }

  // 放置位置：行的上四分之一放到前面，下四分之一放到后面，中间成为其子页面
  function dragOver(e: DragEvent<HTMLDivElement>, url: string) {
    if (!dragURL || blocked?.has(url)) return
    e.preventDefault()
    const rect = e.currentTarget.getBoundingClientRect()
    const y = (e.clientY - rect.top) / rect.height
    const pos: CrawlDropPos = y < 0.25 ? 'before' : y > 0.75 ? 'after' : 'inside'
    if (dropTarget?.url !== url || dropTarget.pos !== pos) setDropTarget({ url, pos })
  }
  function drop(e: DragEvent<HTMLDivElement>) {
    e.preventDefault()
    if (dragURL && dropTarget) setNodes((list) => moveCrawlNode(list, dragURL, dropTarget.url, dropTarget.pos))
    endDrag()
  }
  function endDrag() { setDragURL(null); setDropTarget(null) }

  if (!user) return <Loading className="min-h-[60vh]" label={t('account.common.verifying')} />
  if (!entitlementAllowed(user, 'collect.site', site.collect_site_enabled !== false)) {
    return <Container><EmptyState>{t('collect.siteDisabled')}</EmptyState></Container>
  }
  const included = preview ? preview.tree.length - excluded.size : 0

  return (
    <>
      <Seo siteName={siteName} title={t('collect.title')} noindex />
      <Container>
        <div className="pb-6">
          <h1 className="text-2xl font-bold text-ink">{t('collect.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('collect.subtitle')}</p>
        </div>

        {/* 第一步：地址与渲染模式 */}
        <div className="rounded-xl border border-slate-200 bg-white p-5">
          <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('collect.urlLabel')}</label>
          <div className="flex flex-col gap-2 sm:flex-row">
            <Input value={url} onChange={(e) => setURL(e.target.value)} placeholder="https://docs.example.com/" className="flex-1" />
            <span className="w-full sm:w-40">
              <Select value={renderMode} onChange={(v) => setRenderMode(v as RenderMode)} options={[
                { value: 'auto', label: t('collect.modeAuto') },
                { value: 'static', label: t('collect.modeStatic') },
                { value: 'browser', label: t('collect.modeBrowser') },
              ]} />
            </span>
            <Button loading={previewing} onClick={runPreview} className="shrink-0">{t('collect.preview')}</Button>
          </div>
          <p className="mt-2 text-xs text-slate-400">{t('collect.urlHint')}</p>
        </div>

        {previewing && <Loading className="mt-6" label={t('collect.previewing')} />}

        {preview && (
          <div className="mt-6 grid gap-5 lg:grid-cols-[1fr_1.1fr]">
            {/* 内容区确认 */}
            <div className="rounded-xl border border-slate-200 bg-white p-5">
              <h2 className="text-sm font-semibold text-slate-900">{t('collect.sampleTitle')}</h2>
              <p className="mt-1 text-xs text-slate-400">{t('collect.sampleHint')}</p>
              <p className="mt-2 break-all text-xs text-primary-600">{preview.sample.url}</p>
              {preview.sample.ok ? (
                <div className="markdown-body mt-3 max-h-80 overflow-y-auto rounded-lg border border-slate-100 bg-slate-50/60 p-4 text-sm"
                  dangerouslySetInnerHTML={{ __html: renderMarkdown(preview.sample.markdown || '') }} />
              ) : (
                <p className="mt-3 rounded-lg bg-rose-50 p-3 text-sm text-rose-600">{preview.sample.error || t('collect.sampleFailed')}</p>
              )}
            </div>

            {/* 目录预览 */}
            <div className="rounded-xl border border-slate-200 bg-white p-5">
              <div className="flex items-center justify-between gap-2">
                <h2 className="text-sm font-semibold text-slate-900">{t('collect.treeTitle')}</h2>
                <span className="flex items-center gap-2 text-xs text-slate-400">
                  {reordered && (
                    <button type="button" onClick={() => setNodes(preview.tree)} className="text-primary-600 hover:underline">{t('collect.treeReset')}</button>
                  )}
                  {t('collect.treeCount', { n: included, total: preview.tree.length })}
                </span>
              </div>
              <p className="mt-1 text-xs text-slate-400">{t('collect.treeHint')} {t('collect.treeDragHint')}</p>
              {nodes.length === 0 ? (
                <EmptyState>{t('collect.treeEmpty')}</EmptyState>
              ) : (
                <ul className="mt-3 max-h-[28rem] space-y-0.5 overflow-y-auto" data-testid="crawl-tree">
                  {nodes.map((n) => {
                    const target = dropTarget?.url === n.url ? dropTarget.pos : null
                    return (
                      <li key={n.url}>
                        <div draggable
                          onDragStart={(e) => { e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', n.url); setDragURL(n.url) }}
                          onDragOver={(e) => dragOver(e, n.url)}
                          onDragLeave={() => { if (dropTarget?.url === n.url) setDropTarget(null) }}
                          onDrop={drop}
                          onDragEnd={endDrag}
                          className={`group relative flex items-center gap-2 rounded px-1 py-1 text-sm hover:bg-slate-50 ${dragURL === n.url ? 'opacity-40' : ''} ${target === 'inside' ? 'bg-primary-50 ring-1 ring-primary-300' : ''}`}
                          style={{ paddingLeft: `${n.depth * 18 + 4}px` }}>
                          {target === 'before' && <span aria-hidden="true" className="pointer-events-none absolute inset-x-0 -top-px h-0.5 rounded bg-primary-500" style={{ left: `${n.depth * 18 + 4}px` }} />}
                          {target === 'after' && <span aria-hidden="true" className="pointer-events-none absolute inset-x-0 -bottom-px h-0.5 rounded bg-primary-500" style={{ left: `${n.depth * 18 + 4}px` }} />}
                          <GripIcon className="h-4 w-4 shrink-0 cursor-grab text-slate-300 group-hover:text-slate-400" />
                          <Checkbox checked={!excluded.has(n.url)} onChange={() => toggle(n.url)} ariaLabel={n.title || n.url} />
                          <span className="cursor-pointer truncate text-slate-700" onClick={() => toggle(n.url)}>{n.title || n.url}</span>
                        </div>
                      </li>
                    )
                  })}
                </ul>
              )}
            </div>

            {/* 确认启动 */}
            <div className="rounded-xl border border-slate-200 bg-white p-5 lg:col-span-2">
              {targetBookId ? (
                <p className="mb-3 text-sm text-slate-600">{t('collect.appendTo', { slug: targetBookSlug })}</p>
              ) : (
                <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('collect.bookTitle')}</label>
              )}
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                {!targetBookId && <Input value={title} onChange={(e) => setTitle(e.target.value)} placeholder={t('collect.bookTitlePlaceholder')} className="flex-1" />}
                <Button loading={submitting} disabled={included === 0} onClick={start} className={targetBookId ? '' : 'shrink-0'}>
                  {t('collect.start', { n: included })}
                </Button>
              </div>
              <p className="mt-2 text-xs text-slate-400">{t('collect.startHint', { limit: preview.limit })}</p>
            </div>
          </div>
        )}
      </Container>
    </>
  )
}

export default function CollectPage() {
  return (
    <FeatureGate feature="content-collect">
      <CollectWizard />
    </FeatureGate>
  )
}
