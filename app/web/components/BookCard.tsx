import Link from 'next/link'
import { useState, type ReactNode } from 'react'
import { resolveMediaUrl } from '@/lib/media'
import { formatNumber } from '@/lib/api'
import { Badge } from '@/components/ui'
import TagChips from '@/components/TagChips'
import UserAvatar from '@/components/UserAvatar'
import { ArrowRightIcon, EyeIcon, CalendarIcon, ChevronRightIcon } from '@/components/icons'
import { useTranslation } from '@/lib/i18n'
import type { Book } from '@/lib/types'
import HighlightText from '@/components/HighlightText'
import CoverImage from '@/components/CoverImage'
import BookVersionsModal from '@/components/BookVersionsModal'
import { displayName } from '@/lib/users'

// BookCard 全站统一书籍展示卡。
// 收敛了首页/发现/搜索/收藏/我的书籍/用户主页/相关书籍等全部列表场景，
// 通过开关组合表达各页差异，避免各页自定义卡片导致视觉不一致。
// 根节点恒为 div（内部含多个 Link），避免嵌套 <a> 破坏水合。

const statusTones: Record<string, 'slate' | 'primary' | 'emerald' | 'violet' | 'amber'> = {
  draft: 'slate', in_progress: 'primary', published: 'emerald', completed: 'violet', archived: 'amber',
}

export function StatusBadge({ status }: { status: string }) {
  const { t } = useTranslation()
  return <Badge tone={statusTones[status] || 'slate'}>{t(`book.status.${status}`)}</Badge>
}

export interface BookCardProps {
  book: Book
  /** grid = 封面横幅卡（发现/相关/我的书籍）；list = 水平行卡（搜索/收藏/列表视图） */
  view?: 'grid' | 'list'
  /** 显示作者条（头像 + 用户名）。默认 true */
  showAuthor?: boolean
  /** 作者头像/用户名是否可点（链接到用户主页）。默认 true */
  authorLink?: boolean
  /** 显示状态徽标（草稿/进行中/已发布/已完成/已归档）。默认 false */
  showStatus?: boolean
  /** 显示可见性徽标（公开/仅自己可见）。默认 false */
  showVisibility?: boolean
  /** 追加到状态区域的业务徽标（如当前用户的协作角色） */
  badge?: ReactNode
  /** 标签显示上限；0 = 隐藏。默认 grid=3 / list=1 */
  tagsMax?: number
  /** 标签是否可点（链接到发现页过滤）。默认 true */
  tagsLink?: boolean
  /** 显示简介。默认 true */
  showDescription?: boolean
  /** 显示浏览量。默认 true */
  showViews?: boolean
  /** 显示日期。默认 true */
  showDate?: boolean
  /** 日期字段。默认 'updated'（回退 created） */
  dateField?: 'updated' | 'created'
  /** 覆盖卡片点击目标（默认 /book/detail/<slug>） */
  href?: string
  /** 顶部右侧操作槽（如我的书籍下拉菜单按钮） */
  topActions?: ReactNode
  /** 底部操作槽（如我的书籍「继续写作」行） */
  actions?: ReactNode
  /** 额外元信息槽（如章节计数），拼在浏览/日期之后 */
  meta?: ReactNode
  /** className 透传到卡片根节点 */
  className?: string
  /** 搜索页命中词；通过安全文本节点高亮标题与简介 */
  highlight?: string
}

function useDefaults(book: Book, view: 'grid' | 'list', tagsMax?: number) {
  const resolvedTagsMax = tagsMax ?? (view === 'grid' ? 3 : 1)
  return { resolvedTagsMax }
}

function buildDate(book: Book, field: 'updated' | 'created'): string {
  if (field === 'created') return book.created_at?.slice(0, 10) || ''
  return book.updated_at?.slice(0, 10) || book.created_at?.slice(0, 10) || ''
}

