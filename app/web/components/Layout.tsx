import Head from 'next/head'
import Link from 'next/link'
import { useState, useRef, useEffect, useMemo, ReactNode } from 'react'
import { useRouter } from 'next/router'
import Container from '@/components/Container'
import { ListBulletIcon, BookIcon, TrashIcon, UserCircleIcon, GridIcon, LogOutIcon } from '@/components/icons'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { API_BASE, api } from '@/lib/api'
import { resolveMediaUrl } from '@/lib/media'
import { renderMarkdown } from '@/lib/markdown'
import { Button, ButtonLink, Input, Modal, Tooltip, useFeedback } from '@/components/ui'
import type { FooterLinkGroup } from '@/lib/types'
import NotificationBell from '@/components/NotificationBell'
import UserTasksIndicator from '@/components/UserTasksIndicator'
import LanguageSwitcher from '@/components/LanguageSwitcher'
import { SearchIcon } from '@/components/icons'
import { displayName } from '@/lib/users'
import { promptInstall, subscribeInstallAvailable } from '@/lib/pwa'

function UserMenu() {
  const { user, logout, site } = useApp()
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [canInstall, setCanInstall] = useState(false) // 浏览器支持「安装应用」时显示入口
  useEffect(() => subscribeInstallAvailable(setCanInstall), [])
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  if (!user) {
    return (
      <div className="flex items-center gap-2">
        <Link href="/login" className="rounded-lg px-3 py-2 text-sm font-medium text-slate-600 transition-colors hover:bg-slate-100">{t('nav.auth.login')}</Link>
        <ButtonLink href="/register">{t('nav.auth.register')}</ButtonLink>
      </div>
    )
  }
  const items = [
    { label: t('nav.menu.myBooks'), href: '/books', icon: BookIcon },
    { label: t('nav.menu.analytics'), href: '/user/analytics', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-chart-line ${className || ''}`} aria-hidden="true" /> },
    { label: t('nav.menu.reading'), href: '/user/reading', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-book-open-reader ${className || ''}`} aria-hidden="true" /> },
    ...((site.feature_plugins || []).includes('book-follow') ? [{ label: t('nav.menu.follows'), href: '/user/follows', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-bell ${className || ''}`} aria-hidden="true" /> }] : []),
    { label: t('nav.menu.notes'), href: '/user/notes', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-note-sticky ${className || ''}`} aria-hidden="true" /> },
    ...(site.achievements_enabled === 'true' ? [{ label: t('nav.menu.achievements'), href: '/user/achievements', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-trophy ${className || ''}`} aria-hidden="true" /> }] : []),
    ...((site.feature_plugins || []).includes('growth') ? [{ label: t('nav.menu.growth'), href: '/user/growth', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-ranking-star ${className || ''}`} aria-hidden="true" /> }] : []),
    ...((site.feature_plugins || []).includes('membership') ? [{ label: t('nav.menu.membership'), href: '/user/membership', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-crown ${className || ''}`} aria-hidden="true" /> }] : []),
    ...((site.feature_plugins || []).includes('payment') ? [{ label: t('nav.menu.orders'), href: '/user/orders', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-receipt ${className || ''}`} aria-hidden="true" /> }] : []),
    ...((site.feature_plugins || []).includes('paid-content') ? [
      { label: t('nav.menu.purchases'), href: '/user/purchases', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-bag-shopping ${className || ''}`} aria-hidden="true" /> },
      { label: t('nav.menu.earnings'), href: '/user/earnings', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-sack-dollar ${className || ''}`} aria-hidden="true" /> },
    ] : []),
    { label: t('nav.menu.tasks'), href: '/user/tasks', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-list-check ${className || ''}`} aria-hidden="true" /> },
    { label: t('nav.menu.files'), href: '/user/files', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-folder-open ${className || ''}`} aria-hidden="true" /> },
    ...((site.feature_plugins || []).includes('templates') ? [{ label: t('nav.menu.templates'), href: '/user/templates', icon: ({ className }: { className?: string }) => <i className={`fa-regular fa-clone ${className || ''}`} aria-hidden="true" /> }] : []),
    ...(site.ai_usage_enabled ? [{ label: t('nav.menu.aiUsage'), href: '/user/ai-usage', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-microchip ${className || ''}`} aria-hidden="true" /> }] : []),
    ...((site.feature_plugins || []).includes('qa') ? [{ label: t('nav.menu.qa'), href: '/user/qa', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-comments ${className || ''}`} aria-hidden="true" /> }] : []),
    ...((site.feature_plugins || []).includes('moderation') ? [{ label: t('nav.menu.moderation'), href: '/user/moderation', icon: ({ className }: { className?: string }) => <i className={`fa-solid fa-shield-halved ${className || ''}`} aria-hidden="true" /> }] : []),
    { label: t('nav.menu.profile'), href: '/user/profile', icon: UserCircleIcon },
    // 控制台仅对管理员开放
    ...(user.role === 'admin' ? [{ label: t('nav.menu.console'), href: '/admin/system', icon: GridIcon }] : []),
    { label: t('nav.menu.trash'), href: '/user/trash', icon: TrashIcon },
  ]
  return (
    <div className="relative" ref={ref}>
      <button onClick={() => setOpen(!open)} className="flex items-center gap-2 rounded-full border border-slate-200 bg-white px-2 py-1 hover:bg-slate-50">
        {user.avatar
          ? <img src={user.avatar.startsWith('/') ? API_BASE + user.avatar : user.avatar} alt="" className="h-7 w-7 rounded-full object-cover" />
          : <span className="flex h-7 w-7 items-center justify-center rounded-full bg-primary-500 text-sm font-bold text-white">{displayName(user)[0]?.toUpperCase()}</span>}
        <span className="max-w-[120px] truncate text-sm">{displayName(user)}</span>
      </button>
      {open && (
        <div className="absolute right-0 z-20 mt-2 w-48 overflow-hidden rounded-lg border border-slate-200 bg-white py-1 shadow-lg">
          {items.map((item) => {
            const Icon = item.icon
            return (
              <Link key={item.href} href={item.href} onClick={() => setOpen(false)}
                className="flex items-center gap-3 px-4 py-2.5 text-sm text-slate-600 hover:bg-slate-50 hover:text-slate-900">
                <Icon className="h-4 w-4 text-slate-400" />
                {item.label}
              </Link>
            )
          })}
          {canInstall && (
            <button onClick={() => { setOpen(false); void promptInstall() }} data-testid="install-app"
              className="flex w-full items-center gap-3 px-4 py-2.5 text-sm text-slate-600 hover:bg-slate-50 hover:text-slate-900">
              <i className="fa-solid fa-mobile-screen-button h-4 w-4 text-slate-400" aria-hidden="true" />
              {t('pwa.install.menu')}
            </button>
          )}
          <div className="my-1 border-t border-slate-100" />
          <button onClick={() => { setOpen(false); logout() }}
            className="flex w-full items-center gap-3 px-4 py-2.5 text-sm text-rose-600 hover:bg-rose-50">
            <LogOutIcon className="h-4 w-4" />
            {t('nav.menu.logout')}
          </button>
        </div>
      )}
    </div>
  )
}

