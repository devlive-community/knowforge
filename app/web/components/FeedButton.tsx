import { useTranslation } from '@/lib/i18n'
import { absoluteFeedURL } from '@/lib/feeds'
import { Button, Tooltip, useFeedback } from '@/components/ui'

// FeedButton 复制 RSS 订阅地址（粘贴到阅读器即可订阅）。
export default function FeedButton({ path, className = '' }: { path: string; className?: string }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  async function copy() {
    const url = absoluteFeedURL(path)
    try {
      await navigator.clipboard.writeText(url)
      showToast({ message: t('feeds.copied'), tone: 'success' })
    } catch {
      showToast({ title: t('feeds.copyFailed'), message: url, tone: 'error' })
    }
  }
  return (
    <Tooltip content={t('feeds.subscribe')} className={className}>
      <Button type="button" variant="outline" onClick={() => void copy()} aria-label={t('feeds.subscribe')} className="w-full !px-0 text-orange-500 sm:w-[var(--control-height)]">
        <i className="fa-solid fa-rss" aria-hidden="true" />
      </Button>
    </Tooltip>
  )
}
