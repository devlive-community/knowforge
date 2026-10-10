import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from '@/lib/i18n'
import { ICON_COLORS, ICON_GROUPS, formatIconValue, isValidColor, loadIconCatalog, parseIcon, searchIcons, type IconCatalog, type IconGroup } from '@/lib/icons'
import { IMAGE_RULE, SVG_RULE, acceptOf, formatSize } from '@/lib/upload'
import { Button, Input, Loading, Modal, SegmentedTabs } from '@/components/ui'
import ResourceIcon from './ResourceIcon'
import UploadDropzone from './upload/UploadDropzone'
import { useUpload } from './upload/useUpload'

export interface IconValue { icon_type: string; icon_value: string }

type PanelTab = 'library' | 'image' | 'svg'
const PAGE = 210 // 图标库每次渲染的数量（「显示更多」继续）

// IconPicker 通用图标选择：左侧预览 + 右侧「选择图标」按钮，点击打开选择面板——
//   图标库：Font Awesome 图标，支持搜索、分组筛选与颜色；
//   上传图片：PNG / JPG / WebP；
//   上传 SVG：安全检查、预览，可设置单色颜色。
// 受控组件：value / onChange 为 { icon_type, icon_value }（颜色编码在 icon_value 中，见 lib/icons.ts）。
// 可放在 <form> 中：按钮都是 type="button"，面板经 portal 渲染，不会触发外层表单提交。
export default function IconPicker({ value, onChange, fallback }: {
  value: IconValue
  onChange: (next: IconValue) => void
  fallback?: string
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const current = parseIcon(value.icon_type, value.icon_value)
  const hasIcon = !!(current.type && current.value)
  const label = !hasIcon ? t('iconPicker.none')
    : current.type === 'fa' ? current.value
      : current.type === 'svg' ? t('iconPicker.svg') : t('iconPicker.image')

  return (
    <div className="flex items-center gap-3" data-testid="icon-picker">
      <button type="button" onClick={() => setOpen(true)} aria-label={t('iconPicker.choose')}
        className="rounded-xl ring-offset-2 transition hover:ring-2 hover:ring-primary-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-400">
        <ResourceIcon iconType={value.icon_type} iconValue={value.icon_value} fallback={fallback}
          className={`flex h-12 w-12 shrink-0 items-center justify-center overflow-hidden rounded-xl border text-lg ${hasIcon ? 'border-primary-100 bg-primary-50 text-primary-600' : 'border-dashed border-slate-300 bg-slate-50 text-slate-300'}`} />
      </button>
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm text-slate-700">{label}</div>
        <div className="mt-1 flex flex-wrap gap-2">
          <Button type="button" size="sm" variant="outline" onClick={() => setOpen(true)} data-testid="icon-picker-open">
            <i className="fa-solid fa-icons" aria-hidden="true" /> {hasIcon ? t('iconPicker.change') : t('iconPicker.choose')}
          </Button>
          {hasIcon && (
            <Button type="button" size="sm" variant="ghost" onClick={() => onChange({ icon_type: '', icon_value: '' })}>{t('iconPicker.clear')}</Button>
          )}
        </div>
      </div>
      {/* 面板挂到 body：选择器常放在表单里，避免面板中的按钮与回车触发外层表单提交 */}
      {open && createPortal(<IconPanel value={value} fallback={fallback} onClose={() => setOpen(false)} onPick={(next) => { onChange(next); setOpen(false) }} />, document.body)}
    </div>
  )
}

function IconPanel({ value, fallback, onClose, onPick }: {
  value: IconValue
  fallback?: string
  onClose: () => void
  onPick: (next: IconValue) => void
}) {
  const { t } = useTranslation()
  const initial = parseIcon(value.icon_type, value.icon_value)
  const [tab, setTab] = useState<PanelTab>(initial.type === 'image' ? 'image' : initial.type === 'svg' ? 'svg' : 'library')
  // 暂存的选择：点「确定」才生效
  const [draft, setDraft] = useState(initial)
  const [catalog, setCatalog] = useState<IconCatalog | null>(null)
  const [group, setGroup] = useState<IconGroup>('common')
  const [query, setQuery] = useState('')
  const [limit, setLimit] = useState(PAGE)
  const [customColor, setCustomColor] = useState(initial.color)

  useEffect(() => { void loadIconCatalog().then(setCatalog) }, [])
  useEffect(() => { setLimit(PAGE) }, [group, query])

  const results = useMemo(() => (catalog ? searchIcons(catalog, group, query) : []), [catalog, group, query])
  const image = useUpload({ rule: IMAGE_RULE, onUploaded: (url) => setDraft({ type: 'image', value: url, color: '' }) })
  const svg = useUpload({ rule: SVG_RULE, svg: true, onUploaded: (url) => setDraft((d) => ({ type: 'svg', value: url, color: d.type === 'svg' ? d.color : '' })) })

  const canColor = draft.type === 'fa' || draft.type === 'svg'
  const setColor = (color: string) => { setDraft((d) => ({ ...d, color })); setCustomColor(color) }
  const ready = !!(draft.type && draft.value)

  const colorRow = (
    <div className="space-y-2" data-testid="icon-colors">
      <div className="text-xs font-medium text-slate-500">{draft.type === 'svg' ? t('iconPicker.svgColor') : t('iconPicker.color')}</div>
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" onClick={() => setColor('')} aria-label={t('iconPicker.colorDefault')} aria-pressed={!draft.color}
          className={`flex h-7 items-center rounded-full border px-2.5 text-xs ${!draft.color ? 'border-primary-400 bg-primary-50 text-primary-700' : 'border-slate-200 text-slate-500 hover:border-slate-300'}`}>
          {draft.type === 'svg' ? t('iconPicker.colorOriginal') : t('iconPicker.colorDefault')}
        </button>
        {ICON_COLORS.map((c) => (
          <button key={c} type="button" onClick={() => setColor(c)} aria-label={c} aria-pressed={draft.color === c}
            className={`h-7 w-7 rounded-full ring-offset-2 transition ${draft.color === c ? 'ring-2 ring-slate-500' : 'hover:ring-2 hover:ring-slate-200'}`} style={{ backgroundColor: c }} />
        ))}
        <div className="w-28"><Input size="sm" className="font-mono" value={customColor} maxLength={7} placeholder="#3b82f6" aria-label={t('iconPicker.customColor')}
          onChange={(e) => { const v = e.target.value.trim(); setCustomColor(v); if (isValidColor(v)) setDraft((d) => ({ ...d, color: v.toLowerCase() })) }} /></div>
      </div>
    </div>
  )

  return (
    <Modal open elevated className="max-w-4xl" onClose={onClose} title={t('iconPicker.title')}
      footer={
        <div className="flex w-full flex-wrap items-center gap-2">
          <Button type="button" variant="ghost" className="mr-auto" onClick={() => onPick({ icon_type: '', icon_value: '' })}>
            <i className="fa-solid fa-ban" aria-hidden="true" /> {t('iconPicker.none')}
          </Button>
          <Button type="button" variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button>
          <Button type="button" disabled={!ready} onClick={() => onPick({ icon_type: draft.type, icon_value: formatIconValue(draft.value, canColor ? draft.color : '') })} data-testid="icon-picker-confirm">
            {t('iconPicker.confirm')}
          </Button>
        </div>
      }>
      <div className="space-y-5">
        <div className="flex flex-wrap items-center gap-4">
          <ResourceIcon iconType={draft.type} iconValue={formatIconValue(draft.value, canColor ? draft.color : '')} fallback={fallback}
            className="flex h-14 w-14 shrink-0 items-center justify-center overflow-hidden rounded-2xl border border-primary-100 bg-primary-50 text-2xl text-primary-600" />
          <div className="min-w-0 flex-1 text-sm">
            <div className="font-medium text-slate-800">{ready ? (draft.type === 'fa' ? draft.value : draft.type === 'svg' ? t('iconPicker.svg') : t('iconPicker.image')) : t('iconPicker.none')}</div>
            <div className="text-xs text-slate-400">{t('iconPicker.previewHint')}</div>
          </div>
          <SegmentedTabs size="sm" value={tab} onChange={(v) => setTab(v as PanelTab)} ariaLabel={t('iconPicker.title')} items={[
            { value: 'library', label: t('iconPicker.tab.library') },
            { value: 'image', label: t('iconPicker.tab.image') },
            { value: 'svg', label: t('iconPicker.tab.svg') },
          ]} />
        </div>

        {tab === 'library' && (
          <div className="space-y-4">
            <div className="relative">
              <i className="fa-solid fa-magnifying-glass pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-sm text-slate-400" aria-hidden="true" />
              <Input className="pl-9" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('iconPicker.search')} data-testid="icon-search" />
            </div>
            {!query.trim() && (
              <div className="flex flex-wrap gap-1.5">
                {ICON_GROUPS.map((g) => (
                  <button key={g} type="button" onClick={() => setGroup(g)}
                    className={`rounded-lg px-3 py-1.5 text-sm transition-colors ${group === g ? 'bg-primary-50 font-medium text-primary-700' : 'text-slate-500 hover:bg-slate-50 hover:text-slate-800'}`}>
                    {t(`iconPicker.group.${g}`)}
                  </button>
                ))}
              </div>
            )}
            {!catalog ? <Loading className="py-12" /> : results.length === 0 ? (
              <p className="py-10 text-center text-sm text-slate-400">{t('iconPicker.noResults')}</p>
            ) : (
              <>
                <div className="grid max-h-[22rem] grid-cols-[repeat(auto-fill,minmax(3.25rem,1fr))] gap-2 overflow-y-auto pr-1" data-testid="icon-grid">
                  {results.slice(0, limit).map((name) => {
                    const cls = `fa-${name}`
                    const active = draft.type === 'fa' && draft.value === cls
                    return (
                      <button key={name} type="button" aria-label={name} aria-pressed={active} onClick={() => setDraft((d) => ({ type: 'fa', value: cls, color: d.type === 'fa' ? d.color : customColor && isValidColor(customColor) ? customColor : '' }))}
                        className={`flex aspect-square items-center justify-center rounded-xl border text-lg transition-colors ${active ? 'border-primary-400 bg-primary-50 text-primary-600 ring-2 ring-primary-100' : 'border-slate-200 text-slate-600 hover:border-primary-200 hover:bg-slate-50'}`}>
                        <i className={`fa-solid ${cls}`} style={active && draft.color ? { color: draft.color } : undefined} aria-hidden="true" />
                      </button>
                    )
                  })}
                </div>
                <div className="flex items-center justify-between text-xs text-slate-400">
                  <span>{t('iconPicker.count', { n: results.length })}</span>
                  {results.length > limit && <Button type="button" size="sm" variant="ghost" onClick={() => setLimit((l) => l + PAGE)}>{t('iconPicker.more')}</Button>}
                </div>
              </>
            )}
            {draft.type === 'fa' && colorRow}
          </div>
        )}

        {tab === 'image' && (
          <div className="space-y-4">
            <UploadDropzone state={image.state} accept={acceptOf(IMAGE_RULE)} onFile={(f) => void image.upload(f)} onDismissError={image.reset} testId="icon-image-drop"
              title={t('upload.clickOrDrag')} hint={t('iconPicker.imageHint', { max: formatSize(IMAGE_RULE.maxBytes) })}
              preview={draft.type === 'image' ? <ResourceIcon iconType="image" iconValue={draft.value} className="flex h-16 w-16 items-center justify-center overflow-hidden rounded-xl" /> : undefined} />
            <p className="text-xs text-slate-400">{t('iconPicker.imageTip')}</p>
          </div>
        )}

        {tab === 'svg' && (
          <div className="space-y-4">
            <UploadDropzone state={svg.state} accept={acceptOf(SVG_RULE)} onFile={(f) => void svg.upload(f)} onDismissError={svg.reset} testId="icon-svg-drop"
              title={t('upload.clickOrDrag')} hint={t('iconPicker.svgHint', { max: formatSize(SVG_RULE.maxBytes) })}
              preview={draft.type === 'svg' ? <ResourceIcon iconType="svg" iconValue={formatIconValue(draft.value, draft.color)} className="flex h-16 w-16 items-center justify-center overflow-hidden rounded-xl" /> : undefined} />
            <p className="flex items-start gap-1.5 text-xs text-slate-400"><i className="fa-solid fa-shield-halved mt-0.5" aria-hidden="true" />{t('iconPicker.svgSafety')}</p>
            {draft.type === 'svg' && colorRow}
          </div>
        )}
      </div>
    </Modal>
  )
}
