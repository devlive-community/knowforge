import { useEffect, useState } from 'react'
import Link from 'next/link'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import AdminLayout from '@/components/AdminLayout'
import ClusterStatusCard from '@/components/ClusterStatusCard'
import { Badge, ButtonLink, Loading } from '@/components/ui'
import {
  ShieldCheckIcon, TagIcon, DatabaseIcon, CodeIcon, GlobeIcon, ServerIcon,
  ActivityIcon, ExternalLinkIcon, MailIcon, GithubIcon, UsersIcon,
  BookIcon, FileTextIcon, EyeIcon, ClockIcon, GearIcon,
} from '@/components/icons'
import { useTranslation } from '@/lib/i18n'
import {
  SystemVersion, HealthInfo, MailConfig, StorageConfig, OAuthProviderConfig,
  AdminActivity, ActivityUser, ActivityBook,
  fetchHealth, measure,
} from '@/lib/admin'
import { formatDate } from '@/lib/api'

interface Service { key: string; label: string; icon: (p: { className?: string }) => JSX.Element; ok: boolean; latency: number }

const DB_LABEL: Record<string, string> = { sqlite: 'SQLite', mysql: 'MySQL', postgres: 'PostgreSQL' }

// 管理控制台首页：服务状态总览与配置入口（仅管理员）
export default function AdminSystem() {
  const { user } = useApp()
  const { t } = useTranslation()
  const isAdmin = user?.role === 'admin'
  const [health, setHealth] = useState<HealthInfo | null>(null)
  const [version, setVersion] = useState<SystemVersion | null>(null)
  const [dbType, setDbType] = useState('')
  const [services, setServices] = useState<Service[] | null>(null)
  const [storage, setStorage] = useState<StorageConfig | null>(null)
  const [mail, setMail] = useState<MailConfig | null>(null)
  const [oauthProviders, setOauthProviders] = useState<OAuthProviderConfig[] | null>(null)
  const [userCount, setUserCount] = useState<number | null>(null)
  const [bookCount, setBookCount] = useState<number | null>(null)
  const [docCount, setDocCount] = useState<number | null>(null)
  const [totalViews, setTotalViews] = useState<number | null>(null)
  const [tagCount, setTagCount] = useState<number | null>(null)
  const [activity, setActivity] = useState<AdminActivity | null>(null)
  const [configCount, setConfigCount] = useState<number | null>(null)
  const [loading, setLoading] = useState(true)

  const BOOK_STATUS: Record<string, { label: string; tone: 'slate' | 'primary' | 'emerald' | 'violet' | 'amber' }> = {
    draft: { label: t('book.status.draft'), tone: 'slate' },
    in_progress: { label: t('book.status.in_progress'), tone: 'primary' },
    published: { label: t('book.status.published'), tone: 'emerald' },
    completed: { label: t('book.status.completed'), tone: 'violet' },
    archived: { label: t('book.status.archived'), tone: 'amber' },
  }

  useEffect(() => {
    if (!isAdmin) return
    const healthRequest = fetchHealth().then(async ({ health, latency }) => {
      setHealth(health)
      const [webMs, dbMs] = await Promise.all([measure('/', { method: 'HEAD' }), measure('/api/v1/stats')])
      setServices([
        { key: 'api', label: t('admin.system.service.api'), icon: ServerIcon, ok: health.status === 'ok', latency },
        { key: 'web', label: t('admin.system.service.web'), icon: GlobeIcon, ok: health.web !== 'down', latency: webMs },
        { key: 'db', label: t('admin.system.service.db'), icon: DatabaseIcon, ok: health.db === 'up', latency: dbMs },
      ])
    })
    const statsRequest = api<{ user_count: number; book_count: number; document_count: number; tag_count: number; total_views: number }>('/admin/stats')
      .then((s) => {
        setUserCount(s.user_count)
        setBookCount(s.book_count)
        setDocCount(s.document_count)
        setTagCount(s.tag_count)
        setTotalViews(s.total_views)
      })
    Promise.allSettled([
      healthRequest,
      api<SystemVersion>('/system/version').then(setVersion),
      api<{ db_type?: string }>('/setup/status').then((s) => setDbType(s.db_type || '')),
      api<StorageConfig>('/storage').then(setStorage),
      api<MailConfig>('/mail').then(setMail),
      api<{ providers: OAuthProviderConfig[] }>('/oauth').then((d) => setOauthProviders(d.providers || [])),
      statsRequest,
      api<AdminActivity>('/admin/activity').then(setActivity),
      api<{ items?: unknown[] }>('/admin/configs').then((c) => setConfigCount(c?.items?.length ?? 0)),
    ]).finally(() => setLoading(false))
  }, [isAdmin, t])

  const dbName = DB_LABEL[dbType] || dbType || '—'
  const nodeVer = health?.node ? `Node.js ${health.node.replace(/^v/, '')}` : '—'
  const healthy = health?.status === 'ok'

  if (loading) {
    return (
      <AdminLayout current="system" breadcrumb={t('admin.nav.dashboard')}>
        <Loading className="min-h-[60vh]" label={t('admin.system.loading')} />
      </AdminLayout>
    )
  }

  return (
    <AdminLayout current="system" breadcrumb={t('admin.nav.dashboard')}>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-3xl font-bold text-slate-900">{t('admin.system.title')}</h1>
          <p className="mt-1.5 text-sm text-slate-500">{t('admin.system.description')}</p>
        </div>
        <ButtonLink href="/" variant="outline">
          <ExternalLinkIcon className="h-4 w-4" /> {t('admin.system.viewSite')}
        </ButtonLink>
      </div>

      {/* 内容统计卡片 */}
      <div className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard icon={UsersIcon} tone="primary" label={t('admin.system.stat.users')} value={fmtNum(userCount)} />
        <StatCard icon={BookIcon} tone="sky" label={t('admin.system.stat.books')} value={fmtNum(bookCount)} />
        <StatCard icon={FileTextIcon} tone="violet" label={t('admin.system.stat.documents')} value={fmtNum(docCount)} />
        <StatCard icon={EyeIcon} tone="amber" label={t('admin.system.stat.totalViews')} value={fmtNum(totalViews)} />
      </div>

      {/* 运行环境卡片 */}
      <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard icon={ShieldCheckIcon} tone="emerald" label={t('admin.system.stat.status')}
          value={healthy ? t('admin.system.status.healthy') : t('admin.system.status.unhealthy')} valueClass={healthy ? 'text-emerald-600' : 'text-rose-600'}
          dot={healthy ? 'bg-emerald-500' : 'bg-rose-500'} />
        <StatCard icon={TagIcon} tone="violet" label={t('admin.system.stat.version')} value={health ? `v${health.version}` : '—'} />
        <StatCard icon={DatabaseIcon} tone="sky" label={t('admin.system.stat.database')} value={dbName} />
        <StatCard icon={CodeIcon} tone="primary" label={t('admin.system.stat.runtime')} value={nodeVer} />
      </div>

      {/* 服务状态 + 版本更新 */}
      <div className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <section className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
          <h2 className="mb-1 flex items-center gap-2 text-lg font-semibold text-slate-900">
            <ActivityIcon className="h-5 w-5 text-slate-400" /> {t('admin.system.serviceStatus')}
          </h2>
          <div className="mt-4 divide-y divide-slate-100">
            {services ? services.map((s) => {
              const Icon = s.icon
              return (
                <div key={s.key} className="flex items-center gap-3 py-3.5">
                  <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-slate-100 text-slate-500">
                    <Icon className="h-5 w-5" />
                  </span>
                  <span className="font-medium text-slate-800">{s.label}</span>
                  <span className="ml-auto flex items-center gap-1.5 text-sm">
                    <span className={`h-2 w-2 rounded-full ${s.ok ? 'bg-emerald-500' : 'bg-rose-500'}`} />
                    <span className={s.ok ? 'text-emerald-600' : 'text-rose-600'}>{s.ok ? t('admin.system.status.healthy') : t('admin.system.status.unhealthy')}</span>
                  </span>
                  <span className="w-16 text-right font-mono text-sm text-slate-400">{s.latency >= 0 ? `${s.latency} ms` : '—'}</span>
                </div>
              )
            }) : <p className="py-6 text-sm text-slate-400">{t('admin.system.serviceUnavailable')}</p>}
          </div>
        </section>

        <section className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
          <h2 className="mb-4 text-lg font-semibold text-slate-900">{t('admin.system.versionUpdate')}</h2>
          <dl className="space-y-3 text-sm">
            <div className="flex items-center justify-between">
              <dt className="text-slate-500">{t('admin.system.currentVersion')}</dt>
              <dd className="font-mono font-semibold text-slate-800">{version ? `v${version.version}` : '—'}</dd>
            </div>
            <div className="flex items-center justify-between">
              <dt className="text-slate-500">{t('admin.system.latestVersion')}</dt>
              <dd className="flex items-center gap-3">
                {version?.latest
                  ? <span className="font-mono font-semibold text-slate-800">v{version.latest.version}</span>
                  : <span className="font-mono text-slate-400">{t('admin.system.fetching')}</span>}
                {version && (version.update_available
                  ? <Badge tone="amber">{t('admin.system.upgradeable')}</Badge>
                  : <Badge tone="emerald">{t('admin.system.upToDate')}</Badge>)}
              </dd>
            </div>
          </dl>
          <div className="mt-5">
            {version?.update_available
              ? <ButtonLink href="/admin/upgrade" className="w-full justify-center">{t('admin.system.goUpgrade')}</ButtonLink>
              : <div className="flex w-full items-center justify-center rounded-lg bg-slate-100 text-sm text-slate-400" style={{ height: 'var(--control-height)' }}>{t('admin.system.noUpgrade')}</div>}
          </div>
        </section>
      </div>

      <ClusterStatusCard />

      {/* 最近活动时间线 */}
      <h2 className="mb-4 mt-8 text-lg font-semibold text-slate-900">{t('admin.system.recentActivity')}</h2>
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <ActivityCard
          icon={<UsersIcon className="h-5 w-5" />} title={t('admin.system.recentUsers')}
          empty={t('admin.system.noUsers')}
          items={activity?.recent_users.map((u) => ({
            key: `u${u.id}`,
            primary: u.username,
            href: `/user/${u.username}`,
            meta: (
              <>
                <Badge tone={u.role === 'admin' ? 'primary' : 'slate'}>{u.role === 'admin' ? t('admin.users.role.admin') : t('admin.users.role.user')}</Badge>
                {!u.is_active && <Badge tone="rose">{t('admin.users.status.inactive')}</Badge>}
              </>
            ),
            time: u.created_at,
          })) ?? null}
        />
        <ActivityCard
          icon={<BookIcon className="h-5 w-5" />} title={t('admin.system.recentBooks')}
          empty={t('admin.system.noBooks')}
          items={activity?.recent_books.map((b) => ({
            key: `b${b.id}`,
            primary: b.title,
            href: `/book/detail/${b.slug}`,
            meta: (
              <>
                <Badge tone={BOOK_STATUS[b.status]?.tone || 'slate'}>
                  {BOOK_STATUS[b.status]?.label || b.status}
                </Badge>
                {b.is_public ? <Badge tone="sky">{t('admin.books.visibility.public')}</Badge> : <Badge tone="slate">{t('admin.books.visibility.private')}</Badge>}
              </>
            ),
            time: b.created_at,
            extra: b.user?.username,
          })) ?? null}
        />
      </div>

      {/* 配置中心 */}
      <h2 className="mb-4 mt-8 text-lg font-semibold text-slate-900">{t('admin.system.configCenter')}</h2>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <ConfigCard icon={UsersIcon} tone="primary" title={t('admin.nav.users')} href="/admin/users"
          status={userCount !== null ? t('admin.system.config.users', { users: userCount, books: fmtNum(bookCount) }) : t('admin.system.config.usersManage')} ok />
        <ConfigCard icon={BookIcon} tone="violet" title={t('admin.nav.books')} href="/admin/books"
          status={bookCount !== null ? t('admin.system.config.books', { count: fmtNum(bookCount) }) : t('admin.system.config.booksManage')} ok />
        <ConfigCard icon={FileTextIcon} tone="sky" title={t('admin.nav.documents')} href="/admin/documents"
          status={docCount !== null ? t('admin.system.config.chapters', { count: fmtNum(docCount) }) : t('admin.system.config.chaptersManage')} ok />
        <ConfigCard icon={ServerIcon} tone="sky" title={t('admin.settings.storage.title')} href="/admin/settings/storage"
          status={storage ? (storage.driver === 'qiniu' ? t('admin.system.config.qiniu') : t('admin.system.config.localDisk')) : t('admin.system.config.loading')} ok />
        <ConfigCard icon={MailIcon} tone="amber" title={t('admin.settings.mail.title')} href="/admin/settings/mail"
          status={mail ? (mail.driver === 'smtp' ? t('admin.system.config.smtp') : t('admin.system.config.logDriver')) : t('admin.system.config.loading')} ok={mail?.driver === 'smtp'} />
        <ConfigCard icon={GithubIcon} tone="emerald" title={t('admin.settings.oauth.title')} href="/admin/settings/oauth"
          status={oauthProviders === null ? t('admin.system.config.loading') : (() => {
            const on = oauthProviders.filter((p) => p.enabled && p.client_id && p.client_secret)
            return on.length > 0 ? t('admin.system.config.oauthEnabled', { count: on.length }) : t('admin.system.config.oauthNotConfigured')
          })()}
          ok={!!oauthProviders?.some((p) => p.enabled && p.client_id && p.client_secret)} />
        <ConfigCard icon={TagIcon} tone="violet" title={t('explore.title')} href="/explore"
          status={tagCount !== null ? t('admin.system.config.tags', { count: tagCount }) : t('admin.system.config.goExplore')} ok />
        <ConfigCard icon={GearIcon} tone="primary" title={t('admin.settings.config.title')} href="/admin/settings/config"
          status={configCount !== null ? t('admin.system.config.configItems', { count: configCount }) : t('admin.system.config.configManage')} ok />
        <ConfigCard icon={ClockIcon} tone="amber" title={t('admin.nav.tasks')} href="/admin/tasks"
          status={t('admin.system.config.tasks')} ok />
      </div>
    </AdminLayout>
  )
}

