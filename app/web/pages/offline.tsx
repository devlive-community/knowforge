import { useTranslation } from '@/lib/i18n'
import { Button } from '@/components/ui'

// 离线页：断网时打开尚未离线保存的页面显示此页（由 Service Worker 预先缓存）。
export default function OfflinePage() {
  const { t } = useTranslation()
  return (
    <div className="flex min-h-screen items-center justify-center bg-warm px-4">
      <div className="w-full max-w-md rounded-2xl border border-slate-200 bg-white p-8 text-center shadow-sm" data-testid="offline-page">
        <div className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-full bg-slate-100 text-slate-500">
          <i className="fa-solid fa-wifi text-xl" aria-hidden="true" />
        </div>
        <h1 className="text-lg font-semibold text-slate-900">{t('pwa.offline.title')}</h1>
        <p className="mt-2 text-sm text-slate-500">{t('pwa.offline.description')}</p>
        <Button className="mt-6" onClick={() => window.location.reload()}>{t('pwa.offline.retry')}</Button>
      </div>
    </div>
  )
}
