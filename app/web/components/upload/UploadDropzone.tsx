import { useRef, useState, type ReactNode } from 'react'
import { useTranslation } from '@/lib/i18n'
import type { UploadState } from './useUpload'
import { ProgressRing, StatusBadge, UploadErrorTip } from './UploadVisuals'

// UploadDropzone 统一的上传区域：点击或拖拽选择文件，按状态显示——
// 默认灰色虚线、拖拽悬停紫色边框与浅紫背景、上传中环形进度、成功绿色勾、失败红色图标与说明（右下角悬浮提示）。
export default function UploadDropzone({ state, accept, title, hint, onFile, onDismissError, preview, className = '', testId }: {
  state: UploadState
  accept: string
  title: ReactNode
  hint?: ReactNode
  onFile: (file: File) => void
  onDismissError?: () => void
  /** 成功后显示的预览（替代默认的成功图标） */
  preview?: ReactNode
  className?: string
  testId?: string
}) {
  const { t } = useTranslation()
  const inputRef = useRef<HTMLInputElement>(null)
  const [dragging, setDragging] = useState(false)
  const { status } = state
  const tone = dragging ? 'border-primary-400 bg-primary-50/70'
    : status === 'uploading' ? 'border-primary-300 bg-primary-50/40'
      : status === 'success' ? 'border-emerald-300 bg-emerald-50/40'
        : status === 'error' ? 'border-rose-300 bg-rose-50/40'
          : 'border-slate-300 bg-white hover:border-primary-300 hover:bg-slate-50/60'

  function pick(files: FileList | null | undefined) {
    const file = files?.[0]
    if (file) onFile(file)
  }

  return (
    <div className={`relative ${className}`}>
      <button type="button" data-testid={testId} disabled={status === 'uploading'}
        onClick={() => inputRef.current?.click()}
        onDragOver={(e) => { e.preventDefault(); setDragging(true) }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => { e.preventDefault(); setDragging(false); pick(e.dataTransfer.files) }}
        className={`flex min-h-[10rem] w-full flex-col items-center justify-center gap-2 rounded-xl border-2 border-dashed px-4 py-6 text-center transition-colors disabled:cursor-progress ${tone}`}>
        {status === 'uploading' ? (
          <>
            <ProgressRing percent={state.progress} />
            <span className="text-sm text-primary-600">{t('upload.state.uploading')}</span>
            <span className="text-xs tabular-nums text-primary-500">{state.progress}%</span>
          </>
        ) : status === 'success' ? (
          <>
            {preview || <StatusBadge tone="success" />}
            <span className="text-sm font-medium text-emerald-700">{t('upload.state.success')}</span>
            <span className="max-w-full truncate text-xs text-emerald-600">{state.fileName}</span>
            <span className="text-[11px] text-slate-400">{t('upload.state.replace')}</span>
          </>
        ) : status === 'error' ? (
          <>
            <StatusBadge tone="error" />
            <span className="text-sm font-medium text-rose-600">{t('upload.state.failed')}</span>
            <span className="text-[11px] text-slate-400">{t('upload.state.retry')}</span>
          </>
        ) : (
          <>
            <i className={`fa-solid fa-cloud-arrow-up text-3xl ${dragging ? 'text-primary-500' : 'text-slate-400'}`} aria-hidden="true" />
            <span className="text-sm font-medium text-slate-700">{dragging ? t('upload.state.drop') : title}</span>
            {hint && <span className="text-xs text-slate-400">{hint}</span>}
          </>
        )}
      </button>
      <input ref={inputRef} type="file" hidden accept={accept} onChange={(e) => { pick(e.target.files); e.target.value = '' }} />
      <UploadErrorTip error={status === 'error' ? state.error : null} onClose={onDismissError} />
    </div>
  )
}
