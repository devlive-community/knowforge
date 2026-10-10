import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import UserAvatar, { type UserAvatarUser } from '@/components/UserAvatar'
import { useTranslation } from '@/lib/i18n'
import { AVATAR_RULE, acceptOf, formatSize } from '@/lib/upload'
import { Button, Modal } from '@/components/ui'
import ImageCropper, { type CropperHandle } from './ImageCropper'
import { useUpload } from './useUpload'
import { ProgressRing, UploadErrorTip } from './UploadVisuals'

const OUTPUT = 512 // 裁剪后头像的边长

// AvatarUpload 头像上传：圆形预览 + 右下角相机按钮，点击或拖拽选择图片，先裁剪（缩放、拖动）再上传；
// 可删除（恢复为默认的首字母头像）并在删除后撤销。只回调新的头像地址，由调用方决定何时保存。
// actions=false 时只显示头像与相机按钮（如侧栏身份卡）。
export default function AvatarUpload({ user, value, onChange, size = 'lg', actions = true }: {
  user: UserAvatarUser
  value: string
  onChange: (url: string) => void
  size?: 'md' | 'lg'
  actions?: boolean
}) {
  const { t } = useTranslation()
  const inputRef = useRef<HTMLInputElement>(null)
  const [dragging, setDragging] = useState(false)
  const [cropFile, setCropFile] = useState<File | null>(null)
  const [cropSrc, setCropSrc] = useState('')
  const [removed, setRemoved] = useState('') // 删除前的头像，可撤销
  const cropper = useRef<CropperHandle | null>(null)
  const upload = useUpload({
    rule: AVATAR_RULE,
    onUploaded: (url) => { onChange(url); setRemoved(''); setCropFile(null) },
  })
  const { state } = upload
  const uploading = state.status === 'uploading'

  useEffect(() => {
    if (!cropFile) { setCropSrc(''); return }
    const url = URL.createObjectURL(cropFile)
    setCropSrc(url)
    return () => URL.revokeObjectURL(url)
  }, [cropFile])

  async function choose(file: File | undefined) {
    if (!file) return
    const invalid = await upload.check(file)
    if (invalid) { upload.fail(invalid, file.name); return }
    upload.reset()
    setCropFile(file)
  }

  async function confirmCrop() {
    if (!cropFile || !cropper.current) return
    const type = cropFile.type === 'image/png' || cropFile.type === 'image/webp' ? 'image/png' : 'image/jpeg'
    const blob = await cropper.current.export(OUTPUT, type)
    if (!blob) return
    await upload.upload(new File([blob], type === 'image/png' ? 'avatar.png' : 'avatar.jpg', { type }))
  }

  const onReady = useCallback((handle: CropperHandle) => { cropper.current = handle }, [])
  const dim = size === 'lg' ? 'h-24 w-24 text-3xl' : 'h-14 w-14 text-lg'
  const camera = size === 'lg' ? 'h-8 w-8 text-sm' : 'h-6 w-6 text-[11px]'

  return (
    <div className="flex flex-wrap items-center gap-5" data-testid="avatar-upload">
      <div className="relative shrink-0"
        onDragOver={(e) => { e.preventDefault(); setDragging(true) }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => { e.preventDefault(); setDragging(false); void choose(e.dataTransfer.files?.[0]) }}>
        <button type="button" onClick={() => inputRef.current?.click()} disabled={uploading} aria-label={t('upload.avatar.change')}
          className={`block rounded-full ring-offset-2 transition ${dragging ? 'ring-2 ring-primary-400' : 'hover:ring-2 hover:ring-primary-200'}`}>
          <UserAvatar user={{ ...user, avatar: value }} size={dim} tooltip={false} link={false} />
          {(uploading || dragging) && (
            <span className={`absolute inset-0 flex items-center justify-center rounded-full ${dragging ? 'bg-primary-500/30' : 'bg-white/75'}`}>
              {uploading ? <ProgressRing percent={state.progress} size={size === 'lg' ? 44 : 30} /> : <i className="fa-solid fa-cloud-arrow-up text-xl text-white" aria-hidden="true" />}
            </span>
          )}
        </button>
        <button type="button" onClick={() => inputRef.current?.click()} disabled={uploading} aria-label={t('upload.avatar.change')} data-testid="avatar-camera"
          className={`absolute -bottom-0.5 -right-0.5 flex items-center justify-center rounded-full bg-slate-800 text-white ring-2 ring-white transition-colors hover:bg-primary-600 ${camera}`}>
          <i className="fa-solid fa-camera" aria-hidden="true" />
        </button>
        {!actions && <UploadErrorTip error={state.status === 'error' && !cropFile ? state.error : null} onClose={upload.reset} />}
      </div>
      {actions && (
        <div className="relative min-w-0 flex-1 space-y-2">
          <div className="flex flex-wrap gap-2">
            <Button type="button" onClick={() => inputRef.current?.click()} loading={uploading} data-testid="avatar-upload-button">
              <i className="fa-solid fa-arrow-up-from-bracket" aria-hidden="true" /> {t('upload.avatar.upload')}
            </Button>
            {value && (
              <Button type="button" variant="outline" disabled={uploading} onClick={() => { setRemoved(value); onChange(''); upload.reset() }}>
                <i className="fa-regular fa-trash-can" aria-hidden="true" /> {t('upload.avatar.remove')}
              </Button>
            )}
            {!value && removed && (
              <Button type="button" variant="ghost" onClick={() => { onChange(removed); setRemoved('') }}>
                <i className="fa-solid fa-rotate-left" aria-hidden="true" /> {t('upload.avatar.restore')}
              </Button>
            )}
          </div>
          <p className="text-xs text-slate-400">{t('upload.avatar.hint', { max: formatSize(AVATAR_RULE.maxBytes) })}</p>
          <UploadErrorTip error={state.status === 'error' && !cropFile ? state.error : null} onClose={upload.reset} />
        </div>
      )}
      <input ref={inputRef} type="file" hidden accept={acceptOf(AVATAR_RULE)} onChange={(e) => { void choose(e.target.files?.[0]); e.target.value = '' }} />

      {/* 裁剪弹窗挂到 body：头像组件常放在资料表单里，避免弹窗按钮触发外层表单提交 */}
      {cropFile && createPortal(<Modal open={!!cropFile} onClose={() => { if (!uploading) { setCropFile(null); upload.reset() } }} title={t('upload.crop.title')} className="max-w-lg"
        footer={<>
          <Button type="button" variant="outline" disabled={uploading} onClick={() => { setCropFile(null); upload.reset() }}>{t('common.actions.cancel')}</Button>
          <Button type="button" loading={uploading} onClick={() => void confirmCrop()} data-testid="avatar-crop-confirm">{t('upload.crop.confirm')}</Button>
        </>}>
        <div className="relative">
          {cropSrc && <ImageCropper src={cropSrc} onReady={onReady} />}
          {uploading && <p className="mt-3 text-center text-xs tabular-nums text-primary-600">{t('upload.state.uploading')} {state.progress}%</p>}
          <UploadErrorTip error={state.status === 'error' && cropFile ? state.error : null} onClose={upload.reset} />
        </div>
      </Modal>, document.body)}
    </div>
  )
}
