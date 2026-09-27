import { useEffect, useState } from 'react'
import type { InferGetServerSidePropsType } from 'next'
import { api } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { useApp } from '@/lib/auth'
import { entitlementAllowed } from '@/lib/entitlements'
import { Button, Switch, Checkbox, Field, Select, Input, Loading, useFeedback } from '@/components/ui'
import BookSettingsLayout from '@/components/BookSettingsLayout'
import { getBookSettingsProps } from '@/lib/book-settings'

export const getServerSideProps = getBookSettingsProps

interface BookStyle {
  page_size: string
  include_cover: boolean
  include_toc: boolean
  font_size: number
  code_theme: string
  margin: string
  footer: string
}
const DEFAULT_STYLE: BookStyle = { page_size: 'A4', include_cover: true, include_toc: true, font_size: 15, code_theme: 'light', margin: 'normal', footer: '' }

// 书籍设置 · 导出设置：控制他人能否导出本书、是否共享作者导出样式（仅可管理者）
const ALL_FORMATS: { key: string; label?: string; labelKey?: string; hintKey: string }[] = [
  { key: 'pdf', label: 'PDF', hintKey: 'bookSettings.export.format.pdfHint' },
  { key: 'epub', labelKey: 'bookSettings.export.format.epubLabel', hintKey: 'bookSettings.export.format.epubHint' },
  { key: 'docx', label: 'Word (docx)', hintKey: 'bookSettings.export.format.docxHint' },
  { key: 'markdown', labelKey: 'bookSettings.export.format.mdLabel', hintKey: 'bookSettings.export.format.mdHint' },
]

