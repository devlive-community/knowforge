import { useEffect, useMemo, useRef, useState } from 'react'
import { useRouter } from 'next/router'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import LocalizedFields, { type ResourceTranslations } from '@/components/LocalizedFields'
import AchievementIcon from '@/components/AchievementIcon'
import AchievementPresetsModal from '@/components/AchievementPresetsModal'
import UserAvatar from '@/components/UserAvatar'
import { API_BASE, api, getToken } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { Badge, Button, Card, DateTimePicker, EmptyState, Field, Input, Loading, Modal, Pagination, SegmentedTabs, Select, Switch, Textarea, Tooltip, useFeedback } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'
import type { AchievementAsset, AchievementDefinition, AchievementMetric, AchievementRule, AchievementSettings, PageResult, User } from '@/lib/types'

type Tab = 'overview' | 'definitions' | 'metrics' | 'grants'

interface GrantRow {
  grant: { id: number; user_id: number; achievement_id: number; achievement?: AchievementDefinition; source: string; reason: string; unlocked_at: string }
  user: Pick<User, 'id' | 'username' | 'avatar'>
}

interface FormState {
  id?: number
  translations: ResourceTranslations
  key: string
  name: string
  description: string
  locked_hint: string
  category: string
  status: string
  rarity: string
  icon_type: 'fa' | 'image' | 'svg'
  icon_value: string
  asset_id: number | null
  series_key: string
  tier: number
  reward_xp: number
  supersedes_previous: boolean
  rule_logic: 'all' | 'any'
  grant_mode: 'auto' | 'manual'
  visibility: 'public' | 'private' | 'hidden'
  progress_mode: 'aggregate' | 'primary' | 'hidden'
  active_from: string
  active_until: string
  sort_order: number
  rules: AchievementRule[]
}

const emptyRule = (): AchievementRule => ({
  metric_key: 'reading.chapters_read', operator: 'gte', target_value: 1, target_max: 1,
  window_type: 'lifetime', window_value: 0, distinct_by: '', filters: {},
})

const emptyForm = (): FormState => ({
  key: '', name: '', description: '', locked_hint: '', translations: {},
  category: 'reading', status: 'draft', rarity: 'common', icon_type: 'fa', icon_value: 'fa-trophy', asset_id: null,
  series_key: '', tier: 1, reward_xp: 0, supersedes_previous: false, rule_logic: 'all', grant_mode: 'auto', visibility: 'public',
  progress_mode: 'aggregate', sort_order: 0, rules: [emptyRule()],
  active_from: '', active_until: '',
})

const rarityTone: Record<string, 'slate' | 'sky' | 'violet' | 'amber'> = { common: 'slate', rare: 'sky', epic: 'violet', legendary: 'amber' }

function toForm(definition: AchievementDefinition): FormState {
  return {
    ...definition,
    translations: Object.fromEntries(Object.entries(definition.translations || {}).map(([code, entry]) => [code, { ...entry, publish: false }])),
    active_from: toLocalDateTimeInput(definition.active_from),
    active_until: toLocalDateTimeInput(definition.active_until),
    rules: (definition.rules || []).map((rule) => ({
      ...rule,
      filters: filterObject(rule),
    })),
  }
}

function toLocalDateTimeInput(value: string | null): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16)
}

function filterObject(rule: AchievementRule): Record<string, unknown> {
  if (typeof rule.filters !== 'string') return rule.filters || {}
  try { return JSON.parse(rule.filters || '{}') as Record<string, unknown> } catch { return {} }
}

function AchievementPreview({ form }: { form: FormState }) {
  const { t } = useTranslation()
  return (
    <div className="flex items-center gap-3 rounded-xl border border-slate-200 bg-slate-50 p-4">
      <AchievementIcon achievement={form as AchievementDefinition} />
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-semibold text-slate-900">{form.name || t('admin.achievements.preview.unnamed')}</span>
          <Badge tone={rarityTone[form.rarity] || 'slate'}>{t(`admin.achievements.rarity.${form.rarity}`)}</Badge>
        </div>
        <p className="mt-1 line-clamp-2 text-sm text-slate-500">{form.description || t('admin.achievements.preview.noDescription')}</p>
      </div>
    </div>
  )
}

