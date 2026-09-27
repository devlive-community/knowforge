import Link from 'next/link'
import { ReactNode } from 'react'
import Container from '@/components/Container'
import Seo from '@/components/Seo'
import { resolveMediaUrl } from '@/lib/media'
import { GearIcon, UsersIcon, DownloadIcon, ExternalLinkIcon, PencilIcon, ListIcon, TrashIcon } from '@/components/icons'
import { ButtonLink } from '@/components/ui'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import type { Book } from '@/lib/types'

export type BookSettingsTab = 'basic' | 'info' | 'paid' | 'localization' | 'chapters' | 'analytics' | 'collaborators' | 'export' | 'data' | 'cleanup' | 'danger' | 'crawl-history' | 'watermark' | 'ai-translate' | 'chapter-guides' | 'reader-questions'

interface BookSettingsLayoutProps {
  book: Book
  active: BookSettingsTab
  children: ReactNode
}

// BookSettingsLayout 书籍设置：左侧书籍卡与导航 + 右侧内容区（对齐账户设置的双栏布局）
export default function BookSettingsLayout({ book, active, children }: BookSettingsLayoutProps) {
  const { t } = useTranslation()
  const { site } = useApp()
  const cover = resolveMediaUrl(book.cover_image)
  const base = `/book/settings/${encodeURIComponent(book.slug)}`
  // 多语言/版本都禁用时隐藏该 tab；只启用其一时 tab 名随之为「语言」或「版本」，两者都启用才叫「多语言与版本」。
  const features = site.feature_plugins || []
  const transEnabled = features.includes('book-translations')
  const versionsEnabled = features.includes('book-versions')
  const localizationEnabled = transEnabled || versionsEnabled
  const localizationLabel = transEnabled && versionsEnabled
    ? t('bookSettings.nav.localization')
    : versionsEnabled ? t('bookSettings.nav.versions') : t('bookSettings.nav.languages')

  const NAV: { key: BookSettingsTab; labelKey: string; label?: string; icon: (p: { className?: string }) => JSX.Element; sub: string; danger?: boolean }[] = [
    { key: 'basic', labelKey: 'bookSettings.nav.basic', icon: GearIcon, sub: '' },
    { key: 'info', labelKey: 'bookSettings.nav.info', icon: ({ className }) => <i className={`fa-solid fa-circle-info ${className || ''}`} aria-hidden="true" />, sub: 'info' },
    ...(features.includes('paid-content') ? [{ key: 'paid' as BookSettingsTab, labelKey: 'bookSettings.nav.paid', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-coins ${className || ''}`} aria-hidden="true" />, sub: 'paid' }] : []),
    ...(localizationEnabled ? [{ key: 'localization' as BookSettingsTab, labelKey: 'bookSettings.nav.localization', label: localizationLabel, icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-language ${className || ''}`} aria-hidden="true" />, sub: 'localization' }] : []),
    { key: 'chapters', labelKey: 'bookSettings.nav.chapters', icon: ListIcon, sub: 'chapters' },
    ...(features.includes('qa') ? [{ key: 'reader-questions' as BookSettingsTab, labelKey: 'bookSettings.nav.readerQuestions', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-circle-question ${className || ''}`} aria-hidden="true" />, sub: 'reader-questions' }] : []),
    ...(features.includes('chapter-guide') ? [{ key: 'chapter-guides' as BookSettingsTab, labelKey: 'bookSettings.nav.chapterGuides', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-compass ${className || ''}`} aria-hidden="true" />, sub: 'chapter-guides' }] : []),
    ...(transEnabled ? [{ key: 'ai-translate' as BookSettingsTab, labelKey: 'bookSettings.nav.aiTranslate', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-wand-magic-sparkles ${className || ''}`} aria-hidden="true" />, sub: 'ai-translate' }] : []),
    { key: 'analytics', labelKey: 'bookSettings.nav.analytics', icon: ({ className }) => <i className={`fa-solid fa-chart-line ${className || ''}`} aria-hidden="true" />, sub: 'analytics' },
    ...(features.includes('content-collect') ? [{ key: 'crawl-history' as BookSettingsTab, labelKey: 'bookSettings.nav.crawlHistory', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-spider ${className || ''}`} aria-hidden="true" />, sub: 'crawl-history' }] : []),
    { key: 'collaborators', labelKey: 'bookSettings.nav.collaborators', icon: UsersIcon, sub: 'collaborators' },
    ...(features.includes('watermark') ? [{ key: 'watermark' as BookSettingsTab, labelKey: 'bookSettings.nav.watermark', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-stamp ${className || ''}`} aria-hidden="true" />, sub: 'watermark' }] : []),
    { key: 'export', labelKey: 'bookSettings.nav.export', icon: ({ className }) => <i className={`fa-solid fa-file-export ${className || ''}`} aria-hidden="true" />, sub: 'export' },
    { key: 'data', labelKey: 'bookSettings.nav.data', icon: DownloadIcon, sub: 'data' },
    { key: 'cleanup', labelKey: 'bookSettings.nav.cleanup', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-broom ${className || ''}`} aria-hidden="true" />, sub: 'cleanup' },
    { key: 'danger', labelKey: 'bookSettings.nav.danger', icon: TrashIcon, sub: 'danger', danger: true },
  ]

  const activeNav = NAV.find((n) => n.key === active)
  const activeLabel = activeNav?.label ?? t(activeNav?.labelKey || 'common.settings')
  return (
    <>
      <Seo title={`${book.title} - ${activeLabel}`} noindex />
      <Container>
        <nav className="flex items-center gap-1.5 py-4 text-sm text-slate-500">
          <Link href="/books" className="hover:text-primary-600">{t('nav.main.myBooks')}</Link>
          <span className="text-slate-300">/</span>
          <Link href={`/book/detail/${encodeURIComponent(book.slug)}`} className="max-w-[240px] truncate hover:text-primary-600">{book.title}</Link>
          <span className="text-slate-300">/</span>
          <span className="text-slate-900">{activeLabel}</span>
        </nav>

        <div className="grid gap-6 pb-10 lg:grid-cols-[280px_1fr]">
          {/* 左：书籍卡 + 导航 */}
          <aside className="h-fit rounded-2xl border border-slate-200 bg-white p-5 shadow-sm lg:sticky lg:top-20">
            <div className="flex items-center gap-3">
              <span className="h-14 w-11 shrink-0 overflow-hidden rounded-lg bg-gradient-to-br from-primary-300 to-primary-600">
                {cover
                  ? <img src={cover} alt="" className="h-full w-full object-cover" />
                  : <span className="flex h-full w-full items-center justify-center text-lg font-bold text-white/80">{book.title.slice(0, 1)}</span>}
              </span>
              <div className="min-w-0">
                <div className="truncate font-bold text-slate-900">{book.title}</div>
                <div className="truncate text-sm text-slate-400">{t(`book.status.${book.status}`)} · {book.is_public ? t('book.visibility.public') : t('book.visibility.private')}</div>
              </div>
            </div>

            <nav className="mt-4 space-y-1 border-t border-slate-100 pt-4">
              {NAV.map((item) => {
                const isActive = active === item.key
                const Icon = item.icon
                const activeCls = item.danger
                  ? 'bg-rose-50 font-medium text-rose-700 ring-1 ring-inset ring-rose-100'
                  : 'bg-primary-50 font-medium text-primary-700 ring-1 ring-inset ring-primary-100'
                const idleCls = item.danger ? 'text-rose-600 hover:bg-rose-50' : 'text-slate-600 hover:bg-slate-50'
                return (
                  <Link key={item.key} href={item.sub ? `${base}/${item.sub}` : base}
                    className={`flex items-center gap-2.5 rounded-lg px-3 py-2.5 text-sm transition-colors ${isActive ? activeCls : idleCls}`}>
                    <Icon className="h-4 w-4" /> {item.label ?? t(item.labelKey)}
                  </Link>
                )
              })}
            </nav>

            <div className="mt-6 space-y-2 border-t border-slate-100 pt-4">
              <ButtonLink href={`/book/writer/${encodeURIComponent(book.slug)}`} variant="outline" className="w-full hover:border-primary-400 hover:text-primary-600">
                <PencilIcon className="h-4 w-4" /> {t('bookSettings.action.write')}
              </ButtonLink>
              <ButtonLink href={`/book/detail/${encodeURIComponent(book.slug)}`} variant="ghost" className="w-full text-slate-500 hover:text-primary-600">
                {t('bookSettings.action.viewDetail')} <ExternalLinkIcon className="h-3.5 w-3.5" />
              </ButtonLink>
            </div>
          </aside>

          {/* 右：内容 */}
          <div className="min-w-0">{children}</div>
        </div>
      </Container>
    </>
  )
}
