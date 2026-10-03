import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import Link from 'next/link'
import { useTranslation } from '@/lib/i18n'
import type { LinkGraph, LinkGraphNode } from '@/lib/backlinks'
import { forceLayout, graphBounds, type LayoutPoint } from '@/lib/graph-layout'
import { Badge, Button, Input, Switch, Tooltip } from '@/components/ui'

interface Props {
  graph: LinkGraph
  bookSlug: string
}

const MIN_SCALE = 0.15
const MAX_SCALE = 4

// LinkGraphView 章节关系图：章节为节点（被引用越多越大，草稿为灰色），[[双向链接]] 为带箭头的边；
// 可拖动节点、拖动画布、滚轮缩放、搜索章节；点击章节高亮其关联并在右侧列出链接。可选显示没有链接的章节与目录结构（虚线）。
export default function LinkGraphView({ graph, bookSlug }: Props) {
  const { t } = useTranslation()
  const [showIsolated, setShowIsolated] = useState(false)
  const [showTree, setShowTree] = useState(false)
  const [selected, setSelected] = useState<number | null>(null)
  const [hovered, setHovered] = useState<number | null>(null)
  const [query, setQuery] = useState('')
  const wrapRef = useRef<HTMLDivElement>(null)
  const [size, setSize] = useState({ w: 800, h: 520 })
  const [view, setView] = useState({ s: 1, x: 0, y: 0 })
  const [positions, setPositions] = useState<Map<number, LayoutPoint>>(new Map())
  const drag = useRef<{ kind: 'pan' | 'node'; id?: number; sx: number; sy: number; ox: number; oy: number; moved: boolean } | null>(null)

  const byId = useMemo(() => new Map(graph.nodes.map((n) => [n.id, n])), [graph])
  const nodes = useMemo(() => graph.nodes.filter((n) => showIsolated || n.outgoing.length > 0 || n.incoming.length > 0), [graph, showIsolated])
  const visible = useMemo(() => new Set(nodes.map((n) => n.id)), [nodes])
  const links = useMemo(() => {
    const out: [number, number][] = []
    for (const n of nodes) for (const to of n.outgoing) if (visible.has(to) && to !== n.id) out.push([n.id, to])
    return out
  }, [nodes, visible])
  const treeLinks = useMemo(() => (showTree ? nodes.filter((n) => n.parent_id && visible.has(n.parent_id)).map((n) => [n.parent_id as number, n.id] as [number, number]) : []), [nodes, visible, showTree])

  // 布局：章节或可见范围变化时重新计算
  const layout = useMemo(() => forceLayout(nodes.map((n) => n.id), [...links, ...treeLinks]), [nodes, links, treeLinks])
  useEffect(() => { setPositions(new Map(layout)) }, [layout])

  // 容器尺寸
  useLayoutEffect(() => {
    const el = wrapRef.current
    if (!el) return
    const update = () => setSize({ w: el.clientWidth, h: el.clientHeight })
    update()
    const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(update) : null
    ro?.observe(el)
    return () => ro?.disconnect()
  }, [])

  const fitView = useCallback(() => {
    const b = graphBounds([...layout.values()])
    const s = Math.min(MAX_SCALE, Math.max(MIN_SCALE, Math.min(size.w / b.w, size.h / b.h, 1.4)))
    setView({ s, x: size.w / 2 - (b.x + b.w / 2) * s, y: size.h / 2 - (b.y + b.h / 2) * s })
  }, [layout, size])
  useEffect(() => { fitView() }, [fitView])

  const zoomAt = (factor: number, cx = size.w / 2, cy = size.h / 2) => {
    setView((v) => {
      const s = Math.min(MAX_SCALE, Math.max(MIN_SCALE, v.s * factor))
      return { s, x: cx - ((cx - v.x) / v.s) * s, y: cy - ((cy - v.y) / v.s) * s }
    })
  }
  const centerOn = (id: number) => {
    const p = positions.get(id)
    if (!p) return
    setView((v) => ({ ...v, x: size.w / 2 - p.x * v.s, y: size.h / 2 - p.y * v.s }))
  }

  // 选中章节的关联
  const focus = selected ?? hovered
  const related = useMemo(() => {
    if (focus === null) return null
    const n = byId.get(focus)
    return new Set([focus, ...(n?.outgoing || []), ...(n?.incoming || [])])
  }, [focus, byId])

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase()
    return q ? nodes.filter((n) => n.title.toLowerCase().includes(q) || n.slug.toLowerCase().includes(q)) : []
  }, [query, nodes])

  function onPointerDown(e: React.PointerEvent, id?: number) {
    e.stopPropagation()
    try { (e.currentTarget as Element).setPointerCapture(e.pointerId) } catch { /* 合成事件没有活动指针，忽略 */ }
    if (id !== undefined) {
      const p = positions.get(id)
      drag.current = { kind: 'node', id, sx: e.clientX, sy: e.clientY, ox: p?.x || 0, oy: p?.y || 0, moved: false }
    } else {
      drag.current = { kind: 'pan', sx: e.clientX, sy: e.clientY, ox: view.x, oy: view.y, moved: false }
    }
  }
  function onPointerMove(e: React.PointerEvent) {
    const d = drag.current
    if (!d) return
    const dx = e.clientX - d.sx
    const dy = e.clientY - d.sy
    if (Math.abs(dx) + Math.abs(dy) > 3) d.moved = true
    if (d.kind === 'pan') setView((v) => ({ ...v, x: d.ox + dx, y: d.oy + dy }))
    else if (d.id !== undefined) {
      const id = d.id
      setPositions((m) => new Map(m).set(id, { x: d.ox + dx / view.s, y: d.oy + dy / view.s }))
    }
  }
  function onPointerUp() {
    const d = drag.current
    drag.current = null
    if (!d || d.moved) return
    if (d.kind === 'node' && d.id !== undefined) setSelected((s) => (s === d.id ? null : d.id!))
    else setSelected(null)
  }

  const radius = (n: LinkGraphNode) => 7 + Math.min(11, n.incoming.length * 2)
  const sel = selected !== null ? byId.get(selected) : undefined
  const writer = (n: LinkGraphNode) => `/book/writer/${encodeURIComponent(bookSlug)}/${encodeURIComponent(n.slug)}`
  const reader = (n: LinkGraphNode) => `/book/reader/${encodeURIComponent(bookSlug)}/${encodeURIComponent(n.slug)}`
  const showLabels = view.s >= 0.6

  // 边的端点：从节点边缘出发，箭头停在目标节点边缘
  const segment = (a: number, b: number) => {
    const p = positions.get(a)
    const q = positions.get(b)
    const na = byId.get(a)
    const nb = byId.get(b)
    if (!p || !q || !na || !nb) return null
    const dx = q.x - p.x
    const dy = q.y - p.y
    const d = Math.max(0.01, Math.hypot(dx, dy))
    const ra = radius(na) + 1
    const rb = radius(nb) + 4
    return { x1: p.x + (dx / d) * ra, y1: p.y + (dy / d) * ra, x2: q.x - (dx / d) * rb, y2: q.y - (dy / d) * rb }
  }

  return (
    <div className="flex flex-col gap-3 lg:flex-row" data-testid="link-graph">
      <div className="min-w-0 flex-1">
        <div className="mb-2 flex items-center gap-3">
          <div className="relative min-w-0 flex-1 sm:max-w-xs">
            <Input size="sm" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('backlinks.graph.search')}
              onKeyDown={(e) => { if (e.key === 'Enter' && matches[0]) { setSelected(matches[0].id); centerOn(matches[0].id) } }} />
            {matches.length > 0 && (
              <ul className="absolute left-0 right-0 top-full z-20 mt-1 max-h-60 overflow-y-auto rounded-lg border border-slate-200 bg-white py-1 shadow-lg">
                {matches.slice(0, 12).map((n) => (
                  <li key={n.id}>
                    <button type="button" className="block w-full truncate px-3 py-1.5 text-left text-sm text-slate-700 hover:bg-slate-50"
                      onClick={() => { setSelected(n.id); centerOn(n.id); setQuery('') }}>{n.title}</button>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div className="ml-auto flex shrink-0 items-center gap-1">
            <Tooltip content={t('backlinks.graph.zoomOut')}><Button size="sm" variant="ghost" aria-label={t('backlinks.graph.zoomOut')} onClick={() => zoomAt(0.8)}><i className="fa-solid fa-minus" aria-hidden="true" /></Button></Tooltip>
            <span className="w-12 text-center text-xs tabular-nums text-slate-500">{Math.round(view.s * 100)}%</span>
            <Tooltip content={t('backlinks.graph.zoomIn')}><Button size="sm" variant="ghost" aria-label={t('backlinks.graph.zoomIn')} onClick={() => zoomAt(1.25)}><i className="fa-solid fa-plus" aria-hidden="true" /></Button></Tooltip>
            <Tooltip content={t('backlinks.graph.fit')}><Button size="sm" variant="ghost" aria-label={t('backlinks.graph.fit')} onClick={fitView}><i className="fa-solid fa-expand" aria-hidden="true" /></Button></Tooltip>
          </div>
        </div>
        <div className="mb-3 flex flex-wrap items-center gap-x-5 gap-y-2">
          <label className="flex items-center gap-2 text-xs text-slate-600">
            <Switch checked={showIsolated} onChange={setShowIsolated} ariaLabel={t('backlinks.graph.showIsolated')} />{t('backlinks.graph.showIsolated')}
          </label>
          <label className="flex items-center gap-2 text-xs text-slate-600">
            <Switch checked={showTree} onChange={setShowTree} ariaLabel={t('backlinks.graph.showTree')} />{t('backlinks.graph.showTree')}
          </label>
        </div>
        <div ref={wrapRef} className="relative h-[520px] overflow-hidden rounded-xl border border-slate-200 bg-slate-50/60">
          {nodes.length === 0 ? (
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-1 text-sm text-slate-400">
              <i className="fa-solid fa-diagram-project text-2xl" aria-hidden="true" />
              {t('backlinks.graph.empty')}
            </div>
          ) : (
            <svg width={size.w} height={size.h} className="block cursor-grab touch-none select-none active:cursor-grabbing"
              onPointerDown={(e) => onPointerDown(e)} onPointerMove={onPointerMove} onPointerUp={onPointerUp}
              onWheel={(e) => { const r = (e.currentTarget as SVGSVGElement).getBoundingClientRect(); zoomAt(e.deltaY < 0 ? 1.1 : 1 / 1.1, e.clientX - r.left, e.clientY - r.top) }}>
              <defs>
                <marker id="kf-link-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                  <path d="M 0 0 L 10 5 L 0 10 z" className="fill-slate-400" />
                </marker>
                <marker id="kf-link-arrow-active" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                  <path d="M 0 0 L 10 5 L 0 10 z" className="fill-primary-500" />
                </marker>
              </defs>
              <g transform={`translate(${view.x},${view.y}) scale(${view.s})`}>
                {treeLinks.map(([a, b]) => {
                  const s = segment(a, b)
                  return s && <line key={`t${a}-${b}`} {...s} className="stroke-slate-300" strokeWidth={1.2 / view.s} strokeDasharray={`${4 / view.s} ${4 / view.s}`} opacity={related && !(related.has(a) && related.has(b)) ? 0.15 : 1} />
                })}
                {links.map(([a, b]) => {
                  const s = segment(a, b)
                  const active = focus !== null && (a === focus || b === focus)
                  return s && (
                    <line key={`${a}-${b}`} {...s} className={active ? 'stroke-primary-500' : 'stroke-slate-400'} strokeWidth={(active ? 2 : 1.3) / view.s}
                      markerEnd={`url(#${active ? 'kf-link-arrow-active' : 'kf-link-arrow'})`} opacity={related && !active ? 0.12 : 1} />
                  )
                })}
                {nodes.map((n) => {
                  const p = positions.get(n.id)
                  if (!p) return null
                  const r = radius(n)
                  const dim = related && !related.has(n.id)
                  const isSel = n.id === selected
                  return (
                    <g key={n.id} transform={`translate(${p.x},${p.y})`} opacity={dim ? 0.2 : 1} className="cursor-pointer" data-testid="graph-node"
                      onPointerDown={(e) => onPointerDown(e, n.id)} onPointerEnter={() => setHovered(n.id)} onPointerLeave={() => setHovered(null)}>
                      <circle r={r} className={n.status === 'published' ? 'fill-primary-500' : 'fill-slate-400'} stroke="white" strokeWidth={isSel ? 3 : 1.5} />
                      {isSel && <circle r={r + 5} fill="none" className="stroke-primary-400" strokeWidth={2 / view.s} />}
                      {(showLabels || isSel || n.id === hovered) && (
                        <text y={r + 13} textAnchor="middle" className="fill-slate-700" style={{ fontSize: 12 / Math.max(view.s, 0.6), paintOrder: 'stroke' }} stroke="white" strokeWidth={3 / Math.max(view.s, 0.6)}>
                          {n.title.length > 16 ? n.title.slice(0, 15) + '…' : n.title}
                        </text>
                      )}
                    </g>
                  )
                })}
              </g>
            </svg>
          )}
          <div className="pointer-events-none absolute bottom-2 left-3 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-slate-500">
            <span className="flex items-center gap-1"><span className="h-2.5 w-2.5 rounded-full bg-primary-500" />{t('backlinks.graph.published')}</span>
            <span className="flex items-center gap-1"><span className="h-2.5 w-2.5 rounded-full bg-slate-400" />{t('backlinks.graph.draft')}</span>
            <span className="flex items-center gap-1"><i className="fa-solid fa-arrow-right-long text-slate-400" aria-hidden="true" />{t('backlinks.graph.linkDirection')}</span>
            {showTree && <span className="flex items-center gap-1"><span className="w-4 border-t border-dashed border-slate-400" />{t('backlinks.graph.treeLine')}</span>}
          </div>
        </div>
        <p className="mt-2 text-xs text-slate-400">{t('backlinks.graph.hint')}</p>
      </div>

      <aside className="w-full shrink-0 rounded-xl border border-slate-200 p-4 lg:w-72" data-testid="graph-detail">
        {!sel ? (
          <p className="text-sm text-slate-400">{t('backlinks.graph.pickHint')}</p>
        ) : (
          <div className="space-y-3 text-sm">
            <div>
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="font-semibold text-slate-900 [overflow-wrap:anywhere]">{sel.title}</h3>
                {sel.status !== 'published' && <Badge tone="slate">{t('backlinks.settings.draft')}</Badge>}
              </div>
              <div className="mt-2 flex gap-2">
                <Link href={writer(sel)} className="text-xs font-medium text-primary-600 hover:underline">{t('backlinks.graph.edit')}</Link>
                {sel.status === 'published' && <Link href={reader(sel)} className="text-xs font-medium text-primary-600 hover:underline">{t('backlinks.graph.read')}</Link>}
              </div>
            </div>
            {([['linksTo', sel.outgoing], ['linkedFrom', sel.incoming]] as const).map(([key, ids]) => (
              <div key={key}>
                <div className="text-xs font-medium text-slate-500">{t(`backlinks.settings.${key}`)} · {ids.length}</div>
                {ids.length === 0 ? <p className="mt-1 text-xs text-slate-400">{t('backlinks.graph.none')}</p> : (
                  <ul className="mt-1 space-y-0.5">
                    {ids.map((id) => {
                      const n = byId.get(id)
                      return n && (
                        <li key={id}>
                          <button type="button" className="w-full truncate rounded px-1.5 py-1 text-left text-sm text-slate-700 hover:bg-slate-50"
                            onClick={() => { setSelected(id); if (visible.has(id)) centerOn(id) }}>{n.title}</button>
                        </li>
                      )
                    })}
                  </ul>
                )}
              </div>
            ))}
            {sel.external > 0 && <p className="text-xs text-slate-400">{t('backlinks.settings.external', { n: sel.external })}</p>}
          </div>
        )}
      </aside>
    </div>
  )
}
