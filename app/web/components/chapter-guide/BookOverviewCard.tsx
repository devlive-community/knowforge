import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { api } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { renderMarkdown } from '@/lib/markdown'

// 折叠时显示的高度（px）：约前几段，完整内容点「展开全文」查看，避免长概览把目录挤到下面。
const COLLAPSED_HEIGHT = 176

// BookOverviewCard 书籍详情页「关于这本书」中的「全书概览」（Markdown，经 XSS 净化渲染）；默认折叠，没有概览时不显示。
export default function BookOverviewCard({ bookId, bookSlug }: { bookId: number; bookSlug: string }) {
  const { t } = useTranslation()
  const [content, setContent] = useState('')
  const [expanded, setExpanded] = useState(false)
  const [overflowing, setOverflowing] = useState(false)
  const bodyRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    setExpanded(false)
    api<{ overview: { content: string } | null }>(`/chapter-guides/books/${bookId}/overview`)
      .then((d) => setContent(d.overview?.content || ''))
      .catch(() => setContent(''))
  }, [bookId])
  const html = useMemo(() => (content ? renderMarkdown(content, { bookSlug }) : ''), [content, bookSlug])
  // 内容不超过折叠高度时不显示展开按钮
  useLayoutEffect(() => {
    const el = bodyRef.current
    setOverflowing(!!el && el.scrollHeight > COLLAPSED_HEIGHT + 24)
  }, [html])
  if (!html) return null
  const collapsed = overflowing && !expanded
  return (
    <div className="mt-6 max-w-3xl rounded-xl border border-slate-200 bg-white p-5">
      <h3 className="flex items-center gap-2 text-base font-bold text-slate-900">
        <i className="fa-solid fa-map text-primary-500" aria-hidden="true" />{t('chapterGuide.overview.title')}
      </h3>
      <div className="relative mt-3">
        <div ref={bodyRef} id={`book-overview-${bookId}`} className="markdown-body overflow-hidden text-[15px]"
          style={collapsed ? { maxHeight: COLLAPSED_HEIGHT } : undefined} dangerouslySetInnerHTML={{ __html: html }} />
        {collapsed && <div className="pointer-events-none absolute inset-x-0 bottom-0 h-16 bg-gradient-to-t from-white to-transparent" />}
      </div>
      {overflowing && (
        <button type="button" onClick={() => setExpanded((v) => !v)} aria-expanded={expanded} aria-controls={`book-overview-${bookId}`}
          className="mt-2 inline-flex items-center gap-1 text-sm font-medium text-primary-600 hover:text-primary-700">
          {t(expanded ? 'chapterGuide.overview.collapse' : 'chapterGuide.overview.expand')}
          <i className={`fa-solid fa-chevron-${expanded ? 'up' : 'down'} text-xs`} aria-hidden="true" />
        </button>
      )}
    </div>
  )
}