function CoverLink({ book, href, view, dark }: { book: Book; href: string; view: 'grid' | 'list'; dark?: boolean }) {
  const cover = resolveMediaUrl(book.cover_image)
  const gradient = dark
    ? 'bg-white/10'
    : 'bg-gradient-to-br from-primary-300 to-[#8B8DFF]'
  const sizeClass = view === 'grid'
    ? 'relative block aspect-[16/8] w-full overflow-hidden'
    : 'relative block h-32 w-full shrink-0 overflow-hidden rounded-lg sm:h-20 sm:w-16'
  return (
    <Link href={href} aria-label={book.title} className={`${sizeClass} ${gradient}`}>
      {cover
        ? <CoverImage src={cover} alt={book.title} imgClassName="transition-transform duration-300 group-hover:scale-105" />
        : <span className={`flex h-full w-full items-center justify-center font-bold ${dark ? 'text-white/40' : 'text-white/80'} ${view === 'grid' ? 'text-3xl' : 'text-xl'}`}>{book.title.slice(0, 1)}</span>}
      {view === 'grid' && cover && (
        <span className="absolute right-3 top-3 flex h-9 w-9 items-center justify-center rounded-full bg-white/90 text-slate-600 opacity-0 shadow transition-opacity duration-200 group-hover:opacity-100">
          <ArrowRightIcon className="h-4 w-4" />
        </span>
      )}
    </Link>
  )
}

// VersionCountPill 「版本聚合」后卡片左上角的「N 个版本 >」胶囊：颜色取主题 primary 色阶，点击弹出全部版本。
function VersionCountPill({ count, onClick, floating }: { count: number; onClick: () => void; floating?: boolean }) {
  const { t } = useTranslation()
  return (
    <button type="button" onClick={onClick}
      className={`inline-flex items-center gap-0.5 rounded-full border border-primary-200 bg-primary-50/95 px-2.5 py-0.5 text-xs font-medium text-primary-700 shadow-sm transition-colors hover:bg-primary-100 ${floating ? 'absolute left-3 top-3 z-[1] backdrop-blur' : ''}`}>
      {t('book.variant.count', { count })}
      <ChevronRightIcon className="h-3.5 w-3.5" />
    </button>
  )
}

