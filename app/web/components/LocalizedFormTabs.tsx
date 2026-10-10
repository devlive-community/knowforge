import { SegmentedTabs } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'
import type { ResourceTranslations } from '@/components/LocalizedFields'

export type LocalizedFormTab = 'basic' | 'i18n'

// LocalizedFormTabs 带多语言内容的编辑弹窗统一用两个 tab 区分「基本信息」与「国际化信息」（与成就一致），
// 国际化 tab 上显示已填写名称的内容语言数（X/Y）。弹窗宽度统一用 max-w-5xl。
export default function LocalizedFormTabs({ value, onChange, translations, defaultName = '', ariaLabel, nameKey = 'name' }: {
  value: LocalizedFormTab
  onChange: (tab: LocalizedFormTab) => void
  translations: ResourceTranslations
  /** 默认语言的名称在表单其他字段时传入（如旧表单的 name 字段） */
  defaultName?: string
  ariaLabel: string
  nameKey?: string
}) {
  const { t, locales, defaultLocale } = useTranslation()
  const contentLocales = locales.filter((item) => item.enabled && item.content_enabled)
  const filled = contentLocales.filter((item) => Boolean(translations[item.code]?.fields[nameKey] || (item.code === defaultLocale && defaultName))).length
  const complete = filled >= contentLocales.length
  return (
    <SegmentedTabs value={value} onChange={(v) => onChange(v as LocalizedFormTab)} ariaLabel={ariaLabel} items={[
      { value: 'basic', label: t('i18n.formTab.basic') },
      { value: 'i18n', label: (
        <span className="inline-flex items-center gap-1.5" data-testid="localized-form-tab">
          {t('i18n.formTab.translations')}
          <span className={`rounded-full px-1.5 text-[11px] font-semibold leading-4 ${complete ? 'bg-emerald-100 text-emerald-700' : 'bg-amber-100 text-amber-700'}`}>{filled}/{contentLocales.length}</span>
        </span>
      ) },
    ]} />
  )
}
