import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/router'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import ResourceIcon from '@/components/ResourceIcon'
import IconPicker from '@/components/IconPicker'
import UserSearchSelect, { type UserLite } from '@/components/UserSearchSelect'
import UserAvatar from '@/components/UserAvatar'
import EntitlementEditor from '@/components/EntitlementEditor'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { Badge, Button, Card, EmptyState, Field, Input, Loading, Modal, Pagination, Select, SegmentedTabs, Switch, useFeedback } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'
import { growthReasonLabel, growthRuleLabel } from '@/lib/growth'
import type { EntitlementDef } from '@/lib/entitlements'
import { useChangeEffect, useUrlPage } from '@/lib/use-url-page'

interface Level {
  id: number
  level: number
  name: string
  description?: string
  icon_type?: string
  icon_value?: string
  color?: string
  min_xp: number
  status: string
  entitlements?: Record<string, number> | null
}

interface LevelForm { id?: number; level: number; name: string; description: string; icon_type: string; icon_value: string; color: string; min_xp: number; status: string; entitlements: Record<string, number> }
interface Rule { id: number; rule_key: string; label: string; base_xp: number; daily_cap: number; enabled: boolean }
interface RuleStat { count: number; xp: number }
interface LedgerItem { id: number; rule_key: string; final_xp: number; reason?: string; created_at: string; user?: UserLite }
type Tab = 'levels' | 'rules' | 'events' | 'settings'

// useRuleLabel 经验规则显示名（i18n 优先）。
function useRuleLabel() {
  const { t } = useTranslation()
  return (key: string, fallback?: string) => growthRuleLabel(t, key, fallback)
}

export default function AdminGrowth() {
  return <FeatureGate feature="growth"><AdminGrowthInner /></FeatureGate>
}

