import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from '@/lib/i18n'
import { Button, Modal } from '@/components/ui'
import ImageCropper, { type CropperHandle } from './ImageCropper'
import type { UploadState } from './useUpload'
import { UploadErrorTip } from './UploadVisuals'

// CropDialog 上传前的裁剪弹窗（头像、图标图片等共用）：确认后输出 size×size 的图片交给 onConfirm 上传；
// allowOriginal 时可跳过裁剪直接使用原图。弹窗经 portal 挂到 body，可在表单或其他弹窗中使用。
export default function CropDialog({ file, shape, size, title, state, onConfirm, onCancel, allowOriginal = false }: {
  file: File | null
  shape: 'circle' | 'square'
  size: number
  title: string
  state: UploadState
  onConfirm: (file: File) => void
  onCancel: () => void
  allowOriginal?: boolean
}) {
  const { t } = useTranslation()
  const [src, setSrc] = useState('')
  const cropper = useRef<CropperHandle | null>(null)
  const uploading = state.status === 'uploading'

  useEffect(() => {
    if (!file) { setSrc(''); return }
    const url = URL.createObjectURL(file)
    setSrc(url)
    return () => URL.revokeObjectURL(url)
  }, [file])

  const onReady = useCallback((handle: CropperHandle) => { cropper.current = handle }, [])

  async function confirm() {
    if (!file || !cropper.current) return
    // 透明背景的格式输出 PNG，其余输出 JPEG
    const type = file.type === 'image/png' || file.type === 'image/webp' || file.type === 'image/gif' ? 'image/png' : 'image/jpeg'
    const blob = await cropper.current.export(size, type)
    if (!blob) return
    const base = file.name.replace(/\.[^.]+$/, '') || 'image'
    onConfirm(new File([blob], `${base}.${type === 'image/png' ? 'png' : 'jpg'}`, { type }))
  }

  if (!file) return null
  return createPortal(
    <Modal open elevated onClose={() => { if (!uploading) onCancel() }} title={title} className="max-w-lg"
      footer={<>
        {allowOriginal && (
          <Button type="button" variant="ghost" className="mr-auto" disabled={uploading} onClick={() => onConfirm(file)} data-testid="crop-use-original">
            {t('upload.crop.original')}
          </Button>
        )}
        <Button type="button" variant="outline" disabled={uploading} onClick={onCancel}>{t('common.actions.cancel')}</Button>
        <Button type="button" loading={uploading} onClick={() => void confirm()} data-testid="crop-confirm">{t('upload.crop.confirm')}</Button>
      </>}>
      <div className="relative">
        {src && <ImageCropper src={src} shape={shape} onReady={onReady} />}
        {uploading && <p className="mt-3 text-center text-xs tabular-nums text-primary-600">{t('upload.state.uploading')} {state.progress}%</p>}
        <UploadErrorTip error={state.status === 'error' ? state.error : null} />
      </div>
    </Modal>,
    document.body,
  )
}
