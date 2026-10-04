import { ReactNode, useEffect, useId, useRef } from 'react'
import { CloseIcon } from '@/components/icons'
import { useTranslation } from '../../lib/i18n'
import Tooltip from './Tooltip'

interface ModalProps {
  open: boolean
  onClose: () => void
  title?: string
  children: ReactNode
  footer?: ReactNode
  className?: string
  /** 提升层级（z-200），置于全局确认框（z-150）与写作台侧栏之上；Toast（z-210）仍显示在最上层。二次认证等常在确认框之后弹出，需盖过它 */
  elevated?: boolean
}

export function Modal({ open, onClose, title, children, footer, className, elevated }: ModalProps) {
  const ref = useRef<HTMLDivElement>(null)
  const titleId = useId()

  useEffect(() => {
    if (!open) return
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [open, onClose])

  if (!open) return null

  return (
    <div className={`fixed inset-0 flex items-end justify-center sm:items-center ${elevated ? 'z-[200]' : 'z-50'}`}>
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <div ref={ref} role="dialog" aria-modal="true" aria-labelledby={title ? titleId : undefined} className={`relative mx-0 flex max-h-[calc(100vh-1rem)] w-full flex-col rounded-t-2xl bg-white shadow-2xl sm:mx-4 sm:rounded-2xl ${/max-w-/.test(className || '') ? '' : 'max-w-lg'} ${className || ''}`}
        style={{ borderRadius: 'var(--radius)' }}>
        {title && (
          <div className="flex items-center justify-between border-b border-slate-200 px-6 py-4">
            <h3 id={titleId} className="text-lg font-semibold text-slate-900">{title}</h3>
            <ModalCloseButton onClose={onClose} />
          </div>
        )}
        <div className="min-h-0 overflow-y-auto overflow-x-auto px-6 py-4">{children}</div>
        {footer && (
          <div className="flex shrink-0 justify-end gap-2 border-t border-slate-200 px-6 py-4">{footer}</div>
        )}
      </div>
    </div>
  )
}
// ModalCloseButton 关闭按钮：独立组件以便使用 useTranslation
function ModalCloseButton({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  return (
    <Tooltip content={t('ui.modal.close')}>
      <button type="button" aria-label={t('ui.modal.close')} onClick={onClose} className="flex items-center justify-center text-slate-400 hover:bg-slate-100 hover:text-slate-600"
        style={{ width: 'var(--control-height)', height: 'var(--control-height)', borderRadius: 'var(--radius)' }}>
        <CloseIcon className="h-5 w-5" />
      </button>
    </Tooltip>
  )
}
