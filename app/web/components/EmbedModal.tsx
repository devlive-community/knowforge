import { useMemo, useState } from 'react'
import { useTranslation } from '@/lib/i18n'
import { bookCardEmbedPath, bookEmbedPath, docEmbedPath, embedSnippet } from '@/lib/embed'
import type { Document } from '@/lib/types'
import { Button, Field, Input, Modal, Select, Textarea, useFeedback } from '@/components/ui'

function flatten(docs: Document[] | null | undefined, depth = 0): { doc: Document; depth: number }[] {
  return (docs || []).flatMap((d) => [{ doc: d, depth }, ...flatten(d.children, depth + 1)])
}

// 各嵌入内容的默认高度：书籍信息卡片较矮，整本目录与章节较高（用户改过高度后不再自动切换）。
const DEFAULT_HEIGHT = { card: '240', other: '480' }

// EmbedModal 生成嵌入代码：选择书籍信息卡片、整本目录或某个已发布章节，设置高度，预览并复制 iframe 代码。
export default function EmbedModal({ book, tree, onClose }: { book: { slug: string; title: string }; tree: Document[]; onClose: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const chapters = useMemo(() => flatten(tree).filter(({ doc }) => doc.status === 'published' || !doc.status), [tree])
  const [target, setTarget] = useState('book')
  const [height, setHeight] = useState('480')
  const h = Math.min(2000, Math.max(200, Number(height) || 480))
  const path = target === 'card' ? bookCardEmbedPath(book.slug) : target === 'book' ? bookEmbedPath(book.slug) : docEmbedPath(book.slug, target)
  const url = typeof window === 'undefined' ? path : window.location.origin + path
  const chapter = chapters.find(({ doc }) => doc.slug === target)?.doc
  const code = embedSnippet(url, chapter ? `${chapter.title} - ${book.title}` : book.title, h)

  function changeTarget(next: string) {
    const kind = (v: string) => (v === 'card' ? 'card' : 'other')
    if (height === DEFAULT_HEIGHT[kind(target)]) setHeight(DEFAULT_HEIGHT[kind(next)])
    setTarget(next)
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(code)
      showToast({ message: t('embed.copied'), tone: 'success' })
    } catch {
      showToast({ message: t('embed.copyFailed'), tone: 'error' })
    }
  }

  return (
    <Modal open onClose={onClose} className="max-w-3xl" title={t('embed.title')}
      footer={<><Button variant="outline" onClick={onClose}>{t('common.actions.close')}</Button><Button onClick={() => void copy()}><i className="fa-regular fa-copy" aria-hidden="true" />{t('embed.copyCode')}</Button></>}>
      <div className="space-y-4">
        <p className="text-sm text-slate-500">{t('embed.hint')}</p>
        <div className="grid gap-4 sm:grid-cols-[1fr_160px]">
          <Field label={t('embed.target')}>
            <Select value={target} onChange={changeTarget} searchable
              options={[{ value: 'card', label: t('embed.bookCard') }, { value: 'book', label: t('embed.wholeBook') }, ...chapters.map(({ doc, depth }) => ({ value: doc.slug, label: `${'　'.repeat(depth)}${doc.title}` }))]} />
          </Field>
          <Field label={t('embed.height')}>
            <Input type="number" min={200} max={2000} value={height} onChange={(e) => setHeight(e.target.value)} trailing={<span className="text-xs text-slate-400">px</span>} />
          </Field>
        </div>
        <Field label={t('embed.code')}>
          <Textarea readOnly value={code} aria-label={t('embed.code')} rows={3} className="resize-none bg-slate-50 font-mono text-xs" onFocus={(e) => e.currentTarget.select()} />
        </Field>
        <div>
          <div className="mb-1.5 text-sm font-medium text-slate-700">{t('embed.preview')}</div>
          {/* eslint-disable-next-line no-restricted-syntax -- iframe 的 title 是无障碍名称（屏幕阅读器朗读），不是悬停提示 */}
          <iframe key={path} src={path} title={t('embed.preview')} className="w-full rounded-xl border border-slate-200" style={{ height: Math.min(h, 420) }} loading="lazy" />
        </div>
      </div>
    </Modal>
  )
}
