import Head from 'next/head'
import Link from 'next/link'
import { useRouter } from 'next/router'
import { useState, useRef, useEffect, ReactNode } from 'react'
import { useApp } from '@/lib/auth'
import { API_BASE } from '@/lib/api'
import NotificationBell from '@/components/NotificationBell'
import { ButtonLink, Input, Loading } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'
import {
  GridIcon, GearIcon, UsersIcon, CloudIcon, BookIcon, CodeIcon,
  SearchIcon, ArrowLeftIcon, ChevronDownIcon, ListBulletIcon,
  ActivityIcon, ClockIcon,
} from '@/components/icons'
import { displayName } from '@/lib/users'

export type AdminNavKey = 'system' | 'users' | 'books' | 'documents' | 'tags' | 'achievements' | 'growth' | 'membership' | 'payment' | 'paid' | 'moderation' | 'qa' | 'chapter-guide' | 'book-translations' | 'collect' | 'templates' | 'reports' | 'audit' | 'ai-usage' | 'tasks' | 'languages' | 'settings' | 'plugins' | 'upgrade'

function ReportIcon({ className }: { className?: string }) {
  return <i className={`fa-solid fa-flag ${className || ''}`.trim()} aria-hidden="true" />
}

// 控制台顶栏用户菜单：头像 + 退出登录 / 返回站点
function AdminUserMenu() {
  const { user, logout } = useApp()
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])
  if (!user) return null
  return (
    <div className="relative" ref={ref}>
      <button onClick={() => setOpen(!open)}
        className="flex items-center gap-2 rounded-full border border-slate-200 bg-white pl-1 pr-2 hover:bg-slate-50"
        style={{ height: 'var(--control-height)' }}>
        {user.avatar
          ? <img src={user.avatar.startsWith('/') ? API_BASE + user.avatar : user.avatar} alt="" className="h-7 w-7 rounded-full object-cover" />
          : <span className="flex h-7 w-7 items-center justify-center rounded-full bg-primary-500 text-sm font-bold text-white">{displayName(user)[0]?.toUpperCase()}</span>}
        <span className="hidden max-w-[120px] truncate text-sm text-slate-700 sm:inline">{displayName(user)}</span>
        <ChevronDownIcon className="h-4 w-4 text-slate-400" />
      </button>
      {open && (
        <div className="absolute right-0 z-30 mt-2 w-40 overflow-hidden rounded-lg border border-slate-200 bg-white py-1 shadow-lg">
          <Link href="/user/profile" onClick={() => setOpen(false)} className="block px-4 py-2 text-sm hover:bg-slate-50">{t('account.nav.profile')}</Link>
          <Link href="/" onClick={() => setOpen(false)} className="block px-4 py-2 text-sm hover:bg-slate-50">{t('admin.action.backToSite')}</Link>
          <button onClick={() => { setOpen(false); logout() }}
            className="block w-full px-4 py-2 text-left text-sm text-rose-600 hover:bg-rose-50">{t('nav.menu.logout')}</button>
        </div>
      )}
    </div>
  )
}