// MobileNav 窄屏导航：汉堡按钮 + 下拉面板
function MobileNav() {
  const [open, setOpen] = useState(false)
  const { user, site } = useApp()
  const { t } = useTranslation()
  const router = useRouter()
  useEffect(() => { setOpen(false) }, [router.pathname])
  return (
    <div className="relative md:hidden">
      <button onClick={() => setOpen(!open)} aria-label={t('nav.aria.menu')}
        className="flex items-center justify-center rounded-lg text-slate-600 hover:bg-slate-100"
        style={{ width: 'var(--control-height)', height: 'var(--control-height)' }}>
        <ListBulletIcon className="h-5 w-5" />
      </button>
      {open && (
        <div className="absolute left-0 top-11 z-40 w-40 overflow-hidden rounded-lg border border-slate-200 bg-white py-1 shadow-lg">
          {([[t('nav.main.explore'), '/explore'], [t('nav.main.search'), '/search'], ...((site.feature_plugins || []).includes('book-lists') ? [[t('booklists.nav'), '/lists']] : []), ...(site.growth_leaderboard_enabled ? [[t('nav.main.leaderboard'), '/growth/leaderboard']] : []), ...(user ? [[t('nav.main.myBooks'), '/books']] : [])] as [string, string][]).map(([label, href]) => (
            <Link key={href} href={href} onClick={() => setOpen(false)}
              className="block px-4 py-2.5 text-sm text-slate-700 hover:bg-slate-50">{label}</Link>
          ))}
        </div>
      )}
    </div>
  )
}

