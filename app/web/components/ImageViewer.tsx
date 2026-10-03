import { ReactNode, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from '@/lib/i18n'
import { Button, Tooltip, useFeedback } from '@/components/ui'

export interface ViewerItem {
  key: string | number
  url: string // 可直接访问的地址
  name: string
  editable?: boolean // 是否支持编辑（旋转 / 翻转 / 裁剪）
  info?: ReactNode // 信息面板中的附加内容（来源、上传时间、引用等）
}

export interface ImageEdit {
  rotate: number // 顺时针：0 / 90 / 180 / 270
  flip_h: boolean
  flip_v: boolean
  crop: { x: number; y: number; w: number; h: number } | null // 在旋转、翻转后的图片上（像素）
}

interface Props {
  items: ViewerItem[]
  index: number
  onIndexChange: (index: number) => void
  onClose: () => void
  onDelete?: (item: ViewerItem) => Promise<void> | void
  deleting?: boolean
  onEdit?: (item: ViewerItem, edit: ImageEdit) => Promise<void> // 保存编辑（另存为新图片）
}

const MIN_SCALE = 0.05
const MAX_SCALE = 16

// ImageViewer 全屏图片查看器：缩放（按钮 / 滚轮 / 键盘）、拖动平移、适应窗口与实际大小、旋转与翻转查看、上一张 / 下一张、
// 图片信息、复制链接、下载、删除；可编辑的图片支持旋转、翻转、裁剪后另存（由 onEdit 交给服务端处理）。
// 键盘：Esc 关闭，← → 切换，+ − 缩放，0 适应窗口，R 旋转。
export default function ImageViewer({ items, index, onIndexChange, onClose, onDelete, deleting, onEdit }: Props) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const item = items[index]
  const stageRef = useRef<HTMLDivElement>(null)
  const [natural, setNatural] = useState<{ w: number; h: number } | null>(null)
  const [failed, setFailed] = useState(false)
  const [scale, setScale] = useState(1)
  const [fitScale, setFitScale] = useState(1)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const [rotate, setRotate] = useState(0)
  const [flipH, setFlipH] = useState(false)
  const [showInfo, setShowInfo] = useState(false)
  const [editing, setEditing] = useState(false)
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null)

  // 切换图片时重置查看状态
  useEffect(() => {
    setNatural(null)
    setFailed(false)
    setPan({ x: 0, y: 0 })
    setRotate(0)
    setFlipH(false)
    setEditing(false)
  }, [item?.url])

  // 适应窗口的缩放比例（考虑旋转后的宽高）
  const computeFit = useCallback(() => {
    const stage = stageRef.current
    if (!stage || !natural) return 1
    const sideways = rotate % 180 !== 0
    const w = sideways ? natural.h : natural.w
    const h = sideways ? natural.w : natural.h
    return Math.min(1, (stage.clientWidth - 48) / w, (stage.clientHeight - 48) / h)
  }, [natural, rotate])

  useLayoutEffect(() => {
    if (!natural) return
    const fit = computeFit()
    setFitScale(fit)
    setScale(fit)
    setPan({ x: 0, y: 0 })
  }, [natural, computeFit])

  useEffect(() => {
    const onResize = () => { const fit = computeFit(); setFitScale(fit) }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [computeFit])

  const zoomBy = useCallback((factor: number) => {
    setScale((s) => Math.min(MAX_SCALE, Math.max(MIN_SCALE, s * factor)))
  }, [])
  const fit = useCallback(() => { setScale(fitScale); setPan({ x: 0, y: 0 }) }, [fitScale])
  const go = useCallback((delta: number) => {
    const next = index + delta
    if (next >= 0 && next < items.length) onIndexChange(next)
  }, [index, items.length, onIndexChange])

  // 键盘操作（编辑中只响应 Esc）
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement)?.closest('input, textarea')) return
      if (e.key === 'Escape') { if (editing) setEditing(false); else onClose(); return }
      if (editing) return
      if (e.key === 'ArrowLeft') go(-1)
      else if (e.key === 'ArrowRight') go(1)
      else if (e.key === '+' || e.key === '=') zoomBy(1.25)
      else if (e.key === '-') zoomBy(0.8)
      else if (e.key === '0') fit()
      else if (e.key.toLowerCase() === 'r') setRotate((r) => (r + 90) % 360)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [editing, go, onClose, zoomBy, fit])

  // 锁定页面滚动
  useEffect(() => {
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => { document.body.style.overflow = prev }
  }, [])

  async function copyLink() {
    try {
      await navigator.clipboard.writeText(new URL(item.url, window.location.href).toString())
      showToast({ message: t('imageViewer.linkCopied'), tone: 'success' })
    } catch {
      showToast({ message: t('imageViewer.copyFailed'), tone: 'error' })
    }
  }

  if (!item) return null
  const percent = Math.round(scale * 100)
  const toolButton = (label: string, icon: string, onClick: () => void, opts: { disabled?: boolean; active?: boolean; testId?: string } = {}) => (
    <Tooltip content={label} placement="bottom">
      <button type="button" aria-label={label} onClick={onClick} disabled={opts.disabled} data-testid={opts.testId}
        className={`flex h-9 w-9 items-center justify-center rounded-lg transition-colors disabled:opacity-30 ${opts.active ? 'bg-white/20 text-white' : 'text-slate-200 hover:bg-white/10 hover:text-white'}`}>
        <i className={`fa-solid ${icon}`} aria-hidden="true" />
      </button>
    </Tooltip>
  )

  return createPortal(
    <div className="fixed inset-0 z-[140] flex flex-col bg-slate-950/95 text-slate-100" role="dialog" aria-modal="true" aria-label={item.name} data-testid="image-viewer">
      {/* 顶栏 */}
      <div className="flex flex-wrap items-center gap-1 border-b border-white/10 px-3 py-2">
        <div className="mr-auto flex min-w-0 items-center gap-2 px-1">
          <span className="truncate text-sm font-medium">{item.name}</span>
          {items.length > 1 && <span className="shrink-0 text-xs tabular-nums text-slate-400">{index + 1} / {items.length}</span>}
        </div>
        {!editing && (
          <>
            {toolButton(t('imageViewer.zoomOut'), 'fa-magnifying-glass-minus', () => zoomBy(0.8))}
            <button type="button" onClick={fit} className="w-14 rounded-lg py-1.5 text-center text-xs tabular-nums text-slate-200 hover:bg-white/10" aria-label={t('imageViewer.fit')}>{percent}%</button>
            {toolButton(t('imageViewer.zoomIn'), 'fa-magnifying-glass-plus', () => zoomBy(1.25))}
            {toolButton(t('imageViewer.actualSize'), 'fa-expand', () => { setScale(1); setPan({ x: 0, y: 0 }) })}
            <span className="mx-1 h-5 w-px bg-white/15" />
            {toolButton(t('imageViewer.rotateLeft'), 'fa-rotate-left', () => setRotate((r) => (r + 270) % 360))}
            {toolButton(t('imageViewer.rotateRight'), 'fa-rotate-right', () => setRotate((r) => (r + 90) % 360))}
            {toolButton(t('imageViewer.flip'), 'fa-left-right', () => setFlipH((v) => !v), { active: flipH })}
            <span className="mx-1 h-5 w-px bg-white/15" />
            {onEdit && item.editable && toolButton(t('imageViewer.edit'), 'fa-crop-simple', () => setEditing(true), { disabled: failed, testId: 'image-viewer-edit' })}
            {toolButton(t('imageViewer.copyLink'), 'fa-link', () => void copyLink())}
            <Tooltip content={t('imageViewer.download')} placement="bottom">
              <a href={item.url} download={item.name} aria-label={t('imageViewer.download')} className="flex h-9 w-9 items-center justify-center rounded-lg text-slate-200 hover:bg-white/10 hover:text-white">
                <i className="fa-solid fa-download" aria-hidden="true" />
              </a>
            </Tooltip>
            <Tooltip content={t('imageViewer.openOriginal')} placement="bottom">
              <a href={item.url} target="_blank" rel="noopener noreferrer" aria-label={t('imageViewer.openOriginal')} className="flex h-9 w-9 items-center justify-center rounded-lg text-slate-200 hover:bg-white/10 hover:text-white">
                <i className="fa-solid fa-arrow-up-right-from-square" aria-hidden="true" />
              </a>
            </Tooltip>
            {onDelete && toolButton(t('imageViewer.delete'), deleting ? 'fa-spinner fa-spin' : 'fa-trash-can', () => void onDelete(item), { disabled: deleting })}
            {toolButton(t('imageViewer.info'), 'fa-circle-info', () => setShowInfo((v) => !v), { active: showInfo })}
          </>
        )}
        {toolButton(t('imageViewer.close'), 'fa-xmark', onClose)}
      </div>

      <div className="flex min-h-0 flex-1">
        {editing && onEdit ? (
          <ImageEditor url={item.url} onCancel={() => setEditing(false)} onSave={async (edit) => { await onEdit(item, edit); setEditing(false) }} />
        ) : (
          <div ref={stageRef} className="relative min-w-0 flex-1 cursor-grab overflow-hidden active:cursor-grabbing select-none"
            onWheel={(e) => zoomBy(e.deltaY < 0 ? 1.1 : 1 / 1.1)}
            onPointerDown={(e) => { drag.current = { x: e.clientX, y: e.clientY, px: pan.x, py: pan.y }; (e.target as HTMLElement).setPointerCapture?.(e.pointerId) }}
            onPointerMove={(e) => { const d = drag.current; if (d) setPan({ x: d.px + e.clientX - d.x, y: d.py + e.clientY - d.y }) }}
            onPointerUp={() => { drag.current = null }}
            onDoubleClick={() => (Math.abs(scale - fitScale) < 0.01 ? setScale(1) : fit())}>
            {failed ? (
              <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 text-slate-400">
                <i className="fa-regular fa-image text-4xl" aria-hidden="true" />
                <span className="text-sm">{t('imageViewer.loadFailed')}</span>
              </div>
            ) : (
              <>
                {!natural && <div className="absolute inset-0 flex items-center justify-center text-slate-400"><i className="fa-solid fa-spinner fa-spin text-2xl" aria-hidden="true" /></div>}
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src={item.url} alt={item.name} draggable={false}
                  onLoad={(e) => setNatural({ w: e.currentTarget.naturalWidth || 1, h: e.currentTarget.naturalHeight || 1 })}
                  onError={() => setFailed(true)}
                  className="absolute left-1/2 top-1/2 max-w-none transition-transform duration-75"
                  style={{
                    width: natural?.w, height: natural?.h, visibility: natural ? 'visible' : 'hidden',
                    transform: `translate(-50%, -50%) translate(${pan.x}px, ${pan.y}px) rotate(${rotate}deg) scale(${flipH ? -scale : scale}, ${scale})`,
                  }} />
              </>
            )}
            {items.length > 1 && (
              <>
                <button type="button" onClick={() => go(-1)} disabled={index === 0} aria-label={t('imageViewer.prev')} onPointerDown={(e) => e.stopPropagation()}
                  className="absolute left-3 top-1/2 flex h-11 w-11 -translate-y-1/2 items-center justify-center rounded-full bg-black/40 text-white hover:bg-black/60 disabled:opacity-20">
                  <i className="fa-solid fa-chevron-left" aria-hidden="true" />
                </button>
                <button type="button" onClick={() => go(1)} disabled={index === items.length - 1} aria-label={t('imageViewer.next')} onPointerDown={(e) => e.stopPropagation()}
                  className="absolute right-3 top-1/2 flex h-11 w-11 -translate-y-1/2 items-center justify-center rounded-full bg-black/40 text-white hover:bg-black/60 disabled:opacity-20">
                  <i className="fa-solid fa-chevron-right" aria-hidden="true" />
                </button>
              </>
            )}
          </div>
        )}
        {showInfo && !editing && (
          <aside className="w-72 shrink-0 overflow-y-auto border-l border-white/10 p-4 text-sm" data-testid="image-viewer-info">
            <h2 className="font-semibold">{t('imageViewer.info')}</h2>
            <dl className="mt-3 space-y-2 text-xs">
              <div><dt className="text-slate-400">{t('imageViewer.name')}</dt><dd className="mt-0.5 break-all">{item.name}</dd></div>
              {natural && <div><dt className="text-slate-400">{t('imageViewer.dimensions')}</dt><dd className="mt-0.5 tabular-nums">{natural.w} × {natural.h}</dd></div>}
            </dl>
            {item.info && <div className="mt-2 text-xs">{item.info}</div>}
          </aside>
        )}
      </div>
    </div>,
    document.body,
  )
}

