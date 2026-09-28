import Link from 'next/link'
import CoverImage from '@/components/CoverImage'
import { bookListPath, ownerName, type BookList } from '@/lib/booklists'
import { useTranslation } from '@/lib/i18n'
import { resolveMediaUrl } from '@/lib/media'
import { Badge } from '@/components/ui'

// BookListCard 书单卡片：前几本书的封面拼图、名称、创建者与收录/收藏数。
export default function BookListCard({ list, showOwner = true }: { list: BookList; showOwner?: boolean }) {
  const { t } = useTranslation()
  const covers = list.covers.slice(0, 4)
  return (
    <Link href={bookListPath(list.id)} className="group flex min-w-0 flex-col overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm transition-shadow hover:shadow-md">
      <div className="grid aspect-[16/8] grid-cols-4 gap-px bg-gradient-to-br from-primary-300 to-[#8B8DFF]">
        {covers.length === 0 ? (
          <span className="col-span-4 flex items-center justify-center text-3xl text-white/80"><i className="fa-solid fa-layer-group" aria-hidden="true" /></span>
        ) : covers.map((c) => {
          const src = resolveMediaUrl(c.cover_image)
          return (
            <span key={c.book_id} className={`relative overflow-hidden bg-gradient-to-br from-primary-300 to-[#8B8DFF] ${covers.length === 1 ? 'col-span-4' : covers.length === 2 ? 'col-span-2' : covers.length === 3 && c === covers[0] ? 'col-span-2' : ''}`}>
              {src
                ? <CoverImage src={src} alt={c.title} />
                : <span className="flex h-full w-full items-center justify-center text-xl font-bold text-white/80">{c.title.slice(0, 1)}</span>}
            </span>
          )
        })}
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-1.5 p-4">
        <div className="flex min-w-0 items-center gap-2">
          <h3 className="truncate font-semibold text-slate-900 group-hover:text-primary-600">{list.title}</h3>
          {!list.is_public && <Badge tone="slate">{t('booklists.card.private')}</Badge>}
        </div>
        {list.description && <p className="line-clamp-2 text-sm text-slate-500">{list.description}</p>}
        <div className="mt-auto flex items-center gap-2 pt-1 text-xs text-slate-400">
          {showOwner && list.owner && <span className="truncate">{ownerName(list.owner)}</span>}
          <span className="shrink-0">{t('booklists.card.books', { n: list.item_count })}</span>
          <span className="shrink-0">{t('booklists.card.followers', { n: list.follower_count })}</span>
        </div>
      </div>
    </Link>
  )
}