// fmtNum 数字本地化展示，null 显示为 —；大数加千分位
function fmtNum(n: number | null): string {
  if (n === null) return '—'
  return n.toLocaleString('zh-CN')
}

type Tone = 'emerald' | 'violet' | 'sky' | 'primary' | 'amber'
const iconTone: Record<Tone, string> = {
  emerald: 'bg-emerald-50 text-emerald-600',
  violet: 'bg-violet-50 text-violet-600',
  sky: 'bg-sky-50 text-sky-600',
  primary: 'bg-primary-50 text-primary-600',
  amber: 'bg-amber-50 text-amber-600',
}

function StatCard({ icon: Icon, tone, label, value, valueClass, dot }: {
  icon: (p: { className?: string }) => JSX.Element; tone: Tone; label: string
  value: string; valueClass?: string; dot?: string
}) {
  return (
    <div className="flex items-center gap-4 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
      <span className={`flex h-12 w-12 shrink-0 items-center justify-center rounded-xl ${iconTone[tone]}`}>
        <Icon className="h-6 w-6" />
      </span>
      <div className="min-w-0">
        <p className="text-sm text-slate-500">{label}</p>
        <p className={`mt-0.5 flex items-center gap-2 text-lg font-bold ${valueClass || 'text-slate-900'}`}>
          <span className="truncate">{value}</span>
          {dot && <span className={`h-2 w-2 shrink-0 rounded-full ${dot}`} />}
        </p>
      </div>
    </div>
  )
}

