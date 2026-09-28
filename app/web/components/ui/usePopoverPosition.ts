import { type CSSProperties, type RefObject, useLayoutEffect, useState } from 'react'

const GAP = 6 // 弹层与触发器的间距
const EDGE = 8 // 弹层与视口边缘的最小距离

// usePopoverPosition 以 fixed 定位（配合 createPortal 渲染到 body）放置下拉弹层：
// 默认在触发器下方，下方放不下且上方空间更大时翻到上方，并始终夹在视口内（不会被弹窗或页面底部遮挡）；
// 弹层尺寸变化（如切换月份行数不同）、页面滚动与窗口缩放时重新计算。首次测量前隐藏，避免闪烁。
// insideFloatingLayer 点击是否落在其他浮层内（如日期面板中的时间下拉菜单，渲染在 body 下）：
// 弹层的「点击外部关闭」应忽略这类点击，否则菜单选项还没生效弹层就已关闭。
export function insideFloatingLayer(target: EventTarget | null): boolean {
  return target instanceof Element && target.closest('[data-floating-layer]') !== null
}

export function usePopoverPosition(open: boolean, triggerRef: RefObject<HTMLElement>, popRef: RefObject<HTMLElement>): CSSProperties {
  const [style, setStyle] = useState<CSSProperties>({ top: 0, left: 0, visibility: 'hidden' })
  useLayoutEffect(() => {
    if (!open) {
      setStyle({ top: 0, left: 0, visibility: 'hidden' })
      return
    }
    const place = () => {
      const trigger = triggerRef.current
      const pop = popRef.current
      if (!trigger || !pop) return
      const r = trigger.getBoundingClientRect()
      const h = pop.offsetHeight
      const w = pop.offsetWidth
      const below = window.innerHeight - r.bottom - GAP - EDGE
      const above = r.top - GAP - EDGE
      let top = below >= h || below >= above ? r.bottom + GAP : r.top - GAP - h
      top = Math.max(EDGE, Math.min(top, window.innerHeight - h - EDGE))
      const left = Math.max(EDGE, Math.min(r.left, window.innerWidth - w - EDGE))
      setStyle({ top, left, visibility: 'visible' })
    }
    place()
    const observer = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(place) : null
    if (observer && popRef.current) observer.observe(popRef.current)
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, true)
    return () => {
      observer?.disconnect()
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', place, true)
    }
  }, [open, triggerRef, popRef])
  return style
}
