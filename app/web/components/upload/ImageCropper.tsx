import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { useTranslation } from '@/lib/i18n'

const STAGE = 288 // 裁剪区边长（px）
const INSET = 12 // 取景框距裁剪区边缘（px），输出的就是取景框内的部分
const MAX_ZOOM = 4
const CANVAS_TAG = 'canvas'

export interface CropperHandle {
  /** 按当前取景输出 size×size 的图片 */
  export: (size: number, type: string) => Promise<Blob | null>
}

// ImageCropper 正方形取景的裁剪器（头像为圆形遮罩，图标图片为方形）：拖动平移、滚轮或缩放条缩放
// （缩放条为自绘滑块，可键盘操作），图片始终铺满取景框。
export default function ImageCropper({ src, onReady, shape = 'circle' }: { src: string; onReady: (handle: CropperHandle) => void; shape?: 'circle' | 'square' }) {
  const { t } = useTranslation()
  const imgRef = useRef<HTMLImageElement | null>(null)
  const [natural, setNatural] = useState<{ w: number; h: number } | null>(null)
  const [zoom, setZoom] = useState(1)
  const [offset, setOffset] = useState({ x: 0, y: 0 })
  const drag = useRef<{ x: number; y: number; ox: number; oy: number } | null>(null)
  const trackRef = useRef<HTMLDivElement>(null)

  const baseScale = natural ? STAGE / Math.min(natural.w, natural.h) : 1
  const scale = baseScale * zoom

  // 限制平移，保证取景框内始终是图片
  const clamp = useCallback((x: number, y: number, z = zoom) => {
    if (!natural) return { x: 0, y: 0 }
    const s = baseScale * z
    const mx = Math.max(0, (natural.w * s - STAGE) / 2)
    const my = Math.max(0, (natural.h * s - STAGE) / 2)
    return { x: Math.max(-mx, Math.min(mx, x)), y: Math.max(-my, Math.min(my, y)) }
  }, [natural, baseScale, zoom])

  const applyZoom = useCallback((z: number) => {
    const next = Math.max(1, Math.min(MAX_ZOOM, z))
    setZoom(next)
    setOffset((o) => clamp(o.x, o.y, next))
  }, [clamp])

  useEffect(() => {
    const img = new Image()
    img.onload = () => { imgRef.current = img; setNatural({ w: img.naturalWidth, h: img.naturalHeight }); setZoom(1); setOffset({ x: 0, y: 0 }) }
    img.src = src
  }, [src])

  useEffect(() => {
    onReady({
      export: (size, type) => new Promise((resolve) => {
        const img = imgRef.current
        if (!img || !natural) { resolve(null); return }
        const canvas = document.createElement(CANVAS_TAG)
        canvas.width = size
        canvas.height = size
        const ctx = canvas.getContext('2d')
        if (!ctx) { resolve(null); return }
        const k = size / (STAGE - INSET * 2)
        const w = natural.w * scale * k
        const h = natural.h * scale * k
        if (type === 'image/jpeg') { ctx.fillStyle = '#ffffff'; ctx.fillRect(0, 0, size, size) }
        ctx.drawImage(img, size / 2 + offset.x * k - w / 2, size / 2 + offset.y * k - h / 2, w, h)
        canvas.toBlob((blob) => resolve(blob), type, 0.92)
      }),
    })
  }, [onReady, natural, scale, offset])

  function onPointerDown(e: PointerEvent<HTMLDivElement>) {
    e.currentTarget.setPointerCapture(e.pointerId)
    drag.current = { x: e.clientX, y: e.clientY, ox: offset.x, oy: offset.y }
  }
  function onPointerMove(e: PointerEvent<HTMLDivElement>) {
    if (!drag.current) return
    setOffset(clamp(drag.current.ox + e.clientX - drag.current.x, drag.current.oy + e.clientY - drag.current.y))
  }

  function zoomFromTrack(clientX: number) {
    const rect = trackRef.current?.getBoundingClientRect()
    if (!rect) return
    applyZoom(1 + ((clientX - rect.left) / rect.width) * (MAX_ZOOM - 1))
  }
  function onSliderKey(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key === 'ArrowRight' || e.key === 'ArrowUp') { e.preventDefault(); applyZoom(zoom + 0.1) }
    if (e.key === 'ArrowLeft' || e.key === 'ArrowDown') { e.preventDefault(); applyZoom(zoom - 0.1) }
  }
  const pct = ((zoom - 1) / (MAX_ZOOM - 1)) * 100

  return (
    <div className="flex flex-col items-center gap-4">
      <div className="relative cursor-grab touch-none select-none overflow-hidden rounded-2xl bg-slate-900 active:cursor-grabbing" style={{ width: STAGE, height: STAGE }}
        onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={() => { drag.current = null }} onPointerCancel={() => { drag.current = null }}
        onWheel={(e) => applyZoom(zoom - e.deltaY * 0.0015)} data-testid="avatar-cropper">
        {natural && (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={src} alt="" draggable={false} className="pointer-events-none absolute left-1/2 top-1/2 max-w-none"
            style={{ width: natural.w * scale, height: natural.h * scale, transform: `translate(calc(-50% + ${offset.x}px), calc(-50% + ${offset.y}px))` }} />
        )}
        <div style={{ inset: INSET, boxShadow: '0 0 0 9999px rgba(15, 23, 42, 0.55)' }} className={`pointer-events-none absolute ring-2 ring-white/80 ${shape === 'circle' ? 'rounded-full' : 'rounded-lg'}`} />
      </div>
      <div className="flex w-full max-w-xs items-center gap-3">
        <button type="button" onClick={() => applyZoom(zoom - 0.25)} aria-label={t('upload.crop.zoomOut')} className="text-slate-400 hover:text-slate-700">
          <i className="fa-solid fa-magnifying-glass-minus" aria-hidden="true" />
        </button>
        <div ref={trackRef} role="slider" tabIndex={0} aria-label={t('upload.crop.zoom')} aria-valuemin={100} aria-valuemax={MAX_ZOOM * 100} aria-valuenow={Math.round(zoom * 100)}
          onKeyDown={onSliderKey}
          onPointerDown={(e) => { e.currentTarget.setPointerCapture(e.pointerId); zoomFromTrack(e.clientX) }}
          onPointerMove={(e) => { if (e.buttons) zoomFromTrack(e.clientX) }}
          className="relative h-5 flex-1 cursor-pointer touch-none rounded-full outline-none focus-visible:ring-2 focus-visible:ring-primary-300">
          <div className="absolute inset-x-0 top-1/2 h-1.5 -translate-y-1/2 rounded-full bg-slate-200" />
          <div className="absolute left-0 top-1/2 h-1.5 -translate-y-1/2 rounded-full bg-primary-500" style={{ width: `${pct}%` }} />
          <div className="absolute top-1/2 h-4 w-4 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-white bg-primary-500 shadow" style={{ left: `${pct}%` }} />
        </div>
        <button type="button" onClick={() => applyZoom(zoom + 0.25)} aria-label={t('upload.crop.zoomIn')} className="text-slate-400 hover:text-slate-700">
          <i className="fa-solid fa-magnifying-glass-plus" aria-hidden="true" />
        </button>
      </div>
      <p className="text-xs text-slate-400">{t('upload.crop.hint')}</p>
    </div>
  )
}
