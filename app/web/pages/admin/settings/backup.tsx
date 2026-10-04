import { useCallback, useEffect, useRef, useState } from 'react'
import SettingsLayout from '@/components/SettingsLayout'
import { API_BASE, api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { subscribeUserTasks } from '@/lib/user-tasks'
import { useUrlPage } from '@/lib/use-url-page'
import { Badge, Button, Checkbox, EmptyState, Field, Input, Loading, Pagination, Select, useFeedback } from '@/components/ui'

interface Backup {
  id: number
  name: string
  kind: 'manual' | 'scheduled'
  status: 'running' | 'done' | 'failed'
  include_files: boolean
  size: number
  tables: number
  rows: number
  files: number
  stage: 'database' | 'files' | ''
  done: number
  total: number
  error: string
  app_version: string
  created_at: string
  finished_at: string | null
}

interface BackupSettings { schedule: 'off' | 'daily' | 'weekly'; hour: number; weekday: number; keep: number; include_files: boolean }

interface BackupList {
  items: Backup[]
  total: number
  page: number
  page_size: number
  settings: BackupSettings
  running: boolean
  server_time: string
  server_offset: number
  db_type: string
  data_dir: string
}

const PAGE_SIZE = 10

function formatSize(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(1)} ${units[i]}`
}

function offsetLabel(seconds: number): string {
  const sign = seconds >= 0 ? '+' : '-'
  const abs = Math.abs(seconds)
  return `UTC${sign}${String(Math.floor(abs / 3600)).padStart(2, '0')}:${String(Math.floor((abs % 3600) / 60)).padStart(2, '0')}`
}

// 系统设置 · 备份：立即备份（可选包含本地上传文件）、自动备份计划与保留份数、备份列表（下载 / 删除），以及恢复说明。
// 备份进度经「我的任务」事件流实时更新，不轮询。
export default function SettingsBackup() {
  const { t } = useTranslation()
  const { confirmAction, showToast } = useFeedback()
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<BackupList | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [settings, setSettings] = useState<BackupSettings | null>(null)
  const [savingSettings, setSavingSettings] = useState(false)
  const [includeFiles, setIncludeFiles] = useState(true)
  const [starting, setStarting] = useState(false)
  const [busy, setBusy] = useState<{ id: number; action: 'download' | 'delete' } | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const d = await api<BackupList>('/backups', { params: { page, page_size: PAGE_SIZE } })
      setData(d)
      setSettings((s) => s ?? d.settings)
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
    setLoading(false)
  }, [page])

  useEffect(() => { void load() }, [load])

  // 备份进度：「我的任务」事件流推送备份任务的变化（进度节流到约每秒一次），收到后刷新列表
  const reloadTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => subscribeUserTasks((task) => {
    if (task && task.kind !== 'backup') return
    if (reloadTimer.current) return
    reloadTimer.current = setTimeout(() => { reloadTimer.current = undefined; void load() }, 400)
  }), [load])
  useEffect(() => () => { if (reloadTimer.current) clearTimeout(reloadTimer.current) }, [])

  async function startBackup() {
    setStarting(true)
    try {
      await api('/backups', { method: 'POST', body: { include_files: includeFiles } })
      showToast({ message: t('admin.settings.backup.started'), tone: 'success' })
      if (page !== 1) setPage(1)
      else await load()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setStarting(false)
  }

  async function saveSettings() {
    if (!settings) return
    setSavingSettings(true)
    try {
      const saved = await api<BackupSettings>('/backups/settings', { method: 'PUT', body: settings })
      setSettings(saved)
      showToast({ message: t('admin.settings.backup.settingsSaved'), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setSavingSettings(false)
  }

  async function download(b: Backup) {
    setBusy({ id: b.id, action: 'download' })
    try {
      const { url } = await api<{ url: string }>(`/backups/${b.id}/download-ticket`, { method: 'POST' })
      const link = document.createElement('a')
      link.href = `${API_BASE}${url}`
      link.download = b.name
      document.body.appendChild(link)
      link.click()
      link.remove()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setBusy(null)
  }

  async function remove(b: Backup) {
    if (!(await confirmAction({ title: t('admin.settings.backup.deleteTitle'), message: t('admin.settings.backup.deleteMessage', { name: b.name }), confirmLabel: t('admin.settings.backup.delete'), danger: true }))) return
    setBusy({ id: b.id, action: 'delete' })
    try {
      await api(`/backups/${b.id}`, { method: 'DELETE' })
      showToast({ message: t('admin.settings.backup.deleted'), tone: 'success' })
      await load()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setBusy(null)
  }

  const hours = Array.from({ length: 24 }, (_, h) => ({ value: String(h), label: `${String(h).padStart(2, '0')}:00` }))
  const weekdays = [0, 1, 2, 3, 4, 5, 6].map((d) => ({ value: String(d), label: t(`admin.settings.backup.weekday.${d}`) }))

  return (
    <SettingsLayout active="backup" description={t('admin.settings.backup.description')}>
      {error ? <p className="text-sm text-rose-600">{error}</p> : !data || !settings ? <Loading className="py-16" /> : (
        <div className="space-y-5">
          <p className="flex gap-2 rounded-xl bg-amber-50 px-4 py-3 text-sm text-amber-800">
            <i className="fa-solid fa-triangle-exclamation mt-0.5" aria-hidden="true" />
            <span>{t('admin.settings.backup.sensitive')}</span>
          </p>

          <div className="grid gap-5 lg:grid-cols-2">
            {/* 立即备份 */}
            <section className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
              <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.backup.nowTitle')}</h2>
              <p className="mt-1 text-sm text-slate-500">{t('admin.settings.backup.nowHint', { dir: data.data_dir })}</p>
              <label className="mt-4 flex items-start gap-2 text-sm text-slate-700">
                <Checkbox checked={includeFiles} onChange={setIncludeFiles} />
                <span>
                  {t('admin.settings.backup.includeFiles')}
                  <span className="block text-xs text-slate-400">{t('admin.settings.backup.includeFilesHint')}</span>
                </span>
              </label>
              <div className="mt-5 flex items-center gap-3">
                <Button onClick={() => void startBackup()} loading={starting || data.running} disabled={starting || data.running} data-testid="backup-start">
                  <i className="fa-solid fa-box-archive" aria-hidden="true" /> {t(data.running ? 'admin.settings.backup.running' : 'admin.settings.backup.start')}
                </Button>
              </div>
            </section>

            {/* 自动备份 */}
            <section className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
              <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.backup.scheduleTitle')}</h2>
              <p className="mt-1 text-sm text-slate-500">{t('admin.settings.backup.scheduleHint', { zone: offsetLabel(data.server_offset), time: formatDate(data.server_time) })}</p>
              <div className="mt-4 grid gap-4 sm:grid-cols-2">
                <Field label={t('admin.settings.backup.frequency')}>
                  <Select value={settings.schedule} onChange={(v) => setSettings({ ...settings, schedule: v as BackupSettings['schedule'] })}
                    options={(['off', 'daily', 'weekly'] as const).map((v) => ({ value: v, label: t(`admin.settings.backup.schedule.${v}`) }))} />
                </Field>
                {settings.schedule === 'weekly' && (
                  <Field label={t('admin.settings.backup.weekdayLabel')}>
                    <Select value={String(settings.weekday)} onChange={(v) => setSettings({ ...settings, weekday: Number(v) })} options={weekdays} />
                  </Field>
                )}
                {settings.schedule !== 'off' && (
                  <>
                    <Field label={t('admin.settings.backup.hour')}>
                      <Select value={String(settings.hour)} onChange={(v) => setSettings({ ...settings, hour: Number(v) })} options={hours} />
                    </Field>
                    <Field label={t('admin.settings.backup.keep')} hint={t('admin.settings.backup.keepHint')}>
                      <Input type="number" min={1} max={100} value={settings.keep} onChange={(e) => setSettings({ ...settings, keep: Math.max(1, Math.min(100, Number(e.target.value) || 1)) })} />
                    </Field>
                    <label className="flex items-center gap-2 text-sm text-slate-700 sm:col-span-2">
                      <Checkbox checked={settings.include_files} onChange={(v) => setSettings({ ...settings, include_files: v })} />
                      {t('admin.settings.backup.includeFiles')}
                    </label>
                  </>
                )}
              </div>
              <div className="mt-5 flex justify-end">
                <Button onClick={() => void saveSettings()} loading={savingSettings} data-testid="backup-settings-save">{t('admin.settings.backup.saveSettings')}</Button>
              </div>
            </section>
          </div>

          {/* 备份列表 */}
          <section className="rounded-2xl border border-slate-200 bg-white shadow-sm">
            <div className="border-b border-slate-100 px-6 py-4">
              <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.backup.listTitle')}</h2>
            </div>
            {data.items.length === 0 ? (
              <div className="p-6"><EmptyState>{t('admin.settings.backup.empty')}</EmptyState></div>
            ) : (
              <ul className={`divide-y divide-slate-100 ${loading ? 'opacity-70' : ''}`}>
                {data.items.map((b) => (
                  <li key={b.id} className="flex flex-col gap-3 px-6 py-4 md:flex-row md:items-center" data-testid="backup-row">
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="truncate font-mono text-sm text-slate-800">{b.name}</span>
                        <Badge tone={b.kind === 'scheduled' ? 'sky' : 'slate'}>{t(`admin.settings.backup.kind.${b.kind}`)}</Badge>
                        <Badge tone={b.status === 'done' ? 'emerald' : b.status === 'failed' ? 'rose' : 'primary'}>
                          {b.status === 'running' && <i className="fa-solid fa-spinner fa-spin mr-1" aria-hidden="true" />}
                          {t(`admin.settings.backup.status.${b.status}`)}
                        </Badge>
                      </div>
                      {b.status === 'running' ? (
                        <div className="mt-2 max-w-md">
                          <div className="h-1.5 overflow-hidden rounded-full bg-slate-100">
                            <div className="h-full rounded-full bg-primary-500 transition-all" style={{ width: `${b.total > 0 ? Math.round((b.done / b.total) * 100) : 5}%` }} />
                          </div>
                          <p className="mt-1 text-xs text-slate-500">{b.total > 0 ? t(`admin.settings.backup.stage.${b.stage || 'database'}`, { done: b.done, total: b.total }) : t('admin.settings.backup.preparing')}</p>
                        </div>
                      ) : b.status === 'failed' ? (
                        <p className="mt-1 text-xs text-rose-600">{b.error}</p>
                      ) : (
                        <p className="mt-1 text-xs text-slate-500">
                          {formatSize(b.size)} · {t('admin.settings.backup.summary', { tables: b.tables, rows: b.rows })}
                          {b.include_files ? ` · ${t('admin.settings.backup.files', { n: b.files })}` : ` · ${t('admin.settings.backup.noFiles')}`}
                        </p>
                      )}
                      <p className="mt-0.5 text-xs text-slate-400">{formatDate(b.created_at)} · v{b.app_version}</p>
                    </div>
                    <div className="flex shrink-0 gap-1">
                      {b.status === 'done' && (
                        <Button size="sm" variant="outline" loading={busy?.id === b.id && busy.action === 'download'} disabled={!!busy} onClick={() => void download(b)}>
                          <i className="fa-solid fa-download" aria-hidden="true" /> {t('admin.settings.backup.download')}
                        </Button>
                      )}
                      {b.status !== 'running' && (
                        <Button size="sm" variant="ghost" className="text-rose-600 hover:bg-rose-50" loading={busy?.id === b.id && busy.action === 'delete'} disabled={!!busy} onClick={() => void remove(b)}>
                          {t('admin.settings.backup.delete')}
                        </Button>
                      )}
                    </div>
                  </li>
                ))}
              </ul>
            )}
            {data.total > data.page_size && (
              <div className="border-t border-slate-100 px-6 py-4">
                <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} loading={loading} />
              </div>
            )}
          </section>

          {/* 恢复 */}
          <section className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <h2 className="text-base font-semibold text-slate-900">{t('admin.settings.backup.restoreTitle')}</h2>
            <ol className="mt-3 list-decimal space-y-1.5 pl-5 text-sm text-slate-600">
              <li>{t('admin.settings.backup.restoreStep1')}</li>
              <li>{t('admin.settings.backup.restoreStep2')}</li>
              <li>{t('admin.settings.backup.restoreStep3')}</li>
            </ol>
            <p className="mt-3 text-xs text-slate-400">{t('admin.settings.backup.restoreNote', { db: data.db_type })}</p>
          </section>
        </div>
      )}
    </SettingsLayout>
  )
}
