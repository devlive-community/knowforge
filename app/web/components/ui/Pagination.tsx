import { useEffect, useRef, useState } from 'react'
import { useTranslation } from '../../lib/i18n'
import { ControlSize, controlHeight, sizedControlStyle } from './controlSize'

// Pagination 通用分页条。点击翻页后，被点的按钮显示加载状态、整条禁用，直到目标页加载完成：
// 传入 loading 时，以点击后开始的那次加载结束为准（失败也会恢复）；不传时，以 page（已加载数据的页码，如 data.page）到达目标页为准。
export function Pagination({ page, pageSize, total, onChange, size = 'md', loading }: {
  page: number
  pageSize: number
  total: number
  onChange: (page: number) => void
  size?: ControlSize
  loading?: boolean
}) {
  const { t } = useTranslation()
  const [pending, setPending] = useState<number | null>(null)
  const [clicked, setClicked] = useState<'prev' | 'next' | 'page'>('page') // 显示加载状态的控件：被点击的那一个
  const sawLoading = useRef(false)
  useEffect(() => {
    if (pending === null) return
    if (loading === undefined) {
      if (page === pending) setPending(null)
      return
    }
    if (loading) sawLoading.current = true
    else if (sawLoading.current) setPending(null)
  }, [page, pending, loading])
  const pages = Math.max(1, Math.ceil(total / pageSize))
  if (pages <= 1) return null
  const busy = pending !== null
  const go = (p: number, control: 'prev' | 'next' | 'page' = 'page') => {
    if (busy || p === page || p < 1 || p > pages) return
    sawLoading.current = false
    setClicked(control)
    setPending(p)
    onChange(p)
  }
  const spinner = <span className="inline-block h-3.5 w-3.5 animate-spin rounded-full border-2 border-current border-r-transparent" aria-hidden="true" />
  const list: number[] = []
  for (let i = Math.max(1, page - 2); i <= Math.min(pages, page + 2); i++) list.push(i)

  const navClass =
    'inline-flex items-center rounded-lg border border-slate-300 bg-white px-3 text-sm text-slate-600 ' +
    'transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50'
  const pageClass = (active: boolean) =>
    `inline-flex items-center justify-center rounded-lg border px-3 text-sm transition-colors ${
      active
        ? 'border-primary-500 bg-primary-500 text-white'
        : 'border-slate-300 bg-white text-slate-600 hover:bg-slate-50'
    }`

  return (
    <div className="mt-6 flex items-center justify-center gap-1.5">
      <button disabled={page <= 1 || busy} onClick={() => go(page - 1, 'prev')} className={navClass} style={sizedControlStyle(size)} aria-busy={busy && clicked === 'prev'}>
        {busy && clicked === 'prev' ? <span className="inline-flex items-center gap-1.5">{spinner}{t('ui.pagination.prev')}</span> : t('ui.pagination.prev')}
      </button>
      {list[0] > 1 && <span className="px-1 text-slate-400">…</span>}
      {list.map((p) => (
        <button key={p} onClick={() => go(p)} disabled={busy && pending !== p} className={`${pageClass(p === (pending ?? page))} disabled:opacity-60`}
          style={{ ...sizedControlStyle(size), minWidth: controlHeight[size] }} aria-busy={busy && clicked === 'page' && pending === p} aria-current={p === page ? 'page' : undefined}>
          {busy && clicked === 'page' && pending === p ? spinner : p}
        </button>
      ))}
      {list[list.length - 1] < pages && <span className="px-1 text-slate-400">…</span>}
      <button disabled={page >= pages || busy} onClick={() => go(page + 1, 'next')} className={navClass} style={sizedControlStyle(size)} aria-busy={busy && clicked === 'next'}>
        {busy && clicked === 'next' ? <span className="inline-flex items-center gap-1.5">{t('ui.pagination.next')}{spinner}</span> : t('ui.pagination.next')}
      </button>
    </div>
  )
}
