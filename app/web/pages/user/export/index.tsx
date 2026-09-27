import { useEffect, useState } from 'react'
import Seo from '@/components/Seo'
import Link from 'next/link'
import Container from '@/components/Container'
import { api, API_BASE, getToken } from '@/lib/api'
import { useRequireAuth, useApp } from '@/lib/auth'
import { entitlementAllowed } from '@/lib/entitlements'
import { useTranslation } from '@/lib/i18n'
import { Button, Field, Select, Switch, Input, Loading, Checkbox, EmptyState, useFeedback } from '@/components/ui'
import AccountSettingsLayout from '@/components/AccountSettingsLayout'
import { DownloadIcon } from '@/components/icons'
import type { Book, PageResult } from '@/lib/types'

function BatchExportPanel() {
  const { showToast } = useFeedback()
  const { t } = useTranslation()
  const [books, setBooks] = useState<Book[] | null>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api<PageResult<Book>>('/books', { params: { scope: 'owned', page_size: 100 } })
      .then((d) => {
        const items = d.items || []
        setBooks(items)
        setSelected(new Set(items.map((b) => b.id)))
      })
      .catch(() => setBooks([]))
  }, [])

  const allSelected = books !== null && books.length > 0 && selected.size === books.length

  function toggle(id: number) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  async function download() {
    if (selected.size === 0) return
    setBusy(true)
    try {
      const ids = Array.from(selected).join(',')
      const token = getToken()
      const res = await fetch(`${API_BASE}/api/v1/users/me/export/books?ids=${ids}`, {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      })
      if (!res.ok) {
        const msg = await res.json().then((p) => p.message).catch(() => '')
        throw new Error(msg || t('user.export.exportFailed'))
      }
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `books-export-${new Date().toISOString().slice(0, 10)}.zip`
      a.click()
      URL.revokeObjectURL(url)
    } catch (e) {
      showToast({ title: t('user.export.exportFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mb-6 rounded-2xl border border-slate-200 bg-white shadow-sm">
      <div className="border-b border-slate-100 p-6">
        <h2 className="text-xl font-bold text-slate-900">{t('user.export.batchExport')}</h2>
        <p className="mt-1 text-sm text-slate-500">{t('user.export.batchExportHint')}</p>
      </div>
      {books === null ? (
        <Loading className="py-16" label={t('user.export.loadingBooks')} />
      ) : books.length === 0 ? (
        <EmptyState>{t('user.export.noBooks')}</EmptyState>
      ) : (
        <>
          <div className="flex items-center justify-between border-b border-slate-100 px-6 py-3">
            <button type="button" onClick={() => setSelected(allSelected ? new Set() : new Set(books.map((b) => b.id)))}
              className="text-sm text-primary-600 hover:underline">
              {allSelected ? t('user.export.clearSelection') : t('user.export.selectAll')}
            </button>
            <span className="text-xs text-slate-400">{t('user.export.selected', { count: selected.size, total: books.length })}</span>
          </div>
          <ul className="max-h-72 divide-y divide-slate-50 overflow-y-auto">
            {books.map((book) => {
              const STATUS_LABELS: Record<string, string> = {
                draft: t('user.export.statusDraft'),
                in_progress: t('user.export.statusInProgress'),
                published: t('user.export.statusPublished'),
                completed: t('user.export.statusCompleted'),
                archived: t('user.export.statusArchived'),
              }
              return (
                <li key={book.id}>
                  <label className="flex cursor-pointer items-center gap-3 px-6 py-3 hover:bg-slate-50">
                    <Checkbox checked={selected.has(book.id)} onChange={() => toggle(book.id)} ariaLabel={t('user.export.selectBook', { title: book.title })} />
                    <span className="min-w-0 flex-1 truncate text-sm font-medium text-slate-700">{book.title}</span>
                    <span className="shrink-0 rounded px-1.5 py-0.5 text-xs text-slate-400">{STATUS_LABELS[book.status] || book.status}</span>
                  </label>
                </li>
              )
            })}
          </ul>
          <div className="flex justify-end border-t border-slate-100 px-6 py-4">
            <Button loading={busy} disabled={selected.size === 0} onClick={download}>
              <DownloadIcon className="h-4 w-4" /> {t('user.export.exportSelected', { count: selected.size })}
            </Button>
          </div>
        </>
      )}
    </div>
  )
}

interface ExportSettings {
  page_size: string
  include_cover: boolean
  include_toc: boolean
  font_size: number
  code_theme: string
  margin: string
  footer: string
}

const DEFAULTS: ExportSettings = { page_size: 'A4', include_cover: true, include_toc: true, font_size: 15, code_theme: 'light', margin: 'normal', footer: '' }

export default function ExportSettingsPage() {
  const { site, user: me } = useApp()
  const customFooter = entitlementAllowed(me, 'export.custom_footer', true) // 自定义页脚为权益
  const siteName = site.site_name || 'KnowForge'
  const user = useRequireAuth()
  const { showToast } = useFeedback()
  const { t } = useTranslation()
  const [s, setS] = useState<ExportSettings>(DEFAULTS)
  const [loaded, setLoaded] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!user) return
    api<ExportSettings>('/auth/export-settings').then((d) => setS({ ...DEFAULTS, ...d })).catch(() => {}).finally(() => setLoaded(true))
  }, [user])

  if (!user) return <Loading className="min-h-[60vh]" label={t('user.export.loading')} />

  async function save() {
    setSaving(true)
    try {
      await api('/auth/export-settings', { method: 'PUT', body: s })
      showToast({ title: t('user.export.saved'), message: t('user.export.savedMessage'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('user.export.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <Seo siteName={siteName} title={t('user.export.title')} noindex />
      <Container>
        <nav className="flex items-center gap-1.5 py-4 text-sm text-slate-500">
          <Link href="/" className="hover:text-primary-600">{t('user.export.home')}</Link>
          <span className="text-slate-300">/</span>
          <span className="text-slate-900">{t('user.export.title')}</span>
        </nav>
        <div className="pb-6">
          <h1 className="text-3xl font-bold text-ink">{t('user.export.title')}</h1>
          <p className="mt-2 text-[15px] text-slate-500">{t('user.export.description')}</p>
        </div>

        <AccountSettingsLayout user={user} active="export">
          <BatchExportPanel />
          <div className="rounded-2xl border border-slate-200 bg-white shadow-sm">
            <div className="border-b border-slate-100 p-6">
              <h2 className="text-xl font-bold text-slate-900">{t('user.export.exportSettings')}</h2>
              <p className="mt-1 text-sm text-slate-500">{t('user.export.exportSettingsHint')}</p>
            </div>
            {!loaded ? (
              <Loading className="py-16" label={t('user.export.loadingSettings')} />
            ) : (
              <div className="space-y-5 p-6">
                <div className="grid grid-cols-1 gap-5 sm:grid-cols-2">
                  <Field label={t('user.export.pageSize')}>
                    <Select value={s.page_size} onChange={(v) => setS({ ...s, page_size: v })}
                      options={[{ value: 'A4', label: 'A4' }, { value: 'Letter', label: 'Letter' }]} />
                  </Field>
                  <Field label={t('user.export.margin')}>
                    <Select value={s.margin} onChange={(v) => setS({ ...s, margin: v })}
                      options={[{ value: 'narrow', label: t('user.export.marginNarrow') }, { value: 'normal', label: t('user.export.marginNormal') }, { value: 'wide', label: t('user.export.marginWide') }]} />
                  </Field>
                  <Field label={t('user.export.fontSize')} hint={t('user.export.fontSizeHint')}>
                    <Select value={String(s.font_size)} onChange={(v) => setS({ ...s, font_size: Number(v) })}
                      options={[12, 13, 14, 15, 16, 17, 18, 20].map((n) => ({ value: String(n), label: `${n} px` }))} />
                  </Field>
                  <Field label={t('user.export.codeTheme')}>
                    <Select value={s.code_theme} onChange={(v) => setS({ ...s, code_theme: v })}
                      options={[{ value: 'light', label: t('user.export.themeLight') }, { value: 'dark', label: t('user.export.themeDark') }]} />
                  </Field>
                </div>
                <div className="flex items-center justify-between gap-4 rounded-xl border border-slate-200 p-4">
                  <div><div className="text-sm font-medium text-slate-900">{t('user.export.includeCover')}</div><p className="mt-1 text-xs text-slate-500">{t('user.export.includeCoverHint')}</p></div>
                  <Switch checked={s.include_cover} onChange={(v) => setS({ ...s, include_cover: v })} ariaLabel={t('user.export.includeCover')} />
                </div>
                <div className="flex items-center justify-between gap-4 rounded-xl border border-slate-200 p-4">
                  <div><div className="text-sm font-medium text-slate-900">{t('user.export.includeToc')}</div><p className="mt-1 text-xs text-slate-500">{t('user.export.includeTocHint')}</p></div>
                  <Switch checked={s.include_toc} onChange={(v) => setS({ ...s, include_toc: v })} ariaLabel={t('user.export.includeToc')} />
                </div>
                <Field label={t('user.export.footer')} hint={customFooter ? t('user.export.footerHint', { siteName }) : t('export.footer.notIncluded', { siteName })}>
                  <Input value={s.footer} maxLength={100} placeholder={`Powered by ${siteName}`} disabled={!customFooter}
                    onChange={(e) => setS({ ...s, footer: e.target.value })} />
                </Field>
              </div>
            )}
            <div className="flex justify-end border-t border-slate-100 px-6 py-4">
              <Button loading={saving} onClick={save}>{t('user.export.saveSettings')}</Button>
            </div>
          </div>
        </AccountSettingsLayout>
      </Container>
    </>
  )
}
