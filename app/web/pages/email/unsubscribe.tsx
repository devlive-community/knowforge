import { useEffect, useState } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/router'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Loading } from '@/components/ui'
import Seo from '@/components/Seo'

// 邮件摘要的一键退订落地页：凭邮件中的签名链接把邮件发送方式改为「不发送」，不需要登录。
export default function EmailUnsubscribe() {
  const router = useRouter()
  const { site } = useApp()
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const [status, setStatus] = useState<'loading' | 'ok' | 'error'>('loading')
  const [message, setMessage] = useState('')

  useEffect(() => {
    if (!router.isReady) return
    const u = Number(router.query.u)
    const token = typeof router.query.token === 'string' ? router.query.token : ''
    if (!u || !token) {
      setStatus('error')
      setMessage(t('email.unsubscribe.invalidLink'))
      return
    }
    api('/email/unsubscribe', { method: 'POST', body: { u, token } })
      .then(() => setStatus('ok'))
      .catch((e) => {
        setStatus('error')
        setMessage((e as Error).message || t('email.unsubscribe.failed'))
      })
  }, [router.isReady, router.query.u, router.query.token]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="flex min-h-screen items-center justify-center bg-gradient-to-b from-primary-50 to-slate-50 px-4">
      <Seo siteName={siteName} title={t('email.unsubscribe.seoTitle')} noindex />
      <div className="w-full max-w-sm rounded-xl border border-slate-200 bg-white p-8 text-center shadow-sm" data-testid="unsubscribe-result">
        {status === 'loading' && <Loading className="py-6" label={t('email.unsubscribe.processing')} />}
        {status === 'ok' && (
          <>
            <i className="fa-solid fa-envelope-circle-check mb-3 block text-3xl text-emerald-500" aria-hidden="true" />
            <h1 className="text-lg font-bold text-slate-900">{t('email.unsubscribe.okTitle')}</h1>
            <p className="mt-2 text-sm text-slate-500">{t('email.unsubscribe.okSubtitle')}</p>
            <Link href="/user/notify" className="mt-4 inline-block text-sm text-primary-600 hover:underline">{t('email.unsubscribe.manage')}</Link>
          </>
        )}
        {status === 'error' && (
          <>
            <i className="fa-solid fa-circle-exclamation mb-3 block text-3xl text-rose-500" aria-hidden="true" />
            <h1 className="text-lg font-bold text-rose-600">{t('email.unsubscribe.failed')}</h1>
            <p className="mt-2 text-sm text-slate-500">{message}</p>
            <Link href="/" className="mt-4 inline-block text-sm text-primary-600 hover:underline">{t('email.unsubscribe.backHome')}</Link>
          </>
        )}
      </div>
    </div>
  )
}
