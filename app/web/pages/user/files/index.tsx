import { useCallback, useEffect, useState } from 'react'
import Container from '@/components/Container'
import Seo from '@/components/Seo'
import { api, formatDate } from '@/lib/api'
import { resolveMediaUrl } from '@/lib/media'
import { useRequireAuth, useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, Card, EmptyState, Loading, Pagination, Tooltip, useFeedback } from '@/components/ui'
import { formatBytes, storagePercent } from '@/lib/files'

interface UserFile { id: number; url: string; name: string; ext: string; size: number; source: string; created_at: string }
interface FilesPage { items: { file: UserFile; references: number }[]; total: number; page: number; page_size: number; used_bytes: number; limit_mb: number }

const IMAGE_EXTS = ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'ico']

// 我的文件：上传到站点存储的文件（上传、Markdown 导入的图片、外链图片本地化）、个人存储用量与删除（同时从存储中删除）。
export default function MyFilesPage() {
  const { t } = useTranslation()
  const { user, site } = useApp()
  const { showToast, confirmAction } = useFeedback()
  useRequireAuth()
  const [page, setPage] = useState(1)
  const [data, setData] = useState<FilesPage | null>(null)
  const [deleting, setDeleting] = useState<number | null>(null)

  const load = useCallback(() => {
    api<FilesPage>('/users/me/files', { params: { page, page_size: 24 } })
      .then(setData)
      .catch((e) => showToast({ title: t('files.mine.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [page, showToast, t])
  useEffect(() => { if (user) load() }, [user, load])

  async function remove(item: FilesPage['items'][number]) {
    const message = item.references > 0 ? t('files.mine.deleteInUse', { n: item.references }) : t('files.mine.deleteMessage')
    if (!(await confirmAction({ title: t('files.mine.deleteTitle'), message, confirmLabel: t('files.mine.delete'), danger: true }))) return
    setDeleting(item.file.id)
    try {
      await api(`/users/me/files/${item.file.id}`, { method: 'DELETE' })
      showToast({ message: t('files.mine.deleted'), tone: 'success' })
      load()
    } catch (e) {
      showToast({ title: t('files.mine.deleteFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setDeleting(null)
    }
  }

  if (!user) return <Loading className="min-h-[60vh]" />
  const pct = data ? storagePercent(data.used_bytes, data.limit_mb) : 0
  return (
    <>
      <Seo siteName={site.site_name || 'KnowForge'} title={t('files.mine.title')} noindex />
      <Container>
        <div className="py-8">
          <h1 className="text-2xl font-bold text-ink">{t('files.mine.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('files.mine.subtitle')}</p>
          {!data ? <Loading className="py-12" /> : (
            <>
              <Card className="mt-6 p-5">
                <div className="flex flex-wrap items-baseline gap-2">
                  <span className="text-sm text-slate-500">{t('files.mine.used')}</span>
                  <span className="text-lg font-semibold tabular-nums text-slate-900">{formatBytes(data.used_bytes)}</span>
                  <span className="text-sm text-slate-400">{data.limit_mb < 0 ? t('files.mine.unlimited') : `/ ${data.limit_mb} MB`}</span>
                </div>
                {data.limit_mb >= 0 && (
                  <div className="mt-3 h-2 overflow-hidden rounded-full bg-slate-100">
                    <div className={`h-full rounded-full ${pct >= 90 ? 'bg-rose-500' : pct >= 70 ? 'bg-amber-400' : 'bg-primary-500'}`} style={{ width: `${pct}%` }} />
                  </div>
                )}
                <p className="mt-2 text-xs text-slate-400">{t('files.mine.hint')}</p>
              </Card>
              {data.items.length === 0 ? <div className="mt-6"><EmptyState>{t('files.mine.empty')}</EmptyState></div> : (
                <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                  {data.items.map((item) => {
                    const f = item.file
                    return (
                      <Card key={f.id} className="overflow-hidden">
                        <a href={resolveMediaUrl(f.url)} target="_blank" rel="noopener noreferrer" className="flex h-36 items-center justify-center bg-slate-50">
                          {IMAGE_EXTS.includes(f.ext.toLowerCase())
                            ? <img src={resolveMediaUrl(f.url)} alt={f.name} className="max-h-full max-w-full object-contain" loading="lazy" />
                            : <i className="fa-solid fa-file text-3xl text-slate-300" aria-hidden="true" />}
                        </a>
                        <div className="space-y-2 p-3 text-xs">
                          <div className="flex items-center gap-2">
                            <Tooltip content={f.name} className="min-w-0 flex-1"><span className="block truncate font-medium text-slate-700">{f.name}</span></Tooltip>
                            <span className="shrink-0 tabular-nums text-slate-400">{formatBytes(f.size)}</span>
                          </div>
                          <div className="flex flex-wrap items-center gap-1.5">
                            <Badge>{t(`files.source.${f.source}`)}</Badge>
                            <Badge tone={item.references > 0 ? 'emerald' : 'slate'}>{item.references > 0 ? t('files.mine.inUse', { n: item.references }) : t('files.mine.unused')}</Badge>
                          </div>
                          <div className="flex items-center gap-2">
                            <span className="text-slate-400">{formatDate(f.created_at)}</span>
                            <Button size="sm" variant="ghost" className="ml-auto text-rose-600" loading={deleting === f.id} disabled={deleting !== null}
                              onClick={() => void remove(item)}>{t('files.mine.delete')}</Button>
                          </div>
                        </div>
                      </Card>
                    )
                  })}
                </div>
              )}
              {data.total > data.page_size && <div className="mt-6"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
            </>
          )}
        </div>
      </Container>
    </>
  )
}
