import { useEffect, useRef } from 'react'
import type { GetServerSideProps, InferGetServerSidePropsType } from 'next'
import EmbedFrame from '@/components/EmbedFrame'
import { getSiteConfig, isInstalled, serverApi, siteUrlFrom } from '@/lib/server-api'
import { bindMarkdownInteractivity, renderMarkdown } from '@/lib/markdown'
import { useTranslation } from '@/lib/i18n'
import { embedEnabled } from '@/lib/embed'
import type { Book, Document } from '@/lib/types'

// 嵌入组件 · 单个章节：以游客身份渲染正文（付费章节只有服务端给出的试读部分），链接回本站阅读。
export const getServerSideProps: GetServerSideProps<{ site: Record<string, string>; siteUrl: string; book: Book; doc: Document; html: string; locked: boolean }> = async ({ req, params }) => {
  if (!(await isInstalled())) return { notFound: true }
  const site = await getSiteConfig()
  const slug = typeof params?.slug === 'string' ? params.slug : ''
  const docSlug = typeof params?.doc === 'string' ? params.doc : ''
  if (!embedEnabled(site) || !slug || !docSlug) return { notFound: true }
  try {
    const book = await serverApi<Book>(`/books/slug/${encodeURIComponent(slug)}`)
    const doc = await serverApi<Document>(`/books/${book.id}/documents/slug/${encodeURIComponent(docSlug)}`)
    if (doc.status !== 'published') return { notFound: true }
    return { props: { site, siteUrl: siteUrlFrom(req), book, doc, html: renderMarkdown(doc.content || '', { bookSlug: book.slug }), locked: !!doc.paywall } }
  } catch {
    return { notFound: true }
  }
}

export default function EmbedDoc({ site, siteUrl, book, doc, html, locked }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => { if (ref.current) bindMarkdownInteractivity(ref.current) }, [html])
  const siteName = site.site_name || 'KnowForge'
  const reader = `/book/reader/${encodeURIComponent(book.slug)}/${encodeURIComponent(doc.slug)}`
  return (
    <EmbedFrame siteName={siteName} siteUrl={siteUrl} href={reader} title={`${doc.title} - ${book.title}`}>
      <a href={`${siteUrl}/book/detail/${encodeURIComponent(book.slug)}`} target="_blank" rel="noopener" className="text-xs text-slate-500 hover:text-primary-600">{book.title}</a>
      <h1 className="mt-1 text-xl font-bold text-slate-900">{doc.title}</h1>
      {doc.external_url ? (
        <p className="mt-4 text-sm"><a href={doc.external_url} target="_blank" rel="noopener nofollow" className="text-primary-600">{doc.external_url}</a></p>
      ) : (
        <div ref={ref} className="markdown-body mt-4" dangerouslySetInnerHTML={{ __html: html }} />
      )}
      {locked && (
        <div className="mt-4 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
          {t('embed.paidPreview')} <a href={siteUrl + reader} target="_blank" rel="noopener" className="font-medium text-primary-600">{t('embed.unlockOnSite', { site: siteName })}</a>
        </div>
      )}
    </EmbedFrame>
  )
}
