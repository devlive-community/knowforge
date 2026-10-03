import { useCallback, useEffect, useState } from 'react'
import Link from 'next/link'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import SettingsLayout from '@/components/SettingsLayout'
import { Button, Checkbox, Input, Field, Select, Loading, Modal, Switch, useFeedback } from '@/components/ui'
import { subscribeUserTasks } from '@/lib/user-tasks'
import { useTranslation } from '@/lib/i18n'
import { StorageConfig, emptyStorage } from '@/lib/admin'

interface MigrationState { job_id: number; status: 'running' | 'done' | 'failed'; from_drivers: string[]; target_driver: string; total: number; done: number; failed: number; errors: string[]; finished_at: string | null }
interface Migration { target_driver: string; pending: number; by_driver: Record<string, number>; state: MigrationState | null; running: boolean }

// 系统设置 · 存储配置：本地磁盘、七牛云或 S3 兼容对象存储（仅管理员）。修改存储后可把已有文件迁移到新存储（后台任务）。
export default function SettingsStorage() {
  const { user } = useApp()
  const isAdmin = user?.role === 'admin'
  const { t } = useTranslation()
  const [storage, setStorage] = useState<StorageConfig>(emptyStorage)
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const [loading, setLoading] = useState(true)
  const { showToast } = useFeedback()
  const [migration, setMigration] = useState<Migration | null>(null)
  const [askMigrate, setAskMigrate] = useState(false)
  const [deleteOld, setDeleteOld] = useState(true)
  const [starting, setStarting] = useState(false)
  const driverLabel = useCallback((d: string) => (d === 'qiniu' ? t('admin.settings.storage.driverQiniu') : d === 's3' ? t('admin.settings.storage.driverS3') : t('admin.settings.storage.driverLocal')), [t])
  const loadMigration = useCallback(() => api<Migration>('/storage/migration').then((m) => { setMigration(m); return m }), [])

  useEffect(() => { if (isAdmin) void loadMigration().catch(() => {}) }, [isAdmin, loadMigration])
  // 迁移进度经「我的任务」的推送实时更新
  useEffect(() => subscribeUserTasks((task) => {
    if (!task) { void loadMigration().catch(() => {}); return }
    if (task.kind !== 'storageMigrate') return
    if (task.status === 'done' || task.status === 'failed') { void loadMigration().catch(() => {}); return }
    setMigration((m) => (m && m.state ? { ...m, running: true, state: { ...m.state, status: 'running', done: task.done, failed: task.failed, total: task.total } } : m))
  }), [loadMigration])

  async function startMigration() {
    setStarting(true)
    try {
      await api('/storage/migration', { method: 'POST', body: { delete_old: deleteOld } })
      setAskMigrate(false)
      showToast({ message: t('admin.settings.storage.migrateStarted'), tone: 'success' })
      await loadMigration()
    } catch (e) {
      showToast({ title: t('admin.settings.storage.migrateFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setStarting(false)
    }
  }

  useEffect(() => {
    if (!isAdmin) return
    api<StorageConfig>('/storage')
      .then(setStorage)
      .catch((e) => setMessage((e as Error).message))
      .finally(() => setLoading(false))
  }, [isAdmin])

  async function save() {
    setSaving(true)
    setMessage('')
    try {
      await api('/storage', { method: 'PUT', body: storage })
      setStorage(await api<StorageConfig>('/storage')) // 重新读取：Secret Key 只写，刷新「已配置」状态并清空输入
      setMessage(t('admin.settings.storage.saved'))
      // 存储变了且有文件不在当前存储中：询问是否迁移
      const m = await loadMigration().catch(() => null)
      if (m && m.pending > 0 && !m.running) setAskMigrate(true)
    } catch (e) {
      setMessage((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingsLayout active="storage" description={t('admin.settings.storage.description')}>
      {loading ? <Loading className="max-w-2xl rounded-2xl border border-slate-200 bg-white shadow-sm" label={t('admin.settings.storage.loading')} /> : (
      <div className="max-w-2xl rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
        <div className="space-y-4">
          <Field label={t('admin.settings.storage.driver')}>
            <Select
              options={[{ value: 'local', label: t('admin.settings.storage.driverLocal') }, { value: 'qiniu', label: t('admin.settings.storage.driverQiniu') }, { value: 's3', label: t('admin.settings.storage.driverS3') }]}
              value={storage.driver || 'local'} onChange={(v) => setStorage({ ...storage, driver: v })} />
          </Field>
          {storage.driver === 'qiniu' && (
            <>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <Field label="Access Key">
                  <Input value={storage.qiniu_access_key || ''} onChange={(e) => setStorage({ ...storage, qiniu_access_key: e.target.value })} />
                </Field>
                <Field label="Secret Key">
                  <Input type="password" value={storage.qiniu_secret_key || ''} onChange={(e) => setStorage({ ...storage, qiniu_secret_key: e.target.value })} placeholder={t('admin.settings.storage.secretKeyPlaceholder')} />
                </Field>
              </div>
              <Field label={t('admin.settings.storage.bucket')}>
                <Input value={storage.qiniu_bucket || ''} onChange={(e) => setStorage({ ...storage, qiniu_bucket: e.target.value })} />
              </Field>
              <Field label={t('admin.settings.storage.cdnDomain')} hint={t('admin.settings.storage.cdnDomainHint')}>
                <Input value={storage.qiniu_domain || ''} onChange={(e) => setStorage({ ...storage, qiniu_domain: e.target.value })}
                  placeholder="https://cdn.example.com" />
              </Field>
              <Field label={t('admin.settings.storage.uploadHost')} hint={t('admin.settings.storage.uploadHostHint')}>
                <Input value={storage.qiniu_upload_host || ''} onChange={(e) => setStorage({ ...storage, qiniu_upload_host: e.target.value })}
                  placeholder={t('admin.settings.storage.uploadHostPlaceholder')} />
              </Field>
            </>
          )}
          {storage.driver === 's3' && (
            <>
              <p className="text-xs leading-5 text-slate-500">{t('admin.settings.storage.s3Intro')}</p>
              <Field label={t('admin.settings.storage.s3Endpoint')} hint={t('admin.settings.storage.s3EndpointHint')}>
                <Input value={storage.s3_endpoint || ''} onChange={(e) => setStorage({ ...storage, s3_endpoint: e.target.value })} placeholder="https://oss-cn-hangzhou.aliyuncs.com" />
              </Field>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <Field label={t('admin.settings.storage.bucket')}>
                  <Input value={storage.s3_bucket || ''} onChange={(e) => setStorage({ ...storage, s3_bucket: e.target.value })} />
                </Field>
                <Field label={t('admin.settings.storage.s3Region')} hint={t('admin.settings.storage.s3RegionHint')}>
                  <Input value={storage.s3_region || ''} onChange={(e) => setStorage({ ...storage, s3_region: e.target.value })} placeholder="us-east-1" />
                </Field>
                <Field label="Access Key">
                  <Input value={storage.s3_access_key || ''} autoComplete="off" onChange={(e) => setStorage({ ...storage, s3_access_key: e.target.value })} />
                </Field>
                <Field label="Secret Key">
                  <Input type="password" autoComplete="new-password" value={storage.s3_secret_key || ''} onChange={(e) => setStorage({ ...storage, s3_secret_key: e.target.value })}
                    placeholder={storage.s3_secret_key_set ? t('admin.settings.storage.secretSet') : ''} />
                </Field>
              </div>
              <Field label={t('admin.settings.storage.s3PublicUrl')} hint={t('admin.settings.storage.s3PublicUrlHint')}>
                <Input value={storage.s3_public_url || ''} onChange={(e) => setStorage({ ...storage, s3_public_url: e.target.value })} placeholder="https://img.example.com" />
              </Field>
              <Field label={t('admin.settings.storage.s3Prefix')}>
                <Input value={storage.s3_prefix || ''} onChange={(e) => setStorage({ ...storage, s3_prefix: e.target.value })} placeholder="knowforge/" />
              </Field>
              <label className="flex items-center gap-2 text-sm text-slate-600">
                <Switch checked={Boolean(storage.s3_path_style)} onChange={(v) => setStorage({ ...storage, s3_path_style: v })} ariaLabel={t('admin.settings.storage.s3PathStyle')} />
                {t('admin.settings.storage.s3PathStyle')}
              </label>
            </>
          )}
        </div>
        {message && <div className="mt-4 rounded-lg bg-slate-100 px-4 py-3 text-sm text-slate-600">{message}</div>}
        <div className="mt-5 flex justify-end">
          <Button loading={saving} onClick={save}>{t('admin.settings.storage.save')}</Button>
        </div>
      </div>
      )}
      {migration && <MigrationCard migration={migration} driverLabel={driverLabel} onMigrate={() => setAskMigrate(true)} />}
      {migration && (
        <Modal open={askMigrate} onClose={() => setAskMigrate(false)} title={t('admin.settings.storage.migrateTitle')}
          footer={<>
            <Button variant="outline" onClick={() => setAskMigrate(false)}>{t('admin.settings.storage.migrateLater')}</Button>
            <Button loading={starting} onClick={() => void startMigration()}>{t('admin.settings.storage.migrateStart')}</Button>
          </>}>
          <p className="text-sm text-slate-600">
            {t('admin.settings.storage.migrateQuestion', {
              n: migration.pending,
              from: Object.keys(migration.by_driver).map(driverLabel).join('、'),
              to: driverLabel(migration.target_driver),
            })}
          </p>
          <p className="mt-2 text-xs text-slate-500">{t('admin.settings.storage.migrateHow')}</p>
          <label className="mt-4 flex items-start gap-2 text-sm text-slate-700">
            <Checkbox checked={deleteOld} onChange={setDeleteOld} ariaLabel={t('admin.settings.storage.migrateDeleteOld')} />
            <span>
              {t('admin.settings.storage.migrateDeleteOld')}
              <span className="block text-xs text-slate-400">{t('admin.settings.storage.migrateDeleteOldHint')}</span>
            </span>
          </label>
        </Modal>
      )}
    </SettingsLayout>
  )
}

function MigrationCard({ migration, driverLabel, onMigrate }: { migration: Migration; driverLabel: (d: string) => string; onMigrate: () => void }) {
  const { t } = useTranslation()
  const s = migration.state
  const running = migration.running || s?.status === 'running'
  if (!running && migration.pending === 0 && !s) return null
  const percent = s && s.total > 0 ? Math.round(((s.done + s.failed) / s.total) * 100) : 0
  return (
    <div className="mt-6 max-w-2xl rounded-2xl border border-slate-200 bg-white p-6 shadow-sm" data-testid="storage-migration">
      <h3 className="font-semibold text-slate-900">{t('admin.settings.storage.migrateCardTitle')}</h3>
      {running && s ? (
        <div className="mt-3">
          <div className="flex items-center justify-between text-sm text-slate-600">
            <span><i className="fa-solid fa-spinner fa-spin mr-1.5 text-primary-500" aria-hidden="true" />{t('admin.settings.storage.migrateRunning', { done: s.done + s.failed, total: s.total })}</span>
            <Link href="/user/tasks" className="text-xs font-medium text-primary-600 hover:underline">{t('admin.settings.storage.viewTasks')}</Link>
          </div>
          <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-slate-100"><div className="h-full rounded-full bg-primary-500 transition-all" style={{ width: `${percent}%` }} /></div>
        </div>
      ) : (
        <>
          {migration.pending > 0 ? (
            <div className="mt-3 flex flex-wrap items-center justify-between gap-3 rounded-xl bg-amber-50 px-4 py-3">
              <span className="text-sm text-amber-800">{t('admin.settings.storage.migratePending', { n: migration.pending, to: driverLabel(migration.target_driver) })}</span>
              <Button size="sm" onClick={onMigrate}>{t('admin.settings.storage.migrateNow')}</Button>
            </div>
          ) : (
            <p className="mt-2 text-sm text-emerald-700"><i className="fa-solid fa-circle-check mr-1.5" aria-hidden="true" />{t('admin.settings.storage.migrateAllCurrent', { to: driverLabel(migration.target_driver) })}</p>
          )}
          {s && s.status !== 'running' && (
            <div className="mt-3 text-xs text-slate-500">
              {t('admin.settings.storage.migrateLast', { done: s.done, failed: s.failed, to: driverLabel(s.target_driver) })}
              {s.errors?.length > 0 && (
                <details className="mt-2">
                  <summary className="cursor-pointer text-rose-600">{t('admin.settings.storage.migrateErrors', { n: s.errors.length })}</summary>
                  <ul className="mt-1 space-y-0.5 font-mono text-[11px] text-slate-500">{s.errors.map((e, i) => <li key={i} className="break-all">{e}</li>)}</ul>
                </details>
              )}
            </div>
          )}
        </>
      )}
    </div>
  )
}
