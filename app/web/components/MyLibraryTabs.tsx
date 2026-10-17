import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { SegmentedTabs } from '@/components/ui'

export type LibraryTab = 'books' | 'reading' | 'favorites' | 'likes' | 'follows' | 'export'

// MyLibraryTabs 「我的书籍」相关页面的横向导航：每个 tab 是独立路由（URL 承载），
// 依赖插件的 tab（关注→book-follow 或 user-follow）仅在插件启用时出现。
export default function MyLibraryTabs({ active }: { active: LibraryTab }) {
  const { t } = useTranslation()
  const { site } = useApp()
  const followEnabled = (site.feature_plugins || []).includes('book-follow') || (site.feature_plugins || []).includes('user-follow')
  const items = [
    { value: 'books', label: t('library.tab.books'), href: '/books' },
    { value: 'reading', label: t('library.tab.reading'), href: '/user/reading' },
    { value: 'favorites', label: t('library.tab.favorites'), href: '/user/favorites' },
    { value: 'likes', label: t('library.tab.likes'), href: '/user/likes' },
    ...(followEnabled ? [{ value: 'follows', label: t('library.tab.follows'), href: '/user/follows' }] : []),
    { value: 'export', label: t('library.tab.export'), href: '/user/exports' },
  ]
  return <SegmentedTabs className="mb-6" value={active} ariaLabel={t('library.aria')} items={items} />
}
