import { useState } from 'react'
import type { GetServerSideProps, InferGetServerSidePropsType } from 'next'
import Link from 'next/link'
import { useRouter } from 'next/router'
import BookCard from '@/components/BookCard'
import Container from '@/components/Container'
import Seo from '@/components/Seo'
import UserAvatar from '@/components/UserAvatar'
import ListFormModal from '@/components/booklists/ListFormModal'
import { api, formatDate } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { bookListPath, bookListsEnabled, ownerName, type BookListDetail } from '@/lib/booklists'
import { useTranslation } from '@/lib/i18n'
import { authHeaderFrom, getSiteConfig, isInstalled, serverApi, siteUrlFrom } from '@/lib/server-api'
import { Badge, Button, EmptyState, Tooltip, useFeedback } from '@/components/ui'

export const getServerSideProps: GetServerSideProps<{ site: Record<string, string>; siteUrl: string; initial: BookListDetail }> = async ({ req, params }) => {
  if (!(await isInstalled())) return { redirect: { destination: '/install', permanent: false } }
  const site = await getSiteConfig()
  const id = Number(params?.id)
  if (!bookListsEnabled(site) || !Number.isInteger(id) || id <= 0) return { notFound: true }
  const initial = await serverApi<BookListDetail>(`/book-lists/${id}`, { headers: authHeaderFrom(req) }).catch(() => null)
  if (!initial) return { notFound: true }
  return { props: { site, siteUrl: siteUrlFrom(req), initial } }
}

