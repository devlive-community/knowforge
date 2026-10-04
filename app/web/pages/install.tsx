import { useState, useEffect, useRef, FormEvent } from 'react'
import { useRouter } from 'next/router'
import { api, storeSession } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { postEventStream } from '@/lib/event-stream'
import { Button, Input, Field, Loading, SegmentedTabs, useFeedback } from '@/components/ui'
import { CheckCircleIcon } from '@/components/icons'
import { useTranslation } from '@/lib/i18n'
import type { DatabasePayload, SetupStatus, User } from '@/lib/types'

const dbTypes = [
  { key: 'sqlite' as const, name: 'SQLite', descKey: 'install.db.sqlite.desc' },
  { key: 'mysql' as const, name: 'MySQL', descKey: 'install.db.mysql.desc' },
  { key: 'postgres' as const, name: 'PostgreSQL', descKey: 'install.db.postgres.desc' },
]

interface InstallResponse {
  token: string
  user: User
}

interface RestoreInfo { app_version: string; created_at: string; site_name: string; db_type: string; tables: number; files: number }
interface RestoreProgress { stage: 'migrate' | 'database' | 'files'; done: number; total: number }
interface RestoreResult { tables: number; rows: number; files: number; skipped: string[] }

export default function Install() {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const { installed } = useApp()
  const router = useRouter()
  // 安装方式（?mode=restore 为从备份恢复）：第一步都是选择数据库，第二步分别为填写站点与管理员 / 上传备份文件
  const mode: 'install' | 'restore' = router.query.mode === 'restore' ? 'restore' : 'install'
  const fileRef = useRef<HTMLInputElement>(null)
  const [backupFile, setBackupFile] = useState<File | null>(null)
  const [restoring, setRestoring] = useState(false)
  const [restoreInfo, setRestoreInfo] = useState<RestoreInfo | null>(null)
  const [restoreProgress, setRestoreProgress] = useState<RestoreProgress | null>(null)
  const [restoreResult, setRestoreResult] = useState<RestoreResult | null>(null)
  const [step, setStep] = useState<1 | 2>(1)
  const [error, setError] = useState('')
  const [testing, setTesting] = useState(false)
  const [installing, setInstalling] = useState(false)
  const [done, setDone] = useState(false)
  const [setupLoading, setSetupLoading] = useState(true)

  const [dbType, setDbType] = useState<'sqlite' | 'mysql' | 'postgres'>('sqlite')
  const [db, setDb] = useState({ host: '127.0.0.1', port: '', name: 'knowforge', user: 'root', password: '', path: '' })
  const [sqliteDefaultPath, setSqliteDefaultPath] = useState('')

  useEffect(() => {
    api<SetupStatus>('/setup/status')
      .then((s) => setSqliteDefaultPath(s.sqlite_default_path || ''))
      .catch(() => {})
      .finally(() => setSetupLoading(false))
  }, [])
  const [site, setSite] = useState({ name: '', description: '' })
  const [admin, setAdmin] = useState({ username: '', email: '', password: '', confirm: '' })

  if (installed) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="rounded-xl border border-slate-200 bg-white shadow-sm max-w-md p-8 text-center">
          <h1 className="text-lg font-bold">{t('install.installedTitle')}</h1>
          <p className="mt-2 text-sm text-slate-500">{t('install.installedDesc')}</p>
        </div>
      </div>
    )
  }

  if (setupLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-gradient-to-b from-primary-50 to-slate-50">
        <Loading label={t('install.loadingConfig')} />
      </div>
    )
  }

  const dbPayload = (): DatabasePayload => {
    const payload: DatabasePayload = { type: dbType }
    if (dbType === 'sqlite') {
      payload.path = db.path.trim() || undefined
    } else {
      payload.host = db.host
      payload.port = db.port ? Number(db.port) : undefined
      payload.name = db.name
      payload.user = db.user
      payload.password = db.password
    }
    return payload
  }

  async function testConnection() {
    setError('')
    setTesting(true)
    try {
      await api('/setup/test-connection', { method: 'POST', body: dbPayload() })
      showToast({ message: t('install.dbSuccess'), tone: 'success' })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setTesting(false)
    }
  }

  async function submitInstall(e: FormEvent) {
    e.preventDefault()
    setError('')
    if (!site.name.trim()) return setError(t('install.needSiteName'))
    if (!admin.username.trim()) return setError(t('install.needAdminUser'))
    if (admin.password.length < 6) return setError(t('install.pwTooShort'))
    if (admin.password !== admin.confirm) return setError(t('install.pwMismatch'))

    setInstalling(true)
    try {
      const data = await api<InstallResponse>('/setup/install', {
        method: 'POST',
        body: {
          database: dbPayload(),
          site: { name: site.name.trim(), description: site.description.trim() },
          admin: { username: admin.username.trim(), email: admin.email.trim(), password: admin.password },
        },
      })
      storeSession(data.token, data.user)
      setDone(true)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setInstalling(false)
    }
  }

  // 从备份恢复：上传备份文件，服务端以事件流推送进度（建表 → 写入数据 → 解压文件）
  async function submitRestore() {
    if (!backupFile) return setError(t('install.restore.needFile'))
    setError('')
    setRestoring(true)
    setRestoreInfo(null)
    setRestoreProgress(null)
    const form = new FormData()
    form.append('database', JSON.stringify(dbPayload()))
    form.append('file', backupFile)
    let failed = ''
    try {
      await postEventStream('/setup/restore', form, (name, data) => {
        if (name === 'start') setRestoreInfo(data)
        else if (name === 'progress') setRestoreProgress(data)
        else if (name === 'done') setRestoreResult(data)
        else if (name === 'error') failed = data?.message || t('install.restore.failed')
      })
    } catch (e) {
      failed = (e as Error).message
    }
    if (failed) setError(failed)
    setRestoring(false)
  }

  if (restoreResult) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-gradient-to-b from-primary-50 to-slate-50 px-4">
        <div className="w-full max-w-md rounded-xl border border-slate-200 bg-white p-8 text-center shadow-sm" data-testid="restore-done">
          <div className="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-emerald-100 text-emerald-600">
            <CheckCircleIcon className="h-8 w-8" />
          </div>
          <h1 className="text-xl font-bold text-slate-900">{t('install.restore.doneTitle')}</h1>
          <p className="mt-2 text-sm text-slate-500">{t('install.restore.doneDesc', { tables: restoreResult.tables, rows: restoreResult.rows, files: restoreResult.files })}</p>
          {restoreResult.skipped?.length > 0 && (
            <p className="mt-3 rounded-lg bg-amber-50 px-3 py-2 text-left text-xs text-amber-700">{t('install.restore.skipped', { tables: restoreResult.skipped.join(', ') })}</p>
          )}
          {/* 刻意整页刷新，让 AppProvider 重新读取安装状态 */}
          <Button type="button" className="mt-6 w-full" onClick={() => { window.location.href = '/login' }}>{t('install.restore.goLogin')}</Button>
        </div>
      </div>
    )
  }

  if (done) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-gradient-to-b from-primary-50 to-slate-50 px-4">
        <div className="rounded-xl border border-slate-200 bg-white shadow-sm w-full max-w-md p-8 text-center">
          <div className="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-emerald-100 text-emerald-600">
            <CheckCircleIcon className="h-8 w-8" />
          </div>
          <h1 className="text-xl font-bold text-slate-900">{t('install.doneTitle')}</h1>
          <p className="mt-2 text-sm text-slate-500">{t('install.doneDescPrefix', { name: site.name })}<b>{admin.username}</b>{t('install.doneDescSuffix')}</p>
          {/* 刻意整页刷新，让 AppProvider 重新读取本地会话 */}
          {/* eslint-disable-next-line @next/next/no-html-link-for-pages */}
          <Button type="button" className="mt-6 w-full" onClick={() => { window.location.href = '/' }}>{t('install.goHome')}</Button>
        </div>
      </div>
    )
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gradient-to-b from-primary-50 to-slate-50 px-4 py-10">
      <div className="w-full max-w-xl">
        <div className="mb-6 text-center">
          <img src="/logo.png" alt="KnowForge" className="mx-auto mb-3 h-16 w-16 object-contain" />
          <h1 className="text-2xl font-bold text-slate-900">{t('install.welcome')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('install.subtitle', { step })}</p>
        </div>

        <div className="mb-4 flex justify-center">
          <SegmentedTabs value={mode} ariaLabel={t('install.mode.aria')} items={[
            { value: 'install', label: t('install.mode.install'), href: '/install', disabled: restoring || installing },
            { value: 'restore', label: t('install.mode.restore'), href: '/install?mode=restore', disabled: restoring || installing },
          ]} />
        </div>

        {error && <div className="mb-4 rounded-lg bg-rose-50 px-4 py-3 text-sm text-rose-600">{error}</div>}

        <div className="rounded-xl border border-slate-200 bg-white shadow-sm p-6">
          {step === 1 && (
            <div>
              <h2 className="mb-4 font-semibold text-slate-900">{t('install.chooseDb')}</h2>
              <div className="space-y-3">
                {dbTypes.map((opt) => (
                  <label key={opt.key} className="relative block cursor-pointer">
                    <input type="radio" name="dbtype" checked={dbType === opt.key} onChange={() => setDbType(opt.key)} className="peer sr-only" />
                    <span className={`flex items-start gap-3 rounded-xl border p-4 transition-colors focus-within:border-primary-500 ${
                      dbType === opt.key ? 'border-primary-500 bg-primary-50/50' : 'border-slate-200 hover:bg-slate-50'
                    }`}>
                      <span className="min-w-0 flex-1">
                        <span className="block font-medium text-slate-900">{opt.name}{opt.key === 'sqlite' && (
                          <span className="ml-2 inline-flex items-center rounded-full bg-primary-50 px-2 py-0.5 text-xs font-medium text-primary-700 ring-1 ring-inset ring-primary-200">{t('install.default')}</span>
                        )}</span>
                        <span className="mt-0.5 block text-xs text-slate-500">{t(opt.descKey)}</span>
                      </span>
                      <span className={`mt-1 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border-2 transition-colors ${
                        dbType === opt.key ? 'border-primary-500' : 'border-slate-300'
                      }`}>
                        {dbType === opt.key && <span className="h-2 w-2 rounded-full bg-primary-500" />}
                      </span>
                    </span>
                  </label>
                ))}
              </div>

              {dbType === 'sqlite' ? (
                <div className="mt-4">
                  <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('install.sqlitePath')}</label>
                  <Input value={db.path} onChange={(e) => setDb({ ...db, path: e.target.value })}
                    placeholder={sqliteDefaultPath || 'data/knowforge.db'} />
                  <p className="mt-1.5 text-xs text-slate-400">
                    {t('install.sqlitePathHint', { suffix: sqliteDefaultPath ? `：${sqliteDefaultPath}` : '' })}
                  </p>
                </div>
              ) : (
                <div className="mt-4 grid grid-cols-2 gap-3">
                  <div>
                    <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('install.host')}</label>
                    <Input value={db.host} onChange={(e) => setDb({ ...db, host: e.target.value })} />
                  </div>
                  <div>
                    <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('install.port', { port: dbType === 'mysql' ? '3306' : '5432' })}</label>
                    <Input value={db.port} onChange={(e) => setDb({ ...db, port: e.target.value })} placeholder={dbType === 'mysql' ? '3306' : '5432'} />
                  </div>
                  <div>
                    <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('install.dbName')}</label>
                    <Input value={db.name} onChange={(e) => setDb({ ...db, name: e.target.value })} />
                  </div>
                  <div>
                    <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('install.dbUser')}</label>
                    <Input value={db.user} onChange={(e) => setDb({ ...db, user: e.target.value })} />
                  </div>
                  <div className="col-span-2">
                    <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('install.dbPassword')}</label>
                    <Input type="password" value={db.password} onChange={(e) => setDb({ ...db, password: e.target.value })} placeholder={t('install.dbPasswordPlaceholder')} />
                  </div>
                </div>
              )}

              <div className="mt-6 flex items-center justify-between">
                <Button type="button" variant="outline" disabled={testing} onClick={testConnection}>{t('install.testConnection')}</Button>
                <Button type="button" onClick={() => { setError(''); setStep(2) }}>{t('install.next')}</Button>
              </div>
            </div>
          )}

          {step === 2 && mode === 'restore' && (
            <div data-testid="restore-step">
              <h2 className="font-semibold text-slate-900">{t('install.restore.title')}</h2>
              <p className="mt-1 text-sm text-slate-500">{t('install.restore.hint')}</p>
              <input ref={fileRef} type="file" accept=".zip,application/zip" hidden data-testid="restore-file"
                onChange={(e) => { setBackupFile(e.target.files?.[0] || null); setError(''); e.target.value = '' }} />
              <div className="mt-4 flex items-center gap-3 rounded-xl border border-dashed border-slate-300 p-4">
                <i className="fa-solid fa-file-zipper text-xl text-slate-400" aria-hidden="true" />
                <div className="min-w-0 flex-1 text-sm">
                  {backupFile
                    ? <><div className="truncate font-medium text-slate-800">{backupFile.name}</div><div className="text-xs text-slate-400">{(backupFile.size / 1024 / 1024).toFixed(1)} MB</div></>
                    : <span className="text-slate-400">{t('install.restore.noFile')}</span>}
                </div>
                <Button type="button" size="sm" variant="outline" disabled={restoring} onClick={() => fileRef.current?.click()}>{t('install.restore.chooseFile')}</Button>
              </div>
              {restoring && (
                <div className="mt-4 rounded-xl bg-slate-50 p-4" data-testid="restore-progress">
                  {restoreInfo && <p className="mb-2 text-xs text-slate-500">{t('install.restore.from', { site: restoreInfo.site_name || '-', version: restoreInfo.app_version, db: restoreInfo.db_type })}</p>}
                  <div className="h-1.5 overflow-hidden rounded-full bg-slate-200">
                    <div className="h-full rounded-full bg-primary-500 transition-all"
                      style={{ width: `${restoreProgress && restoreProgress.total > 0 ? Math.round((restoreProgress.done / restoreProgress.total) * 100) : 3}%` }} />
                  </div>
                  <p className="mt-1.5 text-xs text-slate-600">
                    {restoreProgress ? t(`install.restore.stage.${restoreProgress.stage}`, { done: restoreProgress.done, total: restoreProgress.total }) : t('install.restore.uploading')}
                  </p>
                </div>
              )}
              <p className="mt-4 text-xs text-slate-400">{t('install.restore.note')}</p>
              <div className="mt-6 flex items-center justify-between">
                <Button type="button" variant="outline" disabled={restoring} onClick={() => setStep(1)}>{t('install.prev')}</Button>
                <Button type="button" loading={restoring} disabled={restoring || !backupFile} onClick={() => void submitRestore()} data-testid="restore-start">{t('install.restore.start')}</Button>
              </div>
            </div>
          )}

          {step === 2 && mode === 'install' && (
            <form onSubmit={submitInstall}>
              <h2 className="mb-4 font-semibold text-slate-900">{t('install.siteAndAdmin')}</h2>
              <div className="space-y-3">
                <div>
                  <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('install.siteName')}</label>
                  <Input value={site.name} onChange={(e) => setSite({ ...site, name: e.target.value })} placeholder={t('install.siteNamePlaceholder')} />
                </div>
                <div>
                  <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('install.siteDesc')}</label>
                  <Input value={site.description} onChange={(e) => setSite({ ...site, description: e.target.value })} placeholder={t('install.siteDescPlaceholder')} />
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('install.adminUser')}</label>
                    <Input value={admin.username} onChange={(e) => setAdmin({ ...admin, username: e.target.value })} />
                  </div>
                  <div>
                    <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('install.adminEmail')}</label>
                    <Input type="email" value={admin.email} onChange={(e) => setAdmin({ ...admin, email: e.target.value })} />
                  </div>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('install.adminPw')}</label>
                    <Input type="password" value={admin.password} onChange={(e) => setAdmin({ ...admin, password: e.target.value })} placeholder={t('install.adminPwPlaceholder')} />
                  </div>
                  <div>
                    <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('install.confirmPw')}</label>
                    <Input type="password" value={admin.confirm} onChange={(e) => setAdmin({ ...admin, confirm: e.target.value })} placeholder={t('install.confirmPwPlaceholder')} />
                  </div>
                </div>
              </div>
              <div className="mt-6 flex items-center justify-between">
                <Button type="button" variant="outline" onClick={() => setStep(1)}>{t('install.prev')}</Button>
                <Button type="submit" loading={installing}>{t('install.start')}</Button>
              </div>
            </form>
          )}
        </div>
      </div>
    </div>
  )
}
