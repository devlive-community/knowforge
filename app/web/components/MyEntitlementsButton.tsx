import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import Link from 'next/link'
import { api } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { Badge } from '@/components/ui'
import { insideFloatingLayer, usePopoverPosition } from '@/components/ui/usePopoverPosition'
import { entitlementLabel, formatEntitlement, type EntitlementDef, type ResolvedEntitlement } from '@/lib/entitlements'
import type { MyAIUsage } from '@/lib/ai-usage'

const METERED: Record<string, (u: MyAIUsage) => number> = {
  'ai.monthly_tokens': (u) => u.used_tokens,
  'translate.monthly_chars': (u) => u.translate_chars,
}

const sourceTone: Record<string, 'slate' | 'primary' | 'amber' | 'emerald'> = { base: 'slate', level: 'primary', membership: 'amber', admin: 'emerald' }

type Data = { items: ResolvedEntitlement[]; definitions: EntitlementDef[] }

// MyEntitlementsButton 「我的权益」：标题旁的帮助图标，点击弹出各项权益的当前生效值与来源（基础 / 等级 / 会员 / 管理员）；
// 打开时才加载（图标显示加载状态），不可用的项不显示；reloadKey 变化时下次打开重新加载。
export default function MyEntitlementsButton({ reloadKey }: { reloadKey?: number }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [data, setData] = useState<Data | null>(null)
  const [usage, setUsage] = useState<MyAIUsage | null>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const popRef = useRef<HTMLDivElement>(null)
  const style = usePopoverPosition(open, triggerRef, popRef)

  useEffect(() => { setData(null) }, [reloadKey])

  async function toggle() {
    if (open) { setOpen(false); return }
    if (!data) {
      setLoading(true)
      try {
        const d = await api<Data>('/users/me/entitlements')
        setData(d)
        // 按月计量的权益（AI tokens、翻译字数）同时展示本月已用
        if (d.items.some((r) => METERED[r.key] && r.source !== 'unavailable')) api<MyAIUsage>('/users/me/ai-usage').then(setUsage).catch(() => { /* 忽略 */ })
      } catch { /* 加载失败时不打开 */ setLoading(false); return }
      setLoading(false)
    }
    setOpen(true)
  }

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (triggerRef.current?.contains(e.target as Node) || popRef.current?.contains(e.target as Node) || insideFloatingLayer(e.target)) return
      setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => { document.removeEventListener('mousedown', onDown); document.removeEventListener('keydown', onKey) }
  }, [open])

  const defs = new Map((data?.definitions || []).map((d) => [d.key, d]))
  const items = (data?.items || []).filter((r) => r.source !== 'unavailable')
  return (
    <>
      <button ref={triggerRef} type="button" onClick={() => void toggle()} aria-expanded={open} aria-haspopup="dialog" aria-busy={loading}
        aria-label={t('entitlement.mine.title')} data-testid="my-entitlements"
        className="inline-flex h-7 items-center gap-1 rounded-full px-2 text-xs font-medium text-slate-500 transition-colors hover:bg-slate-100 hover:text-primary-600">
        <i className={`fa-solid ${loading ? 'fa-spinner fa-spin' : 'fa-circle-question'} text-sm`} aria-hidden="true" />
        {t('entitlement.mine.title')}
      </button>
      {open && createPortal(
        <div ref={popRef} role="dialog" aria-label={t('entitlement.mine.title')} data-floating-layer="" style={style}
          className="fixed z-[300] w-[min(26rem,calc(100vw-1rem))] rounded-xl border border-slate-200 bg-white p-4 shadow-xl">
          <div className="flex items-center gap-2">
            <h2 className="text-sm font-bold text-slate-900">{t('entitlement.mine.title')}</h2>
            {usage && <Link href="/user/ai-usage" className="ml-auto text-xs font-medium text-primary-600 hover:text-primary-700">{t('entitlement.mine.viewAIUsage')}</Link>}
          </div>
          <p className="mt-1 text-xs text-slate-400">{t('entitlement.mine.hint')}</p>
          {items.length === 0 ? (
            <p className="mt-3 text-sm text-slate-500">{t('entitlement.mine.empty')}</p>
          ) : (
            <ul className="mt-3 max-h-[60vh] space-y-1.5 overflow-y-auto">
              {items.map((r) => (
                <li key={r.key} className="flex items-center justify-between gap-3 rounded-lg bg-slate-50 px-3 py-2">
                  <span className="min-w-0">
                    <span className="block truncate text-sm text-slate-700">{entitlementLabel(t, r.key)}</span>
                    {usage && METERED[r.key] && <span className="block text-xs tabular-nums text-slate-400">{t('entitlement.mine.usedThisMonth', { n: METERED[r.key](usage).toLocaleString() })}</span>}
                  </span>
                  <span className="flex shrink-0 items-center gap-2">
                    <span className="text-sm font-medium tabular-nums text-slate-900">{formatEntitlement(t, defs.get(r.key), r.value)}</span>
                    <Badge tone={sourceTone[r.source] || 'slate'}>{t(`entitlement.source.${r.source}`)}</Badge>
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>,
        document.body,
      )}
    </>
  )
}