function SidebarNav({ current, onNavigate }: { current: AdminNavKey; onNavigate?: () => void }) {
  const { site } = useApp()
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'

  const NAV: { key: AdminNavKey; labelKey: string; href: string; icon: (p: { className?: string }) => JSX.Element }[] = [
    { key: 'system', labelKey: 'admin.nav.system', href: '/admin/system', icon: GridIcon },
    { key: 'users', labelKey: 'admin.nav.users', href: '/admin/users', icon: UsersIcon },
    { key: 'books', labelKey: 'admin.nav.books', href: '/admin/books', icon: BookIcon },
    { key: 'documents', labelKey: 'admin.nav.documents', href: '/admin/documents', icon: ListBulletIcon },
    { key: 'tags', labelKey: 'admin.nav.tags', href: '/admin/tags', icon: ({ className }) => <i className={`fa-solid fa-tags ${className || ''}`} aria-hidden="true" /> },
    { key: 'achievements', labelKey: 'admin.nav.achievements', href: '/admin/achievements', icon: ({ className }) => <i className={`fa-solid fa-trophy ${className || ''}`} aria-hidden="true" /> },
    { key: 'growth', labelKey: 'admin.nav.growth', href: '/admin/growth', icon: ({ className }) => <i className={`fa-solid fa-ranking-star ${className || ''}`} aria-hidden="true" /> },
    { key: 'membership', labelKey: 'admin.nav.membership', href: '/admin/membership', icon: ({ className }) => <i className={`fa-solid fa-crown ${className || ''}`} aria-hidden="true" /> },
    { key: 'payment', labelKey: 'admin.nav.payment', href: '/admin/payment', icon: ({ className }) => <i className={`fa-solid fa-credit-card ${className || ''}`} aria-hidden="true" /> },
    { key: 'paid', labelKey: 'admin.nav.paid', href: '/admin/paid', icon: ({ className }) => <i className={`fa-solid fa-coins ${className || ''}`} aria-hidden="true" /> },
    { key: 'qa', labelKey: 'admin.nav.qa', href: '/admin/qa', icon: ({ className }) => <i className={`fa-solid fa-comments ${className || ''}`} aria-hidden="true" /> },
    { key: 'chapter-guide', labelKey: 'admin.nav.chapterGuide', href: '/admin/chapter-guides', icon: ({ className }) => <i className={`fa-solid fa-compass ${className || ''}`} aria-hidden="true" /> },
    { key: 'book-translations', labelKey: 'admin.nav.bookTranslations', href: '/admin/book-translations', icon: ({ className }) => <i className={`fa-solid fa-language ${className || ''}`} aria-hidden="true" /> },
    { key: 'collect', labelKey: 'admin.nav.collect', href: '/admin/collect', icon: ({ className }) => <i className={`fa-solid fa-spider ${className || ''}`} aria-hidden="true" /> },
    { key: 'templates', labelKey: 'admin.nav.templates', href: '/admin/templates', icon: ({ className }) => <i className={`fa-regular fa-clone ${className || ''}`} aria-hidden="true" /> },
    { key: 'moderation', labelKey: 'admin.nav.moderation', href: '/admin/moderation', icon: ({ className }) => <i className={`fa-solid fa-shield-halved ${className || ''}`} aria-hidden="true" /> },
    { key: 'reports', labelKey: 'admin.nav.reports', href: '/admin/reports', icon: ReportIcon },
    { key: 'audit', labelKey: 'admin.nav.audit', href: '/admin/audit-logs', icon: ActivityIcon },
    { key: 'ai-usage', labelKey: 'admin.nav.aiUsage', href: '/admin/ai-usage', icon: ({ className }) => <i className={`fa-solid fa-microchip ${className || ''}`} aria-hidden="true" /> },
    { key: 'tasks', labelKey: 'admin.nav.tasks', href: '/admin/tasks', icon: ClockIcon },
    { key: 'languages', labelKey: 'admin.nav.languages', href: '/admin/languages', icon: ({ className }) => <i className={`fa-solid fa-language ${className || ''}`} aria-hidden="true" /> },
    { key: 'settings', labelKey: 'admin.nav.settings', href: '/admin/settings/site', icon: GearIcon },
    { key: 'plugins', labelKey: 'admin.nav.plugins', href: '/admin/plugins', icon: CodeIcon },
    { key: 'upgrade', labelKey: 'admin.nav.upgrade', href: '/admin/upgrade', icon: CloudIcon },
  ]

  // 特性插件禁用时隐藏其菜单项（禁用即前后端全禁）
  const features = site.feature_plugins || []
  const visibleNav = NAV.filter((item) => {
    if (item.key === 'achievements') return site.achievements_enabled === 'true'
    if (item.key === 'tags') return features.includes('tags')
    if (item.key === 'growth') return features.includes('growth')
    if (item.key === 'membership') return features.includes('membership')
    if (item.key === 'payment') return features.includes('payment')
    if (item.key === 'moderation') return features.includes('moderation')
    if (item.key === 'paid') return features.includes('paid-content')
    if (item.key === 'qa') return features.includes('qa')
    if (item.key === 'chapter-guide') return features.includes('chapter-guide')
    if (item.key === 'book-translations') return features.includes('book-translations')
    if (item.key === 'collect') return features.includes('content-collect')
    if (item.key === 'templates') return features.includes('templates')
    return true
  })

  return (
    <div className="flex h-full flex-col">
      <Link href="/admin/system" className="flex h-16 shrink-0 items-center gap-2.5 border-b border-slate-100 px-5">
        <img src="/logo.png" alt="" className="h-8 w-8 object-contain" />
        <span className="text-base font-bold text-slate-900">{siteName}</span>
        <span className="text-xs font-medium text-slate-400">{t('admin.title')}</span>
      </Link>
      <nav className="flex-1 space-y-1 overflow-y-auto p-3">
        {visibleNav.map((item) => {
          const active = item.key === current
          const Icon = item.icon
          return (
            <Link key={item.key} href={item.href} onClick={onNavigate}
              className={`flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium transition-colors ${
                active ? 'bg-primary-50 text-primary-700' : 'text-slate-600 hover:bg-slate-100 hover:text-slate-900'
              }`}>
              <Icon className="h-5 w-5 shrink-0" />
              {t(item.labelKey)}
            </Link>
          )
        })}
      </nav>
      <div className="border-t border-slate-100 p-3">
        <Link href="/" onClick={onNavigate}
          className="flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium text-slate-500 transition-colors hover:bg-slate-100 hover:text-slate-900">
          <ArrowLeftIcon className="h-5 w-5 shrink-0" />
          {t('admin.action.backToSite')}
        </Link>
      </div>
    </div>
  )
}

