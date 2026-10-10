import { useRef, useState } from 'react'
import UserAvatar, { type UserAvatarUser } from '@/components/UserAvatar'
import { useTranslation } from '@/lib/i18n'
import { AVATAR_RULE, acceptOf, formatSize } from '@/lib/upload'
import { Button } from '@/components/ui'
import CropDialog from './CropDialog'
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
  const [removed, setRemoved] = useState('') // 删除前的头像，可撤销
  const upload = useUpload({
    rule: AVATAR_RULE,
    onUploaded: (url) => { onChange(url); setRemoved(''); setCropFile(null) },
  })
  const { state } = upload
  const uploading = state.status === 'uploading'


  async function choose(file: File | undefined) {
    if (!file) return
    const invalid = await upload.check(file)
    if (invalid) { upload.fail(invalid, file.name); return }
    upload.reset()
    setCropFile(file)
  }

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

      <CropDialog file={cropFile} shape="circle" size={OUTPUT} title={t('upload.crop.title')} state={state}
        onConfirm={(file) => void upload.upload(file)} onCancel={() => { setCropFile(null); upload.reset() }} />
    </div>
  )
}