// 书单详情：书单信息、收录的书籍（附推荐语）；创建者可编辑、排序、修改推荐语与移出，其他人可收藏。
export default function BookListPage({ site, siteUrl, initial }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const router = useRouter()
  const { user } = useApp()
  const { showToast, confirmAction, requestInput } = useFeedback()
  const [detail, setDetail] = useState(initial)
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState<string | null>(null) // 正在处理的操作（按钮/行级）
  const { list, items, mine } = detail
  const siteName = site.site_name || 'KnowForge'

  async function run(key: string, fn: () => Promise<void>) {
    setBusy(key)
    try {
      await fn()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  const toggleFollow = () => {
    if (!user) { void router.push(`/login?next=${encodeURIComponent(bookListPath(list.id))}`); return }
    void run('follow', async () => {
      const r = await api<{ following: boolean; follower_count: number }>(`/book-lists/${list.id}/follow`, { method: list.following ? 'DELETE' : 'POST' })
      setDetail((d) => ({ ...d, list: { ...d.list, following: r.following, follower_count: r.follower_count } }))
    })
  }

  // 管理员：设为 / 取消精选（展示在发现页）
  const toggleFeatured = () => void run('featured', async () => {
    const r = await api<{ featured: boolean }>(`/admin/book-lists/${list.id}/featured`, { method: 'PUT', body: { featured: !list.featured } })
    setDetail((d) => ({ ...d, list: { ...d.list, featured: r.featured } }))
    showToast({ message: t(r.featured ? 'booklists.detail.featuredOn' : 'booklists.detail.featuredOff'), tone: 'success' })
  })

  const remove = async () => {
    if (!(await confirmAction({ title: t('booklists.detail.deleteTitle'), message: t('booklists.detail.deleteMessage', { title: list.title }), danger: true, confirmLabel: t('common.actions.delete') }))) return
    void run('delete', async () => {
      await api(`/book-lists/${list.id}`, { method: 'DELETE' })
      showToast({ message: t('booklists.detail.deleted'), tone: 'success' })
      void router.replace('/lists?tab=mine')
    })
  }

  const move = (index: number, delta: number) => void run(`move-${items[index].book.id}-${delta}`, async () => {
    const next = [...items]
    const [it] = next.splice(index, 1)
    next.splice(index + delta, 0, it)
    await api(`/book-lists/${list.id}/order`, { method: 'PUT', body: { book_ids: next.map((x) => x.book.id) } })
    setDetail((d) => ({ ...d, items: next }))
  })

  const editNote = async (bookId: number, note: string) => {
    const value = await requestInput({ title: t('booklists.detail.noteTitle'), label: t('booklists.detail.noteLabel'), defaultValue: note, placeholder: t('booklists.detail.notePlaceholder') })
    if (value === null) return
    void run(`note-${bookId}`, async () => {
      const r = await api<{ note: string }>(`/book-lists/${list.id}/items/${bookId}`, { method: 'PUT', body: { note: value } })
      setDetail((d) => ({ ...d, items: d.items.map((x) => (x.book.id === bookId ? { ...x, note: r.note } : x)) }))
    })
  }

  const removeItem = (bookId: number) => void run(`remove-${bookId}`, async () => {
    const r = await api<{ item_count: number }>(`/book-lists/${list.id}/items/${bookId}`, { method: 'DELETE' })
    setDetail((d) => ({ ...d, items: d.items.filter((x) => x.book.id !== bookId), list: { ...d.list, item_count: r.item_count } }))
  })

  return (
    <>
      <Seo siteName={siteName} title={list.title} description={list.description || t('booklists.detail.seoDescription', { owner: ownerName(list.owner), n: list.item_count })}
        url={`${siteUrl}${bookListPath(list.id)}`} noindex={!list.is_public} />
      <Container>
        <nav className="flex items-center gap-1.5 py-4 text-sm text-slate-500">
          <Link href="/lists" className="hover:text-slate-900">{t('booklists.nav')}</Link>
          <span>/</span>
          <span className="truncate text-slate-900">{list.title}</span>
        </nav>
        <header className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
          <div className="flex flex-wrap items-start gap-4">
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <h1 className="text-2xl font-bold text-ink [overflow-wrap:anywhere]">{list.title}</h1>
                {!list.is_public && <Badge tone="slate">{t('booklists.card.private')}</Badge>}
                {list.featured && <Badge tone="amber"><i className="fa-solid fa-star mr-1" aria-hidden="true" />{t('booklists.detail.featured')}</Badge>}
              </div>
              {list.description && <p className="mt-2 whitespace-pre-line text-sm leading-6 text-slate-600 [overflow-wrap:anywhere]">{list.description}</p>}
              <div className="mt-3 flex flex-wrap items-center gap-2 text-sm text-slate-500">
                {list.owner && <><UserAvatar user={list.owner} size="h-6 w-6" /><Link href={`/user/${encodeURIComponent(list.owner.username)}`} className="text-slate-700 hover:text-primary-600">{ownerName(list.owner)}</Link></>}
                <span>· {t('booklists.card.books', { n: list.item_count })}</span>
                <span>· {t('booklists.card.followers', { n: list.follower_count })}</span>
                <span>· {t('booklists.detail.updatedAt', { date: formatDate(list.updated_at).slice(0, 10) })}</span>
              </div>
            </div>
            <div className="flex shrink-0 gap-2">
              {user?.role === 'admin' && list.is_public && (
                <Button variant="outline" onClick={toggleFeatured} loading={busy === 'featured'} aria-pressed={Boolean(list.featured)}>
                  <i className={`${list.featured ? 'fa-solid text-amber-500' : 'fa-regular'} fa-star`} aria-hidden="true" />
                  {t(list.featured ? 'booklists.detail.unfeature' : 'booklists.detail.feature')}
                </Button>
              )}
              {mine ? (
                <>
                  <Button variant="outline" onClick={() => setEditing(true)}><i className="fa-solid fa-pen" aria-hidden="true" />{t('booklists.detail.edit')}</Button>
                  <Button variant="danger" onClick={() => void remove()} loading={busy === 'delete'}>
                    <i className="fa-solid fa-trash" aria-hidden="true" />{t('common.actions.delete')}
                  </Button>
                </>
              ) : (
                <Button variant={list.following ? 'outline' : undefined} onClick={toggleFollow} loading={busy === 'follow'} aria-pressed={list.following}>
                  <i className={`${list.following ? 'fa-solid' : 'fa-regular'} fa-bookmark`} aria-hidden="true" />
                  {t(list.following ? 'booklists.detail.following' : 'booklists.detail.follow')}
                </Button>
              )}
            </div>
          </div>
        </header>

        <section className="py-8">
          {mine && detail.hidden > 0 && (
            <p className="mb-4 rounded-lg bg-amber-50 px-4 py-2.5 text-sm text-amber-700">{t('booklists.detail.hidden', { n: detail.hidden })}</p>
          )}
          {items.length === 0 ? (
            <EmptyState>{t(mine ? 'booklists.detail.emptyMine' : 'booklists.detail.empty')}</EmptyState>
          ) : (
            <ol className="space-y-4">
              {items.map((it, i) => (
                <li key={it.book.id} className="rounded-2xl border border-slate-200 bg-white p-4 shadow-sm">
                  <div className="flex gap-3">
                    <span className="w-6 shrink-0 pt-1 text-center text-sm font-semibold text-slate-300">{i + 1}</span>
                    <div className="min-w-0 flex-1">
                      <BookCard book={it.book} view="list" showStatus={false} tagsMax={3} className="!border-0 !p-0 !shadow-none" />
                      {it.note && (
                        <p className="mt-3 rounded-lg bg-slate-50 px-3 py-2 text-sm text-slate-600 [overflow-wrap:anywhere]">
                          <i className="fa-solid fa-quote-left mr-1.5 text-xs text-slate-300" aria-hidden="true" />{it.note}
                        </p>
                      )}
                    </div>
                    {mine && (
                      <div className="flex shrink-0 flex-col gap-1">
                        <Tooltip content={t('booklists.detail.moveUp')}><Button variant="ghost" size="sm" aria-label={t('booklists.detail.moveUp')} disabled={i === 0 || busy !== null} loading={busy === `move-${it.book.id}--1`} onClick={() => move(i, -1)}><i className="fa-solid fa-arrow-up" aria-hidden="true" /></Button></Tooltip>
                        <Tooltip content={t('booklists.detail.moveDown')}><Button variant="ghost" size="sm" aria-label={t('booklists.detail.moveDown')} disabled={i === items.length - 1 || busy !== null} loading={busy === `move-${it.book.id}-1`} onClick={() => move(i, 1)}><i className="fa-solid fa-arrow-down" aria-hidden="true" /></Button></Tooltip>
                        <Tooltip content={t('booklists.detail.editNote')}><Button variant="ghost" size="sm" aria-label={t('booklists.detail.editNote')} disabled={busy !== null} loading={busy === `note-${it.book.id}`} onClick={() => void editNote(it.book.id, it.note)}><i className="fa-solid fa-comment-dots" aria-hidden="true" /></Button></Tooltip>
                        <Tooltip content={t('booklists.detail.removeItem')}><Button variant="ghost" size="sm" aria-label={t('booklists.detail.removeItem')} disabled={busy !== null} loading={busy === `remove-${it.book.id}`} onClick={() => removeItem(it.book.id)} className="text-rose-500"><i className="fa-solid fa-xmark" aria-hidden="true" /></Button></Tooltip>
                      </div>
                    )}
                  </div>
                </li>
              ))}
            </ol>
          )}
          {mine && <p className="mt-6 text-center text-xs text-slate-400">{t('booklists.detail.addHint')}</p>}
        </section>
      </Container>
      {editing && (
        <ListFormModal list={list} onClose={() => setEditing(false)}
          onSaved={(saved) => { setEditing(false); setDetail((d) => ({ ...d, list: { ...d.list, ...saved } })) }} />
      )}
    </>
  )
}
