import { useCallback, useRef, useState } from 'react'
import { checkSvgFile, uploadFile, validateFile, type FileRule, type UploadError, type UploadStatus } from '@/lib/upload'

export interface UploadState {
  status: UploadStatus
  progress: number
  fileName: string
  error: UploadError | null
}

const IDLE: UploadState = { status: 'idle', progress: 0, fileName: '', error: null }

// useUpload 上传状态机：校验（类型、大小、SVG 安全）→ 上传（带进度）→ 成功 / 失败。
// prepare 可在上传前转换文件（如头像裁剪），返回 null 表示取消。
export function useUpload({ rule, svg = false, onUploaded }: {
  rule: FileRule
  svg?: boolean
  onUploaded: (url: string, file: File) => void
}) {
  const [state, setState] = useState<UploadState>(IDLE)
  const seq = useRef(0)

  const fail = useCallback((error: UploadError, fileName = '') => setState({ status: 'error', progress: 0, fileName, error }), [])

  /** check 只做校验（不上传），返回错误或 null */
  const check = useCallback(async (file: File): Promise<UploadError | null> => {
    const invalid = validateFile(file, rule)
    if (invalid) return invalid
    if (svg) return checkSvgFile(file)
    return null
  }, [rule, svg])

  const upload = useCallback(async (file: File, blob?: Blob) => {
    const id = ++seq.current
    const invalid = blob ? null : await check(file)
    if (id !== seq.current) return
    if (invalid) { fail(invalid, file.name); return }
    setState({ status: 'uploading', progress: 0, fileName: file.name, error: null })
    try {
      const url = await uploadFile(blob || file, file.name, (progress) => {
        if (id === seq.current) setState((s) => ({ ...s, progress }))
      })
      if (id !== seq.current) return
      setState({ status: 'success', progress: 100, fileName: file.name, error: null })
      onUploaded(url, file)
    } catch (e) {
      if (id !== seq.current) return
      const message = (e as Error).message
      fail(message === 'network' ? { key: 'upload.error.network' } : { key: 'upload.error.server', params: { message } }, file.name)
    }
  }, [check, fail, onUploaded])

  const reset = useCallback(() => { seq.current++; setState(IDLE) }, [])

  return { state, upload, check, fail, reset }
}
