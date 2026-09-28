import { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from '@/lib/i18n'
import { CalendarIcon, ChevronLeftIcon, ChevronRightIcon, CloseIcon } from '@/components/icons'
import { Select } from './Input'
import { insideFloatingLayer, usePopoverPosition } from './usePopoverPosition'

// DateTimePicker 日期时间选择器：替代原生 datetime-local，风格与全站控件一致（主题化）。
// value / onChange 使用 'YYYY-MM-DDTHH:mm'（与 <input type="datetime-local"> 一致），留空表示未设置。
interface Parsed { y: number; mo: number; d: number; hh: number; mm: number }
function parseValue(value: string): Parsed | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(value || '')
  if (!m) return null
  return { y: +m[1], mo: +m[2] - 1, d: +m[3], hh: +m[4], mm: +m[5] }
}
function pad(n: number) { return String(n).padStart(2, '0') }
function toValue(p: Parsed) { return `${p.y}-${pad(p.mo + 1)}-${pad(p.d)}T${pad(p.hh)}:${pad(p.mm)}` }

export default function DateTimePicker({ value, onChange, placeholder, ariaLabel, disabled }: {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  ariaLabel?: string
  disabled?: boolean
}) {
  const { t, locale } = useTranslation()
  const intlLocale = locale === 'en' ? 'en-US' : 'zh-CN'
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const popRef = useRef<HTMLDivElement>(null)

  const parsed = parseValue(value)
  const now = new Date()
  const [viewY, setViewY] = useState(parsed?.y ?? now.getFullYear())
  const [viewM, setViewM] = useState(parsed?.mo ?? now.getMonth())

  useEffect(() => {
    if (open) { setViewY(parsed?.y ?? now.getFullYear()); setViewM(parsed?.mo ?? now.getMonth()) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const popStyle = usePopoverPosition(open, triggerRef, popRef)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (triggerRef.current?.contains(e.target as Node) || popRef.current?.contains(e.target as Node) || insideFloatingLayer(e.target)) return
      setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDown); document.addEventListener('keydown', onKey)
    return () => { document.removeEventListener('mousedown', onDown); document.removeEventListener('keydown', onKey) }
  }, [open])

  const weekdays = useMemo(() => {
    const base = new Date(2023, 0, 1) // 2023-01-01 是周日
    return Array.from({ length: 7 }, (_, i) => new Intl.DateTimeFormat(intlLocale, { weekday: 'short' }).format(new Date(base.getTime() + i * 86400000)))
  }, [intlLocale])

  const cells = useMemo(() => {
    const first = new Date(viewY, viewM, 1)
    const start = first.getDay()
    const daysInMonth = new Date(viewY, viewM + 1, 0).getDate()
    const out: (number | null)[] = []
    for (let i = 0; i < start; i++) out.push(null)
    for (let d = 1; d <= daysInMonth; d++) out.push(d)
    while (out.length % 7 !== 0) out.push(null)
    return out
  }, [viewY, viewM])

  const label = parsed
    ? new Intl.DateTimeFormat(intlLocale, { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(parsed.y, parsed.mo, parsed.d, parsed.hh, parsed.mm))
    : ''

  const pick = (day: number) => {
    const base = parsed || { y: viewY, mo: viewM, d: day, hh: 0, mm: 0 }
    onChange(toValue({ ...base, y: viewY, mo: viewM, d: day }))
  }
  const setTime = (hh: number, mm: number) => {
    const base = parsed || { y: viewY, mo: viewM, d: now.getDate(), hh: 0, mm: 0 }
    onChange(toValue({ ...base, hh, mm }))
  }
  const prevMonth = () => { if (viewM === 0) { setViewM(11); setViewY(viewY - 1) } else setViewM(viewM - 1) }
  const nextMonth = () => { if (viewM === 11) { setViewM(0); setViewY(viewY + 1) } else setViewM(viewM + 1) }

  const monthLabel = new Intl.DateTimeFormat(intlLocale, { year: 'numeric', month: 'long' }).format(new Date(viewY, viewM, 1))

  return (
    <>
      <button ref={triggerRef} type="button" disabled={disabled} aria-label={ariaLabel} onClick={() => !disabled && setOpen((v) => !v)}
        className="flex w-full items-center gap-2 rounded-lg border border-slate-200 bg-white px-3.5 text-left text-sm text-slate-900 transition-colors hover:border-slate-300 focus:border-primary-500 focus:outline-none disabled:cursor-not-allowed disabled:bg-slate-50"
        style={{ height: 'var(--control-height)' }}>
        <CalendarIcon className="h-4 w-4 shrink-0 text-slate-400" />
        <span className={`min-w-0 flex-1 truncate ${label ? '' : 'text-slate-400'}`}>{label || placeholder || ''}</span>
        {label && !disabled && (
          <span role="button" tabIndex={0} aria-label={t('common.actions.close')} onClick={(e) => { e.stopPropagation(); onChange('') }}
            className="shrink-0 rounded p-0.5 text-slate-300 hover:bg-slate-100 hover:text-slate-500"><CloseIcon className="h-3.5 w-3.5" /></span>
        )}
      </button>
      {open && typeof document !== 'undefined' && createPortal(
        <div ref={popRef} data-floating-layer="" className="fixed z-[300] w-72 rounded-xl border border-slate-200 bg-white p-3 shadow-xl" style={popStyle}>
          <div className="mb-2 flex items-center justify-between">
            <button type="button" aria-label={t('dtp.prevMonth')} onClick={prevMonth} className="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700"><ChevronLeftIcon className="h-4 w-4" /></button>
            <span className="text-sm font-medium text-slate-800">{monthLabel}</span>
            <button type="button" aria-label={t('dtp.nextMonth')} onClick={nextMonth} className="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700"><ChevronRightIcon className="h-4 w-4" /></button>
          </div>
          <div className="grid grid-cols-7 text-center text-[11px] text-slate-400">
            {weekdays.map((w) => <span key={w} className="py-1">{w}</span>)}
          </div>
          <div className="grid grid-cols-7 gap-0.5">
            {cells.map((day, i) => {
              if (day === null) return <span key={i} />
              const selected = parsed && parsed.y === viewY && parsed.mo === viewM && parsed.d === day
              const isToday = now.getFullYear() === viewY && now.getMonth() === viewM && now.getDate() === day
              return (
                <button key={i} type="button" onClick={() => pick(day)}
                  className={`flex h-8 items-center justify-center rounded-lg text-sm transition-colors ${selected ? 'bg-primary-500 font-medium text-white' : isToday ? 'text-primary-600 ring-1 ring-inset ring-primary-200 hover:bg-primary-50' : 'text-slate-700 hover:bg-slate-100'}`}>
                  {day}
                </button>
              )
            })}
          </div>
          <div className="mt-3 flex items-center gap-2 border-t border-slate-100 pt-3">
            <span className="text-xs text-slate-400">{t('dtp.time')}</span>
            <span className="w-20"><Select size="sm" value={String(parsed?.hh ?? 0)} onChange={(v) => setTime(Number(v), parsed?.mm ?? 0)} options={Array.from({ length: 24 }, (_, h) => ({ value: String(h), label: pad(h) }))} /></span>
            <span className="text-slate-400">:</span>
            <span className="w-20"><Select size="sm" value={String(parsed?.mm ?? 0)} onChange={(v) => setTime(parsed?.hh ?? 0, Number(v))} options={Array.from({ length: 12 }, (_, i) => ({ value: String(i * 5), label: pad(i * 5) }))} /></span>
          </div>
        </div>,
        document.body,
      )}
    </>
  )
}
