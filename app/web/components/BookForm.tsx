import { useEffect, useRef, useState, ReactNode, KeyboardEvent } from 'react'
import { useRouter } from 'next/router'
import { API_BASE, getToken, api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Button, Input, Textarea, Select, Switch } from '@/components/ui'
import { BookIcon, CheckCircleIcon, CloseIcon, ImageIcon, LinkIcon, UploadIcon, EyeIcon } from '@/components/icons'
import type { Book, BookStatus } from '@/lib/types'
import { displayName } from '@/lib/users'

const MAX_TITLE = 60
const MAX_DESC = 1000
const MAX_TAGS = 10
const validSlug = (s: string) => /^[a-z0-9-]+$/.test(s)

const statusOptions = [
  { value: 'draft', labelKey: 'books.status.draft' },
  { value: 'in_progress', labelKey: 'books.status.in_progress' },
  { value: 'published', labelKey: 'books.status.published' },
  { value: 'completed', labelKey: 'books.status.completed' },
  { value: 'archived', labelKey: 'books.status.archived' },
]
const prefixOptions = [
  { value: '', labelKey: 'bookForm.prefix.none' },
  { value: '第', labelKey: 'bookForm.prefix.chapter' },
  { value: 'Chapter ', labelKey: 'bookForm.prefix.englishChapter' },
]

export interface BookFormProps {
  initial?: Book
  heading: string
  subheading: string
  breadcrumb: string
  submitLabel: string
  showSaveDraft?: boolean
  /** 是否渲染内置页头（面包屑+标题）；设置页由外层布局提供时置 false，仅保留操作按钮 */
  showHeader?: boolean
  /** 是否渲染"多语言与版本"区块；设置页拆分为独立 tab 时置 false */
  showLocalization?: boolean
  /** 页头与表单之间的额外内容（如创建页的「从模板开始」） */
  beforeForm?: ReactNode
  onSubmit: (payload: Record<string, unknown>) => Promise<void>
}

