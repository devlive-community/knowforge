// 安装为应用（PWA）与离线阅读：注册 Service Worker（public/sw.js）、清空页面缓存、安装提示，以及把整本书保存到离线缓存。
// 页面缓存名与 sw.js 中的 PAGE_CACHE / ASSET_CACHE 保持一致。

export const PAGE_CACHE = 'kf-pages-v1'
export const ASSET_CACHE = 'kf-assets-v1'
export const STATIC_CACHE = 'kf-static-v1'

// 开发环境默认不注册（开发服务器的脚本没有内容哈希，缓存后会拿到旧代码）；NEXT_PUBLIC_ENABLE_SW=1 时开启（端到端测试）
const swAllowed = process.env.NODE_ENV === 'production' || process.env.NEXT_PUBLIC_ENABLE_SW === '1'

export function pwaSupported(): boolean {
  return typeof window !== 'undefined' && 'serviceWorker' in navigator && 'caches' in window
}

// syncServiceWorker 站点开启 PWA 时注册，关闭时注销并清空缓存。
export async function syncServiceWorker(enabled: boolean): Promise<void> {
  if (!pwaSupported() || !swAllowed) return
  try {
    if (enabled) {
      await navigator.serviceWorker.register('/sw.js', { scope: '/' })
      return
    }
    const regs = await navigator.serviceWorker.getRegistrations()
    await Promise.all(regs.map((r) => r.unregister()))
    for (const name of await caches.keys()) {
      if (name.startsWith('kf-')) await caches.delete(name)
    }
  } catch {
    // 浏览器不允许（如隐私模式）时忽略
  }
}

// clearOfflinePages 清空离线页面与图片缓存（登录、退出登录时调用，避免在共用设备上留下他人的内容）。
export async function clearOfflinePages(): Promise<void> {
  if (typeof window === 'undefined' || !('caches' in window)) return
  try {
    await Promise.all([caches.delete(PAGE_CACHE), caches.delete(ASSET_CACHE)])
  } catch {
    // 忽略
  }
}

// —— 安装提示（Chromium 系浏览器的 beforeinstallprompt）——

interface InstallPromptEvent extends Event {
  prompt: () => Promise<void>
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>
}

let deferredPrompt: InstallPromptEvent | null = null
const listeners = new Set<(available: boolean) => void>()

export function listenInstallPrompt(): () => void {
  if (typeof window === 'undefined') return () => {}
  const onPrompt = (e: Event) => {
    e.preventDefault()
    deferredPrompt = e as InstallPromptEvent
    listeners.forEach((fn) => fn(true))
  }
  const onInstalled = () => {
    deferredPrompt = null
    listeners.forEach((fn) => fn(false))
  }
  window.addEventListener('beforeinstallprompt', onPrompt)
  window.addEventListener('appinstalled', onInstalled)
  return () => {
    window.removeEventListener('beforeinstallprompt', onPrompt)
    window.removeEventListener('appinstalled', onInstalled)
  }
}

export function subscribeInstallAvailable(fn: (available: boolean) => void): () => void {
  listeners.add(fn)
  fn(deferredPrompt !== null)
  return () => { listeners.delete(fn) }
}

export async function promptInstall(): Promise<boolean> {
  if (!deferredPrompt) return false
  const p = deferredPrompt
  deferredPrompt = null
  listeners.forEach((fn) => fn(false))
  await p.prompt()
  return (await p.userChoice).outcome === 'accepted'
}

// —— 离线阅读 ——

export function readerPath(bookSlug: string, docSlug: string): string {
  return `/book/reader/${encodeURIComponent(bookSlug)}/${encodeURIComponent(docSlug)}`
}

// cachedCount 已离线保存的章节数。
export async function cachedCount(paths: string[]): Promise<number> {
  if (typeof window === 'undefined' || !('caches' in window)) return 0
  try {
    const cache = await caches.open(PAGE_CACHE)
    const hits = await Promise.all(paths.map((p) => cache.match(new URL(p, window.location.origin).href, { ignoreVary: true })))
    return hits.filter(Boolean).length
  } catch {
    return 0
  }
}

// cacheMissing 把尚未缓存的地址下载到指定缓存（失败的跳过）。
async function cacheMissing(cache: Cache, urls: string[]): Promise<void> {
  await Promise.all(urls.map(async (u) => {
    const url = new URL(u, window.location.origin).href
    if (await cache.match(url)) return
    const res = await fetch(url).catch(() => null)
    if (res?.ok) await cache.put(url, res)
  }))
}

// saveOffline 下载页面到离线缓存：页面本身、页面引用的脚本与样式（未打开过的页面离线时也能运行）以及正文中的本站图片；
// 逐个回调进度，单个失败不影响其他页面，返回成功数。
export async function saveOffline(paths: string[], onProgress: (done: number, total: number) => void): Promise<number> {
  const pages = await caches.open(PAGE_CACHE)
  const assets = await caches.open(ASSET_CACHE)
  const statics = await caches.open(STATIC_CACHE)
  let saved = 0
  for (let i = 0; i < paths.length; i++) {
    const url = new URL(paths[i], window.location.origin).href
    try {
      const res = await fetch(url, { credentials: 'same-origin', headers: { Accept: 'text/html' } })
      if (res.ok) {
        const html = await res.clone().text()
        await pages.put(url, res)
        saved++
        await cacheMissing(statics, Array.from(new Set(html.match(/\/_next\/static\/[^"'\s)<>]+/g) || [])))
        await cacheMissing(assets, Array.from(new Set(html.match(/\/uploads\/[^"'\s)<>]+/g) || [])))
      }
    } catch {
      // 网络错误：跳过该页
    }
    onProgress(i + 1, paths.length)
  }
  return saved
}

// removeOffline 从离线缓存中移除这些页面。
export async function removeOffline(paths: string[]): Promise<void> {
  const cache = await caches.open(PAGE_CACHE)
  await Promise.all(paths.map((p) => cache.delete(new URL(p, window.location.origin).href, { ignoreVary: true })))
}
