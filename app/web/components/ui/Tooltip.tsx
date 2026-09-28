import { ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

interface TooltipProps {
  content: ReactNode
  placement?: 'top' | 'bottom'
  children: ReactNode
  className?: string
  disabled?: boolean
}

interface TooltipPosition {
  top: number
  left: number
  arrowLeft: number
  placement: 'top' | 'bottom'
}

const VIEWPORT_GAP = 8
const TRIGGER_GAP = 8

// Tooltip 通用气泡提示：通过 Portal 渲染到 body，避免被卡片 overflow 或层叠上下文裁切。
export default function Tooltip({ content, placement = 'top', children, className, disabled = false }: TooltipProps) {
  const [visible, setVisible] = useState(false)
  const [position, setPosition] = useState<TooltipPosition | null>(null)
  const triggerRef = useRef<HTMLSpanElement>(null)
  const tooltipRef = useRef<HTMLSpanElement>(null)

  const updatePosition = useCallback(() => {
    const trigger = triggerRef.current
    const tooltip = tooltipRef.current
    if (!trigger || !tooltip) return

    const triggerRect = trigger.getBoundingClientRect()
    const tooltipWidth = tooltip.offsetWidth
    const tooltipHeight = tooltip.offsetHeight
    const topSpace = triggerRect.top
    const bottomSpace = window.innerHeight - triggerRect.bottom
    let actualPlacement = placement

    if (placement === 'top' && topSpace < tooltipHeight + TRIGGER_GAP + VIEWPORT_GAP && bottomSpace > topSpace) {
      actualPlacement = 'bottom'
    } else if (placement === 'bottom' && bottomSpace < tooltipHeight + TRIGGER_GAP + VIEWPORT_GAP && topSpace > bottomSpace) {
      actualPlacement = 'top'
    }

    const triggerCenter = triggerRect.left + triggerRect.width / 2
    const left = Math.min(
      window.innerWidth - tooltipWidth - VIEWPORT_GAP,
      Math.max(VIEWPORT_GAP, triggerCenter - tooltipWidth / 2),
    )
    const desiredTop = actualPlacement === 'top'
      ? triggerRect.top - tooltipHeight - TRIGGER_GAP
      : triggerRect.bottom + TRIGGER_GAP
    const top = Math.min(
      window.innerHeight - tooltipHeight - VIEWPORT_GAP,
      Math.max(VIEWPORT_GAP, desiredTop),
    )
    const arrowLeft = Math.min(tooltipWidth - 8, Math.max(8, triggerCenter - left))

    setPosition({ top, left, arrowLeft, placement: actualPlacement })
  }, [placement])

  useEffect(() => {
    if (disabled && visible) {
      setVisible(false)
      return
    }
    if (!visible) {
      setPosition(null)
      return
    }
    const frame = window.requestAnimationFrame(updatePosition)
    window.addEventListener('resize', updatePosition)
    window.addEventListener('scroll', updatePosition, true)
    return () => {
      window.cancelAnimationFrame(frame)
      window.removeEventListener('resize', updatePosition)
      window.removeEventListener('scroll', updatePosition, true)
    }
  }, [disabled, visible, updatePosition])

  const layer = visible && typeof document !== 'undefined' && createPortal(
    <span
      ref={tooltipRef}
      role="tooltip"
      className="pointer-events-none fixed z-[320] w-max max-w-[calc(100vw-1rem)] whitespace-normal rounded-md bg-slate-900 px-2 py-1 text-center text-xs font-medium text-white shadow-lg"
      style={{
        animation: 'tooltip-in 120ms ease-out',
        left: position?.left ?? 0,
        top: position?.top ?? 0,
        visibility: position ? 'visible' : 'hidden',
      }}
    >
      {content}
      {position && (
        <span
          className={`absolute h-1.5 w-1.5 -translate-x-1/2 rotate-45 bg-slate-900 ${position.placement === 'top' ? '-bottom-[3px]' : '-top-[3px]'}`}
          style={{ left: position.arrowLeft }}
        />
      )}
    </span>,
    document.body,
  )

  return (
    <span ref={triggerRef} className={`relative inline-flex ${className || ''}`.trim()}
      onMouseEnter={() => { if (!disabled) setVisible(true) }}
      onMouseLeave={() => setVisible(false)}
      onFocusCapture={() => { if (!disabled) setVisible(true) }}
      onBlurCapture={() => setVisible(false)}>
      {children}
      {layer}
    </span>
  )
}
