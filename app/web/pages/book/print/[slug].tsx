import type { GetServerSideProps, InferGetServerSidePropsType } from 'next'
import { useEffect, useState } from 'react'
import Head from 'next/head'
import { authHeaderFrom, isInstalled, serverApi, getSiteConfig } from '@/lib/server-api'
import { renderMarkdown, wikiDocsFromTree } from '@/lib/markdown'
import { resolveMediaUrl } from '@/lib/media'
import { useTranslation } from '@/lib/i18n'
import type { Book, Document } from '@/lib/types'
import { displayName } from '@/lib/users'

interface Chapter { id: number; title: string; level: number; html: string }
interface PrintStyle { pageSize: string; fontSize: number; codeTheme: string; margin: string; cover: boolean; toc: boolean }
interface PrintProps {
  book: Book
  chapters: Chapter[]
  chapterPrefix: string
  style: PrintStyle
  watermarkOn: boolean
}

function flatten(docs: Document[], level = 0): { doc: Document; level: number }[] {
  return docs.flatMap((d) => [{ doc: d, level }, ...flatten(d.children || [], level + 1)])
}

// 打印页：由 PDF 插件的无头浏览器内部访问，渲染整本书 + 作者水印，供 printToPDF 输出。
export const getServerSideProps: GetServerSideProps<PrintProps> = async ({ req, params, query }) => {
  if (!(await isInstalled())) return { redirect: { destination: '/install', permanent: false } }
  const slug = typeof params?.slug === 'string' ? params.slug : ''
  if (!slug) return { notFound: true }
  const auth = authHeaderFrom(req)
  const book = await serverApi<Book>(`/books/slug/${encodeURIComponent(slug)}`, { headers: auth }).catch(() => null)
  if (!book) return { notFound: true }
  const tree = await serverApi<Document[]>(`/books/${book.id}/documents`, { headers: auth }).catch(() => [] as Document[])
  const flat = flatten(tree)
  const docs = wikiDocsFromTree(tree)
  const chapters: Chapter[] = await Promise.all(flat.map(async ({ doc, level }) => {
    const full = await serverApi<Document>(`/documents/${doc.id}`, { headers: auth }).catch(() => null)
    return { id: doc.id, title: doc.title, level, html: renderMarkdown(full?.content || '', { bookSlug: book.slug, docs }) }
  }))
  const style: PrintStyle = {
    pageSize: String(query.page_size || 'A4'),
    fontSize: Number(query.font_size) || 15,
    codeTheme: query.code_theme === 'dark' ? 'dark' : 'light',
    margin: String(query.margin || 'normal'),
    cover: query.cover !== '0',
    toc: query.toc !== '0',
  }
  const site = await getSiteConfig().catch(() => ({}) as Record<string, unknown>)
  const watermarkOn = ((site as { feature_plugins?: string[] }).feature_plugins || []).includes('watermark')
  return { props: { book, chapters, chapterPrefix: book.chapter_prefix || '', style, watermarkOn } }
}

export default function PrintBook({ book, chapters, chapterPrefix, style, watermarkOn }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const [ready, setReady] = useState(false)
  useEffect(() => {
    let done = false
    const finish = () => { if (!done) { done = true; setReady(true) } }
    const waits = Array.from(document.images).map((img) =>
      img.complete ? Promise.resolve() : new Promise<void>((r) => { img.onload = () => r(); img.onerror = () => r() }))
    Promise.all(waits).then(finish)
    const timer = setTimeout(finish, 5000) // 兜底，避免图片卡住迟迟不打印
    return () => clearTimeout(timer)
  }, [])

  const cover = resolveMediaUrl(book.cover_image)
  return (
    <div className="print-book">
      <Head><title>{book.title}</title><meta name="robots" content="noindex, nofollow" /></Head>
      <style dangerouslySetInnerHTML={{ __html: printCss(style) }} />
      {watermarkOn && book.watermark_enabled && book.watermark_text && <PrintWatermark text={book.watermark_text} />}

      {style.cover && (
        <section className="print-cover">
          {cover && <img src={cover} alt="" className="cover-img" />}
          <h1>{book.title}</h1>
          {book.description && <p className="cover-desc">{book.description}</p>}
          {book.user && <p className="cover-author">{displayName(book.user)}</p>}
        </section>
      )}

      {style.toc && chapters.length > 0 && (
        <section className="print-toc">
          <h2>{t('reader.toc')}</h2>
          <ul>{chapters.map((c) => <li key={c.id} style={{ paddingLeft: c.level * 16 }}>{chapterPrefix}{c.title}</li>)}</ul>
        </section>
      )}

      {chapters.map((c) => (
        <section key={c.id} className="print-chapter">
          <h2 style={{ marginLeft: c.level * 16 }}>{chapterPrefix}{c.title}</h2>
          <div className="markdown-body" style={{ fontSize: style.fontSize }} dangerouslySetInnerHTML={{ __html: c.html }} />
        </section>
      ))}

      {ready && <div id="print-ready" />}
    </div>
  )
}

function PrintWatermark({ text }: { text: string }) {
  return (
    <div aria-hidden="true" className="print-watermark">
      {Array.from({ length: 60 }).map((_, i) => <span key={i}>{text}</span>)}
    </div>
  )
}

function printCss(style: PrintStyle): string {
  const dark = style.codeTheme === 'dark'
  return `
    body { background: #fff; margin: 0; }
    .print-book { color: #1e293b; padding: 8px 4px; }
    .print-cover { text-align: center; padding: 28vh 0; page-break-after: always; }
    .print-cover .cover-img { max-width: 60%; max-height: 40vh; object-fit: contain; display: block; margin: 0 auto 24px; border-radius: 8px; }
    .print-cover h1 { font-size: 32px; font-weight: 800; margin: 0; }
    .print-cover .cover-desc { color: #64748b; margin-top: 12px; font-size: 15px; }
    .print-cover .cover-author { color: #94a3b8; margin-top: 24px; font-size: 14px; }
    .print-toc { page-break-after: always; }
    .print-toc h2 { font-size: 22px; font-weight: 700; margin: 0 0 16px; }
    .print-toc ul { list-style: none; padding: 0; margin: 0; line-height: 2; color: #334155; }
    .print-chapter { page-break-before: always; }
    .print-chapter h2 { font-size: 24px; font-weight: 700; margin: 0 0 16px; }
    .print-chapter .markdown-body img { max-width: 100%; }
    .print-chapter .markdown-body pre { white-space: pre-wrap; word-break: break-word; ${dark ? 'background:#0f172a; color:#e2e8f0;' : ''} }
    ${dark ? '.print-chapter .markdown-body pre code { color:#e2e8f0; }' : ''}
    .print-watermark { position: fixed; inset: 0; z-index: 9999; display: flex; flex-wrap: wrap; align-content: space-around; justify-content: space-around; pointer-events: none; overflow: hidden; }
    .print-watermark span { transform: rotate(-28deg); color: rgba(100,116,139,0.10); font-size: 14px; letter-spacing: 0.16em; white-space: nowrap; padding: 40px; }
  `
}