// ActivationBanner 未激活邮箱提示：仅当「注册后必须激活邮箱」开启且用户未激活时出现（email_verified=false）
function ActivationBanner() {
  const { showToast } = useFeedback()
  const { t } = useTranslation()
  const [sending, setSending] = useState(false)
  async function resend() {
    setSending(true)
    try {
      const r = await api<{ message: string }>('/auth/email/resend', { method: 'POST' })
      showToast({ message: r.message || t('nav.activation.sent'), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error).message || t('nav.activation.failed'), tone: 'error' })
    } finally {
      setSending(false)
    }
  }
  return (
    <div className="border-b border-amber-200 bg-amber-50 text-amber-800">
      <div className="mx-auto flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2 text-sm" style={{ maxWidth: 'var(--content-max-width)' }}>
        <i className="fa-solid fa-triangle-exclamation" aria-hidden="true" />
        <span>{t('nav.activation.message')}</span>
        <Button variant="ghost" size="sm" loading={sending} onClick={resend} className="text-amber-900 hover:bg-amber-100">{t('nav.activation.resend')}</Button>
      </div>
    </div>
  )
}

// AnnouncementBanner 全站公告：管理员在站点设置配置；可关闭，公告内容变化后重新出现。
function AnnouncementBanner() {
  const { site } = useApp()
  const { t } = useTranslation()
  const text = (site.announcement_text || '').trim()
  const enabled = site.announcement_enabled === 'true'
  const warning = site.announcement_tone === 'warning'
  const [dismissed, setDismissed] = useState(true)
  useEffect(() => {
    try { setDismissed(localStorage.getItem('knowforge_announcement') === text) } catch { setDismissed(false) }
  }, [text])
  if (!enabled || !text || dismissed) return null
  function dismiss() {
    try { localStorage.setItem('knowforge_announcement', text) } catch { /* 忽略 */ }
    setDismissed(true)
  }
  return (
    <div className={`border-b ${warning ? 'border-amber-200 bg-amber-50 text-amber-800' : 'border-sky-200 bg-sky-50 text-sky-800'}`}>
      <div className="mx-auto flex items-start gap-3 px-4 py-2.5 text-sm" style={{ maxWidth: 'var(--content-max-width)' }}>
        <i className={`fa-solid ${warning ? 'fa-triangle-exclamation' : 'fa-bullhorn'} mt-0.5 shrink-0`} aria-hidden="true" />
        <span className="min-w-0 flex-1 whitespace-pre-wrap">{text}</span>
        <Tooltip content={t('common.announcement.close')}>
          <button type="button" onClick={dismiss} aria-label={t('common.announcement.close')} className="shrink-0 opacity-60 transition-opacity hover:opacity-100">
            <i className="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </Tooltip>
      </div>
    </div>
  )
}