interface AdminLayoutProps {
  current: AdminNavKey
  breadcrumb: string
  children: ReactNode
}

// AdminLayout 管理控制台布局：侧边导航 + 顶栏（仅管理员可访问）
export default function AdminLayout({ current, breadcrumb, children }: AdminLayoutProps) {
  const { user, authReady, site } = useApp()
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const [drawer, setDrawer] = useState(false)
  const router = useRouter()
  useEffect(() => { setDrawer(false) }, [router.pathname])
  const isAdmin = user?.role === 'admin'

  // 鉴权就绪前只渲染极简占位，绝不渲染后台骨架，避免向游客/普通用户泄露布局
  if (!authReady) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-50">
        <Loading label={t('admin.loading.auth')} />
      </div>
    )
  }

  // 非管理员（含未登录）一律呈现 404，控制台对其不可见
  if (!isAdmin) {
    return (
      <>
        <Head><title>404</title><meta name="robots" content="noindex, nofollow" /></Head>
        <div className="flex min-h-screen flex-col items-center justify-center bg-slate-50">
          <h1 className="text-6xl font-bold text-slate-300">404</h1>
          <p className="mt-4 text-slate-500">{t('error.notfound.message')}</p>
          <ButtonLink href="/" className="mt-6">
            {t('error.notfound.backHome')}
          </ButtonLink>
        </div>
      </>
    )
  }

  return (
    <div className="flex min-h-screen bg-slate-50 text-slate-900">
      <Head><title>{`${breadcrumb} - ${siteName} ${t('admin.title')}`}</title></Head>

      {/* 侧边栏（桌面固定） */}
      <aside className="fixed inset-y-0 left-0 z-30 hidden border-r border-slate-200 bg-white md:block" style={{ width: 'var(--sidebar-width)' }}>
        <SidebarNav current={current} />
      </aside>

      {/* 移动端抽屉 */}
      {drawer && (
        <div className="fixed inset-0 z-40 md:hidden">
          <div className="absolute inset-0 bg-slate-900/40" onClick={() => setDrawer(false)} />
          <aside className="absolute inset-y-0 left-0 border-r border-slate-200 bg-white" style={{ width: 'var(--sidebar-width)' }}>
            <SidebarNav current={current} onNavigate={() => setDrawer(false)} />
          </aside>
        </div>
      )}

      <div className="flex min-w-0 flex-1 flex-col md:pl-[var(--sidebar-width)]">
        {/* 顶栏 */}
        <header className="sticky top-0 z-20 flex items-center gap-3 border-b border-slate-200 bg-white/90 px-4 backdrop-blur sm:px-6" style={{ height: 'var(--nav-height)' }}>
          <button onClick={() => setDrawer(true)} aria-label={t('admin.aria.openMenu')}
            className="flex items-center justify-center rounded-lg text-slate-600 hover:bg-slate-100 md:hidden"
            style={{ width: 'var(--control-height)', height: 'var(--control-height)' }}>
            <ListBulletIcon className="h-5 w-5" />
          </button>
          <nav className="flex items-center gap-2 text-sm text-slate-400">
            <span className="hidden sm:inline">{t('admin.title')}</span>
            <span className="hidden sm:inline">/</span>
            <span className="font-medium text-slate-700">{breadcrumb}</span>
          </nav>
          <div className="ml-auto hidden w-full max-w-sm lg:block">
            <Input type="search" placeholder={t('admin.search.placeholder')} leading={<SearchIcon className="h-4 w-4" />} />
          </div>
          <div className="ml-auto flex items-center gap-1.5 lg:ml-0">
            <NotificationBell />
            <AdminUserMenu />
          </div>
        </header>

        <main className="flex-1 px-4 py-6 sm:px-6 lg:px-8">
          <div className="w-full">{children}</div>
        </main>
      </div>
    </div>
  )
}
