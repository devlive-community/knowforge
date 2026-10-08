import { useCallback, useEffect, useState } from 'react'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import ResourceIcon from '@/components/ResourceIcon'
import IconPicker from '@/components/IconPicker'
import { api } from '@/lib/api'
import { Badge, Button, Card, EmptyState, Field, Input, Loading, Modal, Pagination, useFeedback } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'
import type { PageResult } from '@/lib/types'
import { useUrlPage } from '@/lib/use-url-page'

interface Tag {
  id: number
  name: string
  slug: string
  icon_type?: string
  icon_value?: string
  book_count: number
}

interface FormState { id?: number; name: string; icon_type: string; icon_value: string }
const emptyForm = (): FormState => ({ name: '', icon_type: '', icon_value: '' })

export default function AdminTags() {
  const { t } = useTranslation()
  const { showToast, confirmAction } = useFeedback()
  const [data, setData] = useState<PageResult<Tag> | null>(null)
  const [page, setPage] = useUrlPage()
  const [q, setQ] = useState('')
  const [search, setSearch] = useState('')
  const [loading, setLoading] = useState(true)
  const [form, setForm] = useState<FormState | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    api<PageResult<Tag>>('/admin/tags', { params: { page, page_size: 20, q: search } })
      .then(setData)
      .catch((e) => showToast({ title: t('admin.tags.loadFailed'), message: (e as Error).message, tone: 'error' }))
      .finally(() => setLoading(false))
  }, [page, search, showToast, t])
  useEffect(() => { load() }, [load])

  async function save() {
    if (!form || !form.name.trim()) return
    setSaving(true)
    try {
      const path = form.id ? `/admin/tags/${form.id}` : '/admin/tags'
      await api(path, { method: form.id ? 'PUT' : 'POST', body: { name: form.name.trim(), icon_type: form.icon_type, icon_value: form.icon_value } })
      setForm(null)
      load()
      showToast({ message: form.id ? t('admin.tags.updated') : t('admin.tags.created'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.tags.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  async function remove(tag: Tag) {
    if (!(await confirmAction({ title: t('admin.tags.deleteTitle'), message: t('admin.tags.deleteMessage', { name: tag.name }), confirmLabel: t('admin.tags.deleteConfirm'), danger: true }))) return
    try {
      await api(`/admin/tags/${tag.id}`, { method: 'DELETE' })
      load()
      showToast({ message: t('admin.tags.deleted'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.tags.saveFailed'), message: (e as Error).message, tone: 'error' })
    }
  }

  return (
    <FeatureGate feature="tags">
    <AdminLayout current="tags" breadcrumb={t('admin.nav.tags')}>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.tags')}</h1>
          <p className="mt-1.5 text-sm text-slate-500">{t('admin.tags.description')}</p>
        </div>
        <Button onClick={() => setForm(emptyForm())}><i className="fa-solid fa-plus" aria-hidden="true" /> {t('admin.tags.create')}</Button>
      </div>

      <form className="mt-6 flex max-w-md items-center gap-2" onSubmit={(e) => { e.preventDefault(); setPage(1); setSearch(q.trim()) }}>
        <div className="min-w-0 flex-1"><Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('admin.tags.searchPlaceholder')} /></div>
        <Button type="submit" variant="outline" className="shrink-0 whitespace-nowrap">{t('common.actions.search')}</Button>
      </form>

      <div className="mt-6">
        {loading || data === null ? (
          <Loading className="py-16" label={t('admin.tags.loading')} />
        ) : data.total === 0 ? (
          <EmptyState>{t('admin.tags.empty')}</EmptyState>
        ) : (
          <>
            <Card className="overflow-hidden">
              <div className="overflow-x-auto">
                <table className="min-w-[640px] w-full text-sm">
                  <thead className="bg-slate-50 text-left text-xs text-slate-500">
                    <tr>
                      <th className="px-5 py-3">{t('admin.tags.table.tag')}</th>
                      <th className="px-5 py-3">{t('admin.tags.table.slug')}</th>
                      <th className="px-5 py-3">{t('admin.tags.table.books')}</th>
                      <th className="px-5 py-3 text-right">{t('admin.tags.table.action')}</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-100">
                    {data.items.map((tag) => (
                      <tr key={tag.id}>
                        <td className="px-5 py-3">
                          <span className="flex items-center gap-2.5">
                            <ResourceIcon iconType={tag.icon_type} iconValue={tag.icon_value} name={tag.name}
                              className="flex h-8 w-8 shrink-0 items-center justify-center overflow-hidden rounded-lg border border-primary-100 bg-primary-50 text-primary-600" />
                            <span className="font-medium text-slate-800">{tag.name}</span>
                          </span>
                        </td>
                        <td className="px-5 py-3 font-mono text-xs text-slate-400">{tag.slug}</td>
                        <td className="px-5 py-3"><Badge>{t('admin.tags.bookCount', { count: tag.book_count })}</Badge></td>
                        <td className="px-5 py-3 text-right">
                          <span className="flex justify-end gap-2">
                            <Button variant="outline" size="sm" onClick={() => setForm({ id: tag.id, name: tag.name, icon_type: tag.icon_type || '', icon_value: tag.icon_value || '' })}>{t('admin.tags.edit')}</Button>
                            <Button variant="ghost" size="sm" className="text-rose-600" onClick={() => remove(tag)}>{t('admin.tags.delete')}</Button>
                          </span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </Card>
            <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} />
          </>
        )}
      </div>

      <Modal open={form !== null} onClose={() => setForm(null)}
        title={form?.id ? t('admin.tags.editTitle') : t('admin.tags.createTitle')}
        footer={<><Button variant="outline" onClick={() => setForm(null)}>{t('common.actions.cancel')}</Button><Button loading={saving} onClick={save}>{t('common.actions.save')}</Button></>}>
        {form && (
          <div className="space-y-5">
            <Field label={t('admin.tags.form.name')}>
              <Input value={form.name} maxLength={50} onChange={(e) => setForm({ ...form, name: e.target.value })} />
            </Field>
            <Field label={t('admin.tags.form.icon')} hint={t('admin.tags.form.iconHint')}>
              <IconPicker value={{ icon_type: form.icon_type, icon_value: form.icon_value }}
                onChange={(v) => setForm({ ...form, icon_type: v.icon_type, icon_value: v.icon_value })} fallback="fa-hashtag" />
            </Field>
          </div>
        )}
      </Modal>
    </AdminLayout>
    </FeatureGate>
  )
}
