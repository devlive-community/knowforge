import { useTranslation } from '@/lib/i18n'
import type { UploadError } from '@/lib/upload'

// 上传组件共用的视觉：环形进度、状态图标与右下角悬浮错误提示（不弹窗）。

export function ProgressRing({ percent, size = 40 }: { percent: number; size?: number }) {
  const r = (size - 6) / 2
  const c = 2 * Math.PI * r
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} className="-rotate-90" role="progressbar" aria-valuenow={percent} aria-valuemin={0} aria-valuemax={100}>
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" strokeWidth={4} className="stroke-primary-100" />
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" strokeWidth={4} strokeLinecap="round" className="stroke-primary-500 transition-[stroke-dashoffset] duration-200"
        strokeDasharray={c} strokeDashoffset={c * (1 - Math.max(4, percent) / 100)} />
    </svg>
  )
}

export function StatusBadge({ tone }: { tone: 'success' | 'error' }) {
  return tone === 'success'
    ? <span className="flex h-9 w-9 items-center justify-center rounded-full bg-emerald-500 text-white shadow-sm"><i className="fa-solid fa-check" aria-hidden="true" /></span>
    : <span className="flex h-9 w-9 items-center justify-center rounded-full bg-rose-500 text-white shadow-sm"><i className="fa-solid fa-exclamation" aria-hidden="true" /></span>
}

/** UploadErrorTip 组件右下角的悬浮错误提示（父元素需 relative） */
export function UploadErrorTip({ error, onClose }: { error: UploadError | null; onClose?: () => void }) {
  const { t } = useTranslation()
  if (!error) return null
  return (
    <div role="alert" data-testid="upload-error-tip"
      className="absolute bottom-2 right-2 z-10 flex max-w-[16rem] items-start gap-2 rounded-lg bg-rose-600 px-3 py-2 text-xs leading-5 text-white shadow-lg">
      <i className="fa-solid fa-circle-exclamation mt-0.5" aria-hidden="true" />
      <span className="min-w-0 flex-1 break-words">{t(error.key, error.params)}</span>
      {onClose && (
        <button type="button" onClick={onClose} aria-label={t('upload.dismiss')} className="text-white/80 hover:text-white">
          <i className="fa-solid fa-xmark" aria-hidden="true" />
        </button>
      )}
    </div>
  )
}
