import { createContext, useContext, useEffect, useState, useCallback, ReactNode } from 'react'
import { useRouter } from 'next/router'
import { api, getToken, storeSession, clearSession } from './api'
import type { SiteConfig, User } from './types'

// isAuthFailure 令牌无效（401）时才清除登录状态；离线等网络错误时保留，避免断网时被登出并清空离线内容
function isAuthFailure(e: unknown): boolean {
  return (e as { status?: number })?.status === 401
}

export interface ThemeSetting {
  primary_hue: string
  custom_color: string
  radius: string
  custom_radius: string
  button_size: string
  custom_control_height: string
  font_size: string
  custom_font_size: string
  content_width: string
  custom_content_width: string
  nav_height: string
  custom_nav_height: string
  sidebar_width: string
  custom_sidebar_width: string
  page_bg: string
  custom_page_bg: string
}

/** 将主题设置写入 DOM 与 localStorage，供 _document.tsx 防闪烁脚本与全局使用 */
export function applyTheme(s: ThemeSetting) {
  if (typeof document === 'undefined') return
  const el = document.documentElement

  // 主题色
  el.setAttribute('data-primary', s.primary_hue)
  if (s.primary_hue === 'custom' && s.custom_color) {
    el.setAttribute('data-custom-color', s.custom_color)
    el.style.setProperty('--custom-color', s.custom_color)
  } else {
    el.removeAttribute('data-custom-color')
    el.style.removeProperty('--custom-color')
  }

  // 圆角
  el.setAttribute('data-radius', s.radius)
  if (s.radius === 'custom' && s.custom_radius) {
    el.style.setProperty('--radius', s.custom_radius)
  }

  // 控件大小
  el.setAttribute('data-btn', s.button_size)
  if (s.button_size === 'custom' && s.custom_control_height) {
    el.style.setProperty('--control-height', s.custom_control_height)
  } else {
    el.style.removeProperty('--control-height')
  }

  // 字体大小
  el.setAttribute('data-font', s.font_size)
  if (s.font_size === 'custom' && s.custom_font_size) {
    el.style.setProperty('--font-size', s.custom_font_size)
  }

  // 内容区宽度
  el.setAttribute('data-width', s.content_width)
  if (s.content_width === 'custom' && s.custom_content_width) {
    el.style.setProperty('--content-max-width', s.custom_content_width)
  }

  // 导航栏高度
  el.setAttribute('data-nav', s.nav_height)
  if (s.nav_height === 'custom' && s.custom_nav_height) {
    el.style.setProperty('--nav-height', s.custom_nav_height)
  }

  // 侧边栏宽度
  el.setAttribute('data-sidebar', s.sidebar_width)
  if (s.sidebar_width === 'custom' && s.custom_sidebar_width) {
    el.style.setProperty('--sidebar-width', s.custom_sidebar_width)
  }

  // 页面底色
  if (s.page_bg === 'custom' && s.custom_page_bg) {
    el.style.setProperty('--page-bg', s.custom_page_bg)
  } else {
    el.style.setProperty('--page-bg', s.page_bg)
  }

  localStorage.setItem('knowforge_theme', JSON.stringify(s))
}

interface AppContextValue {
  user: User | null
  authReady: boolean
  installed: boolean | null // null = 未知
  site: SiteConfig
  theme: ThemeSetting
  login: (token: string, user: User) => void
  logout: () => void
  refreshUser: () => Promise<User>
  refreshSite: () => Promise<void>
  applyTheme: (s: ThemeSetting) => void
}

const DEFAULT_THEME: ThemeSetting = {
  primary_hue: 'blue',
  custom_color: '',
  radius: 'lg',
  custom_radius: '',
  button_size: 'md',
  custom_control_height: '',
  font_size: '15',
  custom_font_size: '',
  content_width: 'normal',
  custom_content_width: '',
  nav_height: '64',
  custom_nav_height: '',
  sidebar_width: '260',
  custom_sidebar_width: '',
  page_bg: '#F7F6F2',
  custom_page_bg: '',
}

const AppContext = createContext<AppContextValue>({
  user: null,
  authReady: false,
  installed: null,
  site: {},
  theme: DEFAULT_THEME,
  login: () => {},
  logout: () => {},
  refreshUser: async () => { throw new Error('not ready') },
  refreshSite: async () => {},
  applyTheme: () => {},
})

interface AppProviderProps {
  children: ReactNode
  /** SSR 页面通过 pageProps 传入的站点配置，避免客户端首屏闪烁 */
  initialSite?: SiteConfig | null
  /** SSR 页面传入的安装状态（服务端已校验，未安装不会渲染到客户端） */
  initialInstalled?: boolean | null
  /** SSR 经 Cookie 校验出的登录用户，消除刷新时先未登录后登录的闪烁 */
  initialUser?: User | null
}

// ensureAuthCookie 让令牌 Cookie 与本地令牌一致（登录、恢复会话后调用），服务端渲染的页面（如阅读页）据此渲染登录态；
// 切换账号时覆盖旧 Cookie，避免服务端仍按上一个账号渲染。
function ensureAuthCookie() {
  const token = getToken()
  if (token && typeof document !== 'undefined' && !document.cookie.split(';').some((part) => part.trim() === `knowforge_token=${token}`)) {
    document.cookie = `knowforge_token=${token}; path=/; max-age=604800`
  }
}

