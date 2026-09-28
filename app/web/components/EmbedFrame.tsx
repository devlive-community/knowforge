import type { ReactNode } from 'react'
import Seo from '@/components/Seo'
import { useTranslation } from '@/lib/i18n'

// EmbedFrame 嵌入页外框：内容区可滚动，底部固定链接回本站。
export default function EmbedFrame({ siteName, siteUrl, href, title, children }: { siteName: string; siteUrl: string; href: string; title: string; children: ReactNode }) {
  const { t } = useTranslation()
  return (
    <div className="flex h-screen flex-col bg-white text-slate-800">
      <Seo siteName={siteName} title={title} noindex />
      <main className="min-h-0 flex-1 overflow-y-auto px-5 py-4">{children}</main>
      <footer className="flex shrink-0 items-center justify-between gap-3 border-t border-slate-100 px-5 py-2 text-xs text-slate-400">
        <span className="truncate">{t('embed.poweredBy', { site: siteName })}</span>
        <a href={siteUrl + href} target="_blank" rel="noopener" className="shrink-0 font-medium text-primary-600 hover:text-primary-700">{t('embed.openOnSite')} ↗</a>
      </footer>
    </div>
  )
}
