import type { GetServerSideProps, InferGetServerSidePropsType } from 'next'
import EmbedFrame from '@/components/EmbedFrame'
import { getSiteConfig, isInstalled, serverApi, siteUrlFrom } from '@/lib/server-api'
import { resolveMediaUrl } from '@/lib/media'
import { useTranslation } from '@/lib/i18n'
import { embedEnabled } from '@/lib/embed'
import type { Book, Document } from '@/lib/types'

const MAX_CHAPTERS = 50

interface Chapter { title: string; slug: string; depth: number }

function flatten(docs: Document[] | null | undefined, depth = 0): Chapter[] {
  return (docs || []).flatMap((d) => [{ title: d.title, slug: d.slug, depth }, ...flatten(d.children, depth + 1)])
}

// 嵌入组件 · 书籍目录：以游客身份展示书籍信息与章节目录，链接在新窗口打开本站阅读页。
export const getServerSideProps: GetServerSideProps<{ site: Record<string, string>; siteUrl: string; book: Book; chapters: Chapter[]; total: number }> = async ({ req, params }) => {
  if (!(await isInstalled())) return { notFound: true }
  const site = await getSiteConfig()
  const slug = typeof params?.slug === 'string' ? params.slug : ''
  if (!embedEnabled(site) || !slug) return { notFound: true }
  try {
    const book = await serverApi<Book>(`/books/slug/${encodeURIComponent(slug)}`) // 不带登录态：只嵌入游客可读的书
    const tree = await serverApi<Document[]>(`/books/${book.id}/documents`).catch(() => [])
    const all = flatten(tree)
    return { props: { site, siteUrl: siteUrlFrom(req), book, chapters: all.slice(0, MAX_CHAPTERS), total: all.length } }
  } catch {
    return { notFound: true }
  }
}

export default function EmbedBook({ site, siteUrl, book, chapters, total }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const detail = `/book/detail/${encodeURIComponent(book.slug)}`
  return (
    <EmbedFrame siteName={siteName} siteUrl={siteUrl} href={detail} title={book.title}>
      <div className="flex gap-4">
        {book.cover_image && <img src={resolveMediaUrl(book.cover_image)} alt="" className="h-24 w-[4.5rem] shrink-0 rounded-md object-cover shadow-sm" />}
        <div className="min-w-0">
          <a href={siteUrl + detail} target="_blank" rel="noopener" className="text-lg font-bold text-slate-900 hover:text-primary-600">{book.title}</a>
          {book.user && <p className="mt-0.5 text-xs text-slate-500">{book.user.username}</p>}
          {book.description && <p className="mt-2 line-clamp-3 text-sm leading-6 text-slate-600">{book.description}</p>}
        </div>
      </div>
      <h2 className="mt-4 text-sm font-semibold text-slate-900">{t('embed.chapters', { n: total })}</h2>
      <ol className="mt-2 space-y-0.5 text-sm">
        {chapters.map((c) => (
          <li key={c.slug} style={{ paddingLeft: `${c.depth * 1}rem` }}>
            <a href={`${siteUrl}/book/reader/${encodeURIComponent(book.slug)}/${encodeURIComponent(c.slug)}`} target="_blank" rel="noopener"
              className="block truncate rounded px-2 py-1 text-slate-700 hover:bg-slate-50 hover:text-primary-600">{c.title}</a>
          </li>
        ))}
      </ol>
      {total > chapters.length && (
        <a href={siteUrl + detail} target="_blank" rel="noopener" className="mt-2 inline-block px-2 text-xs text-primary-600">{t('embed.moreChapters', { n: total - chapters.length })}</a>
      )}
    </EmbedFrame>
  )
}
