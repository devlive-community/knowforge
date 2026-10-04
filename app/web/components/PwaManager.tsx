import { useEffect, useState } from 'react'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { listenInstallPrompt, syncServiceWorker } from '@/lib/pwa'
import { flushProgressQueue } from '@/lib/reading-progress'

// PwaManager 全站：按站点设置注册 / 注销 Service Worker，记录安装提示，联网时补传离线期间的阅读进度，离线时在底部提示。
export default function PwaManager() {
  const { site, installed } = useApp()
  const { t } = useTranslation()
  const [offline, setOffline] = useState(false)
  const enabled = site.pwa_enabled !== false

  useEffect(() => {
    if (installed === false) return
    void syncServiceWorker(enabled)
  }, [enabled, installed])

  useEffect(() => listenInstallPrompt(), [])

  useEffect(() => {
    const update = () => {
      setOffline(!navigator.onLine)
      if (navigator.onLine) void flushProgressQueue()
    }
    update()
    window.addEventListener('online', update)
    window.addEventListener('offline', update)
    return () => {
      window.removeEventListener('online', update)
      window.removeEventListener('offline', update)
    }
  }, [])

  if (!offline) return null
  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-4 z-[120] flex justify-center px-4" role="status" data-testid="offline-banner">
      <div className="flex items-center gap-2 rounded-full bg-slate-900/90 px-4 py-2 text-xs text-white shadow-lg">
        <i className="fa-solid fa-wifi text-amber-300" aria-hidden="true" />
        {t('pwa.banner.offline')}
      </div>
    </div>
  )
}
