import { useEffect, useRef, useState } from 'react'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, Card, Field, Input, Select, Switch, Textarea } from '@/components/ui'

export interface ResourceTranslation {
  fields: Record<string, string>
  published?: Record<string, string>
  revision: number
  publish: boolean
  dirty?: boolean
}
export type ResourceTranslations = Record<string, ResourceTranslation>
export interface LocalizedField { key: string; label: string; multiline?: boolean; maxLength: number }

export default function LocalizedFields({ value, onChange, fields, onBusyChange }: {
  value: ResourceTranslations
  onChange: (next: ResourceTranslations) => void
  fields: LocalizedField[]
  onBusyChange?: (busy: boolean) => void
}) {
  const { locales, defaultLocale, t } = useTranslation()
  const { site } = useApp()
  const [translating, setTranslating] = useState(false)
  useEffect(() => { onBusyChange?.(translating); return () => onBusyChange?.(false) }, [onBusyChange, translating])
  const [error, setError] = useState('')
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const choices = locales.filter((item) => item.enabled && item.content_enabled)
  const [selected, setSelected] = useState(defaultLocale)
  const active = choices.find((item) => item.code === selected) || choices.find((item) => item.code === defaultLocale) || choices[0]
  if (!active) return <p role="status">{t('i18n.contentUnavailable')}</p>
  const entry = value[active.code] || { fields: {}, revision: 0, publish: false }
  function update(patch: Partial<ResourceTranslation>) { onChange({ ...value, [active.code]: { ...entry, ...patch, dirty: true } }) }
  async function translateMissing() {
    if (!active) return
    setTranslating(true); setError('')
    try {
      const translated = { ...entry.fields }
      const published = value[defaultLocale]?.published
      const source = published && Object.keys(published).length ? published : value[defaultLocale]?.fields || {}
      for (const field of fields) {
        if (translated[field.key] || !source[field.key]) continue
        const result = await api<{ text: string }>('/translate', { method: 'POST', body: {
          text: source[field.key], source_lang: defaultLocale, target_lang: active.code, target_label: active.native_name, ref_type: 'resource',
        } })
        if (Array.from(result.text).length > field.maxLength) throw new Error(t('i18n.translationTooLong'))
        translated[field.key] = result.text
      }
      if (mounted.current) update({ fields: translated, publish: false })
    } catch (cause) { if (mounted.current) setError((cause as Error).message) }
    finally { if (mounted.current) setTranslating(false) }
  }
  return <Card className="space-y-4 p-4">
    <div className="flex flex-wrap items-center gap-3">
      <div className="min-w-0 flex-1"><Select disabled={translating} value={active.code} onChange={setSelected} options={choices.map((item) => ({ value: item.code, label: item.native_name + (value[item.code]?.fields.name ? ' •' : '') }))} /></div>
      <Badge>{active.is_default ? t('i18n.default') : active.code}</Badge>
      {active.code !== defaultLocale && <Button type="button" disabled={translating} variant="outline" onClick={() => update({ fields: Object.fromEntries(fields.map((field) => [field.key, entry.fields[field.key] || value[defaultLocale]?.fields[field.key] || ''])), publish: false })}>{t('i18n.copyDefault')}</Button>}
      {active.code !== defaultLocale && site.translation_enabled && <Button type="button" loading={translating} variant="outline" onClick={translateMissing}>{t('i18n.translateMissing')}</Button>}
    </div>
    <div dir={active.direction} className="grid gap-4 sm:grid-cols-2">
      {fields.map((field) => <Field key={field.key} label={field.label} hint={String((entry.fields[field.key] || '').length) + ' / ' + field.maxLength}>
        {field.multiline ? <Textarea disabled={translating} rows={3} maxLength={field.maxLength} value={entry.fields[field.key] || ''} onChange={(event) => update({ fields: { ...entry.fields, [field.key]: event.target.value } })} />
          : <Input disabled={translating} maxLength={field.maxLength} value={entry.fields[field.key] || ''} onChange={(event) => update({ fields: { ...entry.fields, [field.key]: event.target.value } })} />}
      </Field>)}
    </div>
    {error && <p role="alert" className="max-h-32 overflow-auto break-words text-sm text-rose-600">{error}</p>}
    <div className="flex items-center gap-3"><Switch disabled={translating} ariaLabel={t('i18n.publishWithSave')} checked={entry.publish} onChange={(publish) => update({ publish })} /><span className="text-sm text-slate-600">{t('i18n.publishWithSave')}</span></div>
    <p className="text-xs text-slate-500">{t('i18n.draftHint')}</p>
  </Card>
}