export default function BookSettingsExport({ book }: InferGetServerSidePropsType<typeof getBookSettingsProps>) {
  const { site, user: me } = useApp()
  // 书籍页脚按书籍作者的「自定义导出页脚」权益生效（协作者编辑时以作者为准，这里只在作者本人编辑时提示）
  const footerBlocked = !!me && me.id === book.user_id && !entitlementAllowed(me, 'export.custom_footer', true)
  const { showToast } = useFeedback()
  const { t } = useTranslation()
  const [exportEnabled, setExportEnabled] = useState(book.export_enabled ?? true)
  const [guestExportEnabled, setGuestExportEnabled] = useState(book.guest_export_enabled ?? true)
  const [styleShared, setStyleShared] = useState(book.export_style_shared ?? false)
  // 空字符串表示“全部格式可用”；否则为逗号分隔的允许格式
  const initialFormats = (book.export_formats || '').split(',').map((s) => s.trim()).filter(Boolean)
  const [formats, setFormats] = useState<string[]>(initialFormats.length ? initialFormats : ALL_FORMATS.map((f) => f.key))
  const [style, setStyle] = useState<BookStyle>(DEFAULT_STYLE)
  const [styleLoaded, setStyleLoaded] = useState(false)
  const [pdfAvailable, setPdfAvailable] = useState(true)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    api<BookStyle>(`/books/${book.id}/export-style`).then((d) => setStyle({ ...DEFAULT_STYLE, ...d })).catch(() => {}).finally(() => setStyleLoaded(true))
    api<{ available: boolean }>('/export/pdf-available').then((r) => setPdfAvailable(r.available)).catch(() => {})
  }, [book.id])

  function toggleFormat(key: string) {
    setFormats((cur) => cur.includes(key) ? cur.filter((f) => f !== key) : [...cur, key])
  }

  async function save() {
    // 插件未安装时不允许开启 PDF 格式
    const effective = pdfAvailable ? formats : formats.filter((f) => f !== 'pdf')
    if (effective.length === 0) { showToast({ title: t('bookSettings.export.error.noFormat.title'), message: t('bookSettings.export.error.noFormat.message'), tone: 'error' }); return }
    setSaving(true)
    try {
      // 全选归一化为空串（表示全部），由后端统一处理
      const export_formats = effective.length === ALL_FORMATS.length ? '' : effective.join(',')
      await api(`/books/${book.id}`, { method: 'PUT', body: { export_enabled: exportEnabled, guest_export_enabled: guestExportEnabled, export_style_shared: styleShared, export_formats } })
      await api(`/books/${book.id}/export-style`, { method: 'PUT', body: style })
      showToast({ title: t('bookSettings.export.saved.title'), message: t('bookSettings.export.saved.message'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('bookSettings.export.error.save.title'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <BookSettingsLayout book={book} active="export">
      <div className="rounded-2xl border border-slate-200 bg-white shadow-sm">
        <div className="border-b border-slate-100 p-6">
          <h2 className="text-lg font-bold text-slate-900">{t('bookSettings.nav.export')}</h2>
          <p className="mt-1 text-sm text-slate-500">{t('bookSettings.export.desc')}</p>
        </div>
        <div className="space-y-4 p-6">
          <div className="rounded-xl border border-slate-200 p-4">
            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="text-sm font-medium text-slate-900">{t('bookSettings.export.allowExport')}</div>
                <p className="mt-1 text-xs leading-5 text-slate-500">{t('bookSettings.export.allowExportDesc')}</p>
              </div>
              <Switch checked={exportEnabled} onChange={setExportEnabled} ariaLabel={t('bookSettings.export.allowExport')} />
            </div>
            <div className={`mt-4 flex items-center justify-between gap-4 border-t border-slate-100 pt-4 ${exportEnabled ? '' : 'opacity-50'}`}>
              <div>
                <div className="text-sm font-medium text-slate-900">{t('bookSettings.export.allowGuest')}</div>
                <p className="mt-1 text-xs leading-5 text-slate-500">{t('bookSettings.export.allowGuestDesc')}</p>
              </div>
              <Switch checked={exportEnabled && guestExportEnabled} disabled={!exportEnabled} onChange={setGuestExportEnabled} ariaLabel={t('bookSettings.export.allowGuest')} />
            </div>
          </div>
          <div className="flex items-center justify-between gap-4 rounded-xl border border-slate-200 p-4">
            <div>
              <div className="text-sm font-medium text-slate-900">{t('bookSettings.export.shareStyle')}</div>
              <p className="mt-1 text-xs leading-5 text-slate-500">{t('bookSettings.export.shareStyleDesc')}</p>
            </div>
            <Switch checked={styleShared} onChange={setStyleShared} ariaLabel={t('bookSettings.export.shareStyle')} />
          </div>

          <div className="rounded-xl border border-slate-200 p-4">
            <div className="text-sm font-medium text-slate-900">{t('bookSettings.export.formats')}</div>
            <p className="mt-1 text-xs leading-5 text-slate-500">{t('bookSettings.export.formatsDesc')}</p>
            <div className="mt-3 space-y-2">
              {ALL_FORMATS.map((f) => {
                const disabled = f.key === 'pdf' && !pdfAvailable
                return (
                  <label key={f.key} className={`flex items-start gap-3 rounded-lg border border-slate-200 p-3 ${disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer hover:border-primary-300'}`}>
                    <Checkbox checked={formats.includes(f.key) && !disabled} disabled={disabled} onChange={() => toggleFormat(f.key)} ariaLabel={f.label || (f.labelKey ? t(f.labelKey) : f.key)} />
                    <span>
                      <span className="text-sm font-medium text-slate-800">{f.label || (f.labelKey ? t(f.labelKey) : '')}</span>
                      <span className="mt-0.5 block text-xs text-slate-500">{disabled ? t('bookSettings.export.pdfUnavailable') : t(f.hintKey)}</span>
                    </span>
                  </label>
                )
              })}
            </div>
          </div>

          <div className="rounded-xl border border-slate-200 p-4">
            <div className="text-sm font-medium text-slate-900">{t('bookSettings.export.styleTitle')}</div>
            <p className="mt-1 text-xs leading-5 text-slate-500">{t('bookSettings.export.styleDesc')}</p>
            {!styleLoaded ? (
              <Loading className="py-8" label={t('bookSettings.export.styleLoading')} />
            ) : (
              <div className="mt-3 space-y-4">
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field label={t('bookSettings.export.pageSize')}>
                    <Select value={style.page_size} onChange={(v) => setStyle({ ...style, page_size: v })}
                      options={[{ value: 'A4', label: 'A4' }, { value: 'Letter', label: 'Letter' }]} />
                  </Field>
                  <Field label={t('bookSettings.export.margin')}>
                    <Select value={style.margin} onChange={(v) => setStyle({ ...style, margin: v })}
                      options={[{ value: 'narrow', label: t('bookSettings.export.margin.narrow') }, { value: 'normal', label: t('bookSettings.export.margin.normal') }, { value: 'wide', label: t('bookSettings.export.margin.wide') }]} />
                  </Field>
                  <Field label={t('bookSettings.export.fontSize')} hint={t('bookSettings.export.fontSizeHint')}>
                    <Select value={String(style.font_size)} onChange={(v) => setStyle({ ...style, font_size: Number(v) })}
                      options={[12, 13, 14, 15, 16, 17, 18, 20].map((n) => ({ value: String(n), label: `${n} px` }))} />
                  </Field>
                  <Field label={t('bookSettings.export.codeTheme')}>
                    <Select value={style.code_theme} onChange={(v) => setStyle({ ...style, code_theme: v })}
                      options={[{ value: 'light', label: t('bookSettings.export.theme.light') }, { value: 'dark', label: t('bookSettings.export.theme.dark') }]} />
                  </Field>
                </div>
                <div className="flex flex-wrap gap-6">
                  <label className="flex items-center gap-2 text-sm text-slate-700">
                    <Checkbox checked={style.include_cover} onChange={(v) => setStyle({ ...style, include_cover: v })} ariaLabel={t('bookSettings.export.includeCover')} /> {t('bookSettings.export.includeCover')}
                  </label>
                  <label className="flex items-center gap-2 text-sm text-slate-700">
                    <Checkbox checked={style.include_toc} onChange={(v) => setStyle({ ...style, include_toc: v })} ariaLabel={t('bookSettings.export.includeToc')} /> {t('bookSettings.export.includeToc')}
                  </label>
                </div>
                <Field label={t('bookSettings.export.footer')} hint={footerBlocked ? t('export.footer.notIncluded', { siteName: site.site_name || 'KnowForge' }) : t('bookSettings.export.footerHint')}>
                  <Input value={style.footer} maxLength={100} placeholder={t('bookSettings.export.footerPlaceholder')} disabled={footerBlocked}
                    onChange={(e) => setStyle({ ...style, footer: e.target.value })} />
                </Field>
              </div>
            )}
          </div>
        </div>
        <div className="flex justify-end border-t border-slate-100 px-6 py-4">
          <Button loading={saving} onClick={save}>{t('bookSettings.export.save')}</Button>
        </div>
      </div>
    </BookSettingsLayout>
  )
}