function AdminGrowthInner() {
  const { t } = useTranslation()
  const { showToast, confirmAction } = useFeedback()
  const [levels, setLevels] = useState<Level[] | null>(null)
  const [form, setForm] = useState<LevelForm | null>(null)
  const [saving, setSaving] = useState(false)
  const [adjUser, setAdjUser] = useState<UserLite | null>(null)
  const [adjXP, setAdjXP] = useState('')
  const [adjReason, setAdjReason] = useState('')
  const [adjusting, setAdjusting] = useState(false)
  const [rules, setRules] = useState<Rule[] | null>(null)
  const [entDefs, setEntDefs] = useState<EntitlementDef[]>([])
  const [ruleStats, setRuleStats] = useState<Record<string, RuleStat>>({})
  const router = useRouter()
  const tab: Tab = (['rules', 'events', 'settings'] as Tab[]).includes(router.query.tab as Tab) ? (router.query.tab as Tab) : 'levels' // tab 由 URL 驱动
  const ruleLabel = useRuleLabel()

  const load = useCallback(() => {
    api<{ items: Level[] }>('/admin/growth/levels').then((r) => setLevels(r.items || [])).catch((e) => showToast({ title: t('admin.growth.loadFailed'), message: (e as Error).message, tone: 'error' }))
    api<{ items: Rule[]; stats_7d?: Record<string, RuleStat> }>('/admin/growth/rules').then((r) => { setRules(r.items || []); setRuleStats(r.stats_7d || {}) }).catch(() => {})
  }, [showToast, t])
  useEffect(() => { load() }, [load])
  useEffect(() => {
    api<{ items: EntitlementDef[] }>('/entitlements/definitions').then((r) => setEntDefs(r.items || [])).catch(() => {})
  }, [])

  async function save() {
    if (!form || !form.name.trim()) return
    setSaving(true)
    try {
      const path = form.id ? `/admin/growth/levels/${form.id}` : '/admin/growth/levels'
      await api(path, { method: form.id ? 'PUT' : 'POST', body: {
        level: form.level, name: form.name.trim(), description: form.description,
        icon_type: form.icon_type, icon_value: form.icon_value, color: form.color, min_xp: form.min_xp, status: form.status, entitlements: form.entitlements,
      } })
      setForm(null); load()
      showToast({ message: t('admin.growth.saved'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.growth.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally { setSaving(false) }
  }

  async function remove(lv: Level) {
    if (!(await confirmAction({ title: t('admin.growth.deleteTitle'), message: t('admin.growth.deleteMessage', { name: lv.name }), confirmLabel: t('common.actions.delete'), danger: true }))) return
    try { await api(`/admin/growth/levels/${lv.id}`, { method: 'DELETE' }); load(); showToast({ message: t('admin.growth.deleted'), tone: 'success' }) }
    catch (e) { showToast({ title: t('admin.growth.saveFailed'), message: (e as Error).message, tone: 'error' }) }
  }

  async function adjust() {
    if (!adjUser || !adjXP.trim() || !adjReason.trim()) { showToast({ message: t('admin.growth.adjustRequired'), tone: 'error' }); return }
    setAdjusting(true)
    try {
      const r = await api<{ username: string; lifetime_xp: number; level?: { name: string } }>('/admin/growth/adjust', { method: 'POST', body: { user_id: adjUser.id, xp: Number(adjXP), reason: adjReason.trim() } })
      setAdjUser(null); setAdjXP(''); setAdjReason('')
      showToast({ title: t('admin.growth.adjustDone'), message: t('admin.growth.adjustDoneDetail', { user: r.username, xp: r.lifetime_xp, level: r.level?.name || '' }), tone: 'success' })
    } catch (e) { showToast({ title: t('admin.growth.adjustFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setAdjusting(false) }
  }

  const nextLevel = levels ? (levels.reduce((m, l) => Math.max(m, l.level), 0) + 1) : 1

  return (
    <AdminLayout current="growth" breadcrumb={t('admin.nav.growth')}>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.growth')}</h1>
          <p className="mt-1.5 text-sm text-slate-500">{t('admin.growth.description')}</p>
        </div>
        {tab === 'levels' && (
          <Button onClick={() => setForm({ level: nextLevel, name: `Lv.${nextLevel}`, description: '', icon_type: 'fa', icon_value: 'fa-star', color: '', min_xp: 0, status: 'active', entitlements: {} })}>
            <i className="fa-solid fa-plus" aria-hidden="true" /> {t('admin.growth.addLevel')}
          </Button>
        )}
      </div>

      <SegmentedTabs className="mt-6" value={tab} ariaLabel={t('admin.nav.growth')}
        items={[
          { value: 'levels', label: t('admin.growth.tab.levels'), href: '/admin/growth?tab=levels' },
          { value: 'rules', label: t('admin.growth.tab.rules'), href: '/admin/growth?tab=rules' },
          { value: 'events', label: t('admin.growth.tab.events'), href: '/admin/growth?tab=events' },
          { value: 'settings', label: t('admin.growth.tab.settings'), href: '/admin/growth?tab=settings' },
        ]} />

      <div className={`mt-6 grid gap-6 lg:grid-cols-[1.4fr_1fr] ${tab === 'levels' ? '' : 'hidden'}`}>
        <div>
          {levels === null ? <Loading className="py-16" label={t('admin.growth.loading')} /> : levels.length === 0 ? (
            <EmptyState>{t('admin.growth.empty')}</EmptyState>
          ) : (
            <Card className="overflow-hidden">
              <table className="w-full text-sm">
                <thead className="bg-slate-50 text-left text-xs text-slate-500">
                  <tr><th className="px-4 py-3">{t('admin.growth.col.level')}</th><th className="px-4 py-3">{t('admin.growth.col.minXp')}</th><th className="px-4 py-3">{t('admin.growth.col.status')}</th><th className="px-4 py-3 text-right">{t('admin.growth.col.action')}</th></tr>
                </thead>
                <tbody className="divide-y divide-slate-100">
                  {levels.map((lv) => (
                    <tr key={lv.id}>
                      <td className="px-4 py-3"><span className="flex items-center gap-2.5"><ResourceIcon iconType={lv.icon_type} iconValue={lv.icon_value} fallback="fa-star" className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-primary-100 bg-primary-50 text-primary-600" /><span className="font-medium text-slate-800">{lv.name}</span>{Object.keys(lv.entitlements || {}).length > 0 && <Badge tone="primary">{t('admin.growth.privilegeCount', { n: Object.keys(lv.entitlements || {}).length })}</Badge>}</span></td>
                      <td className="px-4 py-3 text-slate-500">{lv.min_xp}</td>
                      <td className="px-4 py-3"><Badge tone={lv.status === 'active' ? 'emerald' : 'slate'}>{t(`admin.growth.status.${lv.status}`)}</Badge></td>
                      <td className="px-4 py-3 text-right"><span className="flex justify-end gap-2"><Button variant="outline" size="sm" onClick={() => setForm({ id: lv.id, level: lv.level, name: lv.name, description: lv.description || '', icon_type: lv.icon_type || 'fa', icon_value: lv.icon_value || 'fa-star', color: lv.color || '', min_xp: lv.min_xp, status: lv.status, entitlements: { ...(lv.entitlements || {}) } })}>{t('common.actions.edit')}</Button>{lv.level !== 1 && <Button variant="ghost" size="sm" className="text-rose-600" onClick={() => remove(lv)}>{t('common.actions.delete')}</Button>}</span></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </Card>
          )}
        </div>

        {/* 人工调整经验 */}
        <Card className="h-fit p-5">
          <h2 className="font-bold text-slate-900">{t('admin.growth.adjustTitle')}</h2>
          <p className="mt-1 text-xs text-slate-400">{t('admin.growth.adjustHint')}</p>
          <div className="mt-4 space-y-3">
            <Field label={t('admin.growth.adjustUser')}><UserSearchSelect value={adjUser} onChange={setAdjUser} /></Field>
            <Field label={t('admin.growth.adjustXp')}><Input type="number" value={adjXP} onChange={(e) => setAdjXP(e.target.value)} placeholder="100 / -50" /></Field>
            <Field label={t('admin.growth.adjustReason')}><Input value={adjReason} onChange={(e) => setAdjReason(e.target.value)} /></Field>
            <div className="flex justify-end"><Button loading={adjusting} onClick={adjust}>{t('admin.growth.adjustSubmit')}</Button></div>
          </div>
        </Card>
      </div>

      {/* 经验规则（仅在「经验规则」tab 挂载） */}
      {tab === 'rules' && <RulesPanel rules={rules} stats={ruleStats} onRules={setRules} />}

      {/* 经验流水（仅在「经验流水」tab 挂载，按需加载） */}
      {tab === 'events' && <LedgerPanel rules={rules || []} />}
      {tab === 'settings' && <SettingsPanel />}

      <Modal className="max-w-2xl" open={form !== null} onClose={() => setForm(null)} title={form?.id ? t('admin.growth.editLevel') : t('admin.growth.addLevel')}
        footer={<><Button variant="outline" onClick={() => setForm(null)}>{t('common.actions.cancel')}</Button><Button loading={saving} onClick={save}>{t('common.actions.save')}</Button></>}>
        {form && (
          <div className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label={t('admin.growth.form.level')}><Input type="number" min={1} value={form.level} disabled={Boolean(form.id)} onChange={(e) => setForm({ ...form, level: Number(e.target.value) || 1 })} /></Field>
              <Field label={t('admin.growth.form.minXp')} hint={form.level === 1 ? t('admin.growth.form.level1Zero') : undefined}><Input type="number" min={0} value={form.min_xp} disabled={form.level === 1} onChange={(e) => setForm({ ...form, min_xp: Math.max(0, Number(e.target.value) || 0) })} /></Field>
            </div>
            <Field label={t('admin.growth.form.name')}><Input value={form.name} maxLength={120} onChange={(e) => setForm({ ...form, name: e.target.value })} /></Field>
            <Field label={t('admin.growth.form.icon')}><IconPicker value={{ icon_type: form.icon_type, icon_value: form.icon_value }} onChange={(v) => setForm({ ...form, icon_type: v.icon_type || 'fa', icon_value: v.icon_value })} fallback="fa-star" /></Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label={t('admin.growth.form.color')}><Input value={form.color} onChange={(e) => setForm({ ...form, color: e.target.value })} placeholder="#6366f1" /></Field>
              <Field label={t('admin.growth.form.status')}>
                <Select value={form.status} onChange={(v) => setForm({ ...form, status: v })}
                  options={[{ value: 'active', label: t('admin.growth.status.active') }, { value: 'archived', label: t('admin.growth.status.archived') }]} />
              </Field>
            </div>
            {entDefs.length > 0 && (
              <div>
                <div className="text-sm font-medium text-slate-700">{t('admin.growth.levelEntitlements')}</div>
                <p className="mb-2 mt-0.5 text-xs text-slate-400">{t('admin.growth.levelEntitlementsHint')}</p>
                <EntitlementEditor mode="source" defs={entDefs} value={form.entitlements} onChange={(entitlements) => setForm({ ...form, entitlements })} />
              </div>
            )}
          </div>
        )}
      </Modal>
    </AdminLayout>
  )
}

// LedgerPanel 管理端经验流水：按用户/规则筛选、分页；筛选变化回到第一页。
function LedgerPanel({ rules }: { rules: Rule[] }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const ruleLabel = useRuleLabel()
  const [user, setUser] = useState<UserLite | null>(null)
  const [ruleKey, setRuleKey] = useState('')
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<{ items: LedgerItem[]; total: number; page: number; page_size: number } | null>(null)
  const [loading, setLoading] = useState(false)

  useChangeEffect(() => setPage(1), [user, ruleKey])
  useEffect(() => {
    let alive = true
    setLoading(true)
    api<{ items: LedgerItem[]; total: number; page: number; page_size: number }>('/admin/growth/events', {
      params: { page, page_size: 20, user_id: user?.id || undefined, rule_key: ruleKey || undefined },
    })
      .then((r) => { if (alive) setData(r) })
      .catch((e) => { if (alive) showToast({ title: t('admin.growth.events.loadFailed'), message: (e as Error).message, tone: 'error' }) })
      .finally(() => { if (alive) setLoading(false) })
    return () => { alive = false }
  }, [page, user, ruleKey]) // eslint-disable-line react-hooks/exhaustive-deps

  const ruleOptions = [
    { value: '', label: t('admin.growth.events.allRules') },
    ...rules.map((r) => ({ value: r.rule_key, label: ruleLabel(r.rule_key, r.label) })),
    ...(['achievement.unlocked', 'admin.adjust'].filter((k) => !rules.some((r) => r.rule_key === k)).map((k) => ({ value: k, label: ruleLabel(k) }))),
  ]

  return (
    <Card className="mt-6 p-5">
      <h2 className="font-bold text-slate-900">{t('admin.growth.events.title')}</h2>
      <p className="mt-1 text-xs text-slate-400">{t('admin.growth.events.hint')}</p>
      <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:max-w-2xl">
        <UserSearchSelect value={user} onChange={setUser} placeholder={t('admin.growth.events.filterUser')} />
        <Select value={ruleKey} onChange={setRuleKey} options={ruleOptions} searchable />
      </div>
      {!data ? <Loading className="py-10" /> : data.total === 0 ? (
        <div className="mt-4"><EmptyState>{t('admin.growth.events.empty')}</EmptyState></div>
      ) : (
        <div className={`mt-4 overflow-x-auto ${loading ? 'pointer-events-none opacity-60' : ''}`} aria-busy={loading}>
          <table className="w-full min-w-[640px] text-sm">
            <thead className="bg-slate-50 text-left text-xs text-slate-500">
              <tr>
                <th className="px-4 py-2.5">{t('admin.growth.events.col.user')}</th>
                <th className="px-4 py-2.5">{t('admin.growth.events.col.rule')}</th>
                <th className="px-4 py-2.5 text-right">{t('admin.growth.events.col.xp')}</th>
                <th className="px-4 py-2.5">{t('admin.growth.events.col.reason')}</th>
                <th className="px-4 py-2.5">{t('admin.growth.events.col.time')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {data.items.map((e) => (
                <tr key={e.id}>
                  <td className="px-4 py-2.5">
                    {e.user ? (
                      <button type="button" onClick={() => setUser(e.user || null)} className="flex items-center gap-2 text-left hover:text-primary-600">
                        <UserAvatar user={e.user} size="h-6 w-6" link={false} tooltip={false} />
                        <span className="truncate">{e.user.username}</span>
                      </button>
                    ) : <span className="text-slate-400">—</span>}
                  </td>
                  <td className="px-4 py-2.5"><span className="block text-slate-700">{ruleLabel(e.rule_key)}</span><span className="font-mono text-xs text-slate-400">{e.rule_key}</span></td>
                  <td className={`px-4 py-2.5 text-right font-medium tabular-nums ${e.final_xp < 0 ? 'text-rose-600' : 'text-emerald-600'}`}>{e.final_xp > 0 ? `+${e.final_xp}` : e.final_xp}</td>
                  <td className="max-w-[16rem] truncate px-4 py-2.5 text-slate-500">{growthReasonLabel(t, e.reason) || '—'}</td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-slate-400">{new Date(e.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} />
        </div>
      )}
    </Card>
  )
}

interface GrowthSettings { leaderboard_enabled: boolean; leaderboard_min_xp: number; checkin_enabled: boolean; checkin_streak_days: number }

// SettingsPanel 成长设置：经验排行榜（开关、最少上榜经验）与每日签到（开关、连续签到奖励周期）。
function SettingsPanel() {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const { refreshSite } = useApp()
  const [form, setForm] = useState<GrowthSettings | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    api<GrowthSettings>('/admin/growth/settings').then(setForm)
      .catch((e) => showToast({ title: t('admin.growth.settings.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  async function save() {
    if (!form) return
    setSaving(true)
    try {
      setForm(await api<GrowthSettings>('/admin/growth/settings', { method: 'PUT', body: form }))
      await refreshSite() // 同步导航中的排行榜入口
      showToast({ message: t('admin.growth.settings.saved'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.growth.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally { setSaving(false) }
  }

  if (!form) return <Loading className="py-16" />
  return (
    <Card className="mt-6 max-w-2xl p-5">
      <h2 className="font-bold text-slate-900">{t('admin.growth.settings.title')}</h2>
      <div className="mt-4 space-y-5">
        <div className="flex items-start justify-between gap-4">
          <div>
            <div className="text-sm font-medium text-slate-900">{t('admin.growth.settings.leaderboardEnabled')}</div>
            <p className="mt-1 text-xs text-slate-400">{t('admin.growth.settings.leaderboardEnabledHint')}</p>
          </div>
          <Switch ariaLabel={t('admin.growth.settings.leaderboardEnabled')} checked={form.leaderboard_enabled} onChange={(v) => setForm({ ...form, leaderboard_enabled: v })} />
        </div>
        <Field label={t('admin.growth.settings.minXp')} hint={t('admin.growth.settings.minXpHint')}>
          <span className="block w-40">
            <Input type="number" min={1} max={1000000} value={form.leaderboard_min_xp} disabled={!form.leaderboard_enabled}
              onChange={(e) => setForm({ ...form, leaderboard_min_xp: Math.min(1000000, Math.max(1, Number(e.target.value) || 1)) })} trailing={<span className="text-xs text-slate-400">XP</span>} />
          </span>
        </Field>
        <div className="flex items-start justify-between gap-4 border-t border-slate-100 pt-5">
          <div>
            <div className="text-sm font-medium text-slate-900">{t('admin.growth.settings.checkinEnabled')}</div>
            <p className="mt-1 text-xs text-slate-400">{t('admin.growth.settings.checkinEnabledHint')}</p>
          </div>
          <Switch ariaLabel={t('admin.growth.settings.checkinEnabled')} checked={form.checkin_enabled} onChange={(v) => setForm({ ...form, checkin_enabled: v })} />
        </div>
        <Field label={t('admin.growth.settings.streakDays')} hint={t('admin.growth.settings.streakDaysHint')}>
          <span className="block w-40">
            <Input type="number" min={2} max={365} value={form.checkin_streak_days} disabled={!form.checkin_enabled}
              onChange={(e) => setForm({ ...form, checkin_streak_days: Math.min(365, Math.max(2, Number(e.target.value) || 2)) })} trailing={<span className="text-xs text-slate-400">{t('admin.growth.settings.daysUnit')}</span>} />
          </span>
        </Field>
      </div>
      <div className="mt-6 flex justify-end"><Button loading={saving} onClick={save}>{t('common.actions.save')}</Button></div>
    </Card>
  )
}

// 规则分组：按规则键前缀归类（签到/阅读/创作/互动/账号），其余归「其他」。
const RULE_GROUPS = ['checkin', 'reading', 'creation', 'community', 'account', 'other'] as const
type RuleGroup = typeof RULE_GROUPS[number]
function ruleGroup(key: string): RuleGroup {
  const prefix = key.split('.')[0] as RuleGroup
  return (RULE_GROUPS as readonly string[]).includes(prefix) ? prefix : 'other'
}

// RulesPanel 经验规则：启用开关点击即保存（乐观更新，失败回滚）；经验值/每日上限修改后「保存」才生效。
// 按类别分组，展示规则说明与近 7 天发放统计。
function RulesPanel({ rules, stats, onRules }: { rules: Rule[] | null; stats: Record<string, RuleStat>; onRules: (updater: (rs: Rule[] | null) => Rule[] | null) => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const ruleLabel = useRuleLabel()
  // saved 为服务端已保存的值；rules 为界面编辑中的值（仅经验值/上限可能未保存）
  const [saved, setSaved] = useState<Record<number, Rule>>({})
  const [busy, setBusy] = useState<Record<number, 'toggle' | 'save' | undefined>>({})
  useEffect(() => {
    if (!rules) return
    setSaved((prev) => {
      const next = { ...prev }
      for (const r of rules) if (!next[r.id]) next[r.id] = r
      return next
    })
  }, [rules])

  function patch(id: number, changes: Partial<Rule>) {
    onRules((rs) => (rs || []).map((r) => (r.id === id ? { ...r, ...changes } : r)))
  }
  async function put(id: number, body: Pick<Rule, 'base_xp' | 'daily_cap' | 'enabled'>) {
    return api<Rule>(`/admin/growth/rules/${id}`, { method: 'PUT', body })
  }
  async function toggle(r: Rule, enabled: boolean) {
    const base = saved[r.id] || r
    setBusy((b) => ({ ...b, [r.id]: 'toggle' }))
    patch(r.id, { enabled })
    try {
      // 只提交启用状态，经验值/上限用已保存的值，避免把未保存的编辑顺带提交
      const updated = await put(r.id, { base_xp: base.base_xp, daily_cap: base.daily_cap, enabled })
      setSaved((s) => ({ ...s, [r.id]: updated }))
      showToast({ message: t(enabled ? 'admin.growth.rule.enabledToast' : 'admin.growth.rule.disabledToast', { name: ruleLabel(r.rule_key, r.label) }), tone: 'success' })
    } catch (e) {
      patch(r.id, { enabled: !enabled })
      showToast({ title: t('admin.growth.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally { setBusy((b) => ({ ...b, [r.id]: undefined })) }
  }
  async function save(r: Rule) {
    setBusy((b) => ({ ...b, [r.id]: 'save' }))
    try {
      const updated = await put(r.id, { base_xp: r.base_xp, daily_cap: r.daily_cap, enabled: saved[r.id]?.enabled ?? r.enabled })
      setSaved((s) => ({ ...s, [r.id]: updated }))
      showToast({ message: t('admin.growth.ruleSaved'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.growth.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally { setBusy((b) => ({ ...b, [r.id]: undefined })) }
  }
  const dirty = (r: Rule) => { const s = saved[r.id]; return !!s && (s.base_xp !== r.base_xp || s.daily_cap !== r.daily_cap) }
  const ruleDesc = (key: string) => { const k = `admin.growth.ruleDesc.${key}`; const v = t(k); return v === k ? '' : v }

  return (
    <Card className="mt-6 p-5">
      <h2 className="font-bold text-slate-900">{t('admin.growth.rulesTitle')}</h2>
      <p className="mt-1 text-xs text-slate-400">{t('admin.growth.rulesHint')}</p>
      {rules === null ? <Loading className="py-6" /> : rules.length === 0 ? (
        <div className="mt-3"><EmptyState>{t('admin.growth.rulesEmpty')}</EmptyState></div>
      ) : (
        <div className="mt-4 overflow-x-auto">
          <table className="w-full min-w-[760px] text-sm">
            <thead className="bg-slate-50 text-left text-xs text-slate-500">
              <tr>
                <th className="px-4 py-2.5">{t('admin.growth.rule.event')}</th>
                <th className="px-4 py-2.5">{t('admin.growth.rule.baseXp')}</th>
                <th className="px-4 py-2.5">{t('admin.growth.rule.dailyCap')}</th>
                <th className="px-4 py-2.5">{t('admin.growth.rule.stats7d')}</th>
                <th className="px-4 py-2.5">{t('admin.growth.rule.enabled')}</th>
                <th className="px-4 py-2.5 text-right">{t('admin.growth.col.action')}</th>
              </tr>
            </thead>
            {RULE_GROUPS.map((group) => {
              const list = rules.filter((r) => ruleGroup(r.rule_key) === group)
              if (list.length === 0) return null
              return (
                <tbody key={group} className="divide-y divide-slate-100 border-t border-slate-100">
                  <tr><td colSpan={6} className="bg-slate-50/60 px-4 py-1.5 text-xs font-semibold text-slate-500">{t(`admin.growth.ruleGroup.${group}`)}</td></tr>
                  {list.map((r) => {
                    const stat = stats[r.rule_key]
                    return (
                      <tr key={r.id} className={r.enabled ? '' : 'text-slate-400'}>
                        <td className="px-4 py-2.5">
                          <span className={`block font-medium ${r.enabled ? 'text-slate-800' : 'text-slate-500'}`}>{ruleLabel(r.rule_key, r.label)}</span>
                          {ruleDesc(r.rule_key) && <span className="block text-xs text-slate-400">{ruleDesc(r.rule_key)}</span>}
                          <span className="font-mono text-[11px] text-slate-300">{r.rule_key}</span>
                        </td>
                        <td className="px-4 py-2.5"><span className="inline-block w-24"><Input type="number" min={0} value={r.base_xp} onChange={(e) => patch(r.id, { base_xp: Math.max(0, Number(e.target.value) || 0) })} /></span></td>
                        <td className="px-4 py-2.5"><span className="inline-block w-24"><Input type="number" min={0} value={r.daily_cap} onChange={(e) => patch(r.id, { daily_cap: Math.max(0, Number(e.target.value) || 0) })} /></span></td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-xs text-slate-500">{stat ? t('admin.growth.rule.statsValue', { count: stat.count, xp: stat.xp }) : '—'}</td>
                        <td className="px-4 py-2.5">
                          <span className="inline-flex items-center gap-2">
                            <Switch ariaLabel={ruleLabel(r.rule_key, r.label)} checked={r.enabled} disabled={!!busy[r.id]} onChange={(v) => toggle(r, v)} />
                            {busy[r.id] === 'toggle' && <i className="fa-solid fa-spinner fa-spin text-xs text-slate-400" aria-hidden="true" />}
                          </span>
                        </td>
                        <td className="px-4 py-2.5 text-right">
                          <Button size="sm" variant={dirty(r) ? 'primary' : 'outline'} disabled={!dirty(r) || !!busy[r.id]} loading={busy[r.id] === 'save'} onClick={() => save(r)}>{t('common.actions.save')}</Button>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              )
            })}
          </table>
          <p className="mt-2 text-xs text-slate-400">{t('admin.growth.rule.capNote')} {t('admin.growth.rule.autoSaveNote')} {t('admin.growth.rule.catalogNote')}</p>
        </div>
      )}
    </Card>
  )
}
