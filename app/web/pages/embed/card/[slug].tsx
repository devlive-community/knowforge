import type { GetServerSideProps, InferGetServerSidePropsType } from 'next'
import EmbedFrame from '@/components/EmbedFrame'
import { formatDate, formatNumber } from '@/lib/api'
import { embedEnabled } from '@/lib/embed'
import { useTranslation } from '@/lib/i18n'
import { resolveMediaUrl } from '@/lib/media'
import { getSiteConfig, isInstalled, serverApi, siteUrlFrom } from '@/lib/server-api'
import type { Book, Document } from '@/lib/types'

const MAX_TAGS = 4

function countDocs(docs: Document[] | null | undefined): number {
  return (docs || []).reduce((n, d) => n + 1 + countDocs(d.children), 0)
}

// 嵌入组件 · 书籍信息卡片：只展示书籍的基本信息（封面、作者、简介、标签、章节数与阅读量），不含目录，链接在新窗口打开本站。
export const getServerSideProps: GetServerSideProps<{ site: Record<string, string>; siteUrl: string; book: Book; chapters: number }> = async ({ req, params }) => {
  if (!(await isInstalled())) return { notFound: true }
  const site = await getSiteConfig()
  const slug = typeof params?.slug === 'string' ? params.slug : ''
  if (!embedEnabled(site) || !slug) return { notFound: true }
  try {
    const book = await serverApi<Book>(`/books/slug/${encodeURIComponent(slug)}`) // 不带登录态：只嵌入游客可读的书
    const tree = await serverApi<Document[]>(`/books/${book.id}/documents`).catch(() => [])
    return { props: { site, siteUrl: siteUrlFrom(req), book, chapters: countDocs(tree) } }
  } catch {
    return { notFound: true }
  }
}

export default function EmbedBookCard({ site, siteUrl, book, chapters }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const detail = `/book/detail/${encodeURIComponent(book.slug)}`
  const cover = resolveMediaUrl(book.cover_image)
  const author = book.user ? ((book.user as { nickname?: string }).nickname || book.user.username) : ''
  const tags = (book.tags || []).slice(0, MAX_TAGS)
  return (
    <EmbedFrame siteName={siteName} siteUrl={siteUrl} href={detail} title={book.title}>
      <div className="flex gap-4">
        <a href={siteUrl + detail} target="_blank" rel="noopener" aria-hidden="true" tabIndex={-1}
          className="flex h-32 w-24 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-gradient-to-br from-primary-300 to-[#8B8DFF] shadow-sm">
          {cover ? <img src={cover} alt="" className="h-full w-full object-cover" /> : <span className="text-3xl font-bold text-white/80">{book.title.slice(0, 1)}</span>}
        </a>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <a href={siteUrl + detail} target="_blank" rel="noopener" className="text-lg font-bold leading-snug text-slate-900 hover:text-primary-600">{book.title}</a>
            {(book.status === 'in_progress' || book.status === 'completed') && (
              <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500">{t(`books.status.${book.status}`)}</span>
            )}
          </div>
          {author && <p className="mt-0.5 text-xs text-slate-500">{t('embed.card.author', { name: author })}</p>}
          {book.description && <p className="mt-2 line-clamp-3 whitespace-pre-line text-sm leading-6 text-slate-600">{book.description}</p>}
          {tags.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1.5">
              {tags.map((tag) => <span key={tag.id} className="rounded-md bg-primary-50 px-1.5 py-0.5 text-xs text-primary-700">{tag.name}</span>)}
            </div>
          )}
          <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-slate-400">
            <span>{t('embed.card.chapters', { n: chapters })}</span>
            <span>{t('embed.card.views', { n: formatNumber(book.view_count) })}</span>
            <span>{t('embed.card.updated', { date: formatDate(book.updated_at).slice(0, 10) })}</span>
          </div>
          <a href={siteUrl + detail} target="_blank" rel="noopener"
            className="mt-3 inline-flex items-center gap-1.5 rounded-lg bg-primary-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-primary-700">
            {t('embed.card.read')} ↗
          </a>
        </div>
      </div>
    </EmbedFrame>
  )
}
