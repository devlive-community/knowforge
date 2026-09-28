import {
  InputHTMLAttributes,
  TextareaHTMLAttributes,
  ReactNode,
  useCallback,
  forwardRef,
  useEffect,
  useRef,
  useState,
} from 'react'
import { createPortal } from 'react-dom'
import { ControlSize, sizedControlStyle } from './controlSize'
import { useTranslation } from '../../lib/i18n'

// 输入类控件的基础样式：无 focus 外圈阴影，仅边框颜色变化
const controlClass =
  'w-full rounded-lg border border-slate-200 bg-white px-3.5 text-sm text-slate-900 ' +
  'placeholder:text-slate-400 transition-colors hover:border-slate-300 ' +
  'focus:border-primary-500 focus:outline-none ' +
  'disabled:cursor-not-allowed disabled:bg-slate-50 disabled:text-slate-400'

interface InputProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'size'> {
  /** 前置图标：输入框内左侧留出图标位置 */
  leading?: ReactNode
  /** 后置内容：可放置密码显隐等交互按钮 */
  trailing?: ReactNode
  size?: ControlSize
}

// Input 通用文本输入框
export const Input = forwardRef<HTMLInputElement, InputProps>(function Input({ className, leading, trailing, size = 'md', style, ...rest }, ref) {
  if (!leading && !trailing) return <input ref={ref} className={`${controlClass} ${className || ''}`.trim()} {...rest} style={sizedControlStyle(size, style)} />
  return (
    <div className={`relative ${className || ''}`.trim()}>
      {leading && (
        <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-slate-400">{leading}</span>
      )}
      <input ref={ref} className={`${leading ? 'pl-9' : ''} ${trailing ? 'pr-10' : ''} ${controlClass}`} {...rest} style={sizedControlStyle(size, style)} />
      {trailing && (
        <span className="absolute right-2 top-1/2 -translate-y-1/2">{trailing}</span>
      )}
    </div>
  )
})

// Textarea 通用多行文本域（forwardRef 供编辑器操作选区）
export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(
  function Textarea({ className, ...rest }, ref) {
    return <textarea ref={ref} className={`py-2.5 ${controlClass} ${className || ''}`.trim()} {...rest} />
  },
)

export interface SelectOption {
  value: string
  label: string
}

interface SelectProps {
  options: SelectOption[]
  value?: string
  onChange?: (value: string) => void
  className?: string
  placeholder?: string
  disabled?: boolean
  leading?: ReactNode
  menuPlacement?: 'top' | 'bottom'
  size?: ControlSize
  searchable?: boolean // 开启后菜单顶部显示搜索框，客户端按 label 过滤（选项多时用）
  searchPlaceholder?: string
}

interface SelectMenuPosition {
  top: number
  left: number
  width: number
}

const SELECT_MENU_GAP = 4
const SELECT_VIEWPORT_GAP = 8