// 书籍表单：创建与设置页共用，双栏（分区表单 + 实时预览）
export default function BookForm({ initial, heading, subheading, breadcrumb, submitLabel, showSaveDraft, showHeader = true, showLocalization = true, beforeForm, onSubmit }: BookFormProps) {
  const router = useRouter()
  const { user, site } = useApp()
  const tagsEnabled = (site.feature_plugins || []).includes('tags')
  const transEnabled = (site.feature_plugins || []).includes('book-translations')
  const versionsEnabled = (site.feature_plugins || []).includes('book-versions')
  const { t } = useTranslation()
  const isEdit = !!initial

  const [title, setTitle] = useState(initial?.title || '')
  const [description, setDescription] = useState(initial?.description || '')
  const [coverImage, setCoverImage] = useState(initial?.cover_image || '')
  const [slug, setSlug] = useState(initial?.slug || '')
  const [status, setStatus] = useState<BookStatus>(initial?.status || 'draft')
  const [isPublic, setIsPublic] = useState(initial?.is_public || false)
  const [loginRequired, setLoginRequired] = useState(initial?.login_required || false)
  const [chapterPrefix, setChapterPrefix] = useState(initial?.chapter_prefix || '')
  const [childStatusFollowParent, setChildStatusFollowParent] = useState(initial?.child_status_follow_parent === true)
  const [defaultChapterStatus, setDefaultChapterStatus] = useState(initial?.default_chapter_status || 'draft')
  const [language, setLanguage] = useState(initial?.language || '')
  const [transGroup, setTransGroup] = useState(initial?.trans_group || '')
  const [version, setVersion] = useState(initial?.version || '')
  const [versionGroup, setVersionGroup] = useState(initial?.version_group || '')
  // 水印配置已迁移到独立 tab；此处仅从 initial 透传，随基础设置一起原样提交，不再在此编辑。
  const watermarkEnabled = initial?.watermark_enabled || false
  const watermarkText = initial?.watermark_text || ''
  const [tags, setTags] = useState<string[]>((initial?.tags || []).map((t) => t.name))
  const [tagInput, setTagInput] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [host, setHost] = useState('')
  const [showPreview, setShowPreview] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)

  useEffect(() => { setHost(window.location.host) }, [])

  const coverSrc = coverImage ? (/^https?:\/\//.test(coverImage) ? coverImage : API_BASE + coverImage) : ''
  const authorName = displayName(user) || t('bookForm.you')
  const authorAvatar = user?.avatar ? (/^https?:\/\//.test(user.avatar) ? user.avatar : API_BASE + user.avatar) : ''

  function addTagValue(raw: string) {
    const tag = raw.trim()
    if (!tag || tags.includes(tag) || tags.length >= MAX_TAGS) { setTagInput(''); return }
    setTags([...tags, tag]); setTagInput('')
    setTagSuggest([])
  }
  function addTag() { addTagValue(tagInput) }
  function onTagKey(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') { e.preventDefault(); addTag() }
    else if (e.key === 'Backspace' && !tagInput && tags.length) setTags(tags.slice(0, -1))
  }

  // 标签检索：输入时查现有标签，供作者直接引用（后端 /tags?q=，防抖；排除已选）。
  const [tagSuggest, setTagSuggest] = useState<{ name: string; book_count?: number }[]>([])
  useEffect(() => {
    if (!tagsEnabled) return
    const q = tagInput.trim()
    if (!q) { setTagSuggest([]); return }
    const h = setTimeout(() => {
      api<{ name: string; book_count?: number }[]>('/tags', { params: { q, limit: 8 } })
        .then((list) => setTagSuggest((list || []).filter((it) => !tags.includes(it.name))))
        .catch(() => setTagSuggest([]))
    }, 250)
    return () => clearTimeout(h)
  }, [tagInput, tags, tagsEnabled])

  async function uploadCover(file: File | undefined) {
    if (!file) return
    setUploading(true); setError('')
    try {
      const fd = new FormData()
      fd.append('file', file)
      const res = await fetch(`${API_BASE}/api/v1/upload`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${getToken()}` },
        body: fd,
      })
      const payload = await res.json().catch(() => ({}))
      if (!res.ok || payload.success === false) throw new Error(payload.message || t('common.uploadFailed'))
      setCoverImage(payload.data.url)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setUploading(false)
    }
  }

  async function submit(overrideStatus?: BookStatus) {
    setError('')
    if (!title.trim()) { setError(t('bookForm.error.title')); return }
    if (slug && !validSlug(slug)) { setError(t('bookForm.error.slug')); return }
    if (watermarkEnabled && !watermarkText.trim()) { setError(t('bookForm.error.watermark')); return }
    setSaving(true)
    try {
      await onSubmit({
        title: title.trim(),
        description,
        cover_image: coverImage,
        slug: slug || undefined,
        status: overrideStatus ?? status,
        is_public: isPublic,
        login_required: isPublic && loginRequired,
        chapter_prefix: chapterPrefix,
        child_status_follow_parent: childStatusFollowParent,
      default_chapter_status: defaultChapterStatus,
        language: language.trim(),
        trans_group: transGroup.trim(),
        version: version.trim(),
        version_group: versionGroup.trim(),
        watermark_enabled: watermarkEnabled,
        watermark_text: watermarkText.trim(),
        tags,
      })
    } catch (err) {
      setError((err as Error).message)
      setSaving(false)
    }
  }

  return (
    <div>
      {/* 页头：仅创建页显示；设置页标题由外层布局提供 */}
      {showHeader && (
        <div className="mb-6">
          <nav className="mb-1 flex items-center gap-1.5 text-sm text-slate-400">
            <button onClick={() => router.push('/books')} className="hover:text-primary-600">{t('bookForm.breadcrumb')}</button>
            <span>/</span>
            <span className="text-slate-500">{breadcrumb}</span>
          </nav>
          <h1 className="text-2xl font-bold text-slate-900">{heading}</h1>
          <p className="mt-1 text-sm text-slate-500">{subheading}</p>
        </div>
      )}

      {beforeForm}

      {error && <div className="mb-4 rounded-lg bg-rose-50 px-4 py-3 text-sm text-rose-600">{error}</div>}

      {/* 分区表单（整宽；实时预览改为可关闭的悬浮面板） */}
      <div>
        <div className="divide-y divide-slate-100 rounded-2xl border border-slate-200 bg-white shadow-sm">
          {/* 基本信息 */}
          <Section icon={<BookIcon className="h-4 w-4" />} title={t('bookForm.section.basic')}>
            <RowField label={<>{t('bookForm.label.title')} <span className="text-rose-500">*</span></>}>
              <div className="relative">
                <Input value={title} maxLength={MAX_TITLE} onChange={(e) => setTitle(e.target.value)} placeholder={t('bookForm.placeholder.title')}
                  className="pr-16" />
                <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-xs text-slate-400">{title.length} / {MAX_TITLE}</span>
              </div>
            </RowField>
            <RowField label={t('bookForm.label.desc')}>
              <div className="relative">
                <Textarea value={description} maxLength={MAX_DESC} onChange={(e) => setDescription(e.target.value)}
                  placeholder={t('bookForm.placeholder.desc')} className="min-h-[76px] pb-6" />
                <span className="pointer-events-none absolute bottom-2 right-3 text-xs text-slate-400">{description.length} / {MAX_DESC}</span>
              </div>
            </RowField>
            {tagsEnabled && (
            <RowField label={t('bookForm.label.tags')} hint={t('bookForm.hint.tags', { count: MAX_TAGS })}>
              <div className="relative">
                <div className="flex flex-wrap items-center gap-2 rounded-lg border border-slate-200 bg-white px-2 py-1.5 transition-colors focus-within:border-primary-500">
                  {tags.map((tag) => (
                    <span key={tag} className="inline-flex items-center gap-1 rounded-full bg-primary-50 px-2 py-0.5 text-xs font-medium text-primary-700 ring-1 ring-inset ring-primary-200">
                      {tag}
                      <button type="button" aria-label={t('bookForm.aria.removeTag', { tag })} onClick={() => setTags(tags.filter((x) => x !== tag))}
                        className="text-primary-400 hover:text-primary-700"><CloseIcon className="h-3 w-3" /></button>
                    </span>
                  ))}
                  <input value={tagInput} onChange={(e) => setTagInput(e.target.value)} onKeyDown={onTagKey} onBlur={() => setTimeout(addTag, 150)}
                    placeholder={tags.length >= MAX_TAGS ? t('bookForm.placeholder.tagFull') : t('bookForm.placeholder.tag')} disabled={tags.length >= MAX_TAGS}
                    className="min-w-[140px] flex-1 border-0 bg-transparent p-0 text-sm placeholder:text-slate-400 focus:outline-none focus:ring-0" />
                </div>
                {/* 现有标签检索结果：点选即引用，避免重复造词 */}
                {tagSuggest.length > 0 && tags.length < MAX_TAGS && (
                  <div className="absolute z-20 mt-1 max-h-56 w-full overflow-auto rounded-lg border border-slate-200 bg-white shadow-lg">
                    {tagSuggest.map((s) => (
                      <button key={s.name} type="button" onMouseDown={(e) => { e.preventDefault(); addTagValue(s.name) }}
                        className="flex w-full items-center justify-between px-3 py-2 text-left text-sm text-slate-700 hover:bg-slate-50">
                        <span className="truncate">{s.name}</span>
                        {typeof s.book_count === 'number' && <span className="ml-2 shrink-0 text-xs text-slate-400">{t('home.topics.count', { n: s.book_count })}</span>}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            </RowField>
            )}
          </Section>

          {/* 封面 */}
          <Section icon={<ImageIcon className="h-4 w-4" />} title={t('bookForm.section.cover')}>
            <div className="flex gap-4">
              <div className="w-56 shrink-0 self-stretch overflow-hidden rounded-lg border border-slate-200 bg-gradient-to-br from-primary-200 to-[#8B8DFF]">
                {coverSrc && <img src={coverSrc} alt="" className="h-full w-full object-cover" onError={(e) => { e.currentTarget.style.display = 'none' }} />}
              </div>
              <div className="min-w-0 flex-1 space-y-3">
                <button type="button" onClick={() => fileRef.current?.click()} disabled={uploading}
                  className="flex w-full flex-col items-center justify-center gap-1 rounded-lg border border-dashed border-slate-300 bg-slate-50/60 py-6 text-center transition-colors hover:border-primary-400 hover:bg-primary-50/40 disabled:opacity-60">
                  <UploadIcon className="h-5 w-5 text-slate-400" />
                  <span className="text-sm font-medium text-slate-600">{uploading ? t('bookForm.uploading') : t('bookForm.upload')}</span>
                </button>
                <input ref={fileRef} type="file" accept="image/*" hidden onChange={(e) => { uploadCover(e.target.files?.[0]); e.target.value = '' }} />
                <div className="flex items-center gap-3">
                  <span className="text-xs text-slate-400">{t('bookForm.orPaste')}</span>
                  <span className="h-px flex-1 bg-slate-100" />
                </div>
                <div className="flex gap-2">
                  <Input value={/^https?:\/\//.test(coverImage) ? coverImage : ''} onChange={(e) => setCoverImage(e.target.value)}
                    placeholder="https://example.com/cover.jpg" />
                  {coverImage && (
                    <Button variant="outline" type="button" onClick={() => setCoverImage('')} className="shrink-0">{t('bookForm.removeCover')}</Button>
                  )}
                </div>
              </div>
            </div>
          </Section>

          {/* 访问与章节 */}
          <Section icon={<LinkIcon className="h-4 w-4" />} title={t('bookForm.section.access')}>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('bookForm.label.slug')}</label>
                <div className="flex items-stretch overflow-hidden rounded-lg border border-slate-200 focus-within:border-primary-500"
                  style={{ height: 'var(--control-height)' }}>
                  <span className="flex items-center whitespace-nowrap bg-slate-50 px-3 text-xs text-slate-400">{host || 'knowforge'}/book/</span>
                  <input value={slug} onChange={(e) => setSlug(e.target.value)} disabled={isEdit}
                    placeholder="knowledge-garden"
                    className="min-w-0 flex-1 border-0 bg-white px-2 text-sm text-slate-900 placeholder:text-slate-400 focus:outline-none focus:ring-0 disabled:bg-slate-50 disabled:text-slate-400" />
                </div>
                <p className={`mt-1.5 text-xs ${!slug ? 'text-slate-400' : validSlug(slug) ? 'text-emerald-600' : 'text-rose-500'}`}>
                  {isEdit ? t('bookForm.slug.locked') : !slug ? t('bookForm.slug.auto') : validSlug(slug) ? t('bookForm.slug.ok') : t('bookForm.slug.invalid')}
                </p>
              </div>
              <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('bookForm.label.prefix')}</label>
                <Select value={chapterPrefix} onChange={setChapterPrefix} options={prefixOptions.map((o) => ({ value: o.value, label: t(o.labelKey) }))} />
                <p className="mt-1.5 text-xs text-slate-400">{t('bookForm.prefixHint')}</p>
              </div>
              <div className="flex items-center justify-between gap-4">
                <div className="min-w-0">
                  <label className="block text-sm font-medium text-slate-700">{t('bookForm.label.defaultChapterStatus')}</label>
                  <p className="mt-0.5 text-xs text-slate-400">{t('bookForm.defaultChapterStatusHint')}</p>
                </div>
                <div className="w-40 shrink-0">
                  <Select size="sm" value={defaultChapterStatus} onChange={setDefaultChapterStatus}
                    options={[
                      { value: 'draft', label: t('book.status.draft') },
                      { value: 'published', label: t('book.status.published') },
                      { value: 'archived', label: t('book.status.archived') },
                    ]} />
                </div>
              </div>
              <div className="flex items-center justify-between gap-4">
                <div className="min-w-0">
                  <label className="block text-sm font-medium text-slate-700">{t('bookForm.label.followChild')}</label>
                  <p className="mt-0.5 text-xs text-slate-400">{t('bookForm.followChildHint')}</p>
                </div>
                <Switch checked={childStatusFollowParent} onChange={setChildStatusFollowParent} ariaLabel={t('bookForm.label.followChild')} />
              </div>
            </div>
          </Section>

          {/* 多语言与版本：语言/翻译分组 一组，版本/版本分组 一组（设置页拆分为独立 tab，可选隐藏） */}
          {showLocalization && (transEnabled || versionsEnabled) && (
          <Section icon={<i className="fa-solid fa-language text-sm" aria-hidden="true" />} title={t('bookForm.section.localization')}>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              {transEnabled && <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('common.language.label')}</label>
                <Input value={language} onChange={(e) => setLanguage(e.target.value)} placeholder={t('bookForm.placeholder.language')} maxLength={32} />
                <p className="mt-1.5 text-xs text-slate-400">{t('bookForm.languageHint')}</p>
              </div>}
              {transEnabled && <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('bookForm.label.transGroup')}</label>
                <Input value={transGroup} onChange={(e) => setTransGroup(e.target.value)} placeholder={t('bookForm.placeholder.transGroup')} maxLength={64} />
                <p className="mt-1.5 text-xs text-slate-400">{t('bookForm.transGroupHint')}</p>
              </div>}
              {versionsEnabled && <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('bookForm.label.version')}</label>
                <Input value={version} onChange={(e) => setVersion(e.target.value)} placeholder={t('bookForm.placeholder.version')} maxLength={32} />
                <p className="mt-1.5 text-xs text-slate-400">{t('bookForm.versionHint')}</p>
              </div>}
              {versionsEnabled && <div>
                <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('bookForm.label.versionGroup')}</label>
                <Input value={versionGroup} onChange={(e) => setVersionGroup(e.target.value)} placeholder={t('bookForm.placeholder.versionGroup')} maxLength={64} />
                <p className="mt-1.5 text-xs text-slate-400">{t('bookForm.versionGroupHint')}</p>
              </div>}
            </div>
          </Section>
          )}

          {/* 阅读水印已迁移到「书籍设置 → 水印」独立 tab（受 watermark 插件控制）；
              此处仍保留 watermark_enabled/text 于提交负载中，避免基础设置保存时清空既有水印配置。 */}

          {/* 发布设置 */}
          <Section icon={<SlidersIcon className="h-4 w-4" />} title={t('bookForm.section.publish')}>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
              <VisibilityCard active={!isPublic} onClick={() => { setIsPublic(false); setLoginRequired(false) }}
                icon={<LockIcon className="h-5 w-5" />} title={t('bookForm.visibility.private.title')} desc={t('bookForm.visibility.private.desc')} />
              <VisibilityCard active={isPublic && loginRequired} onClick={() => { setIsPublic(true); setLoginRequired(true) }}
                icon={<i className="fa-solid fa-user-lock text-[1.1rem]" aria-hidden="true" />} title={t('bookForm.visibility.login.title')} desc={t('bookForm.visibility.login.desc')} />
              <VisibilityCard active={isPublic && !loginRequired} onClick={() => { setIsPublic(true); setLoginRequired(false) }}
                icon={<GlobeIcon className="h-5 w-5" />} title={t('bookForm.visibility.public.title')} desc={t('bookForm.visibility.public.desc')} />
            </div>
            <div className="mt-4 max-w-xs">
              <label className="mb-1.5 block text-sm font-medium text-slate-700">{t('bookForm.label.initialStatus')}</label>
              <Select value={status} onChange={(v) => setStatus(v as BookStatus)} options={statusOptions.map((o) => ({ value: o.value, label: t(o.labelKey) }))} />
            </div>
            <div className="mt-4 flex items-start gap-2 rounded-lg bg-primary-50/70 px-3 py-2.5 text-sm text-primary-700">
              <InfoIcon className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{isEdit ? t('bookForm.publishHintEdit') : t('bookForm.publishHintCreate')}</span>
            </div>
          </Section>
        </div>

      </div>

      {/* 悬浮实时预览：默认隐藏，可随时开关关闭，不再固定占用版面 */}
      {showPreview && (
        <div className="fixed bottom-6 right-6 z-40 max-h-[calc(100vh-7rem)] w-[340px] space-y-4 overflow-auto rounded-2xl border border-slate-200 bg-white p-4 shadow-xl">
          <div className="flex items-center justify-between">
            <h2 className="font-bold text-slate-900">{t('bookForm.preview.title')}</h2>
            <button type="button" onClick={() => setShowPreview(false)} aria-label={t('bookForm.preview.close')}
              className="flex items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700"
              style={{ width: 'var(--control-height-sm)', height: 'var(--control-height-sm)' }}>
              <CloseIcon className="h-4 w-4" />
            </button>
          </div>
          <div className="overflow-hidden rounded-xl border border-slate-200">
            <div className="aspect-[4/3] w-full bg-gradient-to-br from-primary-200 to-[#8B8DFF]">
              {coverSrc && <img src={coverSrc} alt="" className="h-full w-full object-cover" onError={(e) => { e.currentTarget.style.display = 'none' }} />}
            </div>
            <div className="space-y-2 p-4">
              {tagsEnabled && tags.length > 0 && (
                <div className="flex flex-wrap gap-1.5">
                  {tags.map((tag) => (
                    <span key={tag} className="inline-flex items-center rounded-full bg-emerald-50 px-2 py-0.5 text-xs font-medium text-emerald-700 ring-1 ring-inset ring-emerald-200">{tag}</span>
                  ))}
                </div>
              )}
              <h3 className="line-clamp-2 text-lg font-bold text-slate-900">{title || t('bookForm.preview.emptyTitle')}</h3>
              <p className="line-clamp-3 text-sm text-slate-500">{description || t('bookForm.preview.emptyDesc')}</p>
              <div className="flex items-center gap-2 pt-1">
                {authorAvatar
                  ? <img src={authorAvatar} alt="" className="h-6 w-6 rounded-full object-cover" />
                  : <span className="flex h-6 w-6 items-center justify-center rounded-full bg-slate-100 text-xs text-slate-500">{authorName.slice(0, 1)}</span>}
                <span className="text-sm text-slate-600">{authorName}</span>
              </div>
              <div className="flex items-center gap-1.5 pt-1 text-xs text-slate-400">
                <LockIcon className="h-3.5 w-3.5" />
                {isPublic ? (loginRequired ? t('bookForm.visibility.login.title') : t('bookForm.visibility.public.title')) : t('bookForm.visibility.private.title')} · {statusOptions.find((o) => o.value === status) ? t(statusOptions.find((o) => o.value === status)!.labelKey) : status}
              </div>
            </div>
          </div>
          <div className="rounded-xl border border-slate-200 p-4">
            <h3 className="mb-3 font-bold text-slate-900">{isEdit ? t('bookForm.preview.youCan') : t('bookForm.preview.youCanNew')}</h3>
            <ul className="space-y-3 text-sm text-slate-600">
              {[t('bookForm.preview.feature1'), t('bookForm.preview.feature2'), t('bookForm.preview.feature3')].map((feature) => (
                <li key={feature} className="flex items-center gap-2.5">
                  <CheckCircleIcon className="h-5 w-5 text-emerald-500" /> {feature}
                </li>
              ))}
            </ul>
          </div>
        </div>
      )}

      {/* 底部操作条 */}
      <div className="mt-6 flex items-center justify-between gap-3 rounded-2xl border border-slate-200 bg-white px-5 py-3 shadow-sm">
        <button type="button" onClick={() => setShowPreview((v) => !v)}
          className="flex items-center gap-1.5 text-sm text-slate-500 transition-colors hover:text-primary-600">
          <EyeIcon className="h-4 w-4" /> {showPreview ? t('bookForm.preview.close') : t('bookForm.preview.title')}
        </button>
        <div className="flex items-center gap-3">
          {showSaveDraft && (
            <Button variant="outline" type="button" onClick={() => submit('draft')} disabled={saving}>{t('bookForm.saveDraft')}</Button>
          )}
          <Button variant="outline" type="button" onClick={() => router.back()}>{t('common.actions.cancel')}</Button>
          <Button onClick={() => submit()} loading={saving}>{submitLabel}</Button>
        </div>
      </div>
    </div>
  )
}

/* ── 子组件 ── */

function Section({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <section className="p-5 sm:p-6">
      <div className="mb-4 flex items-center gap-2 font-semibold text-slate-900">
        <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-primary-50 text-primary-600">{icon}</span>
        {title}
      </div>
      <div className="space-y-4">{children}</div>
    </section>
  )
}

function RowField({ label, hint, children }: { label: ReactNode; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-[84px_minmax(0,1fr)] sm:gap-3">
      <label className="pt-2 text-sm font-medium text-slate-700">{label}</label>
      <div>
        {children}
        {hint && <p className="mt-1.5 text-xs text-slate-400">{hint}</p>}
      </div>
    </div>
  )
}

function VisibilityCard({ active, onClick, icon, title, desc }: { active: boolean; onClick: () => void; icon: ReactNode; title: string; desc: string }) {
  return (
    <button type="button" onClick={onClick}
      className={`flex items-center gap-3 rounded-xl border p-3.5 text-left transition-colors ${
        active ? 'border-primary-500 bg-primary-50/60 ring-1 ring-inset ring-primary-200' : 'border-slate-200 hover:border-slate-300'
      }`}>
      <span className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full border ${active ? 'border-primary-500' : 'border-slate-300'}`}>
        {active && <span className="h-2.5 w-2.5 rounded-full bg-primary-500" />}
      </span>
      <span className={active ? 'text-primary-600' : 'text-slate-400'}>{icon}</span>
      <span className="min-w-0">
        <span className="block text-sm font-medium text-slate-900">{title}</span>
        <span className="block text-xs text-slate-500">{desc}</span>
      </span>
    </button>
  )
}

/* 局部图标（图标库未收录） */
function SlidersIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" className={className}>
      <path d="M4 6h10M18 6h2M4 12h2M10 12h10M4 18h10M18 18h2" />
      <circle cx="16" cy="6" r="2" /><circle cx="8" cy="12" r="2" /><circle cx="16" cy="18" r="2" />
    </svg>
  )
}
function LockIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className}>
      <rect x="4" y="10" width="16" height="10" rx="2" /><path d="M8 10V7a4 4 0 0 1 8 0v3" />
    </svg>
  )
}
function GlobeIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className}>
      <circle cx="12" cy="12" r="9" /><path d="M3 12h18M12 3c2.5 2.5 2.5 15 0 18M12 3c-2.5 2.5-2.5 15 0 18" />
    </svg>
  )
}
function InfoIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className}>
      <circle cx="12" cy="12" r="9" /><path d="M12 11v5M12 8h.01" />
    </svg>
  )
}
