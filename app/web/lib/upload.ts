import { API_BASE, requestHeaders } from '@/lib/api'

// 上传底层：文件校验、带进度的上传（XHR，fetch 拿不到上传进度）与 SVG 安全检查。
// 图标选择、头像上传、普通图片上传等组件共用；状态与视觉见 components/upload。

export type UploadStatus = 'idle' | 'uploading' | 'success' | 'error'

/** 文件规则：允许的 MIME 与扩展名、大小上限（字节） */
export interface FileRule {
  mimes: string[]
  exts: string[]
  maxBytes: number
}

export const IMAGE_RULE: FileRule = { mimes: ['image/png', 'image/jpeg', 'image/webp'], exts: ['.png', '.jpg', '.jpeg', '.webp'], maxBytes: 2 << 20 }
export const SVG_RULE: FileRule = { mimes: ['image/svg+xml'], exts: ['.svg'], maxBytes: 1 << 20 }
export const AVATAR_RULE: FileRule = { mimes: ['image/png', 'image/jpeg', 'image/webp', 'image/gif'], exts: ['.png', '.jpg', '.jpeg', '.webp', '.gif'], maxBytes: 5 << 20 }

/** <input accept> 用的字符串 */
export function acceptOf(rule: FileRule): string {
  return [...rule.mimes, ...rule.exts].join(',')
}

/** 可展示的校验错误：i18n 键与参数 */
export interface UploadError { key: string; params?: Record<string, string | number> }

export function formatSize(bytes: number): string {
  return bytes >= 1 << 20 ? `${Math.round((bytes / (1 << 20)) * 10) / 10}MB` : `${Math.round(bytes / 1024)}KB`
}

function extOf(name: string): string {
  const i = name.lastIndexOf('.')
  return i >= 0 ? name.slice(i).toLowerCase() : ''
}

/** validateFile 校验类型与大小（类型按 MIME 或扩展名任一匹配） */
export function validateFile(file: File, rule: FileRule): UploadError | null {
  const typeOk = (file.type && rule.mimes.includes(file.type)) || rule.exts.includes(extOf(file.name))
  if (!typeOk) return { key: 'upload.error.type', params: { types: rule.exts.map((e) => e.slice(1).toUpperCase()).filter((e, i, all) => all.indexOf(e) === i && e !== 'JPEG').join('、') } }
  if (file.size > rule.maxBytes) return { key: 'upload.error.size', params: { max: formatSize(rule.maxBytes) } }
  if (file.size === 0) return { key: 'upload.error.empty' }
  return null
}

// SVG 中不允许的元素与链接协议（与服务端上传检查一致）
const UNSAFE_SVG_TAGS = new Set(['script', 'foreignobject', 'iframe', 'embed', 'object', 'handler', 'listener'])
const UNSAFE_URL = /^\s*(javascript|vbscript|data:text\/html)/i

/** checkSvgText SVG 安全检查：不含脚本、事件属性、危险链接与实体声明；返回错误或 null */
export function checkSvgText(text: string): UploadError | null {
  if (/<!ENTITY/i.test(text)) return { key: 'upload.error.svgUnsafe' }
  const doc = new DOMParser().parseFromString(text, 'image/svg+xml')
  const root = doc.documentElement
  if (!root || root.nodeName.toLowerCase() !== 'svg' || doc.getElementsByTagName('parsererror').length > 0) return { key: 'upload.error.svgInvalid' }
  const all = [root, ...Array.from(root.getElementsByTagName('*'))]
  for (const el of all) {
    if (UNSAFE_SVG_TAGS.has(el.localName.toLowerCase())) return { key: 'upload.error.svgUnsafe' }
    for (const attr of Array.from(el.attributes)) {
      const name = attr.name.toLowerCase()
      if (name.startsWith('on')) return { key: 'upload.error.svgUnsafe' }
      if ((name === 'href' || name.endsWith(':href') || name === 'src') && UNSAFE_URL.test(attr.value)) return { key: 'upload.error.svgUnsafe' }
      if (name === 'style' && /javascript:|expression\(/i.test(attr.value)) return { key: 'upload.error.svgUnsafe' }
    }
  }
  return null
}

export async function checkSvgFile(file: File): Promise<UploadError | null> {
  return checkSvgText(await file.text())
}

/** uploadFile 上传到通用上传接口，返回媒体地址；onProgress 回调 0–100 */
export function uploadFile(file: Blob, filename: string, onProgress?: (percent: number) => void): Promise<string> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `${API_BASE}/api/v1/upload`)
    for (const [k, v] of Object.entries(requestHeaders(false))) xhr.setRequestHeader(k, v)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && onProgress) onProgress(Math.min(99, Math.round((e.loaded / e.total) * 100)))
    }
    xhr.onload = () => {
      let payload: { success?: boolean; message?: string; data?: { url?: string } } = {}
      try { payload = JSON.parse(xhr.responseText || '{}') } catch { /* 非 JSON 响应 */ }
      if (xhr.status >= 200 && xhr.status < 300 && payload.success !== false && payload.data?.url) {
        onProgress?.(100)
        resolve(payload.data.url)
      } else {
        reject(new Error(payload.message || `HTTP ${xhr.status}`))
      }
    }
    xhr.onerror = () => reject(new Error('network'))
    const fd = new FormData()
    fd.append('file', file, filename)
    xhr.send(fd)
  })
}
