import { useEffect, useState } from 'react'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { Badge } from '@/components/ui'

interface ClusterInstance {
  id: string
  host: string
  pid: number
  version: string
  started_at: string
  seen_at: string
  self: boolean
  data_shared: boolean
}
interface ClusterStatus { instances: ClusterInstance[]; database: string; warnings: string[] }

// ClusterStatusCard 服务实例：多实例部署时列出在线实例，并提示部署前提（数据库、共享数据目录、版本一致、在线升级范围）。
export default function ClusterStatusCard() {
  const { t } = useTranslation()
  const [data, setData] = useState<ClusterStatus | null>(null)

  useEffect(() => {
    api<ClusterStatus>('/system/cluster').then(setData).catch(() => {})
  }, [])

  if (!data || data.instances.length === 0) return null
  return (
    <section className="mt-8 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
      <div className="mb-1 flex items-center gap-2">
        <h2 className="text-lg font-semibold text-slate-900">{t('admin.system.cluster.title')}</h2>
        <Badge tone={data.instances.length > 1 ? 'primary' : 'slate'}>{t('admin.system.cluster.count', { n: data.instances.length })}</Badge>
      </div>
      <p className="text-sm text-slate-500">{t('admin.system.cluster.hint')}</p>
      {data.warnings.length > 0 && (
        <ul className="mt-4 space-y-2">
          {data.warnings.map((w) => (
            <li key={w} className="flex gap-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
              <i className="fa-solid fa-triangle-exclamation mt-0.5" aria-hidden="true" />
              <span>{t(`admin.system.cluster.warning.${w}`)}</span>
            </li>
          ))}
        </ul>
      )}
      <ul className="mt-4 divide-y divide-slate-100 rounded-lg border border-slate-200">
        {data.instances.map((it) => (
          <li key={it.id} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 text-sm">
            <span className="flex min-w-0 items-center gap-2">
              <i className="fa-solid fa-server text-slate-400" aria-hidden="true" />
              <span className="truncate font-medium text-slate-800">{it.host || it.id}</span>
              {it.self && <Badge tone="emerald">{t('admin.system.cluster.self')}</Badge>}
              {!it.data_shared && <Badge tone="rose">{t('admin.system.cluster.dataNotShared')}</Badge>}
            </span>
            <span className="font-mono text-xs text-slate-500">v{it.version} · PID {it.pid}</span>
            <span className="ml-auto text-xs text-slate-400">{t('admin.system.cluster.started', { date: formatDate(it.started_at) })}</span>
          </li>
        ))}
      </ul>
    </section>
  )
}
