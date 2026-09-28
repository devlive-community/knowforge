import { useEffect, useMemo, useState } from 'react'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import { api } from '@/lib/api'
import { TRANSLATE_LANGUAGES } from '@/lib/ai-translate'
import { useTranslation } from '@/lib/i18n'
import { Button, Card, Field, Input, Loading, Select, Tooltip, useFeedback } from '@/components/ui'

interface Preset { title: string; slug: string }
interface Presets { default: Preset; languages: Record<string, Preset> }
interface Row { code: string; title: string; slug: string }

const SAMPLE = { title: '示例书', slug: 'sample-book' }

// renderPreview 按与服务端相同的规则展开模板（仅用于预览）。
function renderPreview(tpl: Preset, fallback: Preset, code: string, label: string) {
  const title = (tpl.title || fallback.title).replace(/\{title\}/g, SAMPLE.title).replace(/\{language\}/g, label).replace(/\{slug\}/g, SAMPLE.slug).replace(/\{code\}/g, code.toLowerCase()).trim()
  const slugTpl = tpl.slug || fallback.slug
  const slug = slugTpl ? slugTpl.replace(/\{title\}|\{language\}/g, '').replace(/\{slug\}/g, SAMPLE.slug).replace(/\{code\}/g, code).toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') : ''
  return { title, slug }
}

export default function AdminBookTranslations() {
  return <FeatureGate feature="book-translations"><Inner /></FeatureGate>
}

// 书籍翻译插件管理：新建译本时预填的书名与访问路径模板（默认模板 + 按语言覆盖），作者新建译本时可修改。
function Inner() {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [loaded, setLoaded] = useState(false)
  const [def, setDef] = useState<Preset>({ title: '', slug: '' })
  const [rows, setRows] = useState<Row[]>([])
  const [saving, setSaving] = useState(false)

  const apply = (d: Presets) => {
    setDef(d.default)
    setRows(Object.entries(d.languages || {}).map(([code, p]) => ({ code, ...p })))
    setLoaded(true)
  }
  useEffect(() => {
    api<Presets>('/admin/book-translations/presets').then(apply)
      .catch((e) => showToast({ title: t('admin.bookTranslations.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [showToast, t])

  const labelOf = (code: string) => TRANSLATE_LANGUAGES.find((l) => l.code === code)?.label || code
  const unused = useMemo(() => TRANSLATE_LANGUAGES.filter((l) => !rows.some((r) => r.code === l.code)), [rows])
  const update = (i: number, patch: Partial<Row>) => setRows((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)))

  async function save() {
    setSaving(true)
    try {
      const languages: Record<string, Preset> = {}
      rows.forEach((r) => { languages[r.code] = { title: r.title.trim(), slug: r.slug.trim() } })
      apply(await api<Presets>('/admin/book-translations/presets', { method: 'PUT', body: { default: { title: def.title.trim(), slug: def.slug.trim() }, languages } }))
      showToast({ message: t('admin.bookTranslations.saved'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.bookTranslations.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  const preview = (p: Preset, code: string) => {
    const r = renderPreview(p, def, code, labelOf(code))
    return t('admin.bookTranslations.preview', { title: r.title || t('admin.bookTranslations.aiTitle'), slug: r.slug || t('admin.bookTranslations.autoSlug') })
  }
  return (
    <AdminLayout current="book-translations" breadcrumb={t('admin.nav.bookTranslations')}>
      <div>
        <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.bookTranslations')}</h1>
        <p className="mt-1.5 text-sm text-slate-500">{t('admin.bookTranslations.description')}</p>
      </div>
      {!loaded ? <Loading className="mt-6" /> : (
        <div className="mt-6 max-w-4xl space-y-5">
          <Card className="space-y-4 p-6">
            <div>
              <h2 className="font-medium text-slate-900">{t('admin.bookTranslations.defaultTitle')}</h2>
              <p className="mt-1 text-sm text-slate-500">{t('admin.bookTranslations.placeholders')}</p>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label={t('admin.bookTranslations.titleTemplate')} hint={t('admin.bookTranslations.titleTemplateHint')}>
                <Input value={def.title} maxLength={200} placeholder="{title}（{language}）" onChange={(e) => setDef({ ...def, title: e.target.value })} />
              </Field>
              <Field label={t('admin.bookTranslations.slugTemplate')} hint={t('admin.bookTranslations.slugTemplateHint')}>
                <Input value={def.slug} maxLength={200} placeholder="{slug}-{code}" onChange={(e) => setDef({ ...def, slug: e.target.value })} />
              </Field>
            </div>
            <p className="text-xs text-slate-400">{preview({ title: '', slug: '' }, 'en')}</p>
          </Card>

          <Card className="p-6">
            <h2 className="font-medium text-slate-900">{t('admin.bookTranslations.languagesTitle')}</h2>
            <p className="mt-1 text-sm text-slate-500">{t('admin.bookTranslations.languagesHint')}</p>
            {rows.length === 0 ? <p className="mt-4 text-sm text-slate-400">{t('admin.bookTranslations.noLanguages')}</p> : (
              <ul className="mt-4 space-y-3">
                {rows.map((r, i) => (
                  <li key={r.code} className="rounded-xl border border-slate-200 p-3">
                    <div className="grid gap-3 md:grid-cols-[10rem_1fr_1fr_auto] md:items-center">
                      <span className="text-sm font-medium text-slate-700">{labelOf(r.code)} <span className="font-mono text-xs text-slate-400">{r.code}</span></span>
                      <Input size="sm" value={r.title} maxLength={200} aria-label={t('admin.bookTranslations.titleTemplate')} placeholder={def.title || t('admin.bookTranslations.useDefault')} onChange={(e) => update(i, { title: e.target.value })} />
                      <Input size="sm" value={r.slug} maxLength={200} aria-label={t('admin.bookTranslations.slugTemplate')} placeholder={def.slug || t('admin.bookTranslations.useDefault')} onChange={(e) => update(i, { slug: e.target.value })} />
                      <Tooltip content={t('admin.bookTranslations.remove')}>
                        <Button size="sm" variant="ghost" aria-label={t('admin.bookTranslations.remove')} onClick={() => setRows((rs) => rs.filter((_, j) => j !== i))}>
                          <i className="fa-solid fa-trash text-slate-400" aria-hidden="true" />
                        </Button>
                      </Tooltip>
                    </div>
                    <p className="mt-2 text-xs text-slate-400">{preview(r, r.code)}</p>
                  </li>
                ))}
              </ul>
            )}
            {unused.length > 0 && (
              <div className="mt-4 max-w-xs">
                <Select size="sm" value="" searchable placeholder={t('admin.bookTranslations.addLanguage')}
                  onChange={(code) => code && setRows((rs) => [...rs, { code, title: '', slug: '' }])}
                  options={unused.map((l) => ({ value: l.code, label: `${l.label} (${l.code})` }))} />
              </div>
            )}
          </Card>
          <div className="flex justify-end">
            <Button loading={saving} onClick={() => void save()}>{t('common.actions.save')}</Button>
          </div>
        </div>
      )}
    </AdminLayout>
  )
}
