import { ReactNode, useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import { api } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { renderMarkdown } from '@/lib/markdown'
import type { TemplateDetail, TemplateKind, TemplateLists, TemplateNode, TemplateSummary } from '@/lib/templates'
import { Badge, Button, EmptyState, Input, Loading, Modal } from '@/components/ui'

interface Props {
  open: boolean
  kind: TemplateKind
  onClose: () => void
  /** 使用选中的模板；返回的 Promise 结束前「使用」按钮显示加载中 */
  onUse: (template: TemplateSummary) => Promise<void> | void
  useLabel?: string
  /** 底部左侧的额外操作（如写作台的「将当前章节存为模板」） */
  footerExtra?: ReactNode
  /** 变化时重新加载列表（如刚保存了新模板） */
  reloadKey?: number
}

// TemplatePicker 选择模板：左侧按「站点模板 / 我的模板」分组列出（可搜索），右侧预览选中模板——
// 章节模板渲染正文（变量保持原样，使用时才替换），书籍模板显示章节目录。
export default function TemplatePicker({ open, kind, onClose, onUse, useLabel, footerExtra, reloadKey }: Props) {
  const { t } = useTranslation()
  const [lists, setLists] = useState<TemplateLists | null>(null)
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<number | null>(null)
  const [details, setDetails] = useState<Record<number, TemplateDetail>>({})
  const [using, setUsing] = useState(false)

  useEffect(() => {
    if (!open) return
    setError('')
    api<TemplateLists>('/templates', { params: { kind } })
      .then((d) => {
        setLists(d)
        setSelected((s) => (s && [...d.official, ...d.mine].some((x) => x.id === s) ? s : d.official[0]?.id ?? d.mine[0]?.id ?? null))
      })
      .catch((e) => setError((e as Error).message))
  }, [open, kind, reloadKey])

  useEffect(() => {
    if (!open || selected === null || details[selected]) return
    api<TemplateDetail>(`/templates/${selected}`).then((d) => setDetails((m) => ({ ...m, [d.id]: d }))).catch(() => {})
  }, [open, selected, details])

  const filter = (list: TemplateSummary[]) => {
    const q = query.trim().toLowerCase()
    return q ? list.filter((x) => x.title.toLowerCase().includes(q) || x.description.toLowerCase().includes(q)) : list
  }
  const official = useMemo(() => filter(lists?.official || []), [lists, query]) // eslint-disable-line react-hooks/exhaustive-deps
  const mine = useMemo(() => filter(lists?.mine || []), [lists, query]) // eslint-disable-line react-hooks/exhaustive-deps
  const current = [...(lists?.official || []), ...(lists?.mine || [])].find((x) => x.id === selected) || null
  const detail = selected !== null ? details[selected] : undefined

  async function use() {
    if (!current) return
    setUsing(true)
    try {
      await onUse(current)
    } finally {
      setUsing(false)
    }
  }

  const group = (label: string, items: TemplateSummary[], empty: ReactNode) => (
    <div>
      <div className="px-1 pb-1.5 text-xs font-medium text-slate-400">{label}</div>
      {items.length === 0 ? <div className="px-1 pb-2 text-xs text-slate-400">{empty}</div> : (
        <ul className="space-y-1">
          {items.map((x) => (
            <li key={x.id}>
              <button type="button" onClick={() => setSelected(x.id)} data-testid="template-option"
                className={`w-full rounded-lg px-3 py-2 text-left transition-colors ${x.id === selected ? 'bg-primary-50 ring-1 ring-primary-200' : 'hover:bg-slate-50'}`}>
                <div className={`truncate text-sm font-medium ${x.id === selected ? 'text-primary-700' : 'text-slate-800'}`}>{x.title}</div>
                {x.description && <div className="mt-0.5 line-clamp-2 text-xs text-slate-500">{x.description}</div>}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )

  return (
    <Modal open={open} onClose={onClose} title={t(kind === 'book' ? 'templates.picker.bookTitle' : 'templates.picker.chapterTitle')} className="max-w-4xl"
      footer={(
        <div className="flex w-full flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-3">
            {footerExtra}
            <Link href={`/user/templates${kind === 'book' ? '?kind=book' : ''}`} className="text-xs text-slate-500 hover:text-primary-600">{t('templates.picker.manage')}</Link>
          </div>
          <div className="flex gap-2">
            <Button variant="outline" onClick={onClose}>{t('templates.picker.cancel')}</Button>
            <Button onClick={() => void use()} loading={using} disabled={!current || using} data-testid="template-use">{useLabel || t('templates.picker.use')}</Button>
          </div>
        </div>
      )}>
      {error ? <p className="py-6 text-sm text-rose-600">{error}</p> : !lists ? <Loading className="py-16" /> : (
        <div className="flex flex-col gap-4 md:h-[60vh] md:flex-row" data-testid="template-picker">
          <div className="flex min-h-0 flex-col gap-3 md:w-64 md:shrink-0">
            <Input size="sm" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('templates.picker.search')} />
            <div className="max-h-64 min-h-0 space-y-4 overflow-y-auto md:max-h-none md:flex-1">
              {group(t('templates.picker.official'), official, t('templates.picker.noneOfficial'))}
              {group(t('templates.picker.mine'), mine, t(kind === 'book' ? 'templates.picker.noneMineBook' : 'templates.picker.noneMineChapter'))}
            </div>
          </div>
          <div className="min-h-0 min-w-0 flex-1 overflow-y-auto rounded-xl border border-slate-200">
            {!current ? (
              <div className="p-6"><EmptyState>{t('templates.picker.emptyAll')}</EmptyState></div>
            ) : (
              <div className="p-5">
                <div className="flex flex-wrap items-center gap-2">
                  <h3 className="text-base font-semibold text-slate-900">{current.title}</h3>
                  <Badge tone={current.official ? 'primary' : 'slate'}>{t(current.official ? 'templates.badge.official' : 'templates.badge.mine')}</Badge>
                  {kind === 'book' && <span className="text-xs text-slate-400">{t('templates.picker.chapterCount', { n: current.chapter_count })}</span>}
                </div>
                {current.description && <p className="mt-1 text-sm text-slate-500">{current.description}</p>}
                <div className="mt-4 border-t border-slate-100 pt-4">
                  {!detail ? <Loading className="py-10" /> : kind === 'book'
                    ? <Outline nodes={detail.chapters || []} />
                    : <div className="markdown-body text-sm" data-testid="template-preview" dangerouslySetInnerHTML={{ __html: renderMarkdown(detail.content || '') }} />}
                </div>
                <p className="mt-4 text-xs text-slate-400">{t('templates.picker.variablesHint')}</p>
              </div>
            )}
          </div>
        </div>
      )}
    </Modal>
  )
}

// Outline 书籍模板的章节目录。
export function Outline({ nodes, depth = 0 }: { nodes: TemplateNode[]; depth?: number }) {
  return (
    <ul className={depth ? 'ml-4 border-l border-slate-100 pl-3' : 'space-y-0.5'}>
      {nodes.map((n, i) => (
        <li key={`${depth}-${i}`}>
          <div className="flex items-center gap-2 py-1 text-sm text-slate-700">
            <i className={`fa-regular ${n.children?.length ? 'fa-folder' : 'fa-file-lines'} w-4 text-slate-400`} aria-hidden="true" />
            <span className="truncate">{n.title}</span>
          </div>
          {!!n.children?.length && <Outline nodes={n.children} depth={depth + 1} />}
        </li>
      ))}
    </ul>
  )
}