export default function AdminAchievements() {
  const { confirmAction, requestInput, showToast } = useFeedback()
  const { t, defaultLocale, locales } = useTranslation()
  const { site } = useApp()
  const growthEnabled = (site.feature_plugins || []).includes('growth')
  const fileRef = useRef<HTMLInputElement>(null)
  const [presetsOpen, setPresetsOpen] = useState(false)

  const categoryLabels: Record<string, string> = { reading: t('admin.achievements.category.reading'), creation: t('admin.achievements.category.creation'), community: t('admin.achievements.category.community'), account: t('admin.achievements.category.account'), special: t('admin.achievements.category.special') }
  const statusLabels: Record<string, string> = { draft: t('admin.achievements.status.draft'), active: t('admin.achievements.status.active'), paused: t('admin.achievements.status.paused'), archived: t('admin.achievements.status.archived') }
  const rarityLabels: Record<string, string> = { common: t('admin.achievements.rarity.common'), rare: t('admin.achievements.rarity.rare'), epic: t('admin.achievements.rarity.epic'), legendary: t('admin.achievements.rarity.legendary') }
  const rarityTone: Record<string, 'slate' | 'sky' | 'violet' | 'amber'> = { common: 'slate', rare: 'sky', epic: 'violet', legendary: 'amber' }
  // 指标目录的聚合方式/统计窗口/标签/描述做国际化：缺失键回退到后端返回值（便于后端新增指标时不写死前端）
  const aggLabel = (agg: string) => { const k = `admin.achievements.agg.${agg}`; const v = t(k); return v === k ? agg : v }
  const windowLabel = (w: string) => { const k = `admin.achievements.window.${w}`; const v = t(k); return v === k ? w : v }
  const metricLabel = (m: AchievementMetric) => { const k = `admin.achievements.metric.${m.key}.label`; const v = t(k); return v === k ? m.label : v }
  const metricDesc = (m: AchievementMetric) => { const k = `admin.achievements.metric.${m.key}.desc`; const v = t(k); return v === k ? m.description : v }
  const router = useRouter()
  // 主 Tab 用查询参数承载（?tab=definitions|metrics|grants），不用本地 state
  const tab: Tab = (['definitions', 'metrics', 'grants'].includes(String(router.query.tab)) ? router.query.tab : 'overview') as Tab
  const setTab = (next: Tab) => router.push({ query: next === 'overview' ? {} : { tab: next } }, undefined, { shallow: true })
  const [settings, setSettings] = useState<AchievementSettings | null>(null)
  const [definitions, setDefinitions] = useState<AchievementDefinition[]>([])
  const [metrics, setMetrics] = useState<AchievementMetric[]>([])
  const [grants, setGrants] = useState<GrantRow[]>([])
  const [grantTotal, setGrantTotal] = useState(0)
  const [grantPage, setGrantPage] = useState(1)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [savingSettings, setSavingSettings] = useState(false)
  const [form, setForm] = useState<FormState | null>(null)
  const [formTab, setFormTab] = useState<'basic' | 'i18n'>('basic')
  const [saving, setSaving] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [translationBusy, setTranslationBusy] = useState(false)
  const [grantUsername, setGrantUsername] = useState('')
  const [grantAchievementID, setGrantAchievementID] = useState('')
  const [grantReason, setGrantReason] = useState('')
  const METRIC_PAGE_SIZE = 9
  const [metricPage, setMetricPage] = useState(1)

  async function loadDefinitions() {
    const result = await api<PageResult<AchievementDefinition>>('/admin/achievements', { params: { page_size: 100 } })
    setDefinitions(result.items || [])
  }

  async function loadGrants(page = grantPage) {
    const result = await api<PageResult<GrantRow>>('/admin/achievement-grants', { params: { page, page_size: 20 } })
    setGrants(result.items || [])
    setGrantTotal(result.total)
    setGrantPage(result.page)
  }

  useEffect(() => {
    Promise.all([
      api<AchievementSettings>('/admin/achievement-settings').then(setSettings),
      api<{ items: AchievementMetric[] }>('/admin/achievement-metrics').then((data) => setMetrics(data.items || [])),
      loadDefinitions(),
      loadGrants(1),
    ]).catch((error) => { const message = (error as Error).message || t('admin.achievements.loadFailedMsg'); setLoadError(message); showToast({ title: t('admin.achievements.loadFailed'), message, tone: 'error' }) }).finally(() => setLoading(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const activeCount = definitions.filter((item) => item.status === 'active').length
  const manualDefinitions = definitions.filter((item) => item.status === 'active')
  // 触发规则的指标随成就分类联动：只列出当前分类下的指标
  const metricOptions = metrics.filter((metric) => !form || metric.category === form.category).map((metric) => ({ value: metric.key, label: t('admin.achievements.metricLabel', { label: metricLabel(metric), unit: metric.unit || t('admin.achievements.metricStatus') }) }))
  // 国际化完整度：统计已填写「名称」的内容语言数（默认语言取表单名），用于弹框 tab 上的 X/Y 提示
  const contentLocales = locales.filter((item) => item.enabled && item.content_enabled)
  const i18nFilledCount = form ? contentLocales.filter((item) => item.code === defaultLocale ? Boolean(form.translations[item.code]?.fields.name || form.name) : Boolean(form.translations[item.code]?.fields.name)).length : 0

  async function saveSettings() {
    if (!settings) return
    setSavingSettings(true)
    try {
      const result = await api<AchievementSettings>('/admin/achievement-settings', { method: 'PUT', body: settings })
      setSettings(result)
      showToast({ message: result.enabled ? t('admin.achievements.enabledOn') : t('admin.achievements.enabledOff'), tone: 'success' })
    } catch (error) {
      showToast({ title: t('admin.achievements.saveFailed'), message: (error as Error).message, tone: 'error' })
    } finally {
      setSavingSettings(false)
    }
  }

  // 打开新建/编辑弹框：每次都回到「基本信息」tab
  function openForm(next: FormState) { setFormTab('basic'); setForm(next) }

  function updateRule(index: number, patch: Partial<AchievementRule>) {
    if (!form) return
    setForm({ ...form, rules: form.rules.map((rule, ruleIndex) => ruleIndex === index ? { ...rule, ...patch } : rule) })
  }

  function updateRuleFilter(index: number, key: string, value: unknown) {
    if (!form) return
    const filters = { ...filterObject(form.rules[index]) }
    if (value === '' || value === undefined || value === null || value === false) delete filters[key]
    else filters[key] = value
    updateRule(index, { filters })
  }

  async function uploadIcon(file?: File) {
    if (!file || !form) return
    setUploading(true)
    try {
      const body = new FormData()
      body.append('file', file)
      const response = await fetch(`${API_BASE}/api/v1/admin/achievement-icons`, { method: 'POST', headers: { Authorization: `Bearer ${getToken()}` }, body })
      const payload = await response.json().catch(() => ({}))
      if (!response.ok || payload.success === false) throw new Error(payload.message || t('admin.achievements.uploadFailed'))
      const asset = payload.data as AchievementAsset
      setForm({ ...form, asset_id: asset.id, icon_type: asset.kind, icon_value: asset.url })
      showToast({ message: t('admin.achievements.iconUploaded'), tone: 'success' })
    } catch (error) {
      showToast({ title: t('admin.achievements.iconUploadFailed'), message: (error as Error).message, tone: 'error' })
    } finally {
      setUploading(false)
    }
  }

  async function saveDefinition() {
    if (!form) return
    setSaving(true)
    try {
      const path = form.id ? `/admin/achievements/${form.id}` : '/admin/achievements'
      await api(path, { method: form.id ? 'PUT' : 'POST', body: {
        ...form,
        translations: Object.fromEntries(Object.entries(form.translations).filter(([, entry]) => entry.dirty)),
        active_from: form.active_from ? new Date(form.active_from).toISOString() : null,
        active_until: form.active_until ? new Date(form.active_until).toISOString() : null,
      } })
      await loadDefinitions()
      setForm(null)
      showToast({ message: form.id ? t('admin.achievements.defUpdated') : t('admin.achievements.defCreated'), tone: 'success' })
    } catch (error) {
      showToast({ title: t('admin.achievements.saveFailed'), message: (error as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  async function removeDefinition(definition: AchievementDefinition) {
    if (!await confirmAction({ title: definition.status === 'draft' ? t('admin.achievements.definitions.deleteTitle') : t('admin.achievements.definitions.archiveTitle'), message: definition.status === 'draft' ? t('admin.achievements.definitions.deleteConfirm', { name: definition.name }) : t('admin.achievements.definitions.archiveConfirm', { name: definition.name }), confirmLabel: definition.status === 'draft' ? t('admin.achievements.definitions.deleteConfirmBtn') : t('admin.achievements.definitions.archiveConfirmBtn'), danger: true })) return
    try {
      await api(`/admin/achievements/${definition.id}`, { method: 'DELETE' })
      await loadDefinitions()
      showToast({ message: t('admin.achievements.definitions.operationSuccess'), tone: 'success' })
    } catch (error) {
      showToast({ title: t('admin.achievements.loadFailed'), message: (error as Error).message, tone: 'error' })
    }
  }

  async function recalculate(definition: AchievementDefinition) {
    try {
      await api(`/admin/achievements/${definition.id}/recalculate`, { method: 'POST' })
      showToast({ message: t('admin.achievements.definitions.recalculateSuccess', { name: definition.name }), tone: 'success' })
    } catch (error) {
      showToast({ title: t('admin.achievements.definitions.recalculateFailed'), message: (error as Error).message, tone: 'error' })
    }
  }

  async function submitGrant() {
    if (!grantUsername.trim() || !grantAchievementID) {
      showToast({ message: t('admin.achievements.grants.usernameRequired'), tone: 'error' })
      return
    }
    try {
      await api('/admin/achievement-grants', { method: 'POST', body: { username: grantUsername.trim(), achievement_id: Number(grantAchievementID), reason: grantReason } })
      setGrantUsername(''); setGrantReason('')
      await loadGrants(1)
      showToast({ message: t('admin.achievements.grants.submitSuccess'), tone: 'success' })
    } catch (error) {
      showToast({ title: t('admin.achievements.grants.submitFailed'), message: (error as Error).message, tone: 'error' })
    }
  }

  async function revokeGrant(row: GrantRow) {
    const reason = await requestInput({ title: t('admin.achievements.grants.revokeTitle'), label: t('admin.achievements.grants.revokeLabel'), placeholder: t('admin.achievements.grants.revokePlaceholder'), confirmLabel: t('admin.achievements.grants.revokeConfirm') })
    if (!reason?.trim()) return
    try {
      await api(`/admin/achievement-grants/${row.grant.id}/revoke`, { method: 'POST', body: { reason: reason.trim() } })
      await loadGrants(grantPage)
      showToast({ message: t('admin.achievements.grants.revokeSuccess'), tone: 'success' })
    } catch (error) {
      showToast({ title: t('admin.achievements.grants.revokeFailed'), message: (error as Error).message, tone: 'error' })
    }
  }

  return (
    <FeatureGate feature="achievements">
    <AdminLayout current="achievements" breadcrumb={t('admin.achievements.title')}>
      <AchievementPresetsModal open={presetsOpen} onClose={() => setPresetsOpen(false)} showReward={growthEnabled}
        onInstalled={() => { loadDefinitions().catch(() => {}) }} />
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div><h1 className="text-3xl font-bold text-slate-900">{t('admin.achievements.title')}</h1><p className="mt-1.5 text-sm text-slate-500">{t('admin.achievements.description')}</p></div>
        {tab === 'definitions' && (
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={() => setPresetsOpen(true)}><i className="fa-solid fa-wand-magic-sparkles" aria-hidden="true" /> {t('admin.achievements.presets.open')}</Button>
            <Button onClick={() => openForm(emptyForm())}><i className="fa-solid fa-plus" aria-hidden="true" /> {t('admin.achievements.createNew')}</Button>
          </div>
        )}
      </div>

      <SegmentedTabs className="mt-6" value={tab} onChange={(value) => setTab(value as Tab)} ariaLabel={t('admin.achievements.title')} items={[
        { value: 'overview', label: t('admin.achievements.tab.overview') }, { value: 'definitions', label: t('admin.achievements.tab.definitions') },
        { value: 'metrics', label: t('admin.achievements.tab.metrics') }, { value: 'grants', label: t('admin.achievements.tab.grants') },
      ]} />

      {loading ? <Loading className="min-h-[50vh]" label={t('admin.achievements.loading')} /> : loadError ? (
        <Card className="mt-6 border-rose-200 bg-rose-50 p-5 text-sm text-rose-700"><i className="fa-solid fa-circle-exclamation mr-2" aria-hidden="true" />{loadError}</Card>
      ) : (
        <div className="mt-6">
          {tab === 'overview' && settings && (
            <div className="space-y-5">
              <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
                {[['fa-trophy', t('admin.achievements.overview.definitions'), definitions.length], ['fa-circle-play', t('admin.achievements.overview.active'), activeCount], ['fa-list-check', t('admin.achievements.overview.metrics'), metrics.length], ['fa-award', t('admin.achievements.overview.grants'), grantTotal]].map(([icon, label, value]) => (
                  <Card key={String(label)} className="flex items-center gap-3 p-5"><span className="flex h-11 w-11 items-center justify-center rounded-xl bg-primary-50 text-primary-600"><i className={`fa-solid ${icon}`} aria-hidden="true" /></span><span><strong className="block text-xl text-slate-900">{value}</strong><span className="text-xs text-slate-500">{label}</span></span></Card>
                ))}
              </div>
              <Card className="max-w-3xl p-6">
                <h2 className="text-lg font-semibold text-slate-900">{t('admin.achievements.settings.title')}</h2>
                <div className="mt-5 divide-y divide-slate-100">
                  {[
                    [t('admin.achievements.settings.publicProfile'), t('admin.achievements.settings.publicProfileHint'), 'public_profile_enabled'],
                    [t('admin.achievements.settings.notifications'), t('admin.achievements.settings.notificationsHint'), 'notifications_enabled'],
                    [t('admin.achievements.settings.allowUserHide'), t('admin.achievements.settings.allowUserHideHint'), 'allow_user_hide'],
                  ].map(([label, hint, key]) => (
                    <div key={key} className="flex items-center justify-between gap-4 py-4"><div><div className="text-sm font-medium text-slate-800">{label}</div><div className="mt-1 text-xs text-slate-400">{hint}</div></div><Switch ariaLabel={label} checked={Boolean(settings[key as keyof AchievementSettings])} onChange={(value) => setSettings({ ...settings, [key]: value })} /></div>
                  ))}
                </div>
                <Field label={t('admin.achievements.settings.showcaseLimit')} hint={t('admin.achievements.settings.showcaseLimitHint')}><span className="inline-block w-28"><Input type="number" min={1} max={12} value={settings.showcase_limit} onChange={(event) => setSettings({ ...settings, showcase_limit: Math.max(1, Math.min(12, Number(event.target.value) || 1)) })} /></span></Field>
                <div className="mt-5 flex justify-end"><Button loading={savingSettings} onClick={saveSettings}>{t('admin.achievements.settings.save')}</Button></div>
              </Card>
            </div>
          )}

          {tab === 'definitions' && (
            definitions.length === 0 ? <EmptyState>{t('admin.achievements.definitions.empty')}</EmptyState> :
            <div className="grid gap-4 lg:grid-cols-2">
              {definitions.map((definition) => (
                <Card key={definition.id} className="flex min-w-0 gap-4 p-5">
                  <AchievementIcon achievement={definition} muted={definition.status !== 'active'} />
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2"><h2 className="truncate font-semibold text-slate-900">{definition.name}</h2><Badge>{statusLabels[definition.status]}</Badge><Badge tone={rarityTone[definition.rarity]}>{rarityLabels[definition.rarity]}</Badge></div>
                    <p className="mt-1 truncate font-mono text-xs text-slate-400">{definition.key}</p>
                    <p className="mt-2 line-clamp-2 text-sm text-slate-500">{definition.description || t('admin.achievements.definitions.noDescription')}</p>
                    <div className="mt-3 flex flex-wrap items-center gap-2 text-xs text-slate-400"><span>{t(`admin.achievements.category.${definition.category}`)}</span><span>{t('admin.achievements.definitions.rules', { count: definition.rules.length })}</span><span>v{definition.version}</span></div>
                    <div className="mt-4 flex flex-wrap gap-2"><Button variant="outline" onClick={() => openForm(toForm(definition))}>{t('admin.achievements.definitions.edit')}</Button><Button variant="ghost" disabled={definition.status !== 'active'} onClick={() => recalculate(definition)}>{t('admin.achievements.definitions.recalculate')}</Button><Button variant="ghost" className="text-rose-600" onClick={() => removeDefinition(definition)}>{definition.status === 'draft' ? t('admin.achievements.definitions.delete') : t('admin.achievements.definitions.archive')}</Button></div>
                  </div>
                </Card>
              ))}
            </div>
          )}

          {tab === 'metrics' && (
            <>
              <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">{metrics.slice((metricPage - 1) * METRIC_PAGE_SIZE, metricPage * METRIC_PAGE_SIZE).map((metric) => <Card key={metric.key} className="p-5"><div className="flex items-start justify-between gap-3"><div><h2 className="font-semibold text-slate-900">{metricLabel(metric)}</h2><p className="mt-1 font-mono text-xs text-primary-600">{metric.key}</p></div><Badge>{categoryLabels[metric.category]}</Badge></div><p className="mt-3 text-sm leading-6 text-slate-500">{metricDesc(metric)}</p><div className="mt-4 flex flex-wrap gap-1.5"><Badge tone="primary">{aggLabel(metric.aggregation)}</Badge>{metric.windows.map((window) => <Badge key={window}>{windowLabel(window)}</Badge>)}</div></Card>)}</div>
              {metrics.length > METRIC_PAGE_SIZE && <div className="mt-6"><Pagination page={metricPage} pageSize={METRIC_PAGE_SIZE} total={metrics.length} onChange={setMetricPage} /></div>}
            </>
          )}

          {tab === 'grants' && (
            <div className="space-y-5">
              <Card className="p-5"><h2 className="font-semibold text-slate-900">{t('admin.achievements.grants.title')}</h2><div className="mt-4 grid gap-3 md:grid-cols-[1fr_1fr_1.5fr_auto]"><Input placeholder={t('admin.achievements.grants.usernamePlaceholder')} value={grantUsername} onChange={(event) => setGrantUsername(event.target.value)} /><Select placeholder={t('admin.achievements.grants.achievementPlaceholder')} value={grantAchievementID} onChange={setGrantAchievementID} options={manualDefinitions.map((item) => ({ value: String(item.id), label: item.name }))} /><Input placeholder={t('admin.achievements.grants.reasonPlaceholder')} value={grantReason} onChange={(event) => setGrantReason(event.target.value)} /><Button onClick={submitGrant}>{t('admin.achievements.grants.submit')}</Button></div></Card>
              {grants.length === 0 ? <EmptyState>{t('admin.achievements.grants.empty')}</EmptyState> : <Card className="overflow-hidden"><div className="overflow-x-auto"><table className="min-w-[760px] w-full text-sm"><thead className="bg-slate-50 text-left text-xs text-slate-500"><tr><th className="px-5 py-3">{t('admin.achievements.grants.table.user')}</th><th className="px-5 py-3">{t('admin.achievements.grants.table.achievement')}</th><th className="px-5 py-3">{t('admin.achievements.grants.table.source')}</th><th className="px-5 py-3">{t('admin.achievements.grants.table.unlockedAt')}</th><th className="px-5 py-3 text-right">{t('admin.achievements.grants.table.action')}</th></tr></thead><tbody className="divide-y divide-slate-100">{grants.map((row) => <tr key={row.grant.id}><td className="px-5 py-3"><span className="flex items-center gap-2"><UserAvatar user={row.user} size="h-8 w-8" /><span>{row.user.username}</span></span></td><td className="px-5 py-3 font-medium text-slate-800">{row.grant.achievement?.name || t('admin.achievements.grants.deletedDefinition')}</td><td className="px-5 py-3"><Badge>{row.grant.source === 'manual' ? t('admin.achievements.grants.source.manual') : t('admin.achievements.grants.source.auto')}</Badge></td><td className="px-5 py-3 text-slate-500">{new Date(row.grant.unlocked_at).toLocaleString('zh-CN')}</td><td className="px-5 py-3 text-right"><Button variant="ghost" className="text-rose-600" onClick={() => revokeGrant(row)}>{t('admin.achievements.grants.revoke')}</Button></td></tr>)}</tbody></table></div></Card>}
              <Pagination page={grantPage} pageSize={20} total={grantTotal} loading={loading} onChange={(page) => { setLoading(true); loadGrants(page).finally(() => setLoading(false)) }} />
            </div>
          )}
        </div>
      )}

      <Modal open={form !== null} onClose={() => setForm(null)} title={form?.id ? t('admin.achievements.form.editTitle') : t('admin.achievements.form.createTitle')} className="max-w-5xl" footer={<><Button variant="outline" onClick={() => setForm(null)}>{t('admin.achievements.form.cancel')}</Button><Button disabled={translationBusy || uploading} loading={saving} onClick={saveDefinition}>{t('admin.achievements.form.save')}</Button></>}>
        {form && <div className="space-y-6">
          <AchievementPreview form={form} />
          <SegmentedTabs value={formTab} onChange={(value) => setFormTab(value as 'basic' | 'i18n')} ariaLabel={t('admin.achievements.form.createTitle')} items={[
            { value: 'basic', label: t('admin.achievements.form.tab.basic') },
            { value: 'i18n', label: <span className="inline-flex items-center gap-1.5">{t('admin.achievements.form.tab.i18n')}<span className={`rounded-full px-1.5 text-[11px] font-semibold leading-4 ${i18nFilledCount >= contentLocales.length ? 'bg-emerald-100 text-emerald-700' : 'bg-amber-100 text-amber-700'}`}>{i18nFilledCount}/{contentLocales.length}</span></span> },
          ]} />
          <div className={formTab === 'basic' ? 'space-y-6' : 'hidden'}>
          <div className="grid gap-4 sm:grid-cols-2"><Field label={t('admin.achievements.form.key')}><Input disabled={Boolean(form.id && form.status !== 'draft')} value={form.key} onChange={(event) => setForm({ ...form, key: event.target.value })} /></Field><Field label={t('admin.achievements.form.category')}><Select value={form.category} onChange={(value) => { const catKeys = new Set(metrics.filter((m) => m.category === value).map((m) => m.key)); const fallback = metrics.find((m) => m.category === value)?.key || ''; setForm({ ...form, category: value, rules: form.rules.map((r) => catKeys.has(r.metric_key) ? r : { ...r, metric_key: fallback, window_type: 'lifetime', window_value: 0, filters: {} }) }) }} options={Object.entries(categoryLabels).map(([value, label]) => ({ value, label }))} /></Field></div>
          <div className="grid gap-4 sm:grid-cols-3"><Field label={t('admin.achievements.form.status')}><Select value={form.status} onChange={(value) => setForm({ ...form, status: value })} options={Object.entries(statusLabels).map(([value, label]) => ({ value, label }))} /></Field><Field label={t('admin.achievements.form.rarity')}><Select value={form.rarity} onChange={(value) => setForm({ ...form, rarity: value })} options={Object.entries(rarityLabels).map(([value, label]) => ({ value, label }))} /></Field><Field label={t('admin.achievements.form.grantMode')}><Select value={form.grant_mode} onChange={(value) => setForm({ ...form, grant_mode: value as 'auto' | 'manual', rules: value === 'auto' && form.rules.length === 0 ? [emptyRule()] : form.rules })} options={[{ value: 'auto', label: t('admin.achievements.form.grantModeAuto') }, { value: 'manual', label: t('admin.achievements.form.grantModeManual') }]} /></Field><Field label={t('admin.achievements.form.visibility')}><Select value={form.visibility} onChange={(value) => setForm({ ...form, visibility: value as FormState['visibility'] })} options={[{ value: 'public', label: t('admin.achievements.form.visibilityPublic') }, { value: 'private', label: t('admin.achievements.form.visibilityPrivate') }, { value: 'hidden', label: t('admin.achievements.form.visibilityHidden') }]} /></Field><Field label={t('admin.achievements.form.progressMode')}><Select value={form.progress_mode} onChange={(value) => setForm({ ...form, progress_mode: value as FormState['progress_mode'] })} options={[{ value: 'aggregate', label: t('admin.achievements.form.progressAggregate') }, { value: 'primary', label: t('admin.achievements.form.progressPrimary') }, { value: 'hidden', label: t('admin.achievements.form.progressHidden') }]} /></Field><Field label={t('admin.achievements.form.sortOrder')}><Input type="number" value={form.sort_order} onChange={(event) => setForm({ ...form, sort_order: Number(event.target.value) || 0 })} /></Field><Field label={t('admin.achievements.form.seriesKey')}><Input value={form.series_key} onChange={(event) => setForm({ ...form, series_key: event.target.value })} placeholder={t('admin.achievements.form.seriesKeyPlaceholder')} /></Field><Field label={t('admin.achievements.form.tier')}><Input type="number" min={1} value={form.tier} onChange={(event) => setForm({ ...form, tier: Math.max(1, Number(event.target.value) || 1) })} /></Field>{growthEnabled && <Field label={t('admin.achievements.form.rewardXp')} hint={t('admin.achievements.form.rewardXpHint')}><Input type="number" min={0} value={form.reward_xp} onChange={(event) => setForm({ ...form, reward_xp: Math.max(0, Number(event.target.value) || 0) })} /></Field>}<Field label={t('admin.achievements.form.supersedes')}><div className="flex h-[var(--control-height)] items-center"><Switch ariaLabel={t('admin.achievements.form.supersedes')} checked={form.supersedes_previous} onChange={(value) => setForm({ ...form, supersedes_previous: value })} /></div></Field><Field label={t('admin.achievements.form.activeFrom')}><DateTimePicker value={form.active_from} onChange={(value) => setForm({ ...form, active_from: value })} ariaLabel={t('admin.achievements.form.activeFrom')} /></Field><Field label={t('admin.achievements.form.activeUntil')}><DateTimePicker value={form.active_until} onChange={(value) => setForm({ ...form, active_until: value })} ariaLabel={t('admin.achievements.form.activeUntil')} /></Field></div>
          <Card className="p-5"><div className="flex flex-wrap items-center justify-between gap-3"><div><h3 className="font-semibold text-slate-900">{t('admin.achievements.form.icon.title')}</h3><p className="mt-1 text-xs text-slate-400">{t('admin.achievements.form.icon.description')}</p></div><Select className="w-40" value={form.icon_type} onChange={(value) => setForm({ ...form, icon_type: value as FormState['icon_type'], asset_id: value === 'fa' ? null : form.asset_id })} options={[{ value: 'fa', label: t('admin.achievements.form.icon.fa') }, { value: 'image', label: t('admin.achievements.form.icon.image') }, { value: 'svg', label: t('admin.achievements.form.icon.svg') }]} /></div><div className="mt-4 flex items-center gap-3">{form.icon_type === 'fa' ? <Input value={form.icon_value} onChange={(event) => setForm({ ...form, icon_value: event.target.value })} placeholder="fa-trophy" /> : <><Button variant="outline" loading={uploading} onClick={() => fileRef.current?.click()}>{t('admin.achievements.form.icon.upload')}</Button><span className="text-xs text-slate-400">{t('admin.achievements.form.icon.maxSize')}</span></>}<input ref={fileRef} type="file" hidden accept="image/png,image/jpeg,image/gif,image/webp,image/svg+xml" onChange={(event) => { void uploadIcon(event.target.files?.[0]); event.target.value = '' }} /></div></Card>
          <Card className="p-5"><div className="flex flex-wrap items-center justify-between gap-3"><div><h3 className="font-semibold text-slate-900">{t('admin.achievements.form.rules.title')}</h3><p className="mt-1 text-xs text-slate-400">{t('admin.achievements.form.rules.description')}</p></div><div className="flex items-center gap-2"><Select className="w-32" value={form.rule_logic} onChange={(value) => setForm({ ...form, rule_logic: value as 'all' | 'any' })} options={[{ value: 'all', label: t('admin.achievements.form.rules.all') }, { value: 'any', label: t('admin.achievements.form.rules.any') }]} /><Button variant="outline" disabled={form.rules.length >= 10} onClick={() => setForm({ ...form, rules: [...form.rules, emptyRule()] })}>{t('admin.achievements.form.rules.add')}</Button></div></div><div className="mt-4 space-y-3">{form.grant_mode === 'manual' ? <div className="rounded-lg bg-slate-50 px-4 py-3 text-sm text-slate-500">{t('admin.achievements.form.rules.manualHint')}</div> : form.rules.map((rule, index) => { const metric = metrics.find((item) => item.key === rule.metric_key); const filters = filterObject(rule); return <div key={index} className="grid gap-2 rounded-xl border border-slate-200 p-3 md:grid-cols-[2fr_1fr_1fr_1fr_auto]"><Select value={rule.metric_key} onChange={(value) => updateRule(index, { metric_key: value, window_type: 'lifetime', filters: {} })} options={metricOptions} /><Select value={rule.operator} onChange={(value) => updateRule(index, { operator: value as AchievementRule['operator'] })} options={[{ value: 'gte', label: t('admin.achievements.form.rules.operator.gte') }, { value: 'eq', label: t('admin.achievements.form.rules.operator.eq') }, { value: 'between', label: t('admin.achievements.form.rules.operator.between') }]} /><Input type="number" min={1} value={rule.target_value} onChange={(event) => updateRule(index, { target_value: Math.max(1, Number(event.target.value) || 1) })} aria-label={t('admin.achievements.form.rules.targetValue')} /><Select value={rule.window_type} onChange={(value) => updateRule(index, { window_type: value as AchievementRule['window_type'], window_value: value === 'rolling_days' ? Math.max(1, rule.window_value || 30) : 0 })} options={(metric?.windows || ['lifetime']).map((value) => ({ value, label: windowLabel(value) }))} /><Tooltip content={t('admin.achievements.form.rules.delete')}><Button variant="ghost" aria-label={t('admin.achievements.form.rules.delete')} disabled={form.rules.length === 1} onClick={() => setForm({ ...form, rules: form.rules.filter((_, ruleIndex) => ruleIndex !== index) })}><i className="fa-solid fa-trash" aria-hidden="true" /></Button></Tooltip>{rule.operator === 'between' && <Input className="md:col-start-4" type="number" min={rule.target_value} value={rule.target_max} onChange={(event) => updateRule(index, { target_max: Math.max(rule.target_value, Number(event.target.value) || rule.target_value) })} aria-label={t('admin.achievements.rule.rangeMax')} />}{rule.window_type === 'rolling_days' && <Input className="md:col-start-4" type="number" min={1} max={3650} value={rule.window_value} onChange={(event) => updateRule(index, { window_value: Math.max(1, Math.min(3650, Number(event.target.value) || 1)) })} aria-label={t('admin.achievements.rule.rollingDays')} />}{(metric?.allowed_filters.length || 0) > 0 && <div className="flex flex-wrap items-center gap-2 md:col-span-5"><span className="text-xs font-medium text-slate-500">{t('admin.achievements.rule.objectFilter')}</span>{metric?.allowed_filters.includes('status') && <Input className="w-48" value={Array.isArray(filters.status) ? (filters.status as string[]).join(',') : ''} onChange={(event) => updateRuleFilter(index, 'status', event.target.value.split(',').map((item) => item.trim()).filter(Boolean))} placeholder={t('admin.achievements.rule.statusCsv')} />}{metric?.allowed_filters.includes('is_public') && <Select className="w-36" value={typeof filters.is_public === 'boolean' ? String(filters.is_public) : ''} onChange={(value) => updateRuleFilter(index, 'is_public', value === '' ? '' : value === 'true')} options={[{ value: '', label: t('admin.achievements.rule.allVisibility') }, { value: 'true', label: t('admin.achievements.rule.publicOnly') }, { value: 'false', label: t('admin.achievements.rule.privateOnly') }]} />}{metric?.allowed_filters.includes('kind') && <Select className="w-36" value={Array.isArray(filters.kind) ? String((filters.kind as string[])[0] || '') : ''} onChange={(value) => updateRuleFilter(index, 'kind', value ? [value] : '')} options={[{ value: '', label: t('admin.achievements.rule.allAnnotations') }, { value: 'highlight', label: t('admin.achievements.rule.highlightOnly') }, { value: 'note', label: t('admin.achievements.rule.noteOnly') }, { value: 'bookmark', label: t('admin.achievements.rule.bookmarkOnly') }]} />}{metric?.allowed_filters.includes('replies_only') && <label className="flex items-center gap-2 text-xs text-slate-500"><Switch ariaLabel={t('admin.achievements.rule.repliesOnly')} checked={filters.replies_only === true} onChange={(value) => updateRuleFilter(index, 'replies_only', value)} /><span>{t('admin.achievements.rule.repliesOnly')}</span></label>}</div>}</div> })}</div></Card>
          </div>
          <div className={formTab === 'i18n' ? '' : 'hidden'}>
            <LocalizedFields value={form.translations} onBusyChange={setTranslationBusy} fields={[
              { key: 'name', label: t('i18n.field.name'), maxLength: 120 },
              { key: 'description', label: t('i18n.field.description'), maxLength: 500, multiline: true },
              { key: 'locked_hint', label: t('admin.achievements.form.lockedHint'), maxLength: 255 },
            ]} onChange={(translations) => setForm({ ...form, translations,
              name: translations[defaultLocale]?.fields.name || form.name,
              description: translations[defaultLocale]?.fields.description || '',
            })} />
          </div>
        </div>}
      </Modal>
    </AdminLayout>
    </FeatureGate>
  )
}
