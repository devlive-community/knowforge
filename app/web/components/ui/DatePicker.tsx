import { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { CalendarIcon, ChevronLeftIcon, ChevronRightIcon, CloseIcon } from '@/components/icons'
import { useTranslation } from '../../lib/i18n'
import { ControlSize, sizedControlStyle } from './controlSize'
import { insideFloatingLayer, usePopoverPosition } from './usePopoverPosition'

interface DatePickerProps {
  value: string // 'YYYY-MM-DD' 或 ''
  onChange: (value: string) => void
  placeholder?: string
  className?: string
  size?: ControlSize
  min?: string // 'YYYY-MM-DD'
  max?: string // 'YYYY-MM-DD'
  clearable?: boolean
  ariaLabel?: string
}

// 星期与月份文案通过 i18n 键取（ui.datepicker.weekday* / month*）
const WEEKDAY_KEYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']
const monthKey = (m: number) => `ui.datepicker.month${m + 1}`

function pad(n: number) { return String(n).padStart(2, '0') }
function toKey(y: number, m: number, d: number) { return `${y}-${pad(m + 1)}-${pad(d)}` }
// 本地时区的今天，避免 toISOString() 的 UTC 偏移在清晨把“今天”算成昨天
function localToday() { const now = new Date(); return toKey(now.getFullYear(), now.getMonth(), now.getDate()) }
function parse(value: string): { y: number; m: number; d: number } | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!match) return null
  const y = Number(match[1]); const m = Number(match[2]) - 1; const d = Number(match[3])
  if (m < 0 || m > 11 || d < 1 || d > 31) return null
  return { y, m, d }
}

