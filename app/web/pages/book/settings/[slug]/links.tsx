import { useEffect, useMemo, useState } from 'react'
import type { InferGetServerSidePropsType } from 'next'
import Link from 'next/link'
import BookSettingsLayout from '@/components/BookSettingsLayout'
import { requireBookSettingsFeature } from '@/lib/book-settings'
import { api } from '@/lib/api'
import type { LinkGraph, LinkGraphNode } from '@/lib/backlinks'
import { useTranslation } from '@/lib/i18n'
import { Badge, EmptyState, Loading } from '@/components/ui'

export const getServerSideProps = requireBookSettingsFeature('backlinks')

// 书籍设置 · 章节链接：本书章节之间的 [[双向链接]]（含草稿）、失效链接，以及没有任何链接的章节数。
export default function BookLinksPage({ book }: InferGetServerSidePropsType<typeof getServerSideProps>) {
  const { t } = useTranslation()
  const [graph, setGraph] = useState<LinkGraph | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    setGraph(null)
    setError('')
    api<LinkGraph>(`/backlinks/books/${book.id}/graph`).then(setGraph).catch((e) => setError((e as Error).message))
  }, [book.id])

  const byId = useMemo(() => new Map((graph?.nodes || []).map((n) => [n.id, n])), [graph])
  const linked = useMemo(() => treeOrder(graph?.nodes || []).filter((n) => n.outgoing.length > 0 || n.incoming.length > 0), [graph])
  const isolated = (graph?.nodes.length || 0) - linked.length
  const writer = (n: LinkGraphNode) => `/book/writer/${encodeURIComponent(book.slug)}/${encodeURIComponent(n.slug)}`

  const chips = (ids: number[]) => (
    <span className="flex flex-wrap gap-1.5">
      {ids.map((id) => {
        const n = byId.get(id)
        return n ? (
          <Link key={id} href={writer(n)} className="rounded-md bg-slate-100 px-2 py-0.5 text-xs text-slate-600 hover:bg-primary-50 hover:text-primary-700">{n.title}</Link>
        ) : null
      })}
    </span>
  )

  return (
    <BookSettingsLayout book={book} active="links">
      <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
        <h1 className="text-xl font-bold text-slate-900">{t('backlinks.settings.heading')}</h1>
        <p className="mt-1 text-sm text-slate-500">{t('backlinks.settings.subheading')}</p>
        {error ? <p className="mt-6 text-sm text-rose-600">{error}</p> : !graph ? <Loading className="py-10" /> : (
          <div className="mt-6 space-y-8">
            <div className="grid gap-3 sm:grid-cols-3">
              <Stat label={t('backlinks.settings.edges')} value={graph.edges} />
              <Stat label={t('backlinks.settings.broken')} value={graph.broken.length} warn={graph.broken.length > 0} />
              <Stat label={t('backlinks.settings.isolated')} value={isolated} />
            </div>

            {graph.broken.length > 0 && (
              <section>
                <h2 className="text-sm font-semibold text-slate-900">{t('backlinks.settings.brokenTitle')}</h2>
                <p className="mt-1 text-xs text-slate-500">{t('backlinks.settings.brokenHint')}</p>
                <ul className="mt-3 divide-y divide-slate-100 rounded-xl border border-slate-200">
                  {graph.broken.map((b, i) => {
                    const from = byId.get(b.from)
                    return (
                      <li key={`${b.from}-${i}`} className="flex flex-wrap items-center gap-2 px-4 py-2.5 text-sm">
                        {from && <Link href={writer(from)} className="font-medium text-slate-700 hover:text-primary-600">{from.title}</Link>}
                        <i className="fa-solid fa-arrow-right text-xs text-slate-300" aria-hidden="true" />
                        <code className="rounded bg-amber-50 px-1.5 py-0.5 text-xs text-amber-700">[[{b.target}]]</code>
                      </li>
                    )
                  })}
                </ul>
              </section>
            )}

            <section>
              <h2 className="text-sm font-semibold text-slate-900">{t('backlinks.settings.chaptersTitle')}</h2>
              {linked.length === 0 ? (
                <div className="mt-3">
                  <EmptyState>
                    <p>{t('backlinks.settings.empty')}</p>
                    <p className="mt-1 text-xs">{t('backlinks.settings.emptyHint')}</p>
                  </EmptyState>
                </div>
              ) : (
                <ul className="mt-3 divide-y divide-slate-100 rounded-xl border border-slate-200">
                  {linked.map((n) => (
                    <li key={n.id} className="space-y-2 px-4 py-3">
                      <div className="flex flex-wrap items-center gap-2">
                        <Link href={writer(n)} className="text-sm font-medium text-slate-800 hover:text-primary-600">{n.title}</Link>
                        {n.status !== 'published' && <Badge tone="slate">{t('backlinks.settings.draft')}</Badge>}
                        {n.external > 0 && <span className="text-xs text-slate-400">{t('backlinks.settings.external', { n: n.external })}</span>}
                      </div>
                      {n.outgoing.length > 0 && (
                        <div className="flex gap-2 text-xs text-slate-500">
                          <span className="shrink-0 pt-0.5">{t('backlinks.settings.linksTo')}</span>{chips(n.outgoing)}
                        </div>
                      )}
                      {n.incoming.length > 0 && (
                        <div className="flex gap-2 text-xs text-slate-500">
                          <span className="shrink-0 pt-0.5">{t('backlinks.settings.linkedFrom')}</span>{chips(n.incoming)}
                        </div>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </div>
        )}
      </div>
    </BookSettingsLayout>
  )
}

// treeOrder 按目录顺序排列章节（父章节在前，子章节紧随其后）。
function treeOrder(nodes: LinkGraphNode[]): LinkGraphNode[] {
  const ids = new Set(nodes.map((n) => n.id))
  const children = new Map<number, LinkGraphNode[]>()
  const roots: LinkGraphNode[] = []
  for (const n of nodes) {
    if (n.parent_id && ids.has(n.parent_id)) children.set(n.parent_id, [...(children.get(n.parent_id) || []), n])
    else roots.push(n)
  }
  const walk = (list: LinkGraphNode[]): LinkGraphNode[] => list.flatMap((n) => [n, ...walk(children.get(n.id) || [])])
  return walk(roots)
}

function Stat({ label, value, warn }: { label: string; value: number; warn?: boolean }) {
  return (
    <div className="rounded-xl border border-slate-200 p-4">
      <div className="text-xs text-slate-500">{label}</div>
      <div className={`mt-1 text-2xl font-bold ${warn ? 'text-amber-600' : 'text-slate-900'}`}>{value}</div>
    </div>
  )
}
