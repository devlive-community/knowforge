import { useEffect, useState } from 'react'
import Link from 'next/link'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { boardText, type LeaderboardMeta } from '@/lib/leaderboards'
import { Badge, Button, Card, Loading, Switch, Tooltip, useFeedback } from '@/components/ui'

interface AdminBoard extends LeaderboardMeta {
  plugin: string
  plugin_enabled: boolean
  enabled: boolean
}

export default function AdminLeaderboards() {
  const { t } = useTranslation()
  return (
    <AdminLayout current="leaderboards" breadcrumb={t('admin.nav.leaderboards')}>
      <FeatureGate feature="leaderboards"><Inner /></FeatureGate>
    </AdminLayout>
  )
}

// Inner 榜单管理：开关每个榜单、上下移动调整前台的显示顺序；提供方插件未启用的榜单标明且不会显示。
function Inner() {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const { refreshSite } = useApp()
  const [items, setItems] = useState<AdminBoard[] | null>(null)
  const [saved, setSaved] = useState('')
  const [saving, setSaving] = useState(false)
  const snapshot = (list: AdminBoard[]) => JSON.stringify(list.map((b) => [b.key, b.enabled]))

  useEffect(() => {
    api<{ items: AdminBoard[] }>('/admin/leaderboards')
      .then((r) => { setItems(r.items); setSaved(snapshot(r.items)) })
      .catch((e) => { setItems([]); showToast({ title: t('admin.leaderboards.loadFailed'), message: (e as Error).message, tone: 'error' }) })
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  function move(i: number, d: -1 | 1) {
    setItems((list) => {
      if (!list || i + d < 0 || i + d >= list.length) return list
      const next = list.slice()
      ;[next[i], next[i + d]] = [next[i + d], next[i]]
      return next
    })
  }

  async function save() {
    if (!items) return
    setSaving(true)
    try {
      const r = await api<{ items: AdminBoard[] }>('/admin/leaderboards', { method: 'PUT', body: { boards: items.map((b) => ({ key: b.key, enabled: b.enabled })) } })
      setItems(r.items)
      setSaved(snapshot(r.items))
      await refreshSite() // 同步导航入口与前台榜单
      showToast({ message: t('admin.leaderboards.saved'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.leaderboards.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  if (!items) return <Loading className="py-24" />
  const dirty = snapshot(items) !== saved
  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-bold text-slate-900">
            <i className="fa-solid fa-medal text-xl text-primary-600" aria-hidden="true" />{t('admin.nav.leaderboards')}
          </h1>
          <p className="mt-1.5 text-sm text-slate-500">{t('admin.leaderboards.description')}</p>
        </div>
        <div className="flex gap-2">
          <Link href="/leaderboards" target="_blank" className="inline-flex items-center gap-1.5 rounded-lg px-3 text-sm text-slate-500 hover:text-primary-600" style={{ height: 'var(--control-height)' }}>
            <i className="fa-solid fa-arrow-up-right-from-square text-xs" aria-hidden="true" /> {t('admin.leaderboards.view')}
          </Link>
          <Button loading={saving} disabled={!dirty} onClick={() => void save()}>{t('common.actions.save')}</Button>
        </div>
      </div>
      <Card className="max-w-3xl overflow-hidden">
        <ul className="divide-y divide-slate-100" data-testid="admin-leaderboards">
          {items.map((b, i) => (
            <li key={b.key} className={`flex items-center gap-3 px-4 py-3.5 ${b.plugin_enabled ? '' : 'bg-slate-50/70'}`}>
              <span className="flex flex-col">
                <button type="button" disabled={i === 0} onClick={() => move(i, -1)} aria-label={t('admin.leaderboards.moveUp')} className="px-1 text-slate-400 hover:text-slate-700 disabled:opacity-30">
                  <i className="fa-solid fa-chevron-up text-xs" aria-hidden="true" />
                </button>
                <button type="button" disabled={i === items.length - 1} onClick={() => move(i, 1)} aria-label={t('admin.leaderboards.moveDown')} className="px-1 text-slate-400 hover:text-slate-700 disabled:opacity-30">
                  <i className="fa-solid fa-chevron-down text-xs" aria-hidden="true" />
                </button>
              </span>
              <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600">
                <i className={`fa-solid ${b.icon || 'fa-trophy'}`} aria-hidden="true" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="flex flex-wrap items-center gap-2 text-sm font-medium text-slate-800">
                  {boardText(t, 'name', b.key)}
                  <Badge tone="slate">{t(b.kind === 'book' ? 'admin.leaderboards.kindBook' : 'admin.leaderboards.kindUser')}</Badge>
                  {!b.plugin_enabled && (
                    <Tooltip content={t('admin.leaderboards.pluginOffHint')}><span><Badge tone="amber">{t('admin.leaderboards.pluginOff', { plugin: b.plugin })}</Badge></span></Tooltip>
                  )}
                </span>
                <span className="mt-0.5 block truncate text-xs text-slate-400">{boardText(t, 'desc', b.key)}</span>
              </span>
              <Switch ariaLabel={boardText(t, 'name', b.key)} checked={b.enabled}
                onChange={(v) => setItems((list) => list && list.map((x) => (x.key === b.key ? { ...x, enabled: v } : x)))} />
            </li>
          ))}
        </ul>
      </Card>
    </>
  )
}