// —— 编辑：旋转、翻转、裁剪（预览画在画布上；跨域图片可以绘制显示，处理在服务端完成）——

type Box = { x: number; y: number; w: number; h: number }
type DragMode = 'move' | 'nw' | 'ne' | 'sw' | 'se'

function ImageEditor({ url, onCancel, onSave }: { url: string; onCancel: () => void; onSave: (edit: ImageEdit) => Promise<void> }) {
  const { t } = useTranslation()
  const stageRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const [img, setImg] = useState<HTMLImageElement | null>(null)
  const [rotate, setRotate] = useState(0)
  const [flipH, setFlipH] = useState(false)
  const [flipV, setFlipV] = useState(false)
  const [cropping, setCropping] = useState(false)
  const [crop, setCrop] = useState<Box | null>(null) // 画布像素坐标
  const [display, setDisplay] = useState(1) // 画布显示缩放
  const [saving, setSaving] = useState(false)
  const drag = useRef<{ mode: DragMode; sx: number; sy: number; box: Box } | null>(null)

  useEffect(() => {
    const image = new Image()
    image.onload = () => setImg(image)
    image.src = url
  }, [url])

  const size = img ? (rotate % 180 === 0 ? { w: img.naturalWidth, h: img.naturalHeight } : { w: img.naturalHeight, h: img.naturalWidth }) : null

  // 把旋转、翻转后的图片画到画布上
  useLayoutEffect(() => {
    const canvas = canvasRef.current
    const stage = stageRef.current
    if (!canvas || !img || !size || !stage) return
    canvas.width = size.w
    canvas.height = size.h
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    ctx.save()
    ctx.translate(size.w / 2, size.h / 2)
    ctx.scale(flipH ? -1 : 1, flipV ? -1 : 1)
    ctx.rotate((rotate * Math.PI) / 180)
    ctx.drawImage(img, -img.naturalWidth / 2, -img.naturalHeight / 2)
    ctx.restore()
    setDisplay(Math.min(1, (stage.clientWidth - 64) / size.w, (stage.clientHeight - 64) / size.h))
  }, [img, rotate, flipH, flipV]) // eslint-disable-line react-hooks/exhaustive-deps

  // 旋转、翻转后原裁剪框失效
  useEffect(() => { setCrop(null) }, [rotate, flipH, flipV])

  function startCrop() {
    if (!size) return
    setCropping(true)
    setCrop((c) => c || { x: Math.round(size.w * 0.1), y: Math.round(size.h * 0.1), w: Math.round(size.w * 0.8), h: Math.round(size.h * 0.8) })
  }

  function onPointerDown(mode: DragMode, e: React.PointerEvent) {
    if (!crop) return
    e.stopPropagation()
    ;(e.target as HTMLElement).setPointerCapture?.(e.pointerId)
    drag.current = { mode, sx: e.clientX, sy: e.clientY, box: { ...crop } }
  }
  function onPointerMove(e: React.PointerEvent) {
    const d = drag.current
    if (!d || !size) return
    const dx = (e.clientX - d.sx) / display
    const dy = (e.clientY - d.sy) / display
    const b = { ...d.box }
    const min = 8
    if (d.mode === 'move') {
      b.x = Math.min(Math.max(0, d.box.x + dx), size.w - b.w)
      b.y = Math.min(Math.max(0, d.box.y + dy), size.h - b.h)
    } else {
      if (d.mode.includes('w')) { const x = Math.min(Math.max(0, d.box.x + dx), d.box.x + d.box.w - min); b.w = d.box.x + d.box.w - x; b.x = x }
      if (d.mode.includes('e')) b.w = Math.min(Math.max(min, d.box.w + dx), size.w - d.box.x)
      if (d.mode.includes('n')) { const y = Math.min(Math.max(0, d.box.y + dy), d.box.y + d.box.h - min); b.h = d.box.y + d.box.h - y; b.y = y }
      if (d.mode.includes('s')) b.h = Math.min(Math.max(min, d.box.h + dy), size.h - d.box.y)
    }
    setCrop({ x: Math.round(b.x), y: Math.round(b.y), w: Math.round(b.w), h: Math.round(b.h) })
  }

  async function save() {
    setSaving(true)
    try {
      await onSave({ rotate, flip_h: flipH, flip_v: flipV, crop: cropping && crop ? crop : null })
    } finally {
      setSaving(false)
    }
  }

  const changed = rotate !== 0 || flipH || flipV || (cropping && crop !== null)
  const tool = (label: string, icon: string, onClick: () => void, active = false) => (
    <Tooltip content={label} placement="bottom">
      <button type="button" aria-label={label} onClick={onClick}
        className={`flex h-9 w-9 items-center justify-center rounded-lg ${active ? 'bg-white/20 text-white' : 'text-slate-200 hover:bg-white/10'}`}>
        <i className={`fa-solid ${icon}`} aria-hidden="true" />
      </button>
    </Tooltip>
  )
  const handle = (mode: DragMode, cls: string) => (
    <span onPointerDown={(e) => onPointerDown(mode, e)} className={`absolute h-3.5 w-3.5 rounded-sm border-2 border-white bg-primary-500 ${cls}`} />
  )

  return (
    <div className="flex min-w-0 flex-1 flex-col" data-testid="image-editor">
      <div className="flex flex-wrap items-center gap-1 border-b border-white/10 px-3 py-2">
        <span className="mr-2 text-xs text-slate-400">{t('imageViewer.editHint')}</span>
        {tool(t('imageViewer.rotateLeft'), 'fa-rotate-left', () => setRotate((r) => (r + 270) % 360))}
        {tool(t('imageViewer.rotateRight'), 'fa-rotate-right', () => setRotate((r) => (r + 90) % 360))}
        {tool(t('imageViewer.flipH'), 'fa-left-right', () => setFlipH((v) => !v), flipH)}
        {tool(t('imageViewer.flipV'), 'fa-up-down', () => setFlipV((v) => !v), flipV)}
        {tool(t('imageViewer.crop'), 'fa-crop-simple', () => (cropping ? (setCropping(false), setCrop(null)) : startCrop()), cropping)}
        {cropping && crop && <span className="px-2 text-xs tabular-nums text-slate-300">{crop.w} × {crop.h}</span>}
        <div className="ml-auto flex items-center gap-2">
          <Button size="sm" variant="ghost" className="text-slate-200 hover:bg-white/10" onClick={onCancel} disabled={saving}>{t('common.actions.cancel')}</Button>
          <Button size="sm" onClick={() => void save()} loading={saving} disabled={!changed} data-testid="image-editor-save">{t('imageViewer.saveAsNew')}</Button>
        </div>
      </div>
      <div ref={stageRef} className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden" onPointerMove={onPointerMove} onPointerUp={() => { drag.current = null }}>
        {!img && <i className="fa-solid fa-spinner fa-spin text-2xl text-slate-400" aria-hidden="true" />}
        <div className="relative" style={size ? { width: size.w * display, height: size.h * display } : { display: 'none' }}>
          <canvas ref={canvasRef} className="block h-full w-full" />
          {cropping && crop && (
            <>
              {/* 裁剪框（框外以阴影变暗） */}
              <div className="absolute cursor-move border-2 border-white" onPointerDown={(e) => onPointerDown('move', e)}
                style={{ left: crop.x * display, top: crop.y * display, width: crop.w * display, height: crop.h * display, boxShadow: '0 0 0 9999px rgba(2,6,23,0.6)' }}>
                {handle('nw', '-left-2 -top-2 cursor-nwse-resize')}
                {handle('ne', '-right-2 -top-2 cursor-nesw-resize')}
                {handle('sw', '-bottom-2 -left-2 cursor-nesw-resize')}
                {handle('se', '-bottom-2 -right-2 cursor-nwse-resize')}
              </div>
            </>
          )}
        </div>
      </div>
      <p className="border-t border-white/10 px-4 py-2 text-xs text-slate-400">{t('imageViewer.saveAsNewHint')}</p>
    </div>
  )
}