export function AppProvider({ children, initialSite, initialInstalled, initialUser }: AppProviderProps) {
  const router = useRouter()
  // 客户端首帧必须与 SSR 使用相同登录态，避免 hydration 时导航与管理操作结构不一致。
  // SSR 未拿到用户但本地仍有令牌的旧会话，由下方 effect 在 hydration 后校验并恢复。
  const [user, setUser] = useState<User | null>(initialUser ?? null)
  const [authReady, setAuthReady] = useState(false)
  // 仅信任 SSR 显式传入的安装状态；客户端页面走 boot 检测
  const [installed, setInstalled] = useState<boolean | null>(initialInstalled ?? null)
  const [site, setSite] = useState<SiteConfig>(initialSite ?? {})
  const [theme, setTheme] = useState<ThemeSetting>(DEFAULT_THEME)

  // SSR 页每次导航都带来最新的 site（含 feature_plugins）。用「渲染期同步 state」模式在同一次渲染内
  // 就更新，避免 useEffect 延后一帧导致插件启用/禁用在页面上有可见延迟。
  const [siteSource, setSiteSource] = useState(initialSite)
  if (initialSite && Object.keys(initialSite).length > 0 && initialSite !== siteSource) {
    setSiteSource(initialSite)
    setSite(initialSite)
  }

  // 登录后从服务端拉取主题设置并应用
  const loadAndApplyTheme = useCallback(async () => {
    try {
      const s = await api<ThemeSetting>('/auth/theme-settings')
      setTheme(s)
      applyTheme(s)
    } catch { /* 未登录或接口异常时保持默认 */ }
  }, [])

  useEffect(() => {
    if (initialInstalled) {
      // SSR 已渲染登录态；仅当 SSR 未知用户且本地有令牌时才校验（例如令牌过期）
      if (!initialUser && getToken()) {
        api<User>('/auth/me')
          .then((me) => { setUser(me); ensureAuthCookie(); loadAndApplyTheme() })
          .catch((e) => { if (isAuthFailure(e)) clearSession() })
          .finally(() => setAuthReady(true))
      } else {
        setAuthReady(true)
        if (initialUser) loadAndApplyTheme()
      }
      return
    }
    let cancelled = false
    async function boot() {
      try {
        const status = await api<{ installed: boolean }>('/setup/status')
        if (cancelled) return
        setInstalled(status.installed)
        if (!status.installed) {
          setAuthReady(true)
          return
        }
        try {
          setSite(await api<SiteConfig>('/site'))
        } catch { /* 忽略站点配置错误 */ }
        if (getToken()) {
          try {
            const me = await api<User>('/auth/me')
            if (!cancelled) { setUser(me); ensureAuthCookie(); loadAndApplyTheme() }
          } catch (e) {
            if (isAuthFailure(e)) clearSession()
          }
        }
      } catch {
        if (!cancelled) setInstalled(null)
      } finally {
        if (!cancelled) setAuthReady(true)
      }
    }
    boot()
    return () => { cancelled = true }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // 安装守卫：未安装时强制进入 /install
  useEffect(() => {
    if (installed === null) return
    if (!installed && router.pathname !== '/install') {
      router.replace('/install')
    }
    if (installed && router.pathname === '/install') {
      router.replace('/')
    }
  }, [installed, router])

  const login = useCallback((token: string, u: User) => {
    storeSession(token, u)
    ensureAuthCookie() // 登录后立即种 Cookie：否则在整页刷新前，服务端渲染的页面仍按未登录渲染
    setUser(u)
    loadAndApplyTheme()
  }, [loadAndApplyTheme])

  const logout = useCallback(() => {
    clearSession()
    document.cookie = 'knowforge_token=; Max-Age=0; path=/'
    document.cookie = 'infosphere_token=; Max-Age=0; path=/' // 一并清除改名前的旧 Cookie，避免后端回退读取导致「登出后仍登录」
    localStorage.removeItem('knowforge_theme')
    applyTheme(DEFAULT_THEME)
    setUser(null)
    router.push('/login')
  }, [router])

  const refreshUser = useCallback(async () => {
    const me = await api<User>('/auth/me')
    setUser(me)
    return me
  }, [])

  const refreshSite = useCallback(async () => {
    try { setSite(await api<SiteConfig>('/site')) } catch { /* 忽略站点配置错误 */ }
  }, [])

  const handleApplyTheme = useCallback((s: ThemeSetting) => {
    setTheme(s)
    applyTheme(s)
  }, [])

  return (
    <AppContext.Provider value={{ user, authReady, installed, site, theme, login, logout, refreshUser, refreshSite, applyTheme: handleApplyTheme }}>
      {children}
    </AppContext.Provider>
  )
}

export function useApp(): AppContextValue {
  return useContext(AppContext)
}

// 页面级登录守卫，返回当前用户或 null（未就绪/已跳转登录页）
export function useRequireAuth(): User | null {
  const { user, authReady } = useApp()
  const router = useRouter()
  useEffect(() => {
    if (authReady && !user) router.replace('/login')
  }, [authReady, user, router])
  return authReady && user ? user : null
}