export default function BookCard({
  book,
  view = 'grid',
  showAuthor = true,
  authorLink = true,
  showStatus = false,
  showVisibility = false,
  badge,
  tagsLink = true,
  showDescription = true,
  showViews = true,
  showDate = true,
  dateField = 'updated',
  href,
  topActions,
  actions,
  meta,
  className,
  tagsMax,
  highlight,
}: BookCardProps) {
  const { t } = useTranslation()
  const detailHref = href || `/book/detail/${encodeURIComponent(book.slug)}`
  const { resolvedTagsMax } = useDefaults(book, view, tagsMax)
  const showTags = resolvedTagsMax > 0 && (book.tags?.length ?? 0) > 0
  const date = buildDate(book, dateField)
  const hasBadges = showStatus || showVisibility || Boolean(badge)
  const [versionsOpen, setVersionsOpen] = useState(false)
  const versionCount = book.version_count || 0
  const isGrouped = Boolean(book.version_group)
  // 多版本书籍：本书为最新版时在标题旁显示「最新版」，标题下提示组内最新版本号
  const latestBadge = isGrouped && book.version_is_latest && (
    <Badge tone="emerald">{t('book.variant.latest')}</Badge>
  )
  const latestVersionLine = isGrouped && book.latest_version && (
    <p className="truncate text-xs text-slate-400">{t('book.variant.latestVersion', { version: book.latest_version })}</p>
  )
  const versionsModal = versionCount > 1 && (
    <BookVersionsModal book={book} open={versionsOpen} onClose={() => setVersionsOpen(false)} />
  )

  const tagBlock = showTags && (
    <TagChips tags={book.tags} max={resolvedTagsMax} link={tagsLink} />
  )

  const titleBlock = (
    <Link href={detailHref} className="min-w-0 truncate font-semibold text-slate-900 transition-colors hover:text-primary-600">
      <HighlightText text={book.title} query={highlight} />
    </Link>
  )

  // 头像始终非链接：authorLink 为真时外层已是 <Link>，内部再套 Link 会产生嵌套 <a> 破坏水合
  const authorInner = book.user && (
    <UserAvatar user={book.user} size="h-5 w-5" link={false} tooltip={false} />
  )

  const authorBlock = showAuthor && book.user && (
    authorLink
      ? (
        <Link href={`/user/${encodeURIComponent(book.user.username)}`}
          className="flex shrink-0 items-center gap-1.5 text-xs text-slate-500 transition-colors hover:text-primary-600">
          {authorInner}
          <span className="truncate">{displayName(book.user)}</span>
        </Link>
      )
      : (
        <span className="flex shrink-0 items-center gap-1.5 text-xs text-slate-500">
          {authorInner}
          <span className="truncate">{displayName(book.user)}</span>
        </span>
      )
  )

  // 底部 meta：浏览量与自定义元信息居左，日期居右
  const leftMetaBits: ReactNode[] = []
  if (showViews) {
    leftMetaBits.push(
      <span key="views" className="flex items-center gap-1"><EyeIcon className="h-3.5 w-3.5" /> {formatNumber(book.view_count)}</span>
    )
  }
  if (meta) leftMetaBits.push(<span key="extra" className="flex items-center gap-1">{meta}</span>)

  const dateBit = showDate && date && (
    <span key="date" className="flex items-center gap-1"><CalendarIcon className="h-3.5 w-3.5" /> {date}</span>
  )

  const metaBlock = (leftMetaBits.length > 0 || dateBit) && (
    <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-xs text-slate-400">
      {leftMetaBits.length > 0 && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1">{leftMetaBits}</div>
      )}
      {dateBit}
    </div>
  )

  const badgeBlock = hasBadges && (
    <div className="flex flex-wrap items-center gap-2">
      {showStatus && <StatusBadge status={book.status} />}
      {showVisibility && (
        <Badge tone={book.is_public ? 'sky' : 'slate'}>
          {book.is_public ? t('book.visibility.public') : t('book.visibility.private')}
        </Badge>
      )}
      {badge}
    </div>
  )

  const descBlock = showDescription && (
    <p className={`text-sm leading-6 text-slate-500 ${view === 'grid' ? 'line-clamp-2' : 'line-clamp-2'}`}>
      <HighlightText text={book.description || t('book.card.noDescription')} query={highlight} />
    </p>
  )

  // ── list 行卡 ──
  if (view === 'list') {
    return (
      <div className={`group flex flex-col gap-3 rounded-xl border border-slate-200 bg-white p-3 shadow-sm transition hover:shadow-md sm:flex-row sm:items-center sm:gap-4 sm:p-4 ${className || ''}`}>
        <CoverLink book={book} href={detailHref} view="list" />
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            {versionCount > 1 && <VersionCountPill count={versionCount} onClick={() => setVersionsOpen(true)} />}
            {titleBlock}
            {latestBadge}
            {tagBlock}
            {showStatus && <StatusBadge status={book.status} />}
          </div>
          {latestVersionLine}
          {showVisibility && (
            <div className="flex items-center gap-2"><Badge tone={book.is_public ? 'sky' : 'slate'}>{book.is_public ? t('book.visibility.public') : t('book.visibility.private')}</Badge>{badge}</div>
          )}
          {!showVisibility && badge && <div className="flex items-center gap-2">{badge}</div>}
          {descBlock}
          {metaBlock}
        </div>
        <div className="flex w-full shrink-0 items-center justify-between gap-2 border-t border-slate-100 pt-3 sm:w-auto sm:flex-col sm:items-end sm:justify-start sm:border-0 sm:pt-0">
          {authorBlock}
          {topActions && <div className="shrink-0">{topActions}</div>}
          {actions && <div className="min-w-0 flex-1 sm:flex-none">{actions}</div>}
        </div>
        {versionsModal}
      </div>
    )
  }

  // ── grid 横幅卡 ──
  return (
    <div className={`group flex flex-col overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm transition hover:shadow-md ${className || ''}`}>
      <div className="relative">
        <CoverLink book={book} href={detailHref} view="grid" />
        {versionCount > 1 && <VersionCountPill floating count={versionCount} onClick={() => setVersionsOpen(true)} />}
      </div>
      <div className="flex flex-1 flex-col gap-1.5 p-4">
        <div className="flex items-center justify-between gap-2">
          {tagBlock}
          {(topActions || authorBlock) && <div className="ml-auto flex shrink-0 items-center gap-2">{authorBlock}{topActions}</div>}
        </div>
        {latestBadge ? <div className="flex min-w-0 items-center gap-2">{titleBlock}{latestBadge}</div> : titleBlock}
        {latestVersionLine}
        {badgeBlock}
        {descBlock}
        {metaBlock && <div className="mt-auto pt-2">{metaBlock}</div>}
        {actions && <div className="mt-3 border-t border-slate-100 pt-3">{actions}</div>}
      </div>
      {versionsModal}
    </div>
  )
}