// DatePicker 全站统一的日期选择器：只读输入框 + 弹出月历，值为 'YYYY-MM-DD' 字符串
export function DatePicker({ value, onChange, placeholder, className = '', size = 'md', min, max, clearable = true, ariaLabel }: DatePickerProps) {
  const { t } = useTranslation()
  const resolvedPlaceholder = placeholder ?? t('ui.datepicker.placeholder')
  const weekdays = WEEKDAY_KEYS.map((k) => t(`ui.datepicker.weekday${k}`))
  const months = Array.from({ length: 12 }, (_, i) => t(monthKey(i)))
  const [open, setOpen] = useState(false)
  const parsed = parse(value)
  // 面板当前浏览的年月（不等于已选中的值）
  const [view, setView] = useState(() => {
    const base = parsed || parse(localToday())!
    return { y: base.y, m: base.m }
  })
  const wrapRef = useRef<HTMLDivElement>(null)
  const popRef = useRef<HTMLDivElement>(null)
  const popStyle = usePopoverPosition(open, wrapRef, popRef)

  useEffect(() => {
    if (!open) return
    const anchor = parsed || parse(localToday())!
    setView({ y: anchor.y, m: anchor.m })
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!open) return
    function onDocClick(e: MouseEvent) {
      const target = e.target as Node
      if (wrapRef.current?.contains(target) || popRef.current?.contains(target) || insideFloatingLayer(target)) return
      setOpen(false)
    }
    function onKey(e: KeyboardEvent) { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDocClick)
    document.addEventListener('keydown', onKey)
    return () => { document.removeEventListener('mousedown', onDocClick); document.removeEventListener('keydown', onKey) }
  }, [open])

  // 当月网格：以周一为起始，补齐前导空位
  const cells = useMemo(() => {
    const first = new Date(view.y, view.m, 1)
    const startWeekday = (first.getDay() + 6) % 7 // 周一=0
    const daysInMonth = new Date(view.y, view.m + 1, 0).getDate()
    const list: (number | null)[] = []
    for (let i = 0; i < startWeekday; i++) list.push(null)
    for (let d = 1; d <= daysInMonth; d++) list.push(d)
    return list
  }, [view])

  const minP = min ? parse(min) : null
  const maxP = max ? parse(max) : null
  function outOfRange(key: string) {
    if (minP && key < min!) return true
    if (maxP && key > max!) return true
    return false
  }

  const todayKey = localToday()

  function pick(d: number) {
    const key = toKey(view.y, view.m, d)
    if (outOfRange(key)) return
    onChange(key)
    setOpen(false)
  }

  return (
    <div ref={wrapRef} className={`relative ${className}`}>
      <button type="button" onClick={() => setOpen((v) => !v)} aria-label={ariaLabel || resolvedPlaceholder} aria-haspopup="dialog" aria-expanded={open}
        className="flex w-full items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 text-left text-sm text-slate-900 transition-colors hover:border-slate-300 focus:border-primary-500 focus:outline-none"
        style={sizedControlStyle(size)}>
        <CalendarIcon className="h-4 w-4 shrink-0 text-slate-400" />
        <span className={`flex-1 truncate ${value ? '' : 'text-slate-400'}`}>{value || resolvedPlaceholder}</span>
        {clearable && value && (
          <span role="button" tabIndex={-1} aria-label={t('ui.datepicker.clear')}
            onClick={(e) => { e.stopPropagation(); onChange('') }}
            className="flex h-5 w-5 shrink-0 items-center justify-center rounded text-slate-400 hover:bg-slate-100 hover:text-slate-600">
            <CloseIcon className="h-3.5 w-3.5" />
          </span>
        )}
      </button>

      {open && typeof document !== 'undefined' && createPortal(
        <div ref={popRef} data-floating-layer="" className="fixed z-[300] w-64 rounded-xl border border-slate-200 bg-white p-3 shadow-lg" role="dialog" style={popStyle}>
          <div className="mb-2 flex items-center justify-between">
            <button type="button" aria-label={t('ui.datepicker.prevMonth')} onClick={() => setView((v) => v.m === 0 ? { y: v.y - 1, m: 11 } : { y: v.y, m: v.m - 1 })}
              className="flex h-7 w-7 items-center justify-center rounded-lg text-slate-500 hover:bg-slate-100"><ChevronLeftIcon className="h-4 w-4" /></button>
            <div className="text-sm font-semibold text-slate-800">{t('ui.datepicker.monthYear', { year: view.y, month: months[view.m] })}</div>
            <button type="button" aria-label={t('ui.datepicker.nextMonth')} onClick={() => setView((v) => v.m === 11 ? { y: v.y + 1, m: 0 } : { y: v.y, m: v.m + 1 })}
              className="flex h-7 w-7 items-center justify-center rounded-lg text-slate-500 hover:bg-slate-100"><ChevronRightIcon className="h-4 w-4" /></button>
          </div>
          <div className="mb-1 grid grid-cols-7 gap-0.5">
            {weekdays.map((w) => <div key={w} className="flex h-7 items-center justify-center text-xs text-slate-400">{w}</div>)}
          </div>
          <div className="grid grid-cols-7 gap-0.5">
            {cells.map((d, i) => {
              if (d === null) return <div key={`e${i}`} className="h-8" />
              const key = toKey(view.y, view.m, d)
              const selected = key === value
              const isToday = key === todayKey
              const disabled = outOfRange(key)
              return (
                <button key={key} type="button" disabled={disabled} onClick={() => pick(d)}
                  className={`flex h-8 items-center justify-center rounded-lg text-sm transition-colors ${
                    selected ? 'bg-primary-500 font-medium text-white'
                      : disabled ? 'cursor-not-allowed text-slate-300'
                        : isToday ? 'text-primary-600 ring-1 ring-inset ring-primary-200 hover:bg-primary-50'
                          : 'text-slate-700 hover:bg-slate-100'}`}>
                  {d}
                </button>
              )
            })}
          </div>
          <div className="mt-2 flex items-center justify-between border-t border-slate-100 pt-2">
            <button type="button" onClick={() => { const key = todayKey; if (!outOfRange(key)) { onChange(key); setOpen(false) } }}
              className="text-xs text-primary-600 hover:underline">{t('ui.datepicker.today')}</button>
            {clearable && (
              <button type="button" onClick={() => { onChange(''); setOpen(false) }} className="text-xs text-slate-400 hover:text-slate-600">{t('ui.datepicker.clearText')}</button>
            )}
          </div>
        </div>,
        document.body,
      )}
    </div>
  )
}
