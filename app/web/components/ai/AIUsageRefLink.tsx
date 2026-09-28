import Link from 'next/link'
import type { AIUsageRef } from '@/lib/ai-usage'
import { useTranslation } from '@/lib/i18n'

const ICONS: Record<string, string> = { translation: 'fa-language', qa: 'fa-comments', book: 'fa-book', moderation_case: 'fa-shield-halved' }

// AIUsageRefLink AI 调用记录的关联对象（如翻译任务、书籍问答），点击跳转查看进度或内容。
export default function AIUsageRefLink({ refInfo, className = '' }: { refInfo: AIUsageRef | null | undefined; className?: string }) {
  const { t } = useTranslation()
  if (!refInfo) return null
  return (
    <Link href={refInfo.link} className={`inline-flex min-w-0 max-w-full items-center gap-1.5 text-xs text-primary-600 hover:text-primary-700 hover:underline ${className}`}>
      <i className={`fa-solid ${ICONS[refInfo.kind] || 'fa-link'} shrink-0`} aria-hidden="true" />
      <span className="shrink-0 text-slate-400">{t(`aiUsage.ref.${refInfo.kind in ICONS ? refInfo.kind : 'other'}`)}</span>
      <span className="truncate">{refInfo.title}</span>
    </Link>
  )
}