// Select 自绘下拉选择：选项层通过 Portal 脱离页面 overflow 与层叠上下文。
export function Select({ options, value, onChange, className, placeholder, disabled, leading, menuPlacement = 'bottom', size = 'md', searchable, searchPlaceholder }: SelectProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const visibleOptions = searchable && query.trim()
    ? options.filter((o) => o.label.toLowerCase().includes(query.trim().toLowerCase()))
    : options
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLUListElement>(null)
  const [menuPosition, setMenuPosition] = useState<SelectMenuPosition | null>(null)
  const selected = options.find((o) => o.value === value)

  const updateMenuPosition = useCallback(() => {
    const trigger = triggerRef.current
    const menu = menuRef.current
    if (!trigger || !menu) return

    const triggerRect = trigger.getBoundingClientRect()
    const menuHeight = menu.offsetHeight
    const maxMenuWidth = Math.max(1, window.innerWidth - SELECT_VIEWPORT_GAP * 2)
    const menuWidth = Math.min(menu.offsetWidth, maxMenuWidth)
    const topSpace = triggerRect.top - SELECT_VIEWPORT_GAP
    const bottomSpace = window.innerHeight - triggerRect.bottom - SELECT_VIEWPORT_GAP
    const preferredSpace = menuPlacement === 'top' ? topSpace : bottomSpace
    const alternateSpace = menuPlacement === 'top' ? bottomSpace : topSpace
    const actualPlacement = preferredSpace >= menuHeight || preferredSpace >= alternateSpace
      ? menuPlacement
      : menuPlacement === 'top' ? 'bottom' : 'top'
    const desiredTop = actualPlacement === 'top'
      ? triggerRect.top - menuHeight - SELECT_MENU_GAP
      : triggerRect.bottom + SELECT_MENU_GAP

    setMenuPosition({
      top: Math.min(
        Math.max(SELECT_VIEWPORT_GAP, window.innerHeight - menuHeight - SELECT_VIEWPORT_GAP),
        Math.max(SELECT_VIEWPORT_GAP, desiredTop),
      ),
      left: Math.min(
        window.innerWidth - menuWidth - SELECT_VIEWPORT_GAP,
        Math.max(SELECT_VIEWPORT_GAP, triggerRect.left),
      ),
      width: menuWidth,
    })
  }, [menuPlacement])

  useEffect(() => {
    if (!open) {
      setMenuPosition(null)
      setQuery('')
      return
    }
    const frame = window.requestAnimationFrame(updateMenuPosition)
    function onPointerDown(e: MouseEvent) {
      const target = e.target as Node
      if (!rootRef.current?.contains(target) && !menuRef.current?.contains(target)) setOpen(false)
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onPointerDown)
    document.addEventListener('keydown', onKey)
    window.addEventListener('resize', updateMenuPosition)
    window.addEventListener('scroll', updateMenuPosition, true)
    return () => {
      window.cancelAnimationFrame(frame)
      document.removeEventListener('mousedown', onPointerDown)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('resize', updateMenuPosition)
      window.removeEventListener('scroll', updateMenuPosition, true)
    }
  }, [open, updateMenuPosition])

  useEffect(() => {
    if (disabled && open) setOpen(false)
  }, [disabled, open])

  const menu = open && typeof document !== 'undefined' && createPortal(
    <ul
      ref={menuRef}
      role="listbox"
      data-floating-layer=""
      className="fixed z-[310] max-h-60 space-y-0.5 overflow-y-auto rounded-lg border border-slate-200 bg-white p-1 shadow-lg"
      style={{
        top: menuPosition?.top ?? 0,
        left: menuPosition?.left ?? 0,
        width: menuPosition?.width ?? 'max-content',
        minWidth: Math.min(
          triggerRef.current?.getBoundingClientRect().width ?? 0,
          typeof window === 'undefined' ? 0 : window.innerWidth - SELECT_VIEWPORT_GAP * 2,
        ),
        maxWidth: `calc(100vw - ${SELECT_VIEWPORT_GAP * 2}px)`,
        visibility: menuPosition ? 'visible' : 'hidden',
      }}
    >
      {searchable && (
        <li role="none" className="sticky top-0 z-10 -mx-1 -mt-1 mb-1 bg-white px-1 pt-1">
          <input autoFocus value={query} onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.stopPropagation()} placeholder={searchPlaceholder || t('ui.input.selectPlaceholder')}
            className="w-full rounded-md border border-slate-200 px-2.5 py-1.5 text-sm placeholder:text-slate-400 focus:border-primary-500 focus:outline-none" />
        </li>
      )}
      {visibleOptions.length === 0 && (
        <li role="none" className="px-3 py-2 text-sm text-slate-400">{t('ui.select.noMatch')}</li>
      )}
      {visibleOptions.map((o) => {
        const active = o.value === value
        return (
          <li key={o.value} role="none">
            <button type="button" role="option" aria-selected={active}
              onClick={() => { onChange?.(o.value); setOpen(false) }}
              className={`flex w-full items-center justify-between gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors ${
                active ? 'bg-primary-50 font-medium text-primary-700' : 'text-slate-700 hover:bg-slate-50'
              }`}>
              <span className="truncate">{o.label}</span>
              {active && (
                <svg viewBox="0 0 20 20" fill="none" aria-hidden="true" className="h-4 w-4 shrink-0 text-primary-600">
                  <path d="m5 10 3.5 3.5L15 7" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              )}
            </button>
          </li>
        )
      })}
    </ul>,
    document.body,
  )

  return (
    <div className={`relative ${className || ''}`.trim()} ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className={`flex items-center justify-between gap-2 text-left ${controlClass}`}
        style={sizedControlStyle(size)}
      >
        <span className="flex min-w-0 items-center gap-2">
          {leading}
          <span className={`truncate ${selected ? '' : 'text-slate-400'}`}>{selected?.label || placeholder || t('ui.input.selectPlaceholder')}</span>
        </span>
        <svg viewBox="0 0 20 20" fill="none" aria-hidden="true"
          className={`h-4 w-4 shrink-0 text-slate-400 transition-transform ${open ? 'rotate-180' : ''}`}>
          <path d="M6 8l4 4 4-4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      {menu}
    </div>
  )
}

interface FieldProps {
  label: ReactNode
  hint?: ReactNode
  children: ReactNode
}

// Field 表单字段容器：统一 label 与控件的排版
export function Field({ label, hint, children }: FieldProps) {
  return (
    <div>
      <label className="mb-1.5 block text-sm font-medium text-slate-700">{label}</label>
      {children}
      {hint && <p className="mt-1.5 text-xs text-slate-400">{hint}</p>}
    </div>
  )
}
