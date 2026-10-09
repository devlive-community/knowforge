import { NextRequest, NextResponse } from 'next/server'

// 安装守卫中间件：未安装时所有页面（除 /install）一律服务端重定向到安装向导；
// 已安装时访问 /install 会被送回首页。安装状态来自 Go API（data/config.json）。
const API_INTERNAL = process.env.KNOWFORGE_API_URL || 'http://127.0.0.1:6969'

const INSTALL_PATH = '/install'
const INDEXNOW_VERIFICATION_PATH = /^\/([a-f0-9]{32})\.txt$/

async function serveIndexNowVerificationFile(pathname: string): Promise<NextResponse | null> {
  const match = INDEXNOW_VERIFICATION_PATH.exec(pathname)
  if (!match) return null
  try {
    const res = await fetch(`${API_INTERNAL}/api/v1/indexnow/key`, { cache: 'no-store' })
    const payload = await res.json().catch(() => null)
    if (res.ok && payload?.success !== false && payload?.data?.key === match[1]) {
      return new NextResponse(match[1], {
        status: 200,
        headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Cache-Control': 'public, no-cache, must-revalidate' },
      })
    }
  } catch {
    // A missing API/key/plugin is indistinguishable from an unconfigured verification file.
  }
  return new NextResponse(null, { status: 404, headers: { 'Cache-Control': 'no-store' } })
}

// 模块级缓存：避免每个请求都探测一次安装状态
let cache: { installed: boolean; expires: number } | null = null

async function checkInstalled(): Promise<boolean> {
  if (cache && cache.expires > Date.now()) return cache.installed
  try {
    const res = await fetch(`${API_INTERNAL}/api/v1/setup/status`, { cache: 'no-store' })
    const payload = await res.json()
    const installed = payload?.data?.installed === true
    cache = { installed, expires: Date.now() + 3_000 }
    return installed
  } catch {
    return cache?.installed ?? false
  }
}

export async function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl
  const verificationFile = await serveIndexNowVerificationFile(pathname)
  if (verificationFile) return verificationFile

  const installed = await checkInstalled()

  // standalone 模式下 nextUrl.host 是绑定地址（localhost:6900），
  // 需从 nginx 的转发头还原对外地址，否则重定向会指向内网
  const host = req.headers.get('x-forwarded-host') || req.headers.get('host') || req.nextUrl.host
  const proto = req.headers.get('x-forwarded-proto')?.split(',')[0] || req.nextUrl.protocol.replace(':', '')
  const external = new URL(`${proto}://${host}`)

  if (!installed && pathname !== INSTALL_PATH) {
    external.pathname = INSTALL_PATH
    external.search = ''
    return NextResponse.redirect(external)
  }
  if (installed && pathname === INSTALL_PATH) {
    external.pathname = '/'
    external.search = ''
    return NextResponse.redirect(external)
  }
  return withFrameHeaders(NextResponse.next(), pathname)
}

// withFrameHeaders 防点击劫持：页面默认只允许同源嵌入；嵌入组件页（/embed/*）允许被任意网站嵌入。
function withFrameHeaders(res: NextResponse, pathname: string): NextResponse {
  if (pathname === '/embed' || pathname.startsWith('/embed/')) {
    res.headers.set('Content-Security-Policy', 'frame-ancestors *')
  } else {
    res.headers.set('X-Frame-Options', 'SAMEORIGIN')
    res.headers.set('Content-Security-Policy', "frame-ancestors 'self'")
  }
  return res
}

// 页面请求全部经过中间件；静态资源、内建 API 与上传文件除外
export const config = {
  matcher: ['/((?!_next/static|_next/image|favicon.ico|api/|uploads/).*)'],
}
