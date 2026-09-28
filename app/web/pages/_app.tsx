import '../styles/globals.css'
import '@fortawesome/fontawesome-free/css/all.min.css'
import type { AppProps } from 'next/app'
import NextApp, { type AppContext } from 'next/app'
import { fetchI18nSnapshot, type I18nSnapshot } from '@/lib/i18n'
import { useRouter } from 'next/router'
import { useEffect, useState } from 'react'
import { AppProvider, useApp } from '@/lib/auth'
import { I18nProvider, useTranslation } from '@/lib/i18n'
import Layout from '@/components/Layout'
import Seo from '@/components/Seo'
import { FeedbackProvider, Loading } from '@/components/ui'
import StepUpModal from '@/components/StepUpModal'
import SiteHead from '@/components/SiteHead'
import type { ReactNode } from 'react'
import type { SiteConfig } from '@/lib/types'

const bareRoutes = ['/install', '/login', '/register', '/book/writer', '/book/reader', '/book/print', '/admin', '/embed']

const noindexRoutes = ['/books', '/book/writer', '/user/profile', '/user/security', '/user/achievements', '/admin']

function Shell({ children }: { children: ReactNode }) {
  const router = useRouter()
  const { authReady, installed } = useApp()
  const { t } = useTranslation()

  if (!authReady && installed === null) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-warm">
        <Loading label={t('global.loadingKnowForge')} />
      </div>
    )
  }
  if (bareRoutes.some((r) => router.pathname === r || router.pathname.startsWith(r + '/'))) {
    return (
      <div className="min-h-screen">
        {!router.pathname.startsWith('/book/reader') && <Seo noindex />}
        {children}
      </div>
    )
  }
  return (
    <Layout>
      {noindexRoutes.includes(router.pathname) && <Seo noindex />}
      {children}
    </Layout>
  )
}

function RouteLoading() {
  const router = useRouter()
  const [loading, setLoading] = useState(false)
  const { t } = useTranslation()

  useEffect(() => {
    const start = (_url: string, options: { shallow: boolean }) => {
      if (!options.shallow) setLoading(true)
    }
    const done = () => setLoading(false)
    router.events.on('routeChangeStart', start)
    router.events.on('routeChangeComplete', done)
    router.events.on('routeChangeError', done)
    return () => {
      router.events.off('routeChangeStart', start)
      router.events.off('routeChangeComplete', done)
      router.events.off('routeChangeError', done)
    }
  }, [router.events])

  if (!loading) return null
  return (
    <div className="pointer-events-none fixed inset-x-0 top-0 z-[100] h-1 overflow-hidden bg-primary-100 shadow-[0_1px_5px_rgba(65,105,225,0.18)]" role="status" aria-live="polite">
      <span className="block h-full w-2/3 animate-pulse bg-gradient-to-r from-primary-300 via-primary-600 to-primary-400" />
      <span className="sr-only">{t('global.pageLoading')}</span>
    </div>
  )
}

export default function App({ Component, pageProps, i18n }: AppProps & { i18n?: I18nSnapshot }) {
  return (
    <AppProvider initialSite={pageProps.site ?? null} initialInstalled={pageProps.installed ?? null} initialUser={pageProps.user ?? null}>
      <I18nProvider initial={i18n}>
      <FeedbackProvider>
        <SiteHead />
        <RouteLoading />
        <Shell>
          <Component {...pageProps} />
        </Shell>
        <StepUpModal />
      </FeedbackProvider>
      </I18nProvider>
    </AppProvider>
  )
}

// Runtime languages require request-specific HTML, including pages previously auto-static.
App.getInitialProps = async (context: AppContext) => {
  const props = await NextApp.getInitialProps(context)
  if (context.ctx.req) {
    const { getI18nSnapshot } = await import('@/lib/i18n/server')
    const i18n = await getI18nSnapshot(context.ctx.req)
    if (context.ctx.res && !context.ctx.res.headersSent) {
      context.ctx.res.setHeader('Content-Language', i18n.locale)
      context.ctx.res.setHeader('Cache-Control', 'private, no-store')
      context.ctx.res.setHeader('Vary', 'Cookie, Accept-Language, Authorization')
    }
    return { ...props, i18n }
  }
  return { ...props, i18n: await fetchI18nSnapshot().catch(() => undefined) }
}
