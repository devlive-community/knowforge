/* KnowForge Service Worker：安装为应用与离线阅读。
 *
 * - 页面导航：先请求网络，成功时把书籍详情页与阅读页（以及首页）存入缓存；离线时返回缓存的页面，没有缓存时返回离线页。
 * - 阅读页客户端跳转所需的数据（/_next/data/…/book/…）同样先网络后缓存。
 * - 带内容哈希的静态资源（/_next/static/）与上传的图片（/uploads/）缓存优先。
 * - 接口请求（/api/）、事件流与非 GET 请求一律直连网络，不缓存。
 * 页面缓存与「下载离线阅读」共用（lib/pwa.ts 中的 PAGE_CACHE），登录、退出登录时由页面清空。
 */
const VERSION = 'v1'
const STATIC_CACHE = 'kf-static-' + VERSION
const PAGE_CACHE = 'kf-pages-' + VERSION
const ASSET_CACHE = 'kf-assets-' + VERSION
const SHELL_CACHE = 'kf-shell-' + VERSION // 离线页（清空页面缓存时保留）
const OFFLINE_URL = '/offline'
const KEEP = [STATIC_CACHE, PAGE_CACHE, ASSET_CACHE, SHELL_CACHE]
const LIMITS = { [STATIC_CACHE]: 400, [PAGE_CACHE]: 500, [ASSET_CACHE]: 600 }

// 可缓存的页面：首页、书籍详情、阅读页
function cacheablePage(pathname) {
  return pathname === '/' || pathname.startsWith('/book/reader/') || pathname.startsWith('/book/detail/')
}

function cacheableData(pathname) {
  // /_next/data/<buildId>/book/reader/<slug>/<doc>.json
  return /^\/_next\/data\/[^/]+\/(index\.json$|book\/(reader|detail)\/)/.test(pathname)
}

async function trim(name) {
  const cache = await caches.open(name)
  const keys = await cache.keys()
  const extra = keys.length - (LIMITS[name] || 500)
  for (let i = 0; i < extra; i++) await cache.delete(keys[i]) // keys 按写入顺序，先删最早的
}

// 安装时预缓存离线页及其引用的静态资源
async function precache() {
  const res = await fetch(OFFLINE_URL, { credentials: 'same-origin', cache: 'no-store' })
  if (!res.ok) return
  const html = await res.clone().text()
  await (await caches.open(SHELL_CACHE)).put(OFFLINE_URL, res)
  const assets = Array.from(new Set(html.match(/\/_next\/static\/[^"'\s)]+/g) || []))
  const cache = await caches.open(STATIC_CACHE)
  await Promise.all(assets.map((u) => cache.add(u).catch(() => {})))
}

self.addEventListener('install', (event) => {
  event.waitUntil(precache().catch(() => {}).then(() => self.skipWaiting()))
})

self.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    for (const name of await caches.keys()) {
      if (name.startsWith('kf-') && !KEEP.includes(name)) await caches.delete(name)
    }
    await self.clients.claim()
  })())
})

async function networkFirst(request, store) {
  try {
    const res = await fetch(request)
    if (store && res.ok && res.type === 'basic') {
      const copy = res.clone()
      caches.open(PAGE_CACHE).then((c) => c.put(request.url, copy)).then(() => trim(PAGE_CACHE)).catch(() => {})
    }
    return res
  } catch (err) {
    const cached = await caches.match(request.url, { ignoreVary: true, ignoreSearch: false })
    if (cached) return cached
    throw err
  }
}

async function cacheFirst(request, name) {
  const cached = await caches.match(request, { ignoreVary: true })
  if (cached) return cached
  const res = await fetch(request)
  if (res.ok && (res.type === 'basic' || res.type === 'cors')) {
    const copy = res.clone()
    caches.open(name).then((c) => c.put(request, copy)).then(() => trim(name)).catch(() => {})
  }
  return res
}

self.addEventListener('fetch', (event) => {
  const req = event.request
  if (req.method !== 'GET') return
  const url = new URL(req.url)
  if (url.origin !== self.location.origin) return
  if (url.pathname.startsWith('/api/') || url.pathname === '/metrics' || url.pathname === '/sw.js') return
  if ((req.headers.get('Accept') || '').includes('text/event-stream')) return

  if (req.mode === 'navigate') {
    event.respondWith((async () => {
      try {
        return await networkFirst(req, cacheablePage(url.pathname))
      } catch {
        const offline = await (await caches.open(SHELL_CACHE)).match(OFFLINE_URL)
        return offline || new Response('Offline', { status: 503, headers: { 'Content-Type': 'text/plain; charset=utf-8' } })
      }
    })())
    return
  }
  if (url.pathname.startsWith('/_next/data/')) {
    if (cacheableData(url.pathname)) event.respondWith(networkFirst(req, true))
    return
  }
  if (url.pathname.startsWith('/_next/static/')) {
    event.respondWith(cacheFirst(req, STATIC_CACHE))
    return
  }
  if (url.pathname.startsWith('/uploads/')) {
    event.respondWith(cacheFirst(req, ASSET_CACHE))
  }
})

self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'clear-pages') {
    event.waitUntil(Promise.all([caches.delete(PAGE_CACHE), caches.delete(ASSET_CACHE)]))
  }
})