export default function Layout({ title, children }: { title?: string; children: ReactNode }) {
  const { site, user } = useApp()
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const siteLogo = site.site_logo ? resolveMediaUrl(site.site_logo) : '/logo.png'
  const year = new Date().getFullYear()

  // 管理员在后台配置的页脚链接分组（JSON）；解析失败或为空时回退到默认页脚。
  const customFooterGroups = useMemo<FooterLinkGroup[]>(() => {
    if (!site.site_footer_links) return []
    try {
      const parsed = JSON.parse(site.site_footer_links)
      if (!Array.isArray(parsed)) return []
      return parsed
        .map((g): FooterLinkGroup => ({
          title: String(g?.title || ''),
          links: Array.isArray(g?.links)
            ? g.links.filter((l: unknown): l is { label: string; href: string } => !!l && typeof (l as { href?: unknown }).href === 'string')
              .map((l: { label?: unknown; href: string }) => ({ label: String(l.label || ''), href: l.href }))
            : [],
        }))
        .filter((g) => g.links.length > 0)
    } catch {
      return []
    }
  }, [site.site_footer_links])

  const [showReleaseModal, setShowReleaseModal] = useState(false)
  const [release, setRelease] = useState<{ loading: boolean; body: string; url: string } | null>(null)

  // 打开版本弹窗时按当前版本号拉取 GitHub 对应 release 的发布说明（先试 v<版本> 再试 <版本>）。
  useEffect(() => {
    if (!showReleaseModal || !site.version || release) return
    const v = site.version
    setRelease({ loading: true, body: '', url: '' })
    const base = 'https://api.github.com/repos/devlive-community/knowforge/releases/tags/'
    ;(async () => {
      for (const tag of [`v${v}`, v]) {
        try {
          const res = await fetch(base + encodeURIComponent(tag), { headers: { Accept: 'application/vnd.github+json' } })
          if (res.ok) {
            const data = await res.json()
            setRelease({ loading: false, body: (data.body || '').trim(), url: data.html_url || '' })
            return
          }
        } catch { /* 网络/CSP 失败时回退到静态文案 */ }
      }
      setRelease({ loading: false, body: '', url: '' })
    })()
  }, [showReleaseModal, site.version, release])

  return (
    <div className="flex min-h-screen flex-col overflow-x-clip">
      <Head>
        <title>{title ? `${title} - ${siteName}` : siteName}</title>
        <meta name="description" content={site.site_description || t('home.seo.jsonldDescription')} />
      </Head>

      <AnnouncementBanner />

      <header className="sticky top-0 z-30 border-b border-slate-200 bg-white/90 backdrop-blur">
        <div className="mx-auto flex items-center gap-4 px-4" style={{ height: 'var(--nav-height)', maxWidth: 'var(--content-max-width)' }}>
          <Link href="/" className="flex shrink-0 items-center gap-2.5 text-lg font-bold text-slate-900">
            <img src={siteLogo} alt="" className="h-9 w-9 object-contain" />
            {siteName}
          </Link>
          <nav className="hidden items-center gap-1 text-sm font-medium text-slate-600 md:flex">
            <Link href="/explore" className="rounded-lg px-3 py-2 hover:bg-slate-100 hover:text-slate-900">{t('nav.main.explore')}</Link>
            {(site.feature_plugins || []).includes('book-lists') && <Link href="/lists" className="rounded-lg px-3 py-2 hover:bg-slate-100 hover:text-slate-900">{t('booklists.nav')}</Link>}
            {site.growth_leaderboard_enabled && <Link href="/growth/leaderboard" className="rounded-lg px-3 py-2 hover:bg-slate-100 hover:text-slate-900">{t('nav.main.leaderboard')}</Link>}
            {user && <Link href="/books" className="rounded-lg px-3 py-2 hover:bg-slate-100 hover:text-slate-900">{t('nav.main.myBooks')}</Link>}
          </nav>
          <MobileNav />
          <form action="/search" method="get" className="ml-auto hidden w-full max-w-sm lg:block">
            <Input type="search" name="q" leading={<SearchIcon className="h-4 w-4" />}
              placeholder={t('nav.search.placeholder')} />
          </form>
          <div className="ml-auto flex items-center gap-1.5 lg:ml-0">
            <LanguageSwitcher />
            <UserTasksIndicator />
            <NotificationBell />
            <UserMenu />
          </div>
        </div>
      </header>

      {user && user.email_verified === false && site.registration_require_email_activation === 'true' && <ActivationBanner />}

      <main className="w-full flex-1">{children}</main>

      <footer className="bg-[#0b1f3f] text-slate-300">
        <div className="mx-auto grid gap-10 px-4 py-12 md:grid-cols-[1.6fr_1fr_1fr_1fr]"
          style={{
            maxWidth: 'var(--content-max-width)',
            ...(customFooterGroups.length ? { gridTemplateColumns: `1.6fr repeat(${customFooterGroups.length}, minmax(0, 1fr))` } : {}),
          }}>
          <div>
            <div className="flex items-center gap-2.5 text-lg font-bold text-white">
              <img src={siteLogo} alt="" className="h-9 w-9 object-contain" />
              {siteName}
            </div>
            <p className="mt-3 max-w-xs text-sm leading-6 text-slate-400">
              {site.site_footer_text || t('footer.intro.default')}
            </p>
          </div>
          {customFooterGroups.length ? (
            customFooterGroups.map((g, i) => <FooterColumn key={i} title={g.title} links={g.links} />)
          ) : (
            <>
              <FooterColumn title={t('footer.column.product')} links={[
                { label: t('footer.link.explore'), href: '/explore' },
                ...(user ? [{ label: t('footer.link.myBooks'), href: '/books' }] : []),
              ]} />
              <FooterColumn title={t('footer.column.resources')} links={[
                { label: t('footer.link.docs'), href: 'https://github.com/devlive-community/knowforge' },
                { label: t('footer.link.issues'), href: 'https://github.com/devlive-community/knowforge/issues' },
              ]} />
              <FooterColumn title={t('footer.column.community')} links={[
                { label: t('footer.link.github'), href: 'https://github.com/devlive-community/knowforge' },
                { label: t('footer.link.discussions'), href: 'https://github.com/devlive-community/knowforge/discussions' },
              ]} />
            </>
          )}
        </div>
        <div className="border-t border-white/10">
          <div className="mx-auto flex flex-col justify-between gap-2 px-4 py-4 text-xs text-slate-400 md:flex-row" style={{ maxWidth: 'var(--content-max-width)' }}>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span>© {year} {siteName} · Powered by KnowForge</span>
              {site.site_beian && (
                <a href="https://beian.miit.gov.cn/" target="_blank" rel="noopener noreferrer" className="hover:text-white transition-colors">
                  {site.site_beian}
                </a>
              )}
            </div>
            <div className="flex items-center gap-3">
              <span>{t('footer.meta.license')}</span>
              {site.version && (
                <button onClick={() => setShowReleaseModal(true)} className="hover:text-white transition-colors">
                  v{site.version}
                </button>
              )}
            </div>
          </div>
        </div>
      </footer>

      <Modal open={showReleaseModal} onClose={() => setShowReleaseModal(false)} title={t('footer.release.title')}>
        <div className="space-y-3">
          <div className="flex items-center justify-between bg-slate-50 px-4 py-3" style={{ borderRadius: 'var(--radius)' }}>
            <span className="text-sm text-slate-600">{t('footer.release.current')}</span>
            <span className="font-medium text-slate-900">v{site.version}</span>
          </div>
          <div className="bg-slate-50 px-4 py-3" style={{ borderRadius: 'var(--radius)' }}>
            <p className="mb-2 text-sm font-medium text-slate-700">{t('footer.release.notes')}</p>
            {release?.loading ? (
              <p className="text-sm text-slate-400">{t('footer.release.loading')}</p>
            ) : release?.body ? (
              <div className="markdown-body max-h-72 overflow-y-auto break-words text-sm leading-6 text-slate-600"
                dangerouslySetInnerHTML={{ __html: renderMarkdown(release.body) }} />
            ) : (
              <p className="text-sm text-slate-500">{t('footer.release.empty')}</p>
            )}
          </div>
          <a
            href={release?.url || 'https://github.com/devlive-community/knowforge/releases'}
            target="_blank"
            rel="noopener noreferrer"
            className="block w-full border border-slate-200 bg-white px-4 py-2.5 text-center text-sm font-medium text-slate-700 transition-colors hover:bg-slate-50"
            style={{ borderRadius: 'var(--radius)' }}
          >
            {t('footer.release.viewAll')}
          </a>
        </div>
      </Modal>
    </div>
  )
}

function FooterColumn({ title, links }: { title: string; links: { label: string; href: string }[] }) {
  return (
    <div>
      <h3 className="mb-3 text-sm font-semibold text-white">{title}</h3>
      <ul className="space-y-2 text-sm">
        {links.map((l) => (
          <li key={l.label}>
            {/^https?:\/\//.test(l.href)
              ? <a href={l.href} target="_blank" rel="noopener noreferrer" className="text-slate-400 transition-colors hover:text-white">{l.label}</a>
              : <Link href={l.href} className="text-slate-400 transition-colors hover:text-white">{l.label}</Link>}
          </li>
        ))}
      </ul>
    </div>
  )
}