function ConfigCard({ icon: Icon, tone, title, status, href, ok }: {
  icon: (p: { className?: string }) => JSX.Element; tone: Tone; title: string
  status: string; href: string; ok?: boolean
}) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
      <span className={`flex h-11 w-11 items-center justify-center rounded-xl ${iconTone[tone]}`}>
        <Icon className="h-6 w-6" />
      </span>
      <p className="mt-3 font-semibold text-slate-900">{title}</p>
      <p className="mt-1 flex items-center gap-1.5 text-xs text-slate-500">
        <span className={`h-1.5 w-1.5 rounded-full ${ok ? 'bg-emerald-500' : 'bg-amber-500'}`} />
        {status}
      </p>
      <Link href={href} className="mt-4 inline-flex w-fit items-center rounded-lg border border-slate-300 px-3 text-xs font-medium text-slate-700 transition-colors hover:border-slate-400 hover:bg-slate-50"
        style={{ height: 'var(--control-height-sm)' }}>
        {t('admin.system.manage')}
      </Link>
    </div>
  )
}

interface ActivityItem {
  key: string
  primary: string
  href: string
  meta: React.ReactNode
  time: string
  extra?: string
}

// ActivityCard 时间线卡片：最近用户/书籍列表，每行含主标题、状态徽标、相对时间与可选作者名
function ActivityCard({ icon, title, items, empty }: {
  icon: React.ReactNode; title: string; items: ActivityItem[] | null; empty: string
}) {
  const { t } = useTranslation()
  return (
    <section className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
      <h2 className="mb-4 flex items-center gap-2 text-base font-semibold text-slate-900">
        <span className="text-slate-400">{icon}</span> {title}
      </h2>
      {items && items.length > 0 ? (
        <ul className="space-y-1">
          {items.map((it) => (
            <li key={it.key}>
              <Link href={it.href}
                className="flex items-center gap-3 rounded-lg px-2 py-2.5 transition-colors hover:bg-slate-50">
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-1.5">
                    <span className="truncate font-medium text-slate-800">{it.primary}</span>
                    {it.extra && <span className="shrink-0 text-xs text-slate-400">· {it.extra}</span>}
                  </span>
                </span>
                <span className="flex shrink-0 items-center gap-1.5">{it.meta}</span>
                <span className="hidden w-28 shrink-0 text-right text-xs text-slate-400 sm:block">
                  <ClockIcon className="mr-1 inline h-3 w-3 align-text-bottom" />
                  {formatDate(it.time)}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      ) : items ? (
        <p className="py-6 text-sm text-slate-400">{empty}</p>
      ) : (
        <p className="py-6 text-sm text-slate-400">{t('admin.system.noActivity')}</p>
      )}
    </section>
  )
}
