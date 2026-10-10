import { useCallback, useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/router'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import IconPicker from '@/components/IconPicker'
import LocalizedFields, { type ResourceTranslations } from '@/components/LocalizedFields'
import LocalizedFormTabs, { type LocalizedFormTab } from '@/components/LocalizedFormTabs'
import ResourceIcon from '@/components/ResourceIcon'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { MAX_CATEGORY_DEPTH, categoryHref, categoryOptionLabel, flattenCategories, subtreeIds, treeHeight, type CategoryNode } from '@/lib/categories'
import type { Book, PageResult } from '@/lib/types'
import { useUrlPage } from '@/lib/use-url-page'
import { displayName } from '@/lib/users'
import { Badge, Button, Card, Checkbox, EmptyState, Field, Input, Loading, Modal, Pagination, SegmentedTabs, Select, useFeedback } from '@/components/ui'

type Tab = 'tree' | 'books'

interface TreeData { items: CategoryNode[]; uncategorized: number; max_depth: number }

interface FormState {
  id?: number
  translations: ResourceTranslations // 名称与简介的多语言内容
  slug: string
  icon_type: string
  icon_value: string
  parent_id: string
  sort_order: string
}

type AdminNode = CategoryNode & { translations?: ResourceTranslations }

const emptyForm = (defaultLocale: string, parent = ''): FormState => ({
  translations: { [defaultLocale]: { fields: {}, revision: 0, publish: true } },
  slug: '', icon_type: '', icon_value: '', parent_id: parent, sort_order: '0',
})

export default function AdminCategories() {
  return <FeatureGate feature="categories"><Inner /></FeatureGate>
}

// 书籍分类管理：维护最多三层的分类树（?tab=tree），按关键词与当前分类筛选书籍批量归类（?tab=books）。
function Inner() {
  const { t } = useTranslation()
  const router = useRouter()
  const { showToast } = useFeedback()
  const tab: Tab = router.query.tab === 'books' ? 'books' : 'tree'
  const [data, setData] = useState<TreeData | null>(null)

  const load = useCallback(() => {
    api<TreeData>('/admin/categories').then(setData)
      .catch((e) => showToast({ title: t('admin.categories.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [showToast, t])
  useEffect(() => { load() }, [load])

  return (
    <AdminLayout current="categories" breadcrumb={t('admin.nav.categories')}>
      <div>
        <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.categories')}</h1>
        <p className="mt-1.5 text-sm text-slate-500">{t('admin.categories.description')}</p>
      </div>
      <SegmentedTabs className="mt-6" value={tab} ariaLabel={t('admin.nav.categories')} items={[
        { value: 'tree', label: t('admin.categories.tab.tree'), href: '/admin/categories' },
        { value: 'books', label: t('admin.categories.tab.books'), href: '/admin/categories?tab=books' },
      ]} />
      <div className="mt-6">
        {!data ? <Loading className="py-16" /> : tab === 'tree' ? <TreePanel data={data} reload={load} /> : <BooksPanel tree={data.items} reload={load} />}
      </div>
    </AdminLayout>
  )
}

// —— 分类树 ——

function TreePanel({ data, reload }: { data: TreeData; reload: () => void }) {
  const { t, defaultLocale } = useTranslation()
  const [translationBusy, setTranslationBusy] = useState(false)
  const { showToast, confirmAction } = useFeedback()
  const [form, setForm] = useState<FormState | null>(null)
  const [formTab, setFormTab] = useState<LocalizedFormTab>('basic')
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState<number | null>(null)
  const rows = useMemo(() => flattenCategories(data.items), [data.items])
  const total = rows.length

  // 上级分类候选：排除自身及子孙，且移动后不超过最大层数
  const parentOptions = useMemo(() => {
    if (!form) return []
    const editing = form.id ? rows.find((r) => r.node.id === form.id)?.node : undefined
    const excluded = new Set(editing ? subtreeIds(editing) : [])
    const height = editing ? treeHeight(editing) : 1
    return rows.filter((r) => !excluded.has(r.node.id) && r.depth + 1 + height <= data.max_depth)
      .map((r) => ({ value: String(r.node.id), label: categoryOptionLabel(r.node.name, r.depth) }))
  }, [form, rows, data.max_depth])

  function edit(n: AdminNode) {
    setFormTab('basic')
    // 已有翻译默认不重新发布（与会员方案一致），只提交改动过的语言
    const translations = Object.fromEntries(Object.entries(n.translations || {}).map(([code, entry]) => [code, { ...entry, publish: false }]))
    setForm({ id: n.id, translations, slug: n.slug, icon_type: n.icon_type, icon_value: n.icon_value, parent_id: n.parent_id ? String(n.parent_id) : '', sort_order: String(n.sort_order) })
  }

  async function save() {
    if (!form) return
    // 名称在「国际化信息」中按语言填写：默认语言没有名称时切过去提示
    if (!form.translations[defaultLocale]?.fields.name?.trim()) {
      setFormTab('i18n')
      showToast({ message: t('admin.categories.nameRequired'), tone: 'error' })
      return
    }
    setSaving(true)
    try {
      const body = { slug: form.slug.trim(), icon_type: form.icon_type, icon_value: form.icon_value, parent_id: Number(form.parent_id) || 0, sort_order: Number(form.sort_order) || 0,
        translations: Object.fromEntries(Object.entries(form.translations).filter(([, entry]) => entry.dirty)) }
      await api(form.id ? `/admin/categories/${form.id}` : '/admin/categories', { method: form.id ? 'PUT' : 'POST', body })
      showToast({ message: form.id ? t('admin.categories.updated') : t('admin.categories.created'), tone: 'success' })
      setForm(null)
      reload()
    } catch (err) {
      showToast({ title: t('admin.categories.saveFailed'), message: (err as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  async function remove(n: CategoryNode) {
    if (!await confirmAction({ title: t('admin.categories.deleteTitle'), message: t('admin.categories.deleteMessage', { name: n.name }), confirmLabel: t('admin.categories.delete'), danger: true })) return
    setDeleting(n.id)
    try {
      await api(`/admin/categories/${n.id}`, { method: 'DELETE' })
      showToast({ message: t('admin.categories.deleted'), tone: 'success' })
      reload()
    } catch (err) {
      showToast({ title: t('admin.categories.deleteFailed'), message: (err as Error).message, tone: 'error' })
    } finally {
      setDeleting(null)
    }
  }

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <p className="min-w-0 flex-1 text-sm text-slate-500">
          {t('admin.categories.summary', { count: total, depth: data.max_depth })}
          {data.uncategorized > 0 && <> · <Link href="/admin/categories?tab=books&category=none" className="text-primary-600 hover:underline">{t('admin.categories.uncategorized', { n: data.uncategorized })}</Link></>}
        </p>
        <Button onClick={() => { setFormTab('basic'); setForm(emptyForm(defaultLocale)) }} data-testid="category-create"><i className="fa-solid fa-plus" aria-hidden="true" /> {t('admin.categories.create')}</Button>
      </div>
      {rows.length === 0 ? <EmptyState>{t('admin.categories.empty')}</EmptyState> : (
        <Card className="overflow-x-auto">
          <table className="w-full min-w-[640px] text-sm">
            <thead className="bg-slate-50 text-left text-xs text-slate-500">
              <tr>
                <th className="px-4 py-3">{t('admin.categories.col.name')}</th>
                <th className="px-4 py-3">{t('admin.categories.col.slug')}</th>
                <th className="px-4 py-3">{t('admin.categories.col.books')}</th>
                <th className="px-4 py-3" />
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {rows.map(({ node, depth }) => { const n = node as AdminNode; return (
                <tr key={n.id} data-testid="category-row">
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-2" style={{ paddingLeft: `${depth * 1.25}rem` }}>
                      {depth > 0 && <span className="text-slate-300">└</span>}
                      {n.icon_type ? <ResourceIcon iconType={n.icon_type} iconValue={n.icon_value} className="flex h-5 w-5 items-center justify-center text-slate-500" />
                        : <i className="fa-regular fa-folder text-slate-400" aria-hidden="true" />}
                      <span className="font-medium text-slate-900">{n.name}</span>
                    </div>
                  </td>
                  <td className="px-4 py-3"><Link href={categoryHref(n.slug)} className="font-mono text-xs text-slate-500 hover:text-primary-600">{n.slug}</Link></td>
                  <td className="px-4 py-3 tabular-nums text-slate-600">{n.book_count}</td>
                  <td className="whitespace-nowrap px-4 py-3 text-right">
                    {depth + 1 < data.max_depth && (
                      <Button size="sm" variant="ghost" onClick={() => { setFormTab('basic'); setForm(emptyForm(defaultLocale, String(n.id))) }}>{t('admin.categories.addChild')}</Button>
                    )}
                    <Button size="sm" variant="ghost" onClick={() => edit(n)}>{t('admin.categories.edit')}</Button>
                    <Button size="sm" variant="ghost" className="text-rose-600" loading={deleting === n.id} disabled={deleting !== null} onClick={() => void remove(n)}>{t('admin.categories.delete')}</Button>
                  </td>
                </tr>
              ) })}
            </tbody>
          </table>
        </Card>
      )}

      <Modal className="max-w-5xl" open={form !== null} onClose={() => setForm(null)} title={form?.id ? t('admin.categories.editTitle') : t('admin.categories.createTitle')}
        footer={<>
          <Button variant="outline" onClick={() => setForm(null)}>{t('common.actions.cancel')}</Button>
          <Button loading={saving} disabled={translationBusy} onClick={() => void save()} data-testid="category-save">{t('common.actions.save')}</Button>
        </>}>
        {form && (
          <div className="space-y-6">
            <LocalizedFormTabs value={formTab} onChange={setFormTab} translations={form.translations} ariaLabel={t('admin.categories.createTitle')} />
            <div className={formTab === 'basic' ? 'space-y-5' : 'hidden'}>
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label={t('admin.categories.field.parent')} hint={t('admin.categories.field.parentHint', { depth: MAX_CATEGORY_DEPTH })}>
                  <Select searchable value={form.parent_id} onChange={(v) => setForm({ ...form, parent_id: v })}
                    options={[{ value: '', label: t('admin.categories.field.root') }, ...parentOptions]} />
                </Field>
                <Field label={t('admin.categories.field.slug')} hint={t('admin.categories.field.slugHint')}>
                  <Input value={form.slug} maxLength={60} onChange={(e) => setForm({ ...form, slug: e.target.value })} placeholder="programming" />
                </Field>
                <Field label={t('admin.categories.field.icon')}>
                  <IconPicker value={{ icon_type: form.icon_type, icon_value: form.icon_value }} onChange={(v) => setForm({ ...form, icon_type: v.icon_type, icon_value: v.icon_value })} fallback="fa-folder" />
                </Field>
                <Field label={t('admin.categories.field.sortOrder')} hint={t('admin.categories.field.sortOrderHint')}>
                  <Input type="number" value={form.sort_order} onChange={(e) => setForm({ ...form, sort_order: e.target.value })} />
                </Field>
              </div>
            </div>
            <div className={formTab === 'i18n' ? 'space-y-3' : 'hidden'}>
              <p className="text-xs text-slate-500">{t('admin.categories.field.descriptionHint')}</p>
              <LocalizedFields value={form.translations} onBusyChange={setTranslationBusy} onChange={(translations) => setForm({ ...form, translations })} fields={[
                { key: 'name', label: t('admin.categories.field.name'), maxLength: 40 },
                { key: 'description', label: t('admin.categories.field.description'), maxLength: 300, multiline: true },
              ]} />
            </div>
          </div>
        )}
      </Modal>
    </>
  )
}

// —— 批量归类 ——

function BooksPanel({ tree, reload }: { tree: CategoryNode[]; reload: () => void }) {
  const { t } = useTranslation()
  const router = useRouter()
  const { showToast } = useFeedback()
  const [page, setPage] = useUrlPage()
  const filter = typeof router.query.category === 'string' ? router.query.category : ''
  const qParam = typeof router.query.q === 'string' ? router.query.q : ''
  const [q, setQ] = useState(qParam)
  const [data, setData] = useState<PageResult<Book> | null>(null)
  const [loading, setLoading] = useState(true)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [target, setTarget] = useState('')
  const [assigning, setAssigning] = useState(false)
  const options = useMemo(() => flattenCategories(tree).map(({ node, depth }) => ({ value: String(node.id), label: categoryOptionLabel(node.name, depth) })), [tree])

  const load = useCallback(() => {
    setLoading(true)
    api<PageResult<Book>>('/admin/category-books', { params: { page, page_size: 20, q: qParam, category: filter } })
      .then((r) => { setData(r); setSelected(new Set()) })
      .catch((e) => showToast({ title: t('admin.categories.loadFailed'), message: (e as Error).message, tone: 'error' }))
      .finally(() => setLoading(false))
  }, [page, qParam, filter, showToast, t])
  useEffect(() => { load() }, [load])

  // 筛选条件写入 URL（可分享、刷新保持），并回到第一页
  function setQuery(patch: Record<string, string>) {
    const next: Record<string, string> = { tab: 'books' }
    const merged = { q: qParam, category: filter, ...patch }
    for (const [k, v] of Object.entries(merged)) if (v) next[k] = v
    void router.push({ pathname: router.pathname, query: next }, undefined, { shallow: true })
  }

  async function assign() {
    if (selected.size === 0) return
    setAssigning(true)
    try {
      const r = await api<{ updated: number }>('/admin/category-books/assign', { method: 'POST', body: { book_ids: [...selected], category_id: target === 'none' ? 0 : Number(target) } })
      showToast({ message: t('admin.categories.assigned', { n: r.updated }), tone: 'success' })
      load()
      reload()
    } catch (e) {
      showToast({ title: t('admin.categories.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setAssigning(false)
    }
  }

  const items = data?.items || []
  const allChecked = items.length > 0 && items.every((b) => selected.has(b.id))
  const toggle = (id: number, on: boolean) => setSelected((prev) => {
    const next = new Set(prev)
    if (on) next.add(id); else next.delete(id)
    return next
  })

  return (
    <>
      <div className="mb-4 flex flex-col gap-2 sm:flex-row">
        <form className="flex flex-1 gap-2" onSubmit={(e) => { e.preventDefault(); setQuery({ q: q.trim() }) }}>
          <Input className="flex-1" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('admin.categories.books.search')} />
          <Button type="submit" variant="outline" loading={loading && q.trim() !== qParam}>{t('admin.categories.books.searchButton')}</Button>
        </form>
        <Select className="sm:w-56" value={filter} onChange={(v) => setQuery({ category: v })} searchable
          options={[{ value: '', label: t('admin.categories.books.all') }, { value: 'none', label: t('admin.categories.books.none') }, ...options]} />
      </div>

      <div className="mb-3 flex flex-wrap items-center gap-2 rounded-xl border border-slate-200 bg-white px-4 py-3 text-sm" data-testid="category-assign-bar">
        <span className="text-slate-600">{t('admin.categories.books.selected', { n: selected.size })}</span>
        <Select size="sm" className="min-w-0 flex-1 sm:max-w-xs" value={target} onChange={setTarget} searchable placeholder={t('admin.categories.books.target')}
          options={[{ value: 'none', label: t('admin.categories.books.setNone') }, ...options]} />
        <Button size="sm" loading={assigning} disabled={assigning || selected.size === 0 || !target} onClick={() => void assign()} data-testid="category-assign">
          {t('admin.categories.books.assign')}
        </Button>
      </div>

      {!data ? <Loading className="py-16" /> : items.length === 0 ? <EmptyState>{t('admin.categories.books.empty')}</EmptyState> : (
        <>
          <Card className="overflow-x-auto">
            <table className="w-full min-w-[720px] text-sm">
              <thead className="bg-slate-50 text-left text-xs text-slate-500">
                <tr>
                  <th className="w-10 px-4 py-3">
                    <Checkbox checked={allChecked} ariaLabel={t('admin.categories.books.selectAll')}
                      onChange={(on) => setSelected(on ? new Set(items.map((b) => b.id)) : new Set())} />
                  </th>
                  <th className="px-4 py-3">{t('admin.categories.books.col.book')}</th>
                  <th className="px-4 py-3">{t('admin.categories.books.col.author')}</th>
                  <th className="px-4 py-3">{t('admin.categories.books.col.category')}</th>
                  <th className="px-4 py-3">{t('admin.categories.books.col.updated')}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {items.map((b) => (
                  <tr key={b.id} data-testid="category-book-row">
                    <td className="px-4 py-3"><Checkbox checked={selected.has(b.id)} ariaLabel={b.title} onChange={(on) => toggle(b.id, on)} /></td>
                    <td className="px-4 py-3">
                      <Link href={`/book/detail/${encodeURIComponent(b.slug)}`} className="font-medium text-slate-900 hover:text-primary-600">{b.title}</Link>
                      {!b.is_public && <span className="ml-2"><Badge tone="slate">{t('admin.categories.books.private')}</Badge></span>}
                    </td>
                    <td className="px-4 py-3 text-slate-600">{b.user ? displayName(b.user) : '-'}</td>
                    <td className="px-4 py-3 text-slate-600">
                      {b.category ? (b.category.path || [b.category]).map((c) => c.name).join(' › ') : <span className="text-slate-400">{t('admin.categories.books.none')}</span>}
                    </td>
                    <td className="px-4 py-3 text-slate-500">{formatDate(b.updated_at).slice(0, 10)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>
          <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} loading={loading} />
        </>
      )}
    </>
  )
}
