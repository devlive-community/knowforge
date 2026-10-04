import { useState } from 'react'
import { useTranslation } from '@/lib/i18n'
import type { TemplateSummary } from '@/lib/templates'
import TemplatePicker from './TemplatePicker'

interface Props {
  value: TemplateSummary | null
  onChange: (template: TemplateSummary | null) => void
}

// BookTemplateChoice 新建书籍时选择「空白书籍」或某个书籍模板（创建后按模板生成章节）。
export default function BookTemplateChoice({ value, onChange }: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const option = (active: boolean) =>
    `flex min-w-0 flex-1 items-center gap-3 rounded-xl border px-4 py-3 text-left transition-colors ${active ? 'border-primary-300 bg-primary-50/60 ring-1 ring-primary-200' : 'border-slate-200 bg-white hover:border-slate-300'}`

  return (
    <div className="mb-6 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm" data-testid="book-template-choice">
      <div className="mb-3 flex items-center gap-2">
        <i className="fa-regular fa-clone text-slate-400" aria-hidden="true" />
        <h2 className="text-sm font-semibold text-slate-900">{t('templates.create.heading')}</h2>
        <span className="text-xs text-slate-400">{t('templates.create.hint')}</span>
      </div>
      <div className="flex flex-col gap-3 sm:flex-row">
        <button type="button" className={option(!value)} onClick={() => onChange(null)}>
          <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-slate-100 text-slate-500"><i className="fa-regular fa-file" aria-hidden="true" /></span>
          <span className="min-w-0">
            <span className="block text-sm font-medium text-slate-800">{t('templates.create.blank')}</span>
            <span className="block text-xs text-slate-500">{t('templates.create.blankHint')}</span>
          </span>
        </button>
        <button type="button" className={option(!!value)} onClick={() => setOpen(true)} data-testid="book-template-open">
          <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary-100 text-primary-600"><i className="fa-regular fa-clone" aria-hidden="true" /></span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm font-medium text-slate-800">{value ? value.title : t('templates.create.pick')}</span>
            <span className="block truncate text-xs text-slate-500">
              {value ? t('templates.create.picked', { n: value.chapter_count }) : t('templates.create.pickHint')}
            </span>
          </span>
          {value && <span className="shrink-0 text-xs font-medium text-primary-600">{t('templates.create.change')}</span>}
        </button>
      </div>
      <TemplatePicker open={open} kind="book" onClose={() => setOpen(false)} useLabel={t('templates.create.useThis')}
        onUse={(tpl) => { onChange(tpl); setOpen(false) }} />
    </div>
  )
}
