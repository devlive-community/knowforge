import { useCallback, useEffect, useMemo, useRef, useState, ReactNode } from 'react'
import { useRouter } from 'next/router'
import Link from 'next/link'
import { api, formatDate, API_BASE, getToken, type ApiError } from '@/lib/api'
import { useApp, useRequireAuth } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { renderMarkdown, bindMarkdownInteractivity, headingPlainText, wikiDocsFromTree } from '@/lib/markdown'
import { parseDocMeta, setDocIcon as setDocIconMeta, type DocMeta } from '@/lib/doc-meta'
import Seo from '@/components/Seo'
import DocTreeIcon from '@/components/DocTreeIcon'
import { Button, Input, Textarea, Select, Field, Badge, Checkbox, ContextMenu, ContextMenuItem, EmptyState, Loading, SegmentedTabs, Switch, Tooltip, Modal, useFeedback } from '@/components/ui'
import {
  BookIcon, CheckCircleIcon, ChevronDownIcon, ChevronRightIcon, CloudIcon, CodeIcon,
  CloseIcon, EyeIcon, FileTextIcon, FolderIcon, GlobeIcon, GripIcon, HistoryIcon, ImageIcon, LinkIcon,
  InfoCircleIcon, ListBulletIcon, ListOrderedIcon, MoreIcon, QuoteIcon, SaveIcon, SearchIcon, TrashIcon, UploadIcon,
  TableIcon, StrikethroughIcon, CodeBlockIcon, CheckSquareIcon, ColumnsIcon, OutlineIcon,
  MaximizeIcon, MinimizeIcon,
} from '@/components/icons'
import type { Book, Document, DocumentRevision, DocumentRevisionSummary, BookStatus, DocumentStatus, PageResult } from '@/lib/types'
import { diffLines, diffStats, type DiffRow } from '@/lib/text-diff'
import { HEADING_LEVELS } from '@/lib/editor-blocks'
import { entitlementAllowed } from '@/lib/entitlements'
import AIWriterDrawer, { type AIWriterTab, type WriterEditorBridge } from '@/components/ai-writer/AIWriterDrawer'
import { aiWriterEnabled } from '@/lib/ai-writer'
import TemplatePicker from '@/components/templates/TemplatePicker'
import { CollabAvatar, CollabAvatars, ConflictDialog, SameChapterBanner, useWriterCollab, type ConflictState, type DocSavedEvent, type Presence } from '@/components/writer/collab'
import { merge3, mergeTitle, resolveMerge, type ConflictChoice } from '@/lib/merge'
import CommentsDrawer, { type CommentsTab } from '@/components/writer/CommentsDrawer'
import SuggestionReview, { type AcceptedSuggestion, type WriterSuggestion } from '@/components/writer/SuggestionReview'
import { buildAnchor, type TextAnchor } from '@/lib/anchor'
import { browserTimeZone, templatesEnabled, type TemplateSummary } from '@/lib/templates'
import { displayName } from '@/lib/users'

type SaveState = 'saved' | 'dirty' | 'saving'
type TabKey = 'toc' | 'settings'
type ChapterMenuState = { doc: Document; x: number; y: number; align: 'start' | 'end'; flipY?: number }

interface BookFormState {
  title: string
  description: string
  status: BookStatus
  isPublic: boolean
  tags: string[]
  chapterPrefix: string
  childStatusFollowParent: boolean
}

const STATUS_META: Record<BookStatus, { labelKey: string; tone: 'slate' | 'primary' | 'emerald' | 'violet' | 'amber'; dot: string }> = {
  draft: { labelKey: 'book.status.draft', tone: 'slate', dot: 'bg-slate-400' },
  in_progress: { labelKey: 'book.status.in_progress', tone: 'primary', dot: 'bg-primary-500' },
  published: { labelKey: 'book.status.published', tone: 'emerald', dot: 'bg-emerald-500' },
  completed: { labelKey: 'book.status.completed', tone: 'violet', dot: 'bg-violet-500' },
  archived: { labelKey: 'book.status.archived', tone: 'amber', dot: 'bg-amber-500' },
}

// 临时关闭章节自动保存；恢复时只需改为 true，手动保存与发布流程不受影响。
const AUTO_SAVE_ENABLED = false

interface WriterProps {
  user: import('@/lib/types').User | null
  suggestOnly?: boolean // 「建议者」协作者：修改以建议提交，由作者审阅，不能直接保存
}

// caretCoordinates 用镜像 div 复刻 textarea 样式，测量指定字符位置的视口坐标（斜杠菜单定位用）。
function caretCoordinates(el: HTMLTextAreaElement, pos: number): { top: number; left: number; lineHeight: number } {
  const cs = getComputedStyle(el)
  const div = document.createElement('div')
  const props = [
    'boxSizing', 'width', 'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft',
    'borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth',
    'fontFamily', 'fontSize', 'fontWeight', 'fontStyle', 'letterSpacing', 'lineHeight',
    'textTransform', 'wordSpacing', 'textIndent',
  ] as const
  props.forEach((p) => { (div.style as unknown as Record<string, string>)[p] = (cs as unknown as Record<string, string>)[p] })
  div.style.position = 'absolute'
  div.style.visibility = 'hidden'
  div.style.whiteSpace = 'pre-wrap'
  div.style.wordWrap = 'break-word'
  div.style.overflow = 'hidden'
  div.textContent = el.value.slice(0, pos)
  const span = document.createElement('span')
  span.textContent = el.value.slice(pos) || '.'
  div.appendChild(span)
  document.body.appendChild(div)
  const rect = el.getBoundingClientRect()
  const lineHeight = parseInt(cs.lineHeight, 10) || (parseInt(cs.fontSize, 10) || 14) * 1.5
  const top = rect.top + span.offsetTop - el.scrollTop
  const left = rect.left + span.offsetLeft - el.scrollLeft
  document.body.removeChild(div)
  return { top, left, lineHeight }
}

// 本地草稿备份的 localStorage 键（按章节 id）。
const draftKey = (id: number) => `writer:draft:doc:${id}`

// 有选区时按下配对符包裹选中文本（open -> close）。
const WRAP_PAIRS: Record<string, string> = {
  '(': ')', '[': ']', '{': '}', '`': '`', '*': '*', '_': '_', '~': '~', '"': '"', "'": "'",
}

// 斜杠命令元数据（label 展示，kw 供拉丁关键词过滤）；动作在 selectSlash 里按 key 分派。
const SLASH_COMMANDS: { key: string; labelKey: string; kw: string }[] = [
  { key: 'h1', labelKey: 'writer.slash.h1', kw: 'h1 heading title' },
  { key: 'h2', labelKey: 'writer.slash.h2', kw: 'h2 heading title' },
  { key: 'h3', labelKey: 'writer.slash.h3', kw: 'h3 heading title' },
  { key: 'h4', labelKey: 'writer.slash.h4', kw: 'h4 heading title' },
  { key: 'ul', labelKey: 'writer.slash.ul', kw: 'ul list bullet' },
  { key: 'ol', labelKey: 'writer.slash.ol', kw: 'ol list ordered number' },
  { key: 'task', labelKey: 'writer.slash.task', kw: 'task todo check' },
  { key: 'quote', labelKey: 'writer.slash.quote', kw: 'quote blockquote' },
  { key: 'code', labelKey: 'writer.slash.code', kw: 'code block pre' },
  { key: 'table', labelKey: 'writer.slash.table', kw: 'table grid' },
  { key: 'tabs', labelKey: 'writer.slash.tabs', kw: 'tabs tab 标签页' },
  { key: 'note', labelKey: 'writer.slash.note', kw: 'note 备注' },
  { key: 'tip', labelKey: 'writer.slash.tip', kw: 'tip 提示' },
  { key: 'warning', labelKey: 'writer.slash.warning', kw: 'warning warn 警告' },
  { key: 'accordion', labelKey: 'writer.slash.accordion', kw: 'accordion collapse 折叠 面板' },
  { key: 'steps', labelKey: 'writer.slash.steps', kw: 'steps step 步骤' },
  { key: 'cards', labelKey: 'writer.slash.cards', kw: 'card cards cardgroup grid 卡片 卡片组' },
  { key: 'children', labelKey: 'writer.slash.children', kw: 'children subchapters toc 子章节 目录' },
  { key: 'hr', labelKey: 'writer.slash.hr', kw: 'hr rule divider' },
  { key: 'image', labelKey: 'writer.slash.image', kw: 'image img upload photo' },
  { key: 'collect', labelKey: 'writer.slash.collect', kw: 'collect web fetch import scrape 采集 网页' },
  { key: 'template', labelKey: 'writer.slash.template', kw: 'template 模板' },
  { key: 'ai', labelKey: 'writer.slash.aiWriter', kw: 'ai assistant continue rewrite polish 写作助手 续写 润色 改写' },
  { key: 'import-md', labelKey: 'writer.slash.importMarkdown', kw: 'import markdown md file upload 导入 文件' },
  { key: 'link', labelKey: 'writer.slash.link', kw: 'link url href' },
]

// metaPreviewHtml 预览顶部的章节元数据卡片（元数据本身不作为正文渲染）；值均经转义
function metaPreviewHtml(meta: DocMeta, t: (key: string) => string): string {
  const esc = (v: string) => v.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
  const rows = (['title', 'description', 'url', 'icon'] as const)
    .filter((k) => meta[k])
    .map((k) => `<div class="flex gap-3"><dt class="w-16 shrink-0 text-slate-400">${esc(t(`writer.meta.${k}`))}</dt><dd class="m-0 min-w-0 break-all text-slate-700">${esc(meta[k] || '')}</dd></div>`)
  if (!rows.length) return ''
  return `<div class="not-prose mb-5 rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-3 text-xs" data-doc-meta="1">` +
    `<div class="mb-1.5 font-semibold text-slate-500">${esc(t('writer.meta.previewTitle'))}</div><dl class="m-0 space-y-1">${rows.join('')}</dl></div>`
}

// MergeOutcome 保存冲突的处理结果：合并后的内容（按他人版本的摘要重新保存）、改用他人版本，或取消（null）
type MergeOutcome = { kind: 'merged'; title: string; content: string; hash: string } | { kind: 'theirs'; document: Document } | null

// Writer：书籍与章节编辑器（三栏工作台布局）
export default function Writer({ user, suggestOnly = false }: WriterProps) {
  const { confirmAction, requestInput, showToast } = useFeedback()
  useRequireAuth()
  const router = useRouter()
  const { site, user: me } = useApp()
  const collectEnabled = entitlementAllowed(me, 'collect.page', site.collect_page_enabled !== false) && !suggestOnly // 单页采集权益（含插件启用）；无权限时隐藏采集入口
  const { t } = useTranslation()
  // AI 写作助手（插件）：抽屉状态由 URL 承载（?ai=assist|history）
  const aiOn = aiWriterEnabled(site) && !suggestOnly // 建议者不使用 AI 写作助手、模板与网页采集
  const aiTab: AIWriterTab | null = !aiOn ? null : router.query.ai === 'history' ? 'history' : router.query.ai === 'assist' ? 'assist' : null
  // 写作批注抽屉（?comments=open|resolved），与 AI 写作助手抽屉互斥
  const commentsTab: CommentsTab | null = (['open', 'resolved', 'suggestions'] as const).find((v) => v === router.query.comments) ?? null
  const [pendingComment, setPendingComment] = useState<TextAnchor | null>(null)
  const [commentsReload, setCommentsReload] = useState(0)
  const [commentCounts, setCommentCounts] = useState<Record<string, number>>({})
  const [suggestionsReload, setSuggestionsReload] = useState(0)
  const [suggestionCounts, setSuggestionCounts] = useState<Record<string, number>>({})
  const [reviewing, setReviewing] = useState<WriterSuggestion | null>(null)
  // 模板（插件）：章节模板插入到光标处，也可把当前章节存为模板
  const templatesOn = templatesEnabled(site) && !suggestOnly
  const [templateOpen, setTemplateOpen] = useState(false)
  const [templateReload, setTemplateReload] = useState(0)
  const [savingTemplate, setSavingTemplate] = useState(false)
  const bookSlug = (router.query.slug as string) || ''
  // 路由为可选 catch-all（[[...doc]]）：doc 可能是数组或缺省
  const docSlug = Array.isArray(router.query.doc) ? (router.query.doc[0] || '') : ((router.query.doc as string) || '')
  const siteName = site.site_name || 'KnowForge'

  const [book, setBook] = useState<Book | null>(null)
  const [tree, setTree] = useState<Document[]>([])
  const wikiDocs = useMemo(() => wikiDocsFromTree(tree), [tree])
  const [current, setCurrent] = useState<Document | null>(null)
  const [documentLoading, setDocumentLoading] = useState(false)
  const [tab, setTab] = useState<TabKey>('toc')
  const [search, setSearch] = useState('')
  const [expanded, setExpanded] = useState<Set<number>>(new Set())
  const [chapterMenu, setChapterMenu] = useState<ChapterMenuState | null>(null)
  const [newMenuOpen, setNewMenuOpen] = useState(false)
  const mdInputRef = useRef<HTMLInputElement>(null)
  const [importingMd, setImportingMd] = useState(false) // 从 Markdown 导入章节（进行中显示状态）
  const [insertMenuOpen, setInsertMenuOpen] = useState(false)
  const [metaMenuOpen, setMetaMenuOpen] = useState(false)
  const [translateMenuOpen, setTranslateMenuOpen] = useState(false)
  const [translating, setTranslating] = useState(false)
  const [dragId, setDragId] = useState<number | null>(null)
  const [reorderingId, setReorderingId] = useState<number | null>(null)
  const [dropTarget, setDropTarget] = useState<{ id: number; pos: 'before' | 'inside' | 'after' } | null>(null)
  const [creatingUnder, setCreatingUnder] = useState<number | null>(null) // 新建期间保持高亮的父章节
  const [preview, setPreview] = useState(false)
  const [splitPreview, setSplitPreview] = useState(false)
  const [focusMode, setFocusMode] = useState(false)
  // 斜杠命令菜单；mode 为 link 时是输入 [[ 后弹出的章节选择（插入双向链接）
  const [slash, setSlash] = useState({ open: false, start: 0, query: '', top: 0, left: 0, index: 0, mode: 'cmd' as 'cmd' | 'link' })
  const [shortcutsOpen, setShortcutsOpen] = useState(false)
  const [fontSize, setFontSize] = useState(14) // 编辑区字号（px），本地记忆
  const [draftRecovery, setDraftRecovery] = useState<{ content: string; ts: number } | null>(null)
  // 查找替换
  const [findOpen, setFindOpen] = useState(false)
  const [findText, setFindText] = useState('')
  const [replaceText, setReplaceText] = useState('')
  const [caseSensitive, setCaseSensitive] = useState(false)
  const [activeMatch, setActiveMatch] = useState(0)
  const findInputRef = useRef<HTMLInputElement>(null)
  const [saveState, setSaveState] = useState<SaveState>('saved')
  const [historyOpen, setHistoryOpen] = useState(false)
  const [webImportOpen, setWebImportOpen] = useState(false)
  const [webImportParent, setWebImportParent] = useState<Document | null>(null)

  const closeChapterMenu = useCallback(() => setChapterMenu(null), [])
  const openChapterMenu = useCallback((doc: Document, x: number, y: number, align: 'start' | 'end' = 'start', flipY?: number) => {
    setChapterMenu({ doc, x, y, align, flipY })
  }, [])

  // 章节表单
  const [title, setTitle] = useState('')
  const [content, setContent] = useState('')
  const [status, setStatus] = useState<DocumentStatus>('draft')
  const [parentId, setParentId] = useState('')
  const [sortOrder, setSortOrder] = useState(0)
  const [allowComments, setAllowComments] = useState(true)
  const [slug, setSlug] = useState('') // 文档路径；留空则由标题自动生成（沿用既有逻辑）
  const [externalUrl, setExternalUrl] = useState('') // 外链章节地址；非空时该章节是跳转外部地址的链接，不使用正文编辑器
  const [externalNewTab, setExternalNewTab] = useState(true) // 外链打开方式：新窗口（默认）/当前窗口

  // 书籍设置表单
  const [bookForm, setBookForm] = useState<BookFormState>({ title: '', description: '', status: 'draft', isPublic: false, tags: [] as string[], chapterPrefix: '', childStatusFollowParent: false })
  const [savingBook, setSavingBook] = useState(false)
  const [tagInput, setTagInput] = useState('')

  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const mdImportRef = useRef<HTMLInputElement>(null)
  const scrollLock = useRef<'edit' | 'preview' | null>(null) // 分栏滚动同步防抖锁
  const [uploading, setUploading] = useState(false)
  const [collecting, setCollecting] = useState(false)
  const [collectIncludeSource, setCollectIncludeSource] = useState(false) // 采集网页插入正文时是否附带「来源」脚注（默认关闭，可配置）
  const snapshot = useRef('') // 已保存/已加载表单的快照，用于脏状态判断
  // 协作：打开（或上次保存）时的标题、正文与摘要，作为保存冲突时三方合并的基准
  const baseRef = useRef<{ docId: number; hash: string; title: string; content: string } | null>(null)
  const dirtyRef = useRef(false)
  const formKeyRef = useRef('')
  const [remoteSave, setRemoteSave] = useState<DocSavedEvent | null>(null) // 编辑期间他人保存了当前章节
  const [conflict, setConflict] = useState<(ConflictState & { title: string | null; theirs: Document; resolve: (r: MergeOutcome) => void }) | null>(null)
  const loadedDocId = useRef<number | null>(null) // 当前表单对应的文档，防止切换章节时误触发自动保存
  const saveRef = useRef<(opts?: { status?: DocumentStatus }) => Promise<boolean | undefined>>(async () => undefined)
  const didInitExpand = useRef(false)
  const tocScrollRef = useRef<HTMLDivElement>(null)

  const flatDocs = useMemo(() => flatten(tree), [tree])
  // 当前章节的标题大纲（跳过围栏代码块内的 # 行）；offset 为该标题行在正文中的字符偏移。
  const outline = useMemo(() => {
    const items: { level: number; text: string; offset: number }[] = []
    let inFence = false
    let offset = 0
    for (const line of content.split('\n')) {
      const trimmed = line.trimStart()
      if (trimmed.startsWith('```') || trimmed.startsWith('~~~')) {
        inFence = !inFence
      } else if (!inFence) {
        const m = line.match(/^(#{1,6})\s+(.+?)\s*#*\s*$/)
        if (m) items.push({ level: m[1].length, text: headingPlainText(m[2].trim()) || m[2].trim(), offset })
      }
      offset += line.length + 1 // +1 补回被 split 去掉的换行符
    }
    return items
  }, [content])

  // 查找命中的起始偏移列表（纯子串匹配，可选区分大小写）。
  const matches = useMemo(() => {
    if (!findText) return []
    const hay = caseSensitive ? content : content.toLowerCase()
    const needle = caseSensitive ? findText : findText.toLowerCase()
    const out: number[] = []
    let i = hay.indexOf(needle)
    while (i !== -1) {
      out.push(i)
      i = hay.indexOf(needle, i + Math.max(1, needle.length))
    }
    return out
  }, [content, findText, caseSensitive])

  useEffect(() => { if (findOpen) findInputRef.current?.focus() }, [findOpen])

  // 编辑区字号本地记忆（读一次 + 变化时写入；try/catch 防隐私模式抛错）
  useEffect(() => {
    try {
      const saved = parseInt(localStorage.getItem('writer:font-size') || '', 10)
      if (saved >= 12 && saved <= 22) setFontSize(saved)
    } catch { /* 忽略 */ }
  }, [])
  useEffect(() => {
    try { localStorage.setItem('writer:font-size', String(fontSize)) } catch { /* 忽略 */ }
  }, [fontSize])

  const filteredSlash = useMemo(() => {
    const items = SLASH_COMMANDS.filter((c) => (c.key !== 'collect' || collectEnabled) && (c.key !== 'ai' || aiOn) && (c.key !== 'template' || templatesOn))
    const q = slash.query.toLowerCase()
    if (!q) return items
    return items.filter((c) => t(c.labelKey).includes(slash.query) || c.kw.includes(q) || c.key.includes(q))
  }, [slash.query, t, collectEnabled, aiOn, templatesOn])
  // [[ 章节选择：按标题或 slug 过滤本书其他章节
  const filteredLinks = useMemo(() => {
    if (!slash.open || slash.mode !== 'link') return []
    const q = slash.query.trim().toLowerCase()
    return wikiDocs
      .filter((d) => d.slug !== current?.slug && (!q || d.title.toLowerCase().includes(q) || d.slug.toLowerCase().includes(q)))
      .slice(0, 8)
  }, [slash.open, slash.mode, slash.query, wikiDocs, current?.slug])
  const menuCount = slash.mode === 'link' ? filteredLinks.length : filteredSlash.length
  const previewRef = useRef<HTMLDivElement>(null)
  // 预览内容防抖：输入时避免每键全量重渲染 Markdown
  const [previewHtml, setPreviewHtml] = useState('')
  useEffect(() => {
    if (!preview && !splitPreview) return
    const timer = setTimeout(() => {
      setPreviewHtml(metaPreviewHtml(parseDocMeta(content).meta, t) + renderMarkdown(content, { bookSlug, docs: wikiDocs }))
      if (previewRef.current) bindMarkdownInteractivity(previewRef.current)
    }, 300)
    return () => clearTimeout(timer)
  }, [content, preview, splitPreview, bookSlug, wikiDocs, t])

  // 预览里的任务复选框可点击：点击第 idx 个复选框即翻转正文里第 idx 个任务项标记。
  useEffect(() => {
    const root = previewRef.current
    if (!root || (!preview && !splitPreview)) return
    const boxes = Array.from(root.querySelectorAll('li input[type="checkbox"]')) as HTMLInputElement[]
    const cleanups: Array<() => void> = []
    boxes.forEach((box, idx) => {
      box.disabled = false
      box.style.cursor = 'pointer'
      const handler = (e: Event) => {
        e.preventDefault()
        setContent((prev) => {
          let count = -1
          return prev.replace(/^(\s*(?:[-*+]|\d+\.)\s+)\[([ xX])\]/gm, (m, prefix, mark) => {
            count += 1
            return count === idx ? `${prefix}[${mark === ' ' ? 'x' : ' '}]` : m
          })
        })
      }
      box.addEventListener('click', handler)
      cleanups.push(() => box.removeEventListener('click', handler))
    })
    return () => cleanups.forEach((fn) => fn())
  }, [previewHtml, preview, splitPreview])

  const loadTree = useCallback(async (b: Book) => {
    setTree((await api<Document[]>(`/books/${b.id}/documents`)) || [])
  }, [])

  // importMarkdown 把选择的 .md 文件（可附带图片）或 Markdown ZIP 导入为本书第一级章节（目录结构 → 章节层级）
  async function importMarkdown(files: File[]) {
    if (!book || files.length === 0) return
    setImportingMd(true)
    try {
      const form = new FormData()
      files.forEach((f) => form.append('files', f))
      const token = getToken()
      const response = await fetch(`${API_BASE}/api/v1/books/${book.id}/documents/import-markdown`, { method: 'POST', headers: token ? { Authorization: `Bearer ${token}` } : undefined, body: form })
      const payload = await response.json().catch(() => ({}))
      if (!response.ok || payload.success === false) throw new Error(payload.message || t('writer.importMarkdownFailed'))
      await loadTree(book)
      showToast({ message: t('writer.importMarkdownDone', { count: payload.data?.imported_doc ?? 0 }), tone: 'success' })
    } catch (e) {
      showToast({ title: t('writer.importMarkdownFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setImportingMd(false)
      if (mdInputRef.current) mdInputRef.current.value = ''
    }
  }

  // 首次加载章节树后默认展开所有含子章节的节点（只执行一次，不干扰后续手动折叠）
  useEffect(() => {
    if (didInitExpand.current || tree.length === 0) return
    didInitExpand.current = true
    const ids = new Set<number>()
    const walk = (docs: Document[]) => docs.forEach((d) => { if (d.children?.length) { ids.add(d.id); walk(d.children) } })
    walk(tree)
    setExpanded(ids)
  }, [tree])

  // 目录自动定位：切换/选中章节后，把当前章节滚动到左侧目录可视区
  useEffect(() => {
    const el = tocScrollRef.current?.querySelector('[data-toc-active="1"]')
    el?.scrollIntoView({ block: 'nearest' })
  }, [current?.id, expanded])

  // 切换/新建章节后，编辑区与预览区都回到首行，避免停留在上一章节的滚动位置
  useEffect(() => {
    requestAnimationFrame(() => {
      const el = textareaRef.current
      if (el) { el.scrollTop = 0; el.setSelectionRange(0, 0) }
      if (previewRef.current) previewRef.current.scrollTop = 0
    })
  }, [current?.id])

  // 加载书籍与章节树
  useEffect(() => {
    if (!user || !bookSlug) return
    api<Book>(`/books/slug/${encodeURIComponent(bookSlug)}`)
      .then(async (b) => {
        setBookForm({
          title: b.title, description: b.description || '', status: b.status,
          isPublic: b.is_public, tags: (b.tags || []).map((t) => t.name),
          chapterPrefix: b.chapter_prefix || '',
          childStatusFollowParent: b.child_status_follow_parent === true,
        })
        await loadTree(b)
        setBook(b)
      })
      .catch((e) => showToast({ title: t('writer.bookLoadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [user, bookSlug, loadTree, showToast, t])

  function resetForm() {
    setCurrent(null)
    setCreatingUnder(null)
    setTitle(''); setContent(''); setStatus('draft'); setParentId(''); setSortOrder(0); setAllowComments(true); setSlug(''); setExternalUrl(''); setExternalNewTab(true)
    snapshot.current = JSON.stringify(['', '', 'draft', '', 0, true, '', '', true])
    loadedDocId.current = null
    setSaveState('saved')
  }

  async function confirmDiscard() {
    if (saveState !== 'dirty') return true
    return confirmAction({
      title: t('writer.discardTitle'),
      message: t('writer.discardMsg'),
      confirmLabel: t('writer.discardConfirm'),
      danger: true,
    })
  }

  // 取消选中当前章节（点击目录空白处）
  async function deselect() {
    if (!current) return
    if (!await confirmDiscard()) return
    resetForm()
    router.push(`/book/writer/${encodeURIComponent(bookSlug)}`, undefined, { shallow: true })
  }

  // 选中已有章节时填充表单
  useEffect(() => {
    if (!flatDocs.length && !docSlug) { resetForm(); return }
    const doc = docSlug ? flatDocs.find((d) => d.slug === docSlug) : null
    if (doc && doc.id === loadedDocId.current && dirtyRef.current) {
      // 目录刷新（如协作者新建了章节）时当前章节有未保存的修改：不重新加载，避免覆盖正在编辑的内容
      setCurrent((c) => (c && c.id === doc.id ? { ...doc, content_hash: c.content_hash } : doc))
      return
    }
    if (doc) {
      // active 守卫：切换章节/采集新章节会重跑本 effect，避免上一个仍在途的请求乱序返回后覆盖当前章节内容
      let active = true
      setDocumentLoading(true)
      setCurrent(doc)
      setCreatingUnder(null)
      setDraftRecovery(null)
      api<Document>(`/documents/${doc.id}`).then((full) => {
        if (!active) return
        applyLoadedDoc(full)
        // 本地草稿恢复：若上次离开时有未保存内容且与服务端不同，提示恢复
        try {
          const raw = localStorage.getItem(draftKey(full.id))
          if (raw) {
            const d = JSON.parse(raw)
            if (d && typeof d.content === 'string' && d.content !== (full.content || '')) setDraftRecovery({ content: d.content, ts: d.ts })
            else localStorage.removeItem(draftKey(full.id))
          }
        } catch { /* 忽略 */ }
      }).catch((e) => { if (active) showToast({ title: t('writer.chapterLoadFailed'), message: (e as Error).message, tone: 'error' }) })
        .finally(() => { if (active) setDocumentLoading(false) })
      return () => { active = false }
    } else if (!docSlug) {
      setDocumentLoading(false)
      resetForm()
    }
  }, [docSlug, flatDocs]) // eslint-disable-line react-hooks/exhaustive-deps

  // applyLoadedDoc 用服务端的章节内容填充表单，并记为已保存与合并基准
  function applyLoadedDoc(full: Document) {
    setTitle(full.title)
    setContent(full.content || '')
    setStatus(full.status)
    setParentId(full.parent_id ? String(full.parent_id) : '')
    setSortOrder(full.sort_order)
    setAllowComments(full.allow_comments !== false)
    setSlug(full.slug || '')
    setExternalUrl(full.external_url || '')
    setExternalNewTab(full.external_new_tab !== false)
    snapshot.current = JSON.stringify([full.title, full.content || '', full.status, full.parent_id ? String(full.parent_id) : '', full.sort_order, full.allow_comments !== false, full.slug || '', full.external_url || '', full.external_new_tab !== false])
    loadedDocId.current = full.id
    baseRef.current = { docId: full.id, hash: full.content_hash || '', title: full.title, content: full.content || '' }
    setRemoteSave(null)
    setSaveState('saved')
  }

  // mergeWithLatest 保存冲突：以打开时的内容为基准，把自己的修改与他人已保存的版本三方合并；
  // 不冲突时自动合并，冲突时打开对话框由作者逐块选择。
  async function mergeWithLatest(mine: { title: string; content: string }, data: { document: Document; saved_by?: Presence['user']; saved_at?: string }): Promise<MergeOutcome> {
    const base = baseRef.current
    const theirs = data.document
    if (!base || !theirs) return null
    const name = data.saved_by ? (data.saved_by.display_name || data.saved_by.username) : t('writer.collab.someone')
    const mergedTitle = mergeTitle(base.title, mine.title, theirs.title)
    const result = merge3(base.content, mine.content, theirs.content || '')
    if (result.conflicts === 0 && mergedTitle !== null) {
      showToast({ message: t('writer.collab.autoMerged', { name }), tone: 'success' })
      return { kind: 'merged', title: mergedTitle, content: resolveMerge(result), hash: theirs.content_hash || '' }
    }
    return new Promise((resolve) => setConflict({
      result, titles: mergedTitle === null ? { base: base.title, mine: mine.title, theirs: theirs.title } : null,
      title: mergedTitle, savedBy: data.saved_by, savedAt: data.saved_at, theirs, resolve,
    }))
  }

  function resolveConflict(choices: ConflictChoice[], titleChoice: 'mine' | 'theirs') {
    if (!conflict) return
    const title = conflict.titles ? (titleChoice === 'mine' ? conflict.titles.mine : conflict.titles.theirs) : (conflict.title || '')
    conflict.resolve({ kind: 'merged', title, content: resolveMerge(conflict.result, choices), hash: conflict.theirs.content_hash || '' })
    setConflict(null)
  }

  // 他人保存了当前章节：没有未保存修改时直接载入新内容；有修改时提示，保存时自动合并
  async function onRemoteDocSaved(ev: DocSavedEvent) {
    if (ev.doc_id !== loadedDocId.current || baseRef.current?.hash === ev.content_hash) return
    if (dirtyRef.current) {
      setRemoteSave(ev)
      return
    }
    try {
      const full = await api<Document>(`/documents/${ev.doc_id}`)
      if (loadedDocId.current !== full.id || dirtyRef.current) return
      applyLoadedDoc(full)
      showToast({ message: t('writer.collab.reloaded', { name: ev.by.display_name || ev.by.username }), tone: 'success' })
    } catch { /* 忽略：保存时仍会做冲突检查 */ }
  }

  // 是否有未保存的修改：直接比较表单与已保存快照（保存进行中继续输入的内容也算未保存）
  const formKey = JSON.stringify([title, content, status, parentId, sortOrder, allowComments, slug, externalUrl, externalNewTab])
  const formDirty = loadedDocId.current !== null && formKey !== snapshot.current
  dirtyRef.current = formDirty
  formKeyRef.current = formKey
  const collab = useWriterCollab({
    bookId: book?.id ?? null, docId: current?.id ?? null, dirty: formDirty,
    onDocSaved: (ev) => { void onRemoteDocSaved(ev) },
    onTreeChanged: () => { if (book) void loadTree(book) },
    onComments: (docId) => {
      void loadCommentCounts()
      if (docId === current?.id) setCommentsReload((n) => n + 1)
    },
    onSuggestions: (docId) => {
      void loadSuggestionCounts()
      if (docId === current?.id) setSuggestionsReload((n) => n + 1)
    },
  })
  const sameChapterOthers = collab.others.filter((p) => current && p.doc_id === current.id)
  const presenceByDoc = useMemo(() => {
    const map = new Map<number, Presence[]>()
    for (const p of collab.others) if (p.doc_id) map.set(p.doc_id, [...(map.get(p.doc_id) || []), p])
    return map
  }, [collab.others])

  // 保存：opts.status 允许“发布”一次性覆盖状态
  const save = useCallback(async (opts?: { status?: DocumentStatus }) => {
    if (!book) return
    if (!title.trim()) { showToast({ message: current ? t('writer.needTitle') : t('writer.needSelectAndTitle'), tone: 'error' }); return }
    const effectiveStatus = opts?.status ?? status
    // 父章节状态变更且含子章节：询问是否把新状态一并应用到子章节
    let cascadeStatus = false
    const descendantCount = current ? flatten(current.children).length : 0
    if (current && descendantCount > 0) {
      let prevStatus: string = effectiveStatus
      try { prevStatus = JSON.parse(snapshot.current)[2] } catch { /* 忽略 */ }
      if (effectiveStatus !== prevStatus) {
        cascadeStatus = await confirmAction({
          title: t('writer.setStatusTitle', { status: t(`book.status.${effectiveStatus}`) }),
          message: t('writer.cascadeMsg', { title: current.title, n: descendantCount }),
          confirmLabel: t('writer.cascadeConfirm'), cancelLabel: t('writer.cascadeCancel'),
        })
      }
    }
    const payload = {
      title: title.trim(), content, status: effectiveStatus, sort_order: sortOrder,
      external_url: externalUrl.trim(), external_new_tab: externalNewTab,
      // 外链章节强制关闭评论（无正文页面，公开后也不展示评论区）。
      parent_id: parentId ? Number(parentId) : null, allow_comments: externalUrl.trim() ? false : allowComments,
      // 文档路径：填了就用它，留空则不传（新建按标题自动生成，编辑保持原路径）
      ...(slug.trim() ? { slug: slug.trim() } : {}),
      cascade_status: cascadeStatus,
      create_revision: Boolean(current), revision_reason: opts?.status ? 'publish' : 'save',
    }
    setSaveState('saving')
    const startedAt = Date.now()
    try {
      if (current) {
        // 冲突保护：带上打开时的摘要；期间他人保存过则三方合并后按最新摘要重新保存
        const base = baseRef.current && baseRef.current.docId === current.id ? baseRef.current : null
        let body: Record<string, unknown> = { ...payload, ...(base?.hash ? { base_hash: base.hash } : {}) }
        let updated: Document
        let merged = false
        try {
          updated = await api<Document>(`/documents/${current.id}`, { method: 'PUT', body })
        } catch (e) {
          const err = e as ApiError
          if (err.code !== 'DOC_CONFLICT' || !base) throw e
          const outcome = await mergeWithLatest({ title: payload.title, content: payload.content }, err.data)
          if (!outcome) { setSaveState('dirty'); return false }
          if (outcome.kind === 'theirs') {
            applyLoadedDoc(outcome.document)
            setCurrent(outcome.document)
            return false
          }
          body = { ...body, title: outcome.title, content: outcome.content, base_hash: outcome.hash }
          updated = await api<Document>(`/documents/${current.id}`, { method: 'PUT', body })
          merged = true
        }
        if (merged) {
          setTitle(updated.title)
          setContent(updated.content || '')
        }
        baseRef.current = { docId: updated.id, hash: updated.content_hash || '', title: updated.title, content: updated.content || '' }
        setRemoteSave(null)
        setSlug(updated.slug || '')
        snapshot.current = JSON.stringify([updated.title, updated.content || '', updated.status, updated.parent_id ? String(updated.parent_id) : '', updated.sort_order, updated.allow_comments !== false, updated.slug || '', updated.external_url || '', updated.external_new_tab !== false])
        loadedDocId.current = updated.id
        try { localStorage.removeItem(draftKey(updated.id)) } catch { /* 忽略 */ }
        setDraftRecovery(null)
        setCurrent(updated)
        if (updated.publish_held) {
          setStatus(updated.status) // 发布被内容审核拦截：保持未发布
          showToast({ title: t('moderation.heldTitle'), message: updated.publish_held, tone: 'error' })
        } else if (opts?.status) setStatus(opts.status)
        await loadTree(book)
        selectDoc(updated.slug, true)
      } else {
        const created = await api<Document>(`/books/${book.id}/documents`, { method: 'POST', body: payload })
        setSlug(created.slug || '')
        snapshot.current = JSON.stringify([created.title, created.content || '', created.status, created.parent_id ? String(created.parent_id) : '', created.sort_order, created.allow_comments !== false, created.slug || '', created.external_url || '', created.external_new_tab !== false])
        loadedDocId.current = created.id
        setCurrent(created)
        if (created.publish_held) {
          setStatus(created.status)
          showToast({ title: t('moderation.heldTitle'), message: created.publish_held, tone: 'error' })
        } else if (opts?.status) setStatus(opts.status)
        await loadTree(book)
        selectDoc(created.slug, true)
      }
      const elapsed = Date.now() - startedAt
      if (elapsed < 500) await new Promise((r) => setTimeout(r, 500 - elapsed)) // 让“保存中”至少可见片刻
      // 保存期间若又有输入，保存完成后仍是未保存状态
      setSaveState(formKeyRef.current === snapshot.current ? 'saved' : 'dirty')
      return true
    } catch (e) {
      setSaveState('dirty')
      showToast({ title: t('writer.saveFailed'), message: (e as Error).message, tone: 'error' })
      return false
    }
  }, [book, title, content, status, parentId, sortOrder, allowComments, slug, externalUrl, externalNewTab, current, loadTree]) // eslint-disable-line react-hooks/exhaustive-deps
  saveRef.current = suggestOnly ? async () => { openSuggestDialog(); return false } : save

  // —— 审阅修改建议：采纳的部分以建议依据的原文为基准与当前版本三方合并（冲突时由作者选择），保存后记录结果 ——
  async function applySuggestion(s: WriterSuggestion, accepted: AcceptedSuggestion): Promise<boolean> {
    const base = baseRef.current
    if (!current || !base || current.id !== s.document_id) return false
    if (dirtyRef.current) {
      showToast({ message: t('writer.suggest.saveFirst'), tone: 'error' })
      return false
    }
    const mergedTitle = mergeTitle(s.base_title, base.title, accepted.title)
    const result = merge3(s.base_content, base.content, accepted.content)
    let final: { title: string; content: string } | null
    if (result.conflicts === 0 && mergedTitle !== null) {
      final = { title: mergedTitle, content: resolveMerge(result) }
    } else {
      const theirs = { ...current, title: accepted.title, content: accepted.content }
      const outcome = await new Promise<MergeOutcome>((resolve) => setConflict({
        result, titles: mergedTitle === null ? { base: s.base_title, mine: base.title, theirs: accepted.title } : null,
        title: mergedTitle, savedBy: s.user, theirs, resolve, fromSuggestion: true,
      }))
      final = !outcome ? null : outcome.kind === 'theirs' ? { title: outcome.document.title, content: outcome.document.content || '' } : outcome
    }
    if (!final) return false
    try {
      const updated = await api<Document>(`/documents/${current.id}`, { method: 'PUT', body: { title: final.title, content: final.content, base_hash: base.hash, create_revision: true, revision_reason: 'save' } })
      applyLoadedDoc(updated)
      setCurrent(updated)
      await api(`/suggestions/${s.id}/decide`, { method: 'POST', body: { status: accepted.partial ? 'partial' : 'accepted' } })
      setSuggestionsReload((n) => n + 1)
      void loadSuggestionCounts()
      if (book) void loadTree(book)
      showToast({ message: t(accepted.partial ? 'writer.suggest.appliedPartial' : 'writer.suggest.applied'), tone: 'success' })
      return true
    } catch (e) {
      const err = e as ApiError
      showToast({ message: err.code === 'DOC_CONFLICT' ? t('writer.suggest.changedRetry') : err.message, tone: 'error' })
      return false
    }
  }

  async function rejectSuggestion(s: WriterSuggestion): Promise<boolean> {
    try {
      await api(`/suggestions/${s.id}/decide`, { method: 'POST', body: { status: 'rejected' } })
      setSuggestionsReload((n) => n + 1)
      void loadSuggestionCounts()
      showToast({ message: t('writer.suggest.rejected'), tone: 'success' })
      return true
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
      return false
    }
  }

  // —— 建议者：把修改作为建议提交（附可选说明），提交后编辑区回到当前正式版本 ——
  const [suggestDialog, setSuggestDialog] = useState(false)
  const [suggestNote, setSuggestNote] = useState('')
  const [submittingSuggestion, setSubmittingSuggestion] = useState(false)
  function openSuggestDialog() {
    const base = baseRef.current
    if (!current || !base) return
    if (title.trim() === base.title && content === base.content) {
      showToast({ message: t('writer.suggest.nothing'), tone: 'info' })
      return
    }
    setSuggestNote('')
    setSuggestDialog(true)
  }
  async function submitSuggestion() {
    const base = baseRef.current
    if (!current || !base) return
    setSubmittingSuggestion(true)
    try {
      await api(`/documents/${current.id}/suggestions`, { method: 'POST', body: { base_title: base.title, base_content: base.content, title: title.trim(), content, note: suggestNote } })
      const full = await api<Document>(`/documents/${current.id}`)
      try { localStorage.removeItem(draftKey(current.id)) } catch { /* 忽略 */ }
      applyLoadedDoc(full)
      setSuggestDialog(false)
      setSuggestionsReload((n) => n + 1)
      showToast({ message: t('writer.suggest.submitted'), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setSubmittingSuggestion(false)
  }

  // 脏状态检测；自动保存当前临时关闭，只保留手动保存、快捷键保存与发布。
  useEffect(() => {
    if (!book) return
    const key = JSON.stringify([title, content, status, parentId, sortOrder, allowComments, slug, externalUrl, externalNewTab])
    if (key === snapshot.current) { setSaveState((s) => (s === 'saving' ? s : 'saved')); return }
    if (loadedDocId.current !== null && loadedDocId.current !== current?.id) return
    if (!current && !title.trim()) return
    setSaveState('dirty')
    if (!AUTO_SAVE_ENABLED) return
    const timer = setTimeout(() => { saveRef.current() }, 1500)
    return () => clearTimeout(timer)
  }, [book, title, content, status, parentId, sortOrder, allowComments, slug, externalUrl, externalNewTab, current])

  // 本地草稿备份：脏内容防抖写入 localStorage（服务端自动保存关闭时的兜底），保存后清除。
  useEffect(() => {
    const id = current?.id
    if (!id || loadedDocId.current !== id) return
    const key = JSON.stringify([title, content, status, parentId, sortOrder, allowComments, slug, externalUrl, externalNewTab])
    if (key === snapshot.current) {
      try { localStorage.removeItem(draftKey(id)) } catch { /* 忽略 */ }
      return
    }
    const timer = setTimeout(() => {
      try { localStorage.setItem(draftKey(id), JSON.stringify({ content, title, ts: Date.now() })) } catch { /* 忽略 */ }
    }, 800)
    return () => clearTimeout(timer)
  }, [title, content, status, parentId, sortOrder, allowComments, slug, externalUrl, externalNewTab, current])

  // Ctrl/Cmd + S 手动保存
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
        e.preventDefault()
        saveRef.current()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  async function selectDoc(slug: string, force = false) {
    if (!force && slug !== current?.slug && !await confirmDiscard()) return
    // 切换章节：本页内更新地址与表单（shallow，不重载）
    router.replace(`/book/writer/${encodeURIComponent(bookSlug)}/${encodeURIComponent(slug)}`, undefined, { shallow: true })
  }

  async function publish() {
    if (!title.trim()) { showToast({ message: current ? t('writer.needTitlePublish') : t('writer.needSelectAndTitle'), tone: 'error' }); return }
    const published = await save({ status: 'published' })
    // 发布成功后跳转到书籍详情页，让作者立即看到读者视角的成书效果。
    if (published) router.push(`/book/detail/${encodeURIComponent(bookSlug)}`)
  }

  async function removeDoc(doc: Document) {
    if (!book) return
    if (!await confirmAction({
      title: t('writer.deleteTitle'),
      message: t('writer.deleteMsg', { title: doc.title }),
      confirmLabel: t('writer.deleteConfirm'),
      danger: true,
    })) return
    try {
      await api(`/documents/${doc.id}`, { method: 'DELETE' })
      await loadTree(book)
      if (current?.id === doc.id) { resetForm(); router.push(`/book/writer/${encodeURIComponent(bookSlug)}`, undefined, { shallow: true }) }
    } catch (e) { showToast({ title: t('writer.deleteFailed'), message: (e as Error).message, tone: 'error' }) }
  }

  async function move(doc: Document, delta: -1 | 1) {
    if (!book) return
    const target = flatDocs.find((d) => d.sort_order === doc.sort_order + delta && d.parent_id === doc.parent_id)
    await api(`/documents/${doc.id}`, { method: 'PUT', body: { sort_order: doc.sort_order + delta } })
    if (target) await api(`/documents/${target.id}`, { method: 'PUT', body: { sort_order: doc.sort_order } })
    await loadTree(book)
  }

  // 收集某节点及其所有后代 id（用于禁止把节点拖进自己的子树）
  function subtreeIds(id: number): Set<number> {
    const set = new Set<number>([id])
    const walk = (d: Document) => (d.children || []).forEach((c) => { set.add(c.id); walk(c) })
    const root = flatDocs.find((d) => d.id === id)
    if (root) walk(root)
    return set
  }

  // 拖拽移动：支持跨层级。pos=before/after 挂到目标同级，inside 作为目标的子章节
  async function moveNode(dragDocId: number, targetId: number, pos: 'before' | 'inside' | 'after') {
    if (!book || dragDocId === targetId || reorderingId != null) return
    const drag = flatDocs.find((d) => d.id === dragDocId)
    const target = flatDocs.find((d) => d.id === targetId)
    if (!drag || !target) return
    if (subtreeIds(dragDocId).has(targetId)) return // 不能拖进自身子树

    let newParent: number | null
    let siblings: Document[]
    if (pos === 'inside') {
      newParent = target.id
      siblings = (target.children || []).slice()
    } else {
      newParent = target.parent_id ?? null
      siblings = (newParent === null ? tree : flatDocs.find((d) => d.id === newParent)?.children || []).slice()
    }
    const without = siblings.filter((d) => d.id !== dragDocId)
    let insertAt = without.length
    if (pos !== 'inside') {
      const idx = without.findIndex((d) => d.id === targetId)
      if (idx < 0) return
      insertAt = pos === 'before' ? idx : idx + 1
    }
    without.splice(insertAt, 0, drag)

    const parentChanged = (drag.parent_id ?? null) !== newParent
    const writes: Promise<unknown>[] = []
    without.forEach((d, i) => {
      if (d.id === dragDocId) {
        if (parentChanged || d.sort_order !== i) {
          writes.push(api(`/documents/${d.id}`, { method: 'PUT', body: { parent_id: newParent, sort_order: i } }))
        }
      } else if (d.sort_order !== i) {
        writes.push(api(`/documents/${d.id}`, { method: 'PUT', body: { sort_order: i } }))
      }
    })
    setReorderingId(dragDocId) // 拖拽排序期间在被拖章节行显示 loading，并禁止并发拖拽
    try {
      if (writes.length) await Promise.all(writes)
      if (pos === 'inside') setExpanded(new Set(expanded).add(target.id)) // 展开新父级以显示移入的子章节
      await loadTree(book)
    } finally {
      setReorderingId(null)
    }
  }

  // startNewChapter 在指定父级下追加一个空白新章节（parent 为 null 表示顶级）
  async function startNewChapter(parent: Document | null, siblingsCount: number) {
    if (!await confirmDiscard()) return
    const newParent = parent ? String(parent.id) : ''
    const newSort = siblingsCount
    if (parent) {
      setExpanded(new Set(expanded).add(parent.id)) // 展开父级，保存后新子章节可见
      setCreatingUnder(parent.id) // 新建期间保持父章节高亮作上下文
    } else {
      setCreatingUnder(null)
    }
    // 状态默认值：子章节跟随父章节（开启时）；第一级章节用书籍配置的「章节默认状态」。
    let newStatus: DocumentStatus = 'draft'
    if (parent) {
      if (book?.child_status_follow_parent && STATUS_META[parent.status as BookStatus]) newStatus = parent.status as DocumentStatus
    } else if (book?.default_chapter_status && STATUS_META[book.default_chapter_status as BookStatus]) {
      newStatus = book.default_chapter_status as DocumentStatus
    }
    setParentId(newParent); setStatus(newStatus)
    setCurrent(null)
    setTitle(''); setContent(''); setSortOrder(newSort); setAllowComments(true); setSlug(''); setExternalUrl(''); setExternalNewTab(true)
    snapshot.current = JSON.stringify(['', '', newStatus, newParent, newSort, true, '', '', true])
    loadedDocId.current = null
    setSaveState('dirty') // 新章节等待用户手动保存或发布
    setTimeout(() => textareaRef.current?.focus(), 0)
  }

  // 顶部「新建章节」：有选中项时建到该章节之下（子级），无选中项时建到顶级
  async function createNew() {
    await startNewChapter(current, current ? current.children?.length || 0 : tree.length)
  }

  // 右键菜单「新建子章节」：在目标章节之下追加子章节
  async function createChildOf(doc: Document) {
    await startNewChapter(doc, doc.children?.length || 0)
  }

  // 右键菜单「新建章节」：在目标章节的同级追加一个章节
  async function createSiblingOf(doc: Document) {
    const parent = doc.parent_id ? flatDocs.find((d) => d.id === doc.parent_id) || null : null
    const siblings = parent ? parent.children || [] : tree
    await startNewChapter(parent, siblings.length)
  }

  async function openWebImport(parent: Document | null = current) {
    if (!await confirmDiscard()) return
    setNewMenuOpen(false)
    setWebImportParent(parent)
    setWebImportOpen(true)
  }

  async function handleWebImported(document: Document) {
    setWebImportOpen(false)
    if (document.parent_id) setExpanded((value) => new Set(value).add(document.parent_id as number))
    if (book) await loadTree(book)
    await router.push(`/book/writer/${encodeURIComponent(bookSlug)}/${encodeURIComponent(document.slug)}`, undefined, { shallow: true })
    showToast({ message: t('writer.collectedDraft', { title: document.title }), tone: 'success' })
  }

  async function saveBookSettings() {
    if (!book) return
    setSavingBook(true)
    try {
      const payload = {
        title: bookForm.title.trim(), description: bookForm.description, status: bookForm.status,
        is_public: bookForm.isPublic, chapter_prefix: bookForm.chapterPrefix,
        child_status_follow_parent: bookForm.childStatusFollowParent,
        tags: bookForm.tags,
      }
      const updated = await api<Book>(`/books/${book.id}`, { method: 'PUT', body: payload })
      setBook(updated)
      if (updated.publish_held) showToast({ title: t('moderation.heldBookTitle'), message: updated.publish_held, tone: 'error' })
      else showToast({ message: t('writer.bookSettingsSaved'), tone: 'success' })
    } catch (e) { showToast({ title: t('writer.saveFailed'), message: (e as Error).message, tone: 'error' }) } finally { setSavingBook(false) }
  }

  async function applyRestoredDocument(restored: Document) {
    setTitle(restored.title)
    setContent(restored.content || '')
    setStatus(restored.status)
    setAllowComments(restored.allow_comments !== false)
    setSlug(restored.slug || '')
    setExternalUrl(restored.external_url || '')
    setExternalNewTab(restored.external_new_tab !== false)
    snapshot.current = JSON.stringify([
      restored.title, restored.content || '', restored.status,
      restored.parent_id ? String(restored.parent_id) : '', restored.sort_order,
      restored.allow_comments !== false, restored.slug || '',
      restored.external_url || '', restored.external_new_tab !== false,
    ])
    loadedDocId.current = restored.id
    setCurrent(restored)
    setSaveState('saved')
    showToast({ message: t('writer.versionRestored'), tone: 'success' })
    if (book) await loadTree(book)
  }

  // navigateAI 打开/切换/关闭 AI 写作助手抽屉（写入 URL，保留其余参数）。
  function navigateAI(tab: AIWriterTab | null) {
    const query = { ...router.query }
    if (tab) { query.ai = tab; delete query.comments }
    else delete query.ai
    const el = textareaRef.current
    if (el) setAISelectionLength(el.selectionEnd - el.selectionStart)
    void router.replace({ pathname: router.pathname, query }, undefined, { shallow: true })
  }
  function navigateComments(tab: CommentsTab | null) {
    const query = { ...router.query }
    if (tab) { query.comments = tab; delete query.ai }
    else { delete query.comments; setPendingComment(null) }
    void router.replace({ pathname: router.pathname, query }, undefined, { shallow: true })
  }
  // addComment 以选中的正文为锚点新建批注（没有选中时打开批注抽屉并提示）
  function addComment() {
    const el = textareaRef.current
    if (!current) return
    if (el && el.selectionEnd > el.selectionStart) setPendingComment(buildAnchor(el.value, el.selectionStart, el.selectionEnd))
    else showToast({ message: t('writer.comments.selectFirst'), tone: 'info' })
    navigateComments('open')
  }
  const loadCommentCounts = useCallback(async () => {
    if (!book) return
    try {
      setCommentCounts((await api<{ counts: Record<string, number> }>(`/books/${book.id}/writer-comments/counts`)).counts || {})
    } catch { /* 忽略 */ }
  }, [book])
  useEffect(() => { void loadCommentCounts() }, [loadCommentCounts])
  const loadSuggestionCounts = useCallback(async () => {
    if (!book) return
    try {
      setSuggestionCounts((await api<{ counts: Record<string, number> }>(`/books/${book.id}/suggestions/counts`)).counts || {})
    } catch { /* 忽略 */ }
  }, [book])
  useEffect(() => { void loadSuggestionCounts() }, [loadSuggestionCounts])
  const [aiSelectionLength, setAISelectionLength] = useState(0)
  const aiEditor: WriterEditorBridge = {
    read: () => {
      const el = textareaRef.current
      const value = el ? el.value : content
      return { content: value, start: el ? el.selectionStart : value.length, end: el ? el.selectionEnd : value.length }
    },
    write: (next, selStart, selEnd) => {
      setContent(next)
      requestAnimationFrame(() => {
        const el = textareaRef.current
        if (el) { el.focus(); el.setSelectionRange(selStart, selEnd) }
      })
    },
  }

  // 插入章节模板（变量由服务端替换）：正文为空时直接作为正文，否则插入到光标处并与前后内容空一行
  async function insertTemplate(tpl: TemplateSummary) {
    if (!book) return
    try {
      const { content: text } = await api<{ content: string }>(`/templates/${tpl.id}/render`, {
        method: 'POST', body: { book_id: book.id, chapter: title, tz: browserTimeZone() },
      })
      const { content: value, start, end } = aiEditor.read()
      if (!value.trim()) {
        aiEditor.write(text, text.length, text.length)
      } else {
        const before = value.slice(0, start)
        const after = value.slice(end)
        const lead = !before || before.endsWith('\n\n') ? '' : before.endsWith('\n') ? '\n' : '\n\n'
        const tail = !after || after.startsWith('\n') ? '' : '\n'
        const insert = lead + text.replace(/\n+$/, '\n') + tail
        aiEditor.write(before + insert + after, before.length + insert.length, before.length + insert.length)
      }
      setTemplateOpen(false)
      showToast({ message: t('templates.writer.inserted', { title: tpl.title }), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
  }

  async function saveChapterAsTemplate() {
    const value = aiEditor.read().content
    if (!value.trim()) {
      showToast({ message: t('templates.writer.emptyChapter'), tone: 'error' })
      return
    }
    const name = await requestInput({ title: t('templates.writer.saveTitle'), message: t('templates.writer.saveMessage'), label: t('templates.field.title'), defaultValue: title, confirmLabel: t('templates.writer.saveConfirm') })
    if (!name?.trim()) return
    setSavingTemplate(true)
    try {
      await api('/templates', { method: 'POST', body: { kind: 'chapter', title: name.trim(), content: value } })
      setTemplateReload((n) => n + 1)
      showToast({ message: t('templates.writer.saved', { title: name.trim() }), tone: 'success' })
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    }
    setSavingTemplate(false)
  }

  // Markdown 工具：选区包裹 / 行首插入
  function wrapSelection(before: string, after = before) {
    const el = textareaRef.current
    if (!el) return
    const s = el.selectionStart, e = el.selectionEnd, value = el.value
    const next = value.slice(0, s) + before + value.slice(s, e) + after + value.slice(e)
    setContent(next)
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(s + before.length, e + before.length) })
  }
  function insertAtLineStart(prefix: string) {
    const el = textareaRef.current
    if (!el) return
    const s = el.selectionStart
    const lineStart = el.value.lastIndexOf('\n', s - 1) + 1
    const next = el.value.slice(0, lineStart) + prefix + el.value.slice(lineStart)
    setContent(next)
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(s + prefix.length, s + prefix.length) })
  }

  async function insertLink() {
    const url = await requestInput({ title: t('writer.addLinkTitle'), label: t('writer.addLinkLabel'), defaultValue: 'https://', placeholder: 'https://example.com', confirmLabel: t('writer.insertLinkBtn') })
    if (url) wrapSelection('[', `](${url})`)
  }

  async function insertImage() {
    const url = await requestInput({ title: t('writer.addImageTitle'), message: t('writer.addImageMsg'), label: t('writer.addImageLabel'), defaultValue: 'https://', placeholder: 'https://example.com/image.png', confirmLabel: t('writer.insertImageBtn') })
    if (url) wrapSelection('![', `](${url})`)
  }

  // collectWebContent 采集网页正文并以 Markdown 插入到光标处（不建新章节）。
  async function collectWebContent() {
    const url = await requestInput({ title: t('writer.collectTitle'), message: t('writer.collectMsg'), label: t('writer.collectLabel'), defaultValue: 'https://', placeholder: 'https://example.com/article', confirmLabel: t('writer.collectBtn') })
    const target = (url || '').trim()
    if (!target || !/^https?:\/\//.test(target)) return
    setCollecting(true)
    try {
      const d = await api<{ title: string; markdown: string; source_url: string }>('/import/web-content', { method: 'POST', body: { url: target, render_mode: 'auto', book_id: book?.id } })
      // 是否附带来源默认关闭（collectIncludeSource 开关）：关闭时不追加来源脚注。
      const sourceNote = collectIncludeSource ? `\n\n> ${t('writer.collectSource')}：[${t('writer.collectOrigin')}](${d.source_url})\n` : '\n'
      const block = (d.title ? `## ${d.title}\n\n` : '') + d.markdown + sourceNote
      insertText('\n' + block)
      showToast({ message: t('writer.webCollected'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('writer.collectFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setCollecting(false)
    }
  }

  // importMarkdownFile 导入本地 Markdown 文件到当前章节：正文为空则整体载入，否则在光标处插入；
  // 标题为空时用文件名（去扩展名）填充，方便直接导入成一章。
  async function importMarkdownFile(file: File) {
    if (file.size > 5 * 1024 * 1024) { showToast({ title: t('writer.importMdFailed'), message: t('writer.importMdTooLarge'), tone: 'error' }); return }
    try {
      const text = await file.text()
      if (!content.trim()) setContent(text)
      else insertText('\n' + text)
      if (!title.trim()) setTitle(file.name.replace(/\.(md|markdown|mdown|txt)$/i, ''))
      showToast({ message: t('writer.importMdDone'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('writer.importMdFailed'), message: (e as Error).message, tone: 'error' })
    }
  }

  // lineOffset 计算行数组中第 idx 行起始的字符偏移（含各行后的换行符）。
  function lineOffset(arr: string[], idx: number) {
    return arr.slice(0, idx).reduce((n, l) => n + l.length + 1, 0)
  }

  // moveLines 将选区所跨的整行上/下移动一行，并保持这些行处于选中态。
  function moveLines(dir: -1 | 1) {
    const el = textareaRef.current
    if (!el) return
    const value = el.value
    const startLine = value.slice(0, el.selectionStart).split('\n').length - 1
    const endLine = value.slice(0, el.selectionEnd).split('\n').length - 1
    const lines = value.split('\n')
    if (dir === -1 && startLine === 0) return
    if (dir === 1 && endLine === lines.length - 1) return
    const block = lines.slice(startLine, endLine + 1)
    lines.splice(startLine, block.length)
    lines.splice(startLine + dir, 0, ...block)
    setContent(lines.join('\n'))
    const newStart = startLine + dir
    const lastIdx = newStart + block.length - 1
    const startOff = lineOffset(lines, newStart)
    const endOff = lineOffset(lines, lastIdx) + lines[lastIdx].length
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(startOff, endOff) })
  }

  // duplicateLines 在选区所跨整行下方复制一份，并选中副本。
  function duplicateLines() {
    const el = textareaRef.current
    if (!el) return
    const value = el.value
    const startLine = value.slice(0, el.selectionStart).split('\n').length - 1
    const endLine = value.slice(0, el.selectionEnd).split('\n').length - 1
    const lines = value.split('\n')
    const block = lines.slice(startLine, endLine + 1)
    lines.splice(endLine + 1, 0, ...block)
    setContent(lines.join('\n'))
    const dupStart = endLine + 1
    const lastIdx = dupStart + block.length - 1
    const startOff = lineOffset(lines, dupStart)
    const endOff = lineOffset(lines, lastIdx) + lines[lastIdx].length
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(startOff, endOff) })
  }

  // indentSelection 对选区所跨的每一行整体缩进（+2 空格）或反缩进（去掉行首至多 2 空格）。
  function indentSelection(dir: 1 | -1) {
    const el = textareaRef.current
    if (!el) return
    const value = el.value
    const startLine = value.slice(0, el.selectionStart).split('\n').length - 1
    const endLine = value.slice(0, el.selectionEnd).split('\n').length - 1
    const lines = value.split('\n')
    for (let i = startLine; i <= endLine; i++) {
      lines[i] = dir === 1 ? '  ' + lines[i] : lines[i].replace(/^ {1,2}/, '')
    }
    setContent(lines.join('\n'))
    const startOff = lineOffset(lines, startLine)
    const endOff = lineOffset(lines, endLine) + lines[endLine].length
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(startOff, endOff) })
  }

  // insertText 在光标处替换选区插入文本，并把光标移到插入内容之后。
  function insertText(text: string) {
    const el = textareaRef.current
    if (!el) return
    const s = el.selectionStart, e = el.selectionEnd
    const next = el.value.slice(0, s) + text + el.value.slice(e)
    setContent(next)
    const pos = s + text.length
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(pos, pos) })
  }

  function insertTable() {
    insertText(`\n| ${t('writer.tableCol')} 1 | ${t('writer.tableCol')} 2 |\n| --- | --- |\n| ${t('writer.tableCell')} | ${t('writer.tableCell')} |\n`)
  }

  // 插入结构化组件片段（Tabs / Note / Warning / Tip / Accordion / Steps / CardGroup）；阅读页与预览均可渲染
  const COMPONENT_SNIPPETS: Record<string, string> = {
    tabs: `\n<Tabs>\n<Tab title="${t('writer.snip.tabTitle1')}">\n\n${t('writer.snip.content1')}\n\n</Tab>\n<Tab title="${t('writer.snip.tabTitle2')}">\n\n${t('writer.snip.content2')}\n\n</Tab>\n</Tabs>\n`,
    note: `\n<Note>\n**${t('writer.snip.noteTitle')}**\n\n${t('writer.snip.noteBody')}\n</Note>\n`,
    tip: `\n<Tip>\n**${t('writer.snip.tipTitle')}**\n\n${t('writer.snip.tipBody')}\n</Tip>\n`,
    warning: `\n<Warning>\n**${t('writer.snip.warnTitle')}**\n\n${t('writer.snip.warnBody')}\n</Warning>\n`,
    accordion: `\n<AccordionGroup>\n<Accordion title="${t('writer.snip.accTitle1')}">\n\n${t('writer.snip.content1')}\n\n</Accordion>\n<Accordion title="${t('writer.snip.accTitle2')}">\n\n${t('writer.snip.content2')}\n\n</Accordion>\n</AccordionGroup>\n`,
    cards: `\n<CardGroup cols={2}>\n<Card title="${t('writer.snip.cardTitle1')}" icon="book">\n${t('writer.snip.cardBody')}\n</Card>\n<Card title="${t('writer.snip.cardTitle2')}" icon="rocket">\n${t('writer.snip.cardBody')}\n</Card>\n</CardGroup>\n`,
    steps: `\n<Steps>\n<Step title="${t('writer.snip.stepTitle1')}">\n\n${t('writer.snip.stepBody')}\n\n</Step>\n<Step title="${t('writer.snip.stepTitle2')}">\n\n${t('writer.snip.stepBody')}\n\n</Step>\n</Steps>\n`,
  }
  function insertComponent(kind: keyof typeof COMPONENT_SNIPPETS) {
    insertText(COMPONENT_SNIPPETS[kind])
    setInsertMenuOpen(false)
  }
  // 插入「子章节目录」宏；渲染时替换为当前章节的直接子章节链接列表
  function insertChildrenToc() {
    insertText('\n[children]\n')
    setInsertMenuOpen(false)
  }

  // 元数据：章节图标。文档开头的 <!-- icon: xxx --> 或 front-matter 中的 icon 键（见 lib/doc-meta）；正文中的同样注释不算元数据。
  async function setDocIcon() {
    setMetaMenuOpen(false)
    const el = textareaRef.current
    const value = el?.value ?? content
    const currentIcon = parseDocMeta(value).meta.icon || ''
    const input = await requestInput({
      title: t('writer.setIconTitle'),
      message: t('writer.setIconMsg'),
      label: t('writer.setIconLabel'), defaultValue: currentIcon, placeholder: 'database', confirmLabel: t('common.actions.save'),
    })
    if (input === null) return // 取消
    const icon = input.trim()
    const next = setDocIconMeta(value, icon)
    setContent(next)
    showToast({ message: icon ? t('writer.iconSet') : t('writer.iconRemoved'), tone: 'success' })
  }

  // 可选目标语言（后台翻译服务负责实际转换）
  const TRANSLATE_TARGETS: { code: string; label: string }[] = [
    { code: 'en', label: 'English' },
    { code: 'zh-CN', label: '简体中文' },
    { code: 'zh-TW', label: '繁體中文' },
    { code: 'ja', label: '日本語' },
    { code: 'ko', label: '한국어' },
    { code: 'fr', label: 'Français' },
    { code: 'de', label: 'Deutsch' },
    { code: 'es', label: 'Español' },
    { code: 'ru', label: 'Русский' },
  ]
  // 翻译：有选区则译选区并原地替换；否则译整章内容（替换前需确认，避免误覆盖）。
  async function translateContent(target: string, label: string) {
    setTranslateMenuOpen(false)
    const el = textareaRef.current
    if (!el) return
    const hasSelection = el.selectionStart !== el.selectionEnd
    const source = hasSelection ? el.value.slice(el.selectionStart, el.selectionEnd) : el.value
    if (!source.trim()) { showToast({ message: t('writer.noTranslateContent'), tone: 'error' }); return }
    if (!hasSelection) {
      const okToReplace = await confirmAction({
        title: t('writer.translateToTitle', { label }),
        message: t('writer.translateMsg'),
        confirmLabel: t('writer.translateConfirm'),
      })
      if (!okToReplace) return
    }
    const selStart = el.selectionStart
    const selEnd = el.selectionEnd
    setTranslating(true)
    try {
      const d = await api<{ text: string }>('/translate', { method: 'POST', body: { text: source, target_lang: target, target_label: label, ...(current ? { ref_type: 'document', ref_id: current.id } : {}) } })
      if (hasSelection) {
        const next = el.value.slice(0, selStart) + d.text + el.value.slice(selEnd)
        setContent(next)
        requestAnimationFrame(() => { el.focus(); el.setSelectionRange(selStart, selStart + d.text.length) })
      } else {
        setContent(d.text)
      }
      showToast({ message: t('writer.translated'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('writer.translateFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setTranslating(false)
    }
  }

  function insertCodeBlock() {
    const el = textareaRef.current
    if (!el) return
    const s = el.selectionStart, e = el.selectionEnd
    const selected = el.value.slice(s, e)
    const block = '```\n' + (selected || '') + '\n```\n'
    const next = el.value.slice(0, s) + block + el.value.slice(e)
    setContent(next)
    const pos = s + 4 // 光标落在 ``` 之后的空行（语言标识可选补写）
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(pos, pos) })
  }

  // 上传图片文件并在光标处插入 Markdown；先插占位符，成功后替换、失败后移除。
  async function uploadAndInsertImage(file: File) {
    if (!file.type.startsWith('image/')) {
      showToast({ title: t('writer.onlyImages'), message: t('writer.selectImageFile'), tone: 'error' })
      return
    }
    const token = `uploading-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
    const placeholder = `![${t('writer.uploadingImg')}](${token})`
    insertText(placeholder)
    setUploading(true)
    try {
      const fd = new FormData()
      fd.append('file', file)
      const res = await fetch(`${API_BASE}/api/v1/upload`, { method: 'POST', headers: { Authorization: `Bearer ${getToken()}` }, body: fd })
      const payload = await res.json().catch(() => ({}))
      if (!res.ok || payload.success === false) throw new Error(payload.message || t('writer.uploadFailed'))
      const alt = file.name.replace(/\.[^.]+$/, '') || t('writer.imageAlt')
      setContent((prev) => prev.replace(placeholder, `![${alt}](${payload.data.url})`))
    } catch (err) {
      setContent((prev) => prev.replace(placeholder, ''))
      showToast({ title: t('writer.imageUploadFailed'), message: (err as Error).message, tone: 'error' })
    } finally {
      setUploading(false)
    }
  }

  async function uploadImages(files: FileList | File[]) {
    const images = Array.from(files).filter((f) => f.type.startsWith('image/'))
    for (const file of images) {
      // 顺序上传，保证插入顺序与占位符替换稳定
      // eslint-disable-next-line no-await-in-loop
      await uploadAndInsertImage(file)
    }
  }

  // collectImageFiles 从剪贴板/拖拽数据里收集图片文件：优先 items.getAsFile()
  // （截图、从网页复制的图片常只在 items 里，files 为空），再并入 files，按 名称+大小+类型 去重。
  function collectImageFiles(dt: DataTransfer | null): File[] {
    if (!dt) return []
    const out: File[] = []
    const seen = new Set<string>()
    const add = (f: File | null) => {
      if (!f || !f.type.startsWith('image/')) return
      const key = `${f.name}:${f.size}:${f.type}`
      if (seen.has(key)) return
      seen.add(key)
      out.push(f)
    }
    for (const item of Array.from(dt.items || [])) {
      if (item.kind === 'file') add(item.getAsFile())
    }
    for (const f of Array.from(dt.files || [])) add(f)
    return out
  }

  // selectRange 选中编辑区某区间并按行比例滚动到该处（分栏时预览随之同步）。
  function selectRange(start: number, end: number) {
    const el = textareaRef.current
    if (!el) return
    el.focus()
    el.setSelectionRange(start, end)
    const linesBefore = content.slice(0, start).split('\n').length - 1
    const totalLines = content.split('\n').length
    const ratio = totalLines > 1 ? linesBefore / totalLines : 0
    el.scrollTop = ratio * (el.scrollHeight - el.clientHeight)
    scrollLock.current = 'edit'
    syncScroll('edit')
  }

  function jumpToHeading(offset: number) {
    selectRange(offset, offset)
  }

  function selectMatch(idx: number) {
    if (matches.length === 0) return
    const i = ((idx % matches.length) + matches.length) % matches.length
    setActiveMatch(i)
    selectRange(matches[i], matches[i] + findText.length)
  }
  function findNext() { selectMatch(activeMatch + 1) }
  function findPrev() { selectMatch(activeMatch - 1) }

  function replaceCurrent() {
    if (matches.length === 0) return
    const i = Math.min(activeMatch, matches.length - 1)
    const off = matches[i]
    setContent(content.slice(0, off) + replaceText + content.slice(off + findText.length))
    requestAnimationFrame(() => {
      const el = textareaRef.current
      if (el) { const pos = off + replaceText.length; el.focus(); el.setSelectionRange(pos, pos) }
    })
  }

  function replaceAll() {
    if (!findText || matches.length === 0) return
    const next = caseSensitive
      ? content.split(findText).join(replaceText)
      : content.replace(new RegExp(findText.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'gi'), () => replaceText)
    setContent(next)
    setActiveMatch(0)
    showToast({ message: t('writer.replacedCount', { n: matches.length }), tone: 'success' })
  }

  function closeFind() {
    setFindOpen(false)
    requestAnimationFrame(() => textareaRef.current?.focus())
  }

  // refreshSlash 根据光标处的 "/查询" 上下文开/关斜杠菜单并更新位置（在编辑区 onChange 时调用）。
  function refreshSlash() {
    const el = textareaRef.current
    if (!el || el.selectionStart !== el.selectionEnd) {
      setSlash((s) => (s.open ? { ...s, open: false } : s))
      return
    }
    const pos = el.selectionStart
    const value = el.value
    const lineStart = value.lastIndexOf('\n', pos - 1) + 1
    const before = value.slice(lineStart, pos)
    const link = before.match(/\[\[([^[\]|\n]*)$/) // 未闭合的 "[[查询"：弹出章节选择
    if (link) {
      const start = lineStart + (link.index ?? 0)
      const c = caretCoordinates(el, start)
      setSlash({ open: true, start, query: link[1], top: c.top + c.lineHeight, left: c.left, index: 0, mode: 'link' })
      return
    }
    const m = before.match(/(^|\s)(?:\/|\$\.)([^\s/]*)$/) // 行首或空白后的 "/查询" 或 "$.查询" 宏（查询内无空格）
    if (!m) {
      setSlash((s) => (s.open ? { ...s, open: false } : s))
      return
    }
    const slashOffset = lineStart + (m.index ?? 0) + m[1].length
    const c = caretCoordinates(el, slashOffset)
    setSlash({ open: true, start: slashOffset, query: m[2], top: c.top + c.lineHeight, left: c.left, index: 0, mode: 'cmd' })
  }

  function closeSlash() {
    setSlash((s) => (s.open ? { ...s, open: false } : s))
  }

  // selectLink 把已输入的 "[[查询" 替换为指向所选章节的双向链接（标题在本书唯一时用标题，否则用 slug）。
  function selectLink(doc: { slug: string; title: string }) {
    const el = textareaRef.current
    if (!el) return
    const value = el.value
    const end = el.selectionStart
    const title = doc.title.trim()
    const unique = title && !title.includes('|') && wikiDocs.filter((d) => d.title.trim().toLowerCase() === title.toLowerCase()).length === 1
    // 光标后紧跟自动配对的 "]]" 时一并替换，避免重复
    const tail = value.slice(end, end + 2) === ']]' ? 2 : 0
    const insert = `[[${unique ? title : doc.slug}]]`
    setContent(value.slice(0, slash.start) + insert + value.slice(end + tail))
    setSlash((s) => ({ ...s, open: false }))
    const caret = slash.start + insert.length
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(caret, caret) })
  }

  // selectSlash 删除已输入的 "/查询" 再执行对应插入动作。
  function selectSlash(cmd: { key: string }) {
    const el = textareaRef.current
    if (!el) return
    const value = el.value
    const end = el.selectionStart
    setContent(value.slice(0, slash.start) + value.slice(end))
    setSlash((s) => ({ ...s, open: false }))
    requestAnimationFrame(() => {
      el.focus()
      el.setSelectionRange(slash.start, slash.start)
      const heading = HEADING_LEVELS.find((h) => h.prefix.trim() === '#'.repeat(Number(cmd.key.slice(1)) || 0))
      switch (cmd.key) {
        case 'h1': case 'h2': case 'h3': case 'h4': case 'h5': case 'h6':
          if (heading) insertAtLineStart(heading.prefix)
          break
        case 'ul': insertAtLineStart('- '); break
        case 'ol': insertAtLineStart('1. '); break
        case 'task': insertAtLineStart('- [ ] '); break
        case 'quote': insertAtLineStart('> '); break
        case 'code': insertCodeBlock(); break
        case 'table': insertTable(); break
        case 'tabs': insertComponent('tabs'); break
        case 'note': insertComponent('note'); break
        case 'tip': insertComponent('tip'); break
        case 'warning': insertComponent('warning'); break
        case 'accordion': insertComponent('accordion'); break
        case 'steps': insertComponent('steps'); break
        case 'cards': insertComponent('cards'); break
        case 'children': insertChildrenToc(); break
        case 'hr': insertText('---\n'); break
        case 'image': fileInputRef.current?.click(); break
        case 'collect': if (collectEnabled) void collectWebContent(); break
        case 'template': if (templatesOn) setTemplateOpen(true); break
        case 'ai': if (aiOn) navigateAI('assist'); break
        case 'import-md': mdImportRef.current?.click(); break
        case 'link': void insertLink(); break
      }
    })
  }

  // syncScroll 分栏模式下按比例双向同步编辑区与预览区滚动；scrollLock 防止两边互相触发导致抖动。
  function syncScroll(from: 'edit' | 'preview' = 'edit') {
    if (!splitPreview) return
    const el = textareaRef.current, pv = previewRef.current
    if (!el || !pv) return
    if (scrollLock.current && scrollLock.current !== from) { scrollLock.current = null; return }
    scrollLock.current = from
    const src = from === 'edit' ? el : pv
    const dst = from === 'edit' ? pv : el
    const denom = src.scrollHeight - src.clientHeight
    const ratio = denom > 0 ? src.scrollTop / denom : 0
    dst.scrollTop = ratio * (dst.scrollHeight - dst.clientHeight)
  }

  function onEditorPaste(e: React.ClipboardEvent<HTMLTextAreaElement>) {
    const files = collectImageFiles(e.clipboardData)
    if (files.length > 0) {
      e.preventDefault() // 有图片时拦截默认粘贴，避免同时插入图片的文本表示
      void uploadImages(files)
      return
    }
    // 选中文本时粘贴 URL：包成 [选中文本](url)
    const el = textareaRef.current
    const text = (e.clipboardData?.getData('text/plain') || '').trim()
    if (el && el.selectionStart !== el.selectionEnd && /^https?:\/\/\S+$/.test(text)) {
      e.preventDefault()
      wrapSelection('[', `](${text})`)
    }
  }

  function onEditorDrop(e: React.DragEvent<HTMLTextAreaElement>) {
    const files = collectImageFiles(e.dataTransfer)
    if (files.length > 0) {
      e.preventDefault()
      void uploadImages(files)
    }
  }

  // 编辑器按键增强：加粗/斜体/链接快捷键、列表/引用回车续行、Tab 缩进列表。
  function onEditorKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    const el = textareaRef.current
    if (!el) return
    // ⌘/Ctrl + Alt + M：为选中的文字添加批注
    if ((e.metaKey || e.ctrlKey) && e.altKey && (e.key === 'm' || e.key === 'M' || e.code === 'KeyM')) { e.preventDefault(); addComment(); return }
    if (slash.open && menuCount > 0) {
      if (e.key === 'ArrowDown') { e.preventDefault(); setSlash((s) => ({ ...s, index: Math.min(s.index + 1, menuCount - 1) })); return }
      if (e.key === 'ArrowUp') { e.preventDefault(); setSlash((s) => ({ ...s, index: Math.max(0, s.index - 1) })); return }
      if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault()
        const i = Math.min(slash.index, menuCount - 1)
        if (slash.mode === 'link') selectLink(filteredLinks[i])
        else selectSlash(filteredSlash[i])
        return
      }
      if (e.key === 'Escape') { e.preventDefault(); closeSlash(); return }
    }
    if (e.key === 'Escape' && focusMode && !findOpen) { e.preventDefault(); setFocusMode(false); return }
    // Alt+↑/↓ 移动整行
    if (e.altKey && !e.metaKey && !e.ctrlKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) {
      e.preventDefault()
      moveLines(e.key === 'ArrowUp' ? -1 : 1)
      return
    }
    // 有选区时按配对符包裹选中文本（非组合键、非 IME 输入中）
    if (!e.metaKey && !e.ctrlKey && !e.altKey && !e.nativeEvent.isComposing &&
        el.selectionStart !== el.selectionEnd && e.key.length === 1 && WRAP_PAIRS[e.key]) {
      e.preventDefault()
      wrapSelection(e.key, WRAP_PAIRS[e.key])
      return
    }
    if (e.metaKey || e.ctrlKey) {
      const k = e.key.toLowerCase()
      if (k === 'b') { e.preventDefault(); wrapSelection('**'); return }
      if (k === 'i') { e.preventDefault(); wrapSelection('*'); return }
      if (k === 'k') { e.preventDefault(); void insertLink(); return }
      if (k === 'd' && e.shiftKey) { e.preventDefault(); duplicateLines(); return }
      if (k === 'f') {
        e.preventDefault()
        const sel = el.value.slice(el.selectionStart, el.selectionEnd)
        if (sel && !sel.includes('\n')) setFindText(sel)
        setFindOpen(true)
        return
      }
      return
    }
    const value = el.value
    const lineStart = value.lastIndexOf('\n', el.selectionStart - 1) + 1
    const lineEnd = value.indexOf('\n', el.selectionStart)
    const line = value.slice(lineStart, lineEnd === -1 ? value.length : lineEnd)
    const listMatch = line.match(/^(\s*)([-*+]|\d+\.|- \[[ x]\]|>)(\s+)(.*)$/)

    // 多行选区按 Tab 整体缩进 / Shift+Tab 反缩进
    if (e.key === 'Tab' && el.selectionStart !== el.selectionEnd && value.slice(el.selectionStart, el.selectionEnd).includes('\n')) {
      e.preventDefault()
      indentSelection(e.shiftKey ? -1 : 1)
      return
    }

    if (e.key === 'Enter' && !e.shiftKey && listMatch && el.selectionStart === el.selectionEnd) {
      const [, indent, marker, gap, rest] = listMatch
      // 空列表项按回车：退出列表（清空该行）
      if (rest.trim() === '') {
        e.preventDefault()
        const next = value.slice(0, lineStart) + value.slice(lineEnd === -1 ? value.length : lineEnd)
        setContent(next)
        requestAnimationFrame(() => { el.focus(); el.setSelectionRange(lineStart, lineStart) })
        return
      }
      // 续行：有序列表自增序号，任务列表新建未勾选项
      let nextMarker = marker
      const ordered = /^\d+\.$/.test(marker)
      if (ordered) nextMarker = `${parseInt(marker, 10) + 1}.`
      else if (/^- \[[ x]\]$/.test(marker)) nextMarker = '- [ ]'
      const insert = `\n${indent}${nextMarker}${gap}`
      e.preventDefault()
      insertText(insert)
      return
    }

    if (e.key === 'Tab' && listMatch) {
      e.preventDefault()
      if (e.shiftKey) {
        // 退格缩进：去掉行首两个空格
        if (line.startsWith('  ')) {
          const next = value.slice(0, lineStart) + line.slice(2) + value.slice(lineEnd === -1 ? value.length : lineEnd)
          setContent(next)
          const pos = Math.max(lineStart, el.selectionStart - 2)
          requestAnimationFrame(() => { el.focus(); el.setSelectionRange(pos, pos) })
        }
      } else {
        const next = value.slice(0, lineStart) + '  ' + value.slice(lineStart)
        setContent(next)
        const pos = el.selectionStart + 2
        requestAnimationFrame(() => { el.focus(); el.setSelectionRange(pos, pos) })
      }
    }
  }

  async function navigateAway(href: string) {
    if (!await confirmDiscard()) return
    await router.push(href)
  }

  if (!user) return <Loading className="min-h-screen" label={t('writer.verifyingPerms')} />

  const chapterPrefix = book?.chapter_prefix || ''
  const byId = new Map(flatDocs.map((d) => [d.id, d]))
  function isDescendantOf(doc: Document, ancestorId: number): boolean {
    let p = doc.parent_id
    while (p !== null && p !== undefined) {
      if (p === ancestorId) return true
      p = byId.get(p)?.parent_id ?? null
    }
    return false
  }
  const parentCandidates = flatDocs.filter((d) => !current || (d.id !== current.id && !isDescendantOf(d, current.id)))
  const parentDoc = parentId ? flatDocs.find((d) => String(d.id) === parentId) : null
  const wordCount = content.replace(/\s/g, '').length
  const readingMinutes = wordCount ? Math.max(1, Math.round(wordCount / 400)) : 0 // 约 400 字/分钟
  const dragBlocked = dragId != null ? subtreeIds(dragId) : null

  if (!book) {
    return (
      <div className="flex h-screen items-center justify-center bg-warm">
        <Loading label={t('writer.loadingBook')} />
      </div>
    )
  }

  const titleText = current ? `${chapterPrefix}${current.title} · ${book.title}` : `${t('writer.newChapterTitle')} · ${book.title}`

  const filteredTree = search.trim() ? filterTree(tree, search.trim()) : tree

  return (
    <div className={`flex h-screen flex-col bg-warm ${aiTab || commentsTab ? 'lg:pr-96 2xl:pr-[28rem]' : ''}`}>
      <Seo siteName={siteName} title={titleText} noindex />
      {conflict && (
        <ConflictDialog state={conflict} onResolve={resolveConflict}
          onUseTheirs={() => { conflict.resolve({ kind: 'theirs', document: conflict.theirs }); setConflict(null) }}
          onCancel={() => { conflict.resolve(null); setConflict(null) }} />
      )}
      {templatesOn && (
        <TemplatePicker open={templateOpen} kind="chapter" onClose={() => setTemplateOpen(false)} onUse={insertTemplate} reloadKey={templateReload}
          useLabel={t('templates.writer.insert')}
          footerExtra={(
            <Button size="sm" variant="ghost" onClick={() => void saveChapterAsTemplate()} loading={savingTemplate} data-testid="template-save-chapter">
              <i className="fa-regular fa-floppy-disk" aria-hidden="true" /> {t('templates.writer.saveCurrent')}
            </Button>
          )} />
      )}
      {commentsTab && (
        <CommentsDrawer bookId={book.id} docId={current?.id ?? null} tab={commentsTab} onNavigate={navigateComments}
          pending={pendingComment} onClearPending={() => setPendingComment(null)} content={content}
          onSelect={(start, end) => selectRange(start, end)} reloadKey={commentsReload} onChanged={() => void loadCommentCounts()}
          currentUserId={user?.id ?? 0} canManage={!!user && (book.user_id === user.id || user.role === 'admin')}
          canReview={!suggestOnly} suggestionCount={current ? suggestionCounts[current.id] || 0 : 0} suggestionsReload={suggestionsReload}
          onReview={setReviewing} />
      )}
      {reviewing && (
        <SuggestionReview suggestion={reviewing} onClose={() => setReviewing(null)}
          onApply={(accepted) => applySuggestion(reviewing, accepted)} onReject={() => rejectSuggestion(reviewing)} />
      )}
      {suggestDialog && (
        <Modal open elevated onClose={() => { if (!submittingSuggestion) setSuggestDialog(false) }} title={t('writer.suggest.submitTitle')}
          footer={(
            <>
              <Button variant="outline" disabled={submittingSuggestion} onClick={() => setSuggestDialog(false)}>{t('writer.collab.cancel')}</Button>
              <Button loading={submittingSuggestion} onClick={() => void submitSuggestion()} data-testid="suggest-confirm">{t('writer.suggest.submit')}</Button>
            </>
          )}>
          <p className="text-sm text-slate-600">{t('writer.suggest.submitMessage')}</p>
          <Textarea className="mt-3 min-h-[88px] text-sm" value={suggestNote} maxLength={500} onChange={(e) => setSuggestNote(e.target.value)} placeholder={t('writer.suggest.notePlaceholder')} />
        </Modal>
      )}
      {aiTab && (
        <AIWriterDrawer bookId={book.id} docId={current?.id ?? null} tab={aiTab} onNavigate={navigateAI}
          selectionLength={aiSelectionLength} editor={aiEditor} />
      )}
      {/* 顶栏 */}
      <header className="flex h-14 shrink-0 items-center justify-between gap-4 border-b border-slate-200 bg-white px-4">
        <div className="flex min-w-0 items-center gap-2 text-sm">
          <Link href="/" onClick={(event) => { event.preventDefault(); navigateAway('/') }} className="flex shrink-0 items-center gap-2 font-bold text-slate-900">
            <img src="/logo.png" alt="" className="h-8 w-8 object-contain" />
            {siteName}
          </Link>
          <span className="text-slate-300">/</span>
          <Link href="/books" onClick={(event) => { event.preventDefault(); navigateAway('/books') }} className="shrink-0 text-slate-500 hover:text-primary-600">{t('writer.myBooks')}</Link>
          <span className="text-slate-300">/</span>
          <Link href={`/book/detail/${encodeURIComponent(book.slug)}`} onClick={(event) => { event.preventDefault(); navigateAway(`/book/detail/${encodeURIComponent(book.slug)}`) }}
            className="truncate font-medium text-slate-900 hover:text-primary-600">{book.title}</Link>
        </div>
        <div className={`hidden items-center gap-1.5 whitespace-nowrap text-sm text-slate-400 ${aiTab ? '2xl:flex' : 'md:flex'}`}>
          {saveState === 'saved' && <><CheckCircleIcon className="h-4 w-4 text-emerald-500" /> {t('writer.allSaved')}</>}
          {saveState === 'dirty' && <><CloudIcon className="h-4 w-4 text-amber-500" /> {t('writer.unsaved')}</>}
          {saveState === 'saving' && <><span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-slate-200 border-t-primary-500" /> <span className="text-primary-600">{t('writer.savingState')}</span></>}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <CollabAvatars others={collab.others} docTitle={(id) => flatDocs.find((d) => d.id === id)?.title || ''} />
          {site.help_doc_url && (
            <Button variant="ghost" title={t('writer.mdHelp')} onClick={() => window.open(site.help_doc_url, '_blank', 'noopener,noreferrer')}>
              <InfoCircleIcon className="h-4 w-4" /> <span className="hidden md:inline">{t('writer.help')}</span>
            </Button>
          )}
          {!suggestOnly && (
            <Button variant="ghost" onClick={() => setHistoryOpen(true)} disabled={!current}>
              <HistoryIcon className="h-4 w-4" /> {t('writer.history')}
            </Button>
          )}
          <Button variant="ghost" className="hidden md:inline-flex" onClick={() => { setSplitPreview((v) => !v); setPreview(false) }}>
            <ColumnsIcon className="h-4 w-4" /> {splitPreview ? t('writer.exitSplit') : t('writer.split')}
          </Button>
          <Button variant="ghost" onClick={() => { setPreview((p) => !p); setSplitPreview(false) }}>
            <EyeIcon className="h-4 w-4" /> {preview ? t('writer.edit') : t('writer.preview')}
          </Button>
          {suggestOnly ? (
            <Button onClick={openSuggestDialog} disabled={!current || submittingSuggestion} data-testid="suggest-submit">
              <i className="fa-solid fa-code-pull-request" aria-hidden="true" /> {t('writer.suggest.button')}
            </Button>
          ) : (
            <>
              <Button variant="outline" onClick={() => saveRef.current()} disabled={saveState === 'saving'}>
                <SaveIcon className="h-4 w-4" /> {saveState === 'saving' ? t('writer.savingState') : t('writer.save')}
              </Button>
              <Button onClick={publish} disabled={saveState === 'saving'}>
                <UploadIcon className="h-4 w-4" /> {t('writer.publish')}
              </Button>
            </>
          )}
        </div>
      </header>

      {/* 透明遮罩：任一浮层菜单打开时点击外部即关闭（菜单层级更高，不受影响） */}
      {newMenuOpen && (
        <div className="fixed inset-0 z-10" onClick={() => setNewMenuOpen(false)} />
      )}

      <div className="flex min-h-0 flex-1">
        {/* 左栏：书籍与章节树 */}
        <aside className="flex w-72 shrink-0 flex-col border-r border-slate-200 bg-white">
          <div className="flex items-center gap-3 border-b border-slate-100 p-4">
            <div className="h-14 w-14 shrink-0 overflow-hidden rounded-lg bg-gradient-to-br from-primary-300 to-[#8B8DFF]">
              {book.cover_image && <img src={book.cover_image} alt="" className="h-full w-full object-cover" />}
            </div>
            <div className="min-w-0">
              <div className="truncate font-semibold text-slate-900">{book.title}</div>
              <div className="mt-1"><Badge tone={STATUS_META[book.status].tone}>{t(STATUS_META[book.status].labelKey)}</Badge></div>
            </div>
          </div>

          <div className={suggestOnly ? 'hidden' : 'px-3 pt-3'}>
            <SegmentedTabs fullWidth size="sm" value={tab} ariaLabel={t('writer.sidebarAria')}
              onChange={(value) => setTab(value as TabKey)} items={[
                { value: 'toc', label: t('writer.tabToc') },
                { value: 'settings', label: t('writer.tabSettings') },
              ]} />
          </div>

          {tab === 'toc' || suggestOnly ? (
            <div className="flex min-h-0 flex-1 flex-col">
              <div className="p-3">
                <div className="relative">
                  <SearchIcon className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
                  <Input className="pl-9" placeholder={t('writer.searchChapter')} value={search} onChange={(e) => setSearch(e.target.value)} />
                </div>
                {suggestOnly ? (
                  <p className="mt-2.5 rounded-lg bg-sky-50 px-3 py-2 text-xs text-sky-800" data-testid="suggest-hint">{t('writer.suggest.hint')}</p>
                ) : (
                <div className="relative mt-2.5">
                  <button onClick={() => createNew()}
                    className="flex w-full items-center justify-center gap-1.5 rounded-lg border border-primary-500 text-sm font-medium text-primary-600 transition-colors hover:bg-primary-50"
                    style={{ height: 'var(--control-height)' }}>
                    + {t('writer.newChapterBtn')}
                  </button>
                  <button onClick={() => setNewMenuOpen(!newMenuOpen)} aria-label={t('writer.moreCreate')}
                    className="absolute right-1 top-1 flex items-center justify-center rounded-md text-primary-600 hover:bg-primary-50"
                    style={{ width: 'var(--control-height-sm)', height: 'var(--control-height-sm)' }}>
                    <ChevronDownIcon className="h-4 w-4" />
                  </button>
                  {newMenuOpen && (
                    <div className="absolute left-0 right-0 top-11 z-20 overflow-hidden rounded-lg border border-slate-200 bg-white py-1 shadow-lg">
                      <button onClick={() => { createNew(); setNewMenuOpen(false) }} className="flex w-full items-center gap-2 px-3 py-2 text-sm hover:bg-slate-50"><FileTextIcon className="h-4 w-4 text-slate-400" /> {t('writer.newChapterBtn')}</button>
                      <button onClick={() => { setNewMenuOpen(false); if (!current) { showToast({ message: t('writer.needParent'), tone: 'error' }); return } createNew() }} className="flex w-full items-center gap-2 px-3 py-2 text-sm hover:bg-slate-50"><FolderIcon className="h-4 w-4 text-slate-400" /> {t('writer.newSubChapter')}</button>
                      {collectEnabled && <button onClick={() => openWebImport()} className="flex w-full items-center gap-2 px-3 py-2 text-sm hover:bg-slate-50"><GlobeIcon className="h-4 w-4 text-slate-400" /> {t('writer.fromWeb')}</button>}
                      <button onClick={() => { setNewMenuOpen(false); mdInputRef.current?.click() }} className="flex w-full items-center gap-2 px-3 py-2 text-sm hover:bg-slate-50"><i className="fa-brands fa-markdown w-4 text-center text-slate-400" aria-hidden="true" /> {t('writer.fromMarkdown')}</button>
                    </div>
                  )}
                  <input ref={mdInputRef} type="file" multiple accept=".md,.markdown,.zip,application/zip,image/*" className="sr-only" aria-label={t('writer.fromMarkdown')}
                    onChange={(e) => void importMarkdown(Array.from(e.target.files || []))} />
                  {importingMd && <div className="mt-2 rounded-lg border border-primary-100 bg-primary-50/50 px-3 py-2"><Loading className="py-0.5" label={t('writer.importingMarkdown')} /></div>}
                </div>
                )}
              </div>
              <div ref={tocScrollRef} className="min-h-0 flex-1 overflow-y-auto overflow-x-auto px-3 pb-2"
                onClick={(e) => { if (e.target === e.currentTarget) deselect() }}>
                {filteredTree.length === 0 ? (
                  <EmptyState>{search ? t('writer.noMatch') : t('writer.noChapters')}</EmptyState>
                ) : (
                  <TreeItems items={filteredTree} search={search.trim()} expanded={expanded} setExpanded={setExpanded} presence={presenceByDoc} commentCounts={commentCounts} suggestionCounts={suggestionCounts}
                    currentId={current?.id ?? creatingUnder ?? undefined} chapterPrefix={chapterPrefix}
                    onSelect={selectDoc} onMove={move} onDelete={removeDoc}
                    menuFor={chapterMenu?.doc.id ?? null} onOpenMenu={openChapterMenu} onCloseMenu={closeChapterMenu}
                    dragEnabled={!search.trim() && !suggestOnly} readOnly={suggestOnly} dragId={dragId} dragBlocked={dragBlocked} dropTarget={dropTarget} reorderingId={reorderingId}
                    onDragStartItem={(d) => setDragId(d.id)}
                    onDragOverItem={(d, pos) => setDropTarget({ id: d.id, pos })}
                    onDropItem={(d) => { if (dragId != null && dropTarget) moveNode(dragId, d.id, dropTarget.pos); setDragId(null); setDropTarget(null) }}
                    onDragEndItem={() => { setDragId(null); setDropTarget(null) }} />
                )}
              </div>
              <div className="flex items-center gap-2 border-t border-slate-100 px-4 py-3 text-xs text-slate-400">
                <ListBulletIcon className="h-4 w-4" /> {t('writer.chapterCount', { n: flatDocs.length })}
              </div>
            </div>
          ) : (
            <div className="min-h-0 flex-1 space-y-3.5 overflow-y-auto p-4">
              <Field label={t('writer.bookTitleLabel')}><Input value={bookForm.title} onChange={(e) => setBookForm({ ...bookForm, title: e.target.value })} /></Field>
              <Field label={t('writer.intro')}><Textarea className="min-h-[72px]" value={bookForm.description} onChange={(e) => setBookForm({ ...bookForm, description: e.target.value })} /></Field>
              <Field label={t('writer.statusLabel')}>
                <Select value={bookForm.status} onChange={(v) => setBookForm({ ...bookForm, status: v as BookStatus })}
                  options={[
                    { value: 'draft', label: t('book.status.draft') }, { value: 'in_progress', label: t('book.status.in_progress') },
                    { value: 'published', label: t('book.status.published') }, { value: 'completed', label: t('book.status.completed') },
                    { value: 'archived', label: t('book.status.archived') },
                  ]} />
              </Field>
              <Field label={t('writer.visibilityLabel')}>
                <div className="grid grid-cols-2 gap-2">
                  <VisibilityCard active={!bookForm.isPublic} onClick={() => setBookForm({ ...bookForm, isPublic: false })} title={t('writer.visPrivate')} desc={t('writer.visPrivateDesc')} />
                  <VisibilityCard active={bookForm.isPublic} onClick={() => setBookForm({ ...bookForm, isPublic: true })} title={t('writer.visPublic')} desc={t('writer.visPublicDesc')} />
                </div>
              </Field>
              <Field label={t('writer.tagsLabel')} hint={t('writer.tagsHint')}>
                <div className="flex flex-wrap items-center gap-2 rounded-lg border border-slate-200 bg-white px-2 py-1.5 transition-colors focus-within:border-primary-500">
                  {bookForm.tags.map((tag) => (
                    <span key={tag} className="inline-flex items-center gap-1 rounded-full bg-primary-50 px-2 py-0.5 text-xs font-medium text-primary-700 ring-1 ring-inset ring-primary-200">
                      {tag}
                      <button type="button" aria-label={t('writer.removeTag', { tag })} onClick={() => setBookForm({ ...bookForm, tags: bookForm.tags.filter((x) => x !== tag) })}
                        className="text-primary-400 hover:text-primary-700"><CloseIcon className="h-3 w-3" /></button>
                    </span>
                  ))}
                  <input value={tagInput} onChange={(e) => setTagInput(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') { e.preventDefault(); const tag = tagInput.trim(); if (tag && !bookForm.tags.includes(tag) && bookForm.tags.length < 10) { setBookForm({ ...bookForm, tags: [...bookForm.tags, tag] }) } setTagInput('') }
                      else if (e.key === 'Backspace' && !tagInput && bookForm.tags.length) { setBookForm({ ...bookForm, tags: bookForm.tags.slice(0, -1) }) }
                    }}
                    onBlur={() => { const tag = tagInput.trim(); if (tag && !bookForm.tags.includes(tag) && bookForm.tags.length < 10) { setBookForm({ ...bookForm, tags: [...bookForm.tags, tag] }) } setTagInput('') }}
                    placeholder={bookForm.tags.length >= 10 ? t('writer.tagsLimit') : t('writer.addTag')}
                    disabled={bookForm.tags.length >= 10}
                    className="min-w-[100px] flex-1 border-0 bg-transparent p-0 text-sm placeholder:text-slate-400 focus:outline-none focus:ring-0" />
                </div>
              </Field>
              <Field label={t('writer.chapterPrefixLabel')}><Input value={bookForm.chapterPrefix} onChange={(e) => setBookForm({ ...bookForm, chapterPrefix: e.target.value })} placeholder="第" /></Field>
              <Field label={t('writer.followChild')} hint={t('writer.followChildHint')}>
                <Switch ariaLabel={t('writer.followChild')} checked={bookForm.childStatusFollowParent} onChange={(v) => setBookForm({ ...bookForm, childStatusFollowParent: v })} />
              </Field>
              <Button className="w-full" loading={savingBook} onClick={saveBookSettings}>{t('writer.saveBookSettings')}</Button>
            </div>
          )}
        </aside>

        {/* 中栏：编辑器 */}
        <main className="relative flex min-w-0 flex-1 flex-col overflow-hidden">
          {documentLoading && (
            <div className="absolute inset-0 z-10 flex items-center justify-center bg-white/75 backdrop-blur-[1px]">
              <Loading label={t('writer.loadingChapter')} />
            </div>
          )}
          <div className="flex min-h-0 w-full flex-1 flex-col px-8 py-6">
            {parentDoc && (
              <p className="mb-1 shrink-0 text-sm text-slate-400">{chapterPrefix}{parentDoc.title}</p>
            )}
            {/* 标题：原生输入，无边框，避免与 Input 组件的 border 样式冲突 */}
            <input
              className="w-full shrink-0 border-0 bg-transparent p-0 text-3xl font-bold text-ink placeholder:text-slate-300 focus:outline-none focus:ring-0"
              placeholder={t('writer.chapterTitlePlaceholder')} value={title} onChange={(e) => setTitle(e.target.value)} />

            {suggestOnly && current && (
              <div className="mt-3 flex items-center gap-2 rounded-lg border border-sky-200 bg-sky-50 px-4 py-2 text-sm text-sky-800" data-testid="suggest-banner">
                <i className="fa-solid fa-code-pull-request" aria-hidden="true" />
                <span>{t('writer.suggest.banner')}</span>
              </div>
            )}
            {current && <SameChapterBanner others={sameChapterOthers} remoteSave={remoteSave} />}

            {draftRecovery && (
              <div className="mt-3 flex flex-wrap items-center gap-3 rounded-lg border border-amber-200 bg-amber-50 px-4 py-2.5 text-sm text-amber-800">
                <span className="flex-1">{t('writer.draftFound', { time: formatDate(new Date(draftRecovery.ts).toISOString()).slice(5) })}</span>
                <Button size="sm" variant="outline" onClick={() => { setContent(draftRecovery.content); setDraftRecovery(null) }}>{t('writer.restoreDraft')}</Button>
                <Button size="sm" variant="ghost" onClick={() => { if (current) { try { localStorage.removeItem(draftKey(current.id)) } catch { /* 忽略 */ } } setDraftRecovery(null) }}>{t('writer.discardDraft')}</Button>
              </div>
            )}

            {/* 编辑卡片：工具条 + 正文 + 底栏合为一个圆角边框，宽高跟随中列；专注模式下 fixed 覆盖全屏 */}
            <div className={focusMode
              ? 'fixed inset-0 z-50 flex flex-col overflow-hidden bg-white'
              : 'mt-5 flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border border-slate-200 bg-white'}>
              {!preview && (
                <div className="flex shrink-0 flex-wrap items-center gap-1 border-b border-slate-200 px-2 py-1.5">
                  <ToolbarSelect onPick={(prefix) => insertAtLineStart(prefix)} />
                  <ToolbarOutline outline={outline} onJump={jumpToHeading} />
                  <ToolbarDivider />
                  <ToolbarButton title={t('writer.tb.bold')} onClick={() => wrapSelection('**')}><span className="font-bold">B</span></ToolbarButton>
                  <ToolbarButton title={t('writer.tb.italic')} onClick={() => wrapSelection('*')}><span className="italic">I</span></ToolbarButton>
                  <ToolbarButton title={t('writer.tb.strike')} onClick={() => wrapSelection('~~')}><StrikethroughIcon className="h-4 w-4" /></ToolbarButton>
                  <ToolbarDivider />
                  <ToolbarButton title={t('writer.tb.link')} onClick={insertLink}><LinkIcon className="h-4 w-4" /></ToolbarButton>
                  <ToolbarButton title={t('writer.tb.quote')} onClick={() => insertAtLineStart('> ')}><QuoteIcon className="h-4 w-4" /></ToolbarButton>
                  <ToolbarButton title={t('writer.tb.inlineCode')} onClick={() => wrapSelection('`')}><CodeIcon className="h-4 w-4" /></ToolbarButton>
                  <ToolbarButton title={t('writer.tb.codeBlock')} onClick={insertCodeBlock}><CodeBlockIcon className="h-4 w-4" /></ToolbarButton>
                  <ToolbarDivider />
                  <ToolbarButton title={t('writer.tb.ul')} onClick={() => insertAtLineStart('- ')}><ListBulletIcon className="h-4 w-4" /></ToolbarButton>
                  <ToolbarButton title={t('writer.tb.ol')} onClick={() => insertAtLineStart('1. ')}><ListOrderedIcon className="h-4 w-4" /></ToolbarButton>
                  <ToolbarButton title={t('writer.tb.task')} onClick={() => insertAtLineStart('- [ ] ')}><CheckSquareIcon className="h-4 w-4" /></ToolbarButton>
                  <ToolbarButton title={t('writer.tb.table')} onClick={insertTable}><TableIcon className="h-4 w-4" /></ToolbarButton>
                  <span className="relative">
                    <ToolbarButton title={t('writer.tb.insert')} onClick={() => setInsertMenuOpen((v) => !v)}>
                      <ColumnsIcon className="h-4 w-4" />
                    </ToolbarButton>
                    {insertMenuOpen && (
                      <>
                        <div className="fixed inset-0 z-10" onClick={() => setInsertMenuOpen(false)} />
                        <div className="absolute left-0 top-9 z-20 w-44 overflow-hidden rounded-lg border border-slate-200 bg-white py-1 shadow-lg">
                          <button type="button" onClick={() => insertComponent('tabs')} className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-slate-50">{t('writer.insertMenu.tabs')}</button>
                          <button type="button" onClick={() => insertComponent('note')} className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-slate-50">{t('writer.insertMenu.note')}</button>
                          <button type="button" onClick={() => insertComponent('tip')} className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-slate-50">{t('writer.insertMenu.tip')}</button>
                          <button type="button" onClick={() => insertComponent('warning')} className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-slate-50">{t('writer.insertMenu.warning')}</button>
                          <button type="button" onClick={() => insertComponent('accordion')} className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-slate-50">{t('writer.insertMenu.accordion')}</button>
                          <button type="button" onClick={() => insertComponent('steps')} className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-slate-50">{t('writer.insertMenu.steps')}</button>
                          <button type="button" onClick={() => insertComponent('cards')} className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-slate-50">{t('writer.insertMenu.cards')}</button>
                          <div className="my-1 h-px bg-slate-100" />
                          <button type="button" onClick={insertChildrenToc} className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-slate-50">{t('writer.insertMenu.children')}</button>
                        </div>
                      </>
                    )}
                  </span>
                  <span className="relative">
                    <ToolbarButton title={t('writer.tb.meta')} onClick={() => setMetaMenuOpen((v) => !v)}>
                      <i className="fa-solid fa-tags text-[15px]" aria-hidden="true" />
                    </ToolbarButton>
                    {metaMenuOpen && (
                      <>
                        <div className="fixed inset-0 z-10" onClick={() => setMetaMenuOpen(false)} />
                        <div className="absolute left-0 top-9 z-20 w-40 overflow-hidden rounded-lg border border-slate-200 bg-white py-1 shadow-lg">
                          <button type="button" onClick={() => void setDocIcon()} className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-slate-50">
                            <i className="fa-solid fa-icons w-4 text-center text-slate-400" aria-hidden="true" /> {t('writer.tb.chapterIcon')}
                          </button>
                        </div>
                      </>
                    )}
                  </span>
                  {site.translation_enabled && (
                    <span className="relative">
                      <ToolbarButton title={translating ? t('writer.tb.translating') : t('writer.tb.translate')} onClick={() => { if (!translating) setTranslateMenuOpen((v) => !v) }}>
                        {translating ? <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-slate-200 border-t-primary-500" /> : <GlobeIcon className="h-4 w-4" />}
                      </ToolbarButton>
                      {translateMenuOpen && (
                        <>
                          <div className="fixed inset-0 z-10" onClick={() => setTranslateMenuOpen(false)} />
                          <div className="absolute left-0 top-9 z-20 max-h-72 w-40 overflow-y-auto rounded-lg border border-slate-200 bg-white py-1 shadow-lg">
                            {TRANSLATE_TARGETS.map((tg) => (
                              <button key={tg.code} type="button" onClick={() => void translateContent(tg.code, tg.label)}
                                className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-slate-50">{tg.label}</button>
                            ))}
                          </div>
                        </>
                      )}
                    </span>
                  )}
                  <ToolbarDivider />
                  <ToolbarButton title={t('writer.tb.find')} onClick={() => setFindOpen((v) => !v)}><SearchIcon className="h-4 w-4" /></ToolbarButton>
                  <ToolbarDivider />
                  <ToolbarButton title={t('writer.tb.uploadImage')} onClick={() => fileInputRef.current?.click()}><UploadIcon className="h-4 w-4" /></ToolbarButton>
                  <ToolbarButton title={t('writer.tb.imageLink')} onClick={insertImage}><ImageIcon className="h-4 w-4" /></ToolbarButton>
                  {current && (
                    <ToolbarButton title={t('writer.tb.comment')} onClick={addComment}>
                      <i className={`fa-regular fa-comment-dots text-[15px] ${commentsTab ? 'text-primary-600' : ''}`} aria-hidden="true" />
                    </ToolbarButton>
                  )}
                  {templatesOn && !suggestOnly && (
                    <ToolbarButton title={t('writer.tb.template')} onClick={() => setTemplateOpen(true)}>
                      <i className="fa-regular fa-clone text-[15px]" aria-hidden="true" />
                    </ToolbarButton>
                  )}
                  {aiOn && !suggestOnly && (
                    <ToolbarButton title={t('writer.tb.aiWriter')} onClick={() => navigateAI(aiTab ? null : 'assist')}>
                      <i className={`fa-solid fa-wand-magic-sparkles text-[15px] ${aiTab ? 'text-primary-600' : ''}`} aria-hidden="true" />
                    </ToolbarButton>
                  )}
                  {collectEnabled && (
                    <ToolbarButton title={collecting ? t('writer.tb.collecting') : t('writer.tb.collect')} onClick={() => { if (!collecting) void collectWebContent() }}>
                      {collecting ? <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-slate-200 border-t-primary-500" /> : <GlobeIcon className="h-4 w-4" />}
                    </ToolbarButton>
                  )}
                  {!suggestOnly && <ToolbarButton title={t('writer.tb.importMarkdown')} onClick={() => mdImportRef.current?.click()}><FileTextIcon className="h-4 w-4" /></ToolbarButton>}
                  <input ref={fileInputRef} type="file" accept="image/*" multiple hidden
                    onChange={(e) => { if (e.target.files?.length) void uploadImages(e.target.files); e.target.value = '' }} />
                  <input ref={mdImportRef} type="file" accept=".md,.markdown,.mdown,.txt,text/markdown,text/plain" hidden
                    onChange={(e) => { const f = e.target.files?.[0]; if (f) void importMarkdownFile(f); e.target.value = '' }} />
                </div>
              )}

              {findOpen && !preview && (
                <div className="flex flex-wrap items-center gap-1.5 border-b border-slate-200 bg-slate-50 px-3 py-2 text-sm">
                  <input ref={findInputRef} value={findText} onChange={(e) => setFindText(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') { e.preventDefault(); e.shiftKey ? findPrev() : findNext() }
                      else if (e.key === 'Escape') { e.preventDefault(); closeFind() }
                    }}
                    placeholder="查找" aria-label="查找"
                    className="h-8 w-36 rounded-md border border-slate-200 bg-white px-2 focus:outline-none focus:ring-1 focus:ring-primary-400" />
                  <span className="w-14 shrink-0 text-center text-xs tabular-nums text-slate-400">
                    {matches.length ? `${Math.min(activeMatch + 1, matches.length)}/${matches.length}` : '无结果'}
                  </span>
                  <FindBtn onClick={findPrev} disabled={matches.length === 0}>上一个</FindBtn>
                  <FindBtn onClick={findNext} disabled={matches.length === 0}>下一个</FindBtn>
                  <button type="button" onClick={() => setCaseSensitive((v) => !v)} aria-pressed={caseSensitive}
                    className={`h-8 rounded-md border px-2 text-xs transition-colors ${caseSensitive ? 'border-primary-500 bg-primary-50 text-primary-600' : 'border-slate-200 bg-white text-slate-500 hover:bg-slate-100'}`}>
                    Aa
                  </button>
                  <span className="mx-1 h-5 w-px bg-slate-200" />
                  <input value={replaceText} onChange={(e) => setReplaceText(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Escape') { e.preventDefault(); closeFind() } }}
                    placeholder="替换为" aria-label="替换为"
                    className="h-8 w-36 rounded-md border border-slate-200 bg-white px-2 focus:outline-none focus:ring-1 focus:ring-primary-400" />
                  <FindBtn onClick={replaceCurrent} disabled={matches.length === 0}>替换</FindBtn>
                  <FindBtn onClick={replaceAll} disabled={matches.length === 0}>全部替换</FindBtn>
                  <button type="button" onClick={closeFind} aria-label="关闭查找"
                    className="ml-auto flex h-8 w-8 items-center justify-center rounded-md text-slate-400 hover:bg-slate-100 hover:text-slate-700">
                    <CloseIcon className="h-4 w-4" />
                  </button>
                </div>
              )}

              {/* 正文：铺满剩余高度，内部滚动。外链章节不使用编辑器，仅展示目标地址；否则三态：全屏预览 / 分栏（编辑+预览）/ 纯编辑 */}
              {externalUrl.trim() ? (
                <div className="min-h-0 flex-1 overflow-y-auto px-6 py-10">
                  <div className="mx-auto max-w-md rounded-xl border border-dashed border-slate-300 bg-slate-50/60 p-6 text-center">
                    <i className="fa-solid fa-arrow-up-right-from-square mb-3 text-2xl text-primary-500" aria-hidden="true" />
                    <p className="text-sm text-slate-600">{t('writer.externalChapterNote')}</p>
                    <a href={externalUrl.trim()} target="_blank" rel="noopener noreferrer"
                      className="mt-3 inline-block break-all text-sm text-primary-600 hover:underline">{externalUrl.trim()}</a>
                  </div>
                </div>
              ) : preview ? (
                <div ref={previewRef}
                  className="markdown-body min-h-0 flex-1 overflow-y-auto px-6 py-5"
                  dangerouslySetInnerHTML={{ __html: previewHtml }} />
              ) : splitPreview ? (
                <div className="flex min-h-0 flex-1 flex-col md:flex-row">
                  <textarea ref={textareaRef}
                    className="min-h-0 w-full flex-1 resize-none border-b border-slate-200 bg-transparent px-6 py-5 font-mono leading-7 text-slate-900 placeholder:text-slate-400 focus:outline-none focus:ring-0 md:w-1/2 md:border-b-0 md:border-r"
                    style={{ fontSize }}
                    placeholder={t('writer.editorPlaceholder')} value={content}
                    onChange={(e) => { setContent(e.target.value); refreshSlash() }}
                    onKeyDown={onEditorKeyDown}
                    onPaste={onEditorPaste}
                    onDrop={onEditorDrop}
                    onScroll={() => { syncScroll('edit'); closeSlash() }}
                    onSelect={(e) => setAISelectionLength(e.currentTarget.selectionEnd - e.currentTarget.selectionStart)}
                  onBlur={closeSlash} />
                  <div ref={previewRef} onScroll={() => syncScroll('preview')}
                    className="markdown-body min-h-0 w-full flex-1 overflow-y-auto px-6 py-5 md:w-1/2"
                    dangerouslySetInnerHTML={{ __html: previewHtml }} />
                </div>
              ) : (
                <textarea ref={textareaRef}
                  className="min-h-0 w-full flex-1 resize-none border-0 bg-transparent px-6 py-5 font-mono leading-7 text-slate-900 placeholder:text-slate-400 focus:outline-none focus:ring-0"
                  style={{ fontSize }}
                  placeholder={t('writer.editorPlaceholder')} value={content}
                  onChange={(e) => { setContent(e.target.value); refreshSlash() }}
                  onKeyDown={onEditorKeyDown}
                  onPaste={onEditorPaste}
                  onDrop={onEditorDrop}
                  onSelect={(e) => setAISelectionLength(e.currentTarget.selectionEnd - e.currentTarget.selectionStart)}
                  onBlur={closeSlash} />
              )}

              <div className="flex shrink-0 items-center justify-between border-t border-slate-200 px-4 py-2.5 text-xs text-slate-400">
                <span className="flex items-center gap-2">
                  <span className="flex items-center gap-1">Markdown <ChevronDownIcon className="h-3.5 w-3.5" /></span>
                  <button type="button" onClick={() => setFocusMode((v) => !v)} aria-label={focusMode ? t('writer.exitFocusMode') : t('writer.focusMode')}
                    className="flex items-center gap-1 rounded px-1.5 py-0.5 text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700">
                    {focusMode ? <MinimizeIcon className="h-3.5 w-3.5" /> : <MaximizeIcon className="h-3.5 w-3.5" />}
                    {focusMode ? t('writer.exitFocus') : t('writer.focus')}
                  </button>
                  <button type="button" onClick={() => setShortcutsOpen(true)} aria-label={t('writer.shortcuts')}
                    className="flex h-5 w-5 items-center justify-center rounded-full border border-slate-200 text-[10px] font-semibold text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700">
                    ?
                  </button>
                  <span className="flex items-center overflow-hidden rounded border border-slate-200">
                    <button type="button" onClick={() => setFontSize((s) => Math.max(12, s - 1))} disabled={fontSize <= 12}
                      aria-label={t('writer.decreaseFont')} className="px-1.5 text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700 disabled:opacity-40">A-</button>
                    <span className="px-1 tabular-nums text-slate-400">{fontSize}</span>
                    <button type="button" onClick={() => setFontSize((s) => Math.min(22, s + 1))} disabled={fontSize >= 22}
                      aria-label={t('writer.increaseFont')} className="px-1.5 text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700 disabled:opacity-40">A+</button>
                  </span>
                </span>
                <span className="flex items-center gap-2">
                  {uploading && <span className="flex items-center gap-1 text-primary-500"><span className="h-3 w-3 animate-spin rounded-full border-2 border-primary-200 border-t-primary-500" /> {t('writer.uploadingImg')}</span>}
                  <span>{t('writer.wordCount', { n: wordCount })}{readingMinutes > 0 && ` · ${t('writer.readingMinutes', { n: readingMinutes })}`}</span>
                </span>
                {current ? <span>{t('writer.updatedAt', { time: formatDate(current.updated_at).slice(11) })}</span> : <span />}
              </div>
            </div>
          </div>

          {/* 斜杠命令菜单：fixed 定位于光标处，不受编辑卡片 overflow-hidden 裁剪 */}
          {slash.open && slash.mode === 'link' && filteredLinks.length > 0 && (
            <div role="listbox" aria-label={t('writer.wikiLink.menu')}
              className="fixed z-[60] max-h-72 w-72 overflow-y-auto rounded-lg border border-slate-200 bg-white py-1 text-sm shadow-lg"
              style={{ top: slash.top, left: Math.min(slash.left, (typeof window !== 'undefined' ? window.innerWidth : 9999) - 300) }}>
              <div className="px-3 pb-1 pt-0.5 text-xs text-slate-400">{t('writer.wikiLink.menu')}</div>
              {filteredLinks.map((d, i) => (
                <button key={d.slug} type="button" role="option" aria-selected={i === Math.min(slash.index, filteredLinks.length - 1)}
                  onMouseDown={(e) => { e.preventDefault(); selectLink(d) }}
                  className={`flex w-full items-baseline gap-2 px-3 py-1.5 text-left ${i === Math.min(slash.index, filteredLinks.length - 1) ? 'bg-primary-50 text-primary-700' : 'text-slate-700 hover:bg-slate-50'}`}>
                  <span className="min-w-0 flex-1 truncate">{d.title}</span>
                  <span className="shrink-0 truncate font-mono text-xs text-slate-400">{d.slug}</span>
                </button>
              ))}
            </div>
          )}
          {slash.open && slash.mode === 'cmd' && filteredSlash.length > 0 && (
            <div className="fixed z-[60] max-h-72 w-52 overflow-y-auto rounded-lg border border-slate-200 bg-white py-1 text-sm shadow-lg"
              style={{ top: slash.top, left: Math.min(slash.left, (typeof window !== 'undefined' ? window.innerWidth : 9999) - 220) }}>
              {filteredSlash.map((c, i) => (
                <button key={c.key} type="button"
                  onMouseDown={(e) => { e.preventDefault(); selectSlash(c) }}
                  className={`block w-full px-3 py-1.5 text-left ${i === Math.min(slash.index, filteredSlash.length - 1) ? 'bg-primary-50 text-primary-700' : 'text-slate-700 hover:bg-slate-50'}`}>
                  {t(c.labelKey)}
                </button>
              ))}
            </div>
          )}

          <Modal open={shortcutsOpen} onClose={() => setShortcutsOpen(false)} title={t('writer.shortcutsTitle')}>
            <dl className="space-y-2.5">
              {[
                [t('writer.sc.save'), '⌘/Ctrl + S'],
                [t('writer.sc.format'), '⌘/Ctrl + B / I / K'],
                [t('writer.sc.find'), '⌘/Ctrl + F'],
                [t('writer.sc.comment'), '⌘/Ctrl + Alt + M'],
                [t('writer.sc.dupLine'), '⌘/Ctrl + Shift + D'],
                [t('writer.sc.moveLine'), 'Alt + ↑ / ↓'],
                [t('writer.sc.slashMenu'), t('writer.sc.slashMenuKey')],
                [t('writer.sc.wrap'), '( [ { ` * _ ~ " \''],
                [t('writer.sc.listContinue'), 'Enter'],
                [t('writer.sc.indent'), 'Tab / Shift + Tab'],
                [t('writer.sc.escape'), 'Esc'],
              ].map(([action, keys]) => (
                <div key={action} className="flex items-center justify-between gap-4">
                  <dt className="text-sm text-slate-600">{action}</dt>
                  <dd className="shrink-0 rounded-md border border-slate-200 bg-slate-50 px-2 py-0.5 font-mono text-xs text-slate-500">{keys}</dd>
                </div>
              ))}
            </dl>
          </Modal>
        </main>

        {/* 右栏：章节设置（建议者看到的是建议说明） */}
        {suggestOnly ? (
          <aside className="hidden w-72 shrink-0 overflow-y-auto border-l border-slate-200 bg-white p-4 xl:block">
            <h2 className="mb-3 font-bold text-slate-900">{t('writer.suggest.panelTitle')}</h2>
            <p className="text-sm leading-6 text-slate-600">{t('writer.suggest.panelText')}</p>
            <Button variant="outline" className="mt-4 w-full" onClick={() => navigateComments('suggestions')} disabled={!current}>
              <i className="fa-solid fa-code-pull-request" aria-hidden="true" /> {t('writer.suggest.mine')}
            </Button>
          </aside>
        ) : (
        <aside className="hidden w-72 shrink-0 overflow-y-auto border-l border-slate-200 bg-white p-4 xl:block">
          <h2 className="mb-4 font-bold text-slate-900">{t('writer.chapterSettings')}</h2>
          <div className="space-y-4">
            <Field label={t('writer.publishStatus')}>
              <Select value={status} onChange={(v) => setStatus(v as DocumentStatus)}
                leading={<span className={`h-2 w-2 shrink-0 rounded-full ${STATUS_META[status].dot}`} />}
                options={[{ value: 'draft', label: t('book.status.draft') }, { value: 'published', label: t('book.status.published') }, { value: 'archived', label: t('book.status.archived') }]} />
            </Field>
            <Field label={t('writer.parentChapter')}>
              <Select value={parentId} onChange={(v) => setParentId(v)}
                options={[{ value: '', label: t('writer.asTopLevel') }, ...parentCandidates.map((d) => {
                  let depth = 0
                  for (let p = d.parent_id; p; p = byId.get(p)?.parent_id ?? null) depth++
                  return { value: String(d.id), label: `${'　'.repeat(depth)}${depth > 0 ? '└ ' : ''}${chapterPrefix}${d.title}` }
                })]} />
            </Field>
            <Field label={t('writer.sortOrder')}>
              <Input type="number" value={sortOrder} onChange={(e) => setSortOrder(Number(e.target.value) || 0)} />
            </Field>
            <Field label={t('writer.docPath')} hint={t('writer.docPathHint')}>
              <Input value={slug} onChange={(e) => setSlug(e.target.value)} placeholder={t('writer.docPathPlaceholder')} />
            </Field>
            <Field label={t('writer.externalUrl')} hint={t('writer.externalUrlHint')}>
              <Input value={externalUrl} onChange={(e) => setExternalUrl(e.target.value)} placeholder="https://…" />
            </Field>
            {externalUrl.trim() && (
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium text-slate-700">{t('writer.externalNewTab')}</span>
                <button role="switch" aria-checked={externalNewTab} onClick={() => setExternalNewTab(!externalNewTab)}
                  className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors ${externalNewTab ? 'bg-primary-500' : 'bg-slate-300'}`}>
                  <span className={`inline-block h-5 w-5 transform rounded-full bg-white shadow transition-transform ${externalNewTab ? 'translate-x-[22px]' : 'translate-x-0.5'}`} />
                </button>
              </div>
            )}
            {externalUrl.trim() ? (
              // 外链章节仅是跳转链接、没有正文页面，因此不支持评论（公开后也不显示评论区）。
              <p className="text-xs leading-5 text-slate-400">{t('writer.externalNoComments')}</p>
            ) : (
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium text-slate-700">{t('writer.allowComments')}</span>
                <button role="switch" aria-checked={allowComments} onClick={() => setAllowComments(!allowComments)}
                  className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors ${allowComments ? 'bg-primary-500' : 'bg-slate-300'}`}>
                  <span className={`inline-block h-5 w-5 transform rounded-full bg-white shadow transition-transform ${allowComments ? 'translate-x-[22px]' : 'translate-x-0.5'}`} />
                </button>
              </div>
            )}
            <div className="flex items-start justify-between gap-3">
              <span className="text-sm font-medium text-slate-700">{t('writer.includeSource')}<span className="mt-0.5 block text-xs font-normal text-slate-400">{t('writer.includeSourceHint')}</span></span>
              <Switch checked={collectIncludeSource} onChange={setCollectIncludeSource} ariaLabel={t('writer.includeSource')} />
            </div>
          </div>

          <div className="mt-6 border-t border-slate-100 pt-5">
            <h3 className="mb-3 font-semibold text-slate-900">{t('writer.chapterInfo')}</h3>
            <dl className="space-y-3 text-sm">
              <div><dt className="text-slate-400">{t('writer.createdTime')}</dt><dd className="mt-0.5 text-slate-700">{current ? formatDate(current.created_at) : '-'}</dd></div>
              <div><dt className="text-slate-400">{t('writer.updatedTime')}</dt><dd className="mt-0.5 text-slate-700">{current ? formatDate(current.updated_at) : '-'}</dd></div>
            </dl>
          </div>

          <div className="mt-6 border-t border-slate-100 pt-5">
            <Button variant="outline" className="w-full" onClick={() => setHistoryOpen(true)} disabled={!current}>
              <HistoryIcon className="h-4 w-4" /> {t('writer.viewVersions')}
            </Button>
          </div>

          <div className="mt-6 border-t border-slate-100 pt-5">
            <button onClick={() => current && removeDoc(current)} disabled={!current}
              className="flex items-center gap-1.5 text-sm text-rose-500 transition-colors hover:text-rose-600 disabled:cursor-not-allowed disabled:opacity-40">
              <TrashIcon className="h-4 w-4" /> {t('writer.deleteChapter')}
            </button>
          </div>
        </aside>
        )}
      </div>
      <ContextMenu open={chapterMenu !== null} x={chapterMenu?.x ?? 0} y={chapterMenu?.y ?? 0}
        align={chapterMenu?.align} flipY={chapterMenu?.flipY} onClose={closeChapterMenu} label={t('writer.chapterActions')}>
        <ContextMenuItem onClick={() => { const d = chapterMenu?.doc; closeChapterMenu(); if (d) createSiblingOf(d) }}>
          <FileTextIcon className="h-4 w-4" /> {t('writer.newChapterBtn')}
        </ContextMenuItem>
        <ContextMenuItem onClick={() => { const d = chapterMenu?.doc; closeChapterMenu(); if (d) createChildOf(d) }}>
          <FolderIcon className="h-4 w-4" /> {t('writer.newSubChapter')}
        </ContextMenuItem>
        {collectEnabled && (
          <ContextMenuItem onClick={() => { const d = chapterMenu?.doc; closeChapterMenu(); if (d) openWebImport(d) }}>
            <GlobeIcon className="h-4 w-4" /> {t('writer.fromWeb')}
          </ContextMenuItem>
        )}
        <div role="separator" className="my-1 border-t border-slate-100" />
        <ContextMenuItem onClick={() => { if (chapterMenu) move(chapterMenu.doc, -1); closeChapterMenu() }}>
          <i className="fa-solid fa-arrow-up" aria-hidden="true" /> {t('writer.moveUp')}
        </ContextMenuItem>
        <ContextMenuItem onClick={() => { if (chapterMenu) move(chapterMenu.doc, 1); closeChapterMenu() }}>
          <i className="fa-solid fa-arrow-down" aria-hidden="true" /> {t('writer.moveDown')}
        </ContextMenuItem>
        <div role="separator" className="my-1 border-t border-slate-100" />
        <ContextMenuItem danger onClick={() => { if (chapterMenu) removeDoc(chapterMenu.doc); closeChapterMenu() }}>
          <TrashIcon className="h-4 w-4" /> {t('writer.deleteAction')}
        </ContextMenuItem>
      </ContextMenu>
      <RevisionDrawer
        open={historyOpen}
        document={current}
        currentContent={content}
        hasUnsavedChanges={saveState === 'dirty'}
        onClose={() => setHistoryOpen(false)}
        onRestored={applyRestoredDocument}
      />
      {book && (
        <WebDocumentImportDialog
          open={webImportOpen}
          bookId={book.id}
          parent={webImportParent}
          topLevelCount={tree.length}
          onClose={() => setWebImportOpen(false)}
          onImported={handleWebImported}
        />
      )}
    </div>
  )
}

/* ── 子组件 ── */

const REVISION_REASON_LABEL: Record<DocumentRevisionSummary['reason'], string> = {
  create: 'writer.rev.create',
  save: 'writer.rev.save',
  publish: 'writer.rev.publish',
  pre_restore: 'writer.rev.pre_restore',
  restore: 'writer.rev.restore',
}

type WebRenderMode = 'auto' | 'static' | 'browser'

function WebDocumentImportDialog({ open, bookId, parent, topLevelCount, onClose, onImported }: {
  open: boolean
  bookId: number
  parent: Document | null
  topLevelCount: number
  onClose: () => void
  onImported: (document: Document) => Promise<void>
}) {
  const { t } = useTranslation()
  const [url, setURL] = useState('')
  const [title, setTitle] = useState('')
  const [renderMode, setRenderMode] = useState<WebRenderMode>('auto')
  const [includeSource, setIncludeSource] = useState(false)
  const [browserAvailable, setBrowserAvailable] = useState(true)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    setURL('')
    setTitle('')
    setRenderMode('auto')
    setIncludeSource(false)
    setError('')
    api<{ available: boolean }>('/import/browser-available').then((r) => setBrowserAvailable(r.available)).catch(() => {})
  }, [open])

  useEffect(() => {
    if (!open) return
    function onKey(event: KeyboardEvent) { if (event.key === 'Escape' && !loading) onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, loading, onClose])

  if (!open) return null

  async function collect() {
    if (!url.trim()) {
      setError(t('writer.needCollectUrl'))
      return
    }
    setLoading(true)
    setError('')
    try {
      const result = await api<{ document: Document }>(`/books/${bookId}/documents/import-web`, {
        method: 'POST',
        body: {
          url: url.trim(), title: title.trim(), render_mode: renderMode, include_source: includeSource,
          parent_id: parent?.id ?? null,
          sort_order: parent ? parent.children?.length || 0 : topLevelCount,
        },
      })
      await onImported(result.document)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/30 p-4 backdrop-blur-[2px]"
      role="dialog" aria-modal="true" aria-label={t('writer.webCollectAria')}
      onMouseDown={(event) => { if (!loading && event.target === event.currentTarget) onClose() }}>
      <section className="flex max-h-[calc(100vh-2rem)] w-full max-w-xl flex-col overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xl">
        <header className="flex shrink-0 items-start justify-between border-b border-slate-100 px-6 py-5">
          <div className="flex gap-3">
            <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-primary-50 text-primary-600">
              <GlobeIcon className="h-5 w-5" />
            </span>
            <div>
              <h2 className="text-lg font-bold text-slate-900">{t('writer.webCollectTitle')}</h2>
              <p className="mt-1 text-sm text-slate-500">
                {parent ? t('writer.saveAsChildOf', { title: parent.title }) : t('writer.saveAsTopLevel')}{t('writer.keepDraft')}
              </p>
            </div>
          </div>
          <button type="button" aria-label={t('writer.closeWebCollect')} disabled={loading} onClick={onClose}
            className="flex items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-800 disabled:opacity-40"
            style={{ width: 'var(--control-height-sm)', height: 'var(--control-height-sm)' }}>
            <CloseIcon className="h-5 w-5" />
          </button>
        </header>

        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-6 py-6">
          <Field label={t('writer.webUrl')} hint={t('writer.webUrlHint')}>
            <Input type="url" value={url} onChange={(event) => setURL(event.target.value)}
              placeholder="https://example.com/article" leading={<GlobeIcon className="h-4 w-4" />} />
          </Field>
          <Field label={<>{t('writer.chapterTitleFieldLabel')} <span className="font-normal text-slate-400">{t('writer.optional')}</span></>} hint={t('writer.chapterTitleHint')}>
            <Input value={title} onChange={(event) => setTitle(event.target.value)} placeholder={t('writer.useWebTitle')} />
          </Field>
          <Field label={t('writer.parseMethod')} hint={browserAvailable ? undefined : t('writer.parseHint')}>
            <Select menuPlacement="top" value={renderMode} onChange={(value) => setRenderMode(value as WebRenderMode)} options={[
              { value: 'auto', label: t('writer.parseAuto') },
              { value: 'static', label: t('writer.parseStatic') },
              ...(browserAvailable ? [{ value: 'browser', label: t('writer.parseBrowser') }] : []),
            ]} />
          </Field>
          <label className="flex cursor-pointer items-start gap-2.5">
            <Checkbox checked={includeSource} onChange={setIncludeSource} ariaLabel={t('writer.includeSource')} />
            <span className="text-sm text-slate-600">{t('writer.includeSource')}<span className="mt-0.5 block text-xs text-slate-400">{t('writer.includeSourceHint')}</span></span>
          </label>

          {error && <div role="alert" className="max-h-32 overflow-y-auto whitespace-pre-wrap break-words rounded-xl border border-rose-100 bg-rose-50 px-4 py-3 text-sm leading-6 text-rose-600 [overflow-wrap:anywhere]">{error}</div>}
          {loading && <div className="rounded-xl border border-primary-100 bg-primary-50/50 px-4 py-4"><Loading className="py-1" label={t('writer.extracting')} /></div>}

          <div className="flex justify-end gap-3 border-t border-slate-100 pt-5">
            <Button variant="outline" disabled={loading} onClick={onClose}>{t('common.actions.cancel')}</Button>
            <Button loading={loading} onClick={collect}>{t('writer.collectAsChapter')}</Button>
          </div>
        </div>
      </section>
    </div>
  )
}

function RevisionDrawer({
  open, document, currentContent, hasUnsavedChanges, onClose, onRestored,
}: {
  open: boolean
  document: Document | null
  currentContent: string
  hasUnsavedChanges: boolean
  onClose: () => void
  onRestored: (document: Document) => Promise<void>
}) {
  const { t } = useTranslation()
  const { confirmAction } = useFeedback()
  const [result, setResult] = useState<(PageResult<DocumentRevisionSummary> & { hidden?: number; keep?: number }) | null>(null)
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [detail, setDetail] = useState<DocumentRevision | null>(null)
  const [listLoading, setListLoading] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [restoring, setRestoring] = useState(false)
  const [error, setError] = useState('')
  // 差异对比：viewMode 统一/并排；compareId 对比对象（'current' 为当前编辑内容，或另一版本 id）
  const [viewMode, setViewMode] = useState<'unified' | 'split'>('unified')
  const [compareId, setCompareId] = useState<number | 'current'>('current')
  const [compareContent, setCompareContent] = useState('')
  const [compareLoading, setCompareLoading] = useState(false)

  const loadList = useCallback(async () => {
    if (!document) return
    setListLoading(true)
    setError('')
    try {
      const next = await api<PageResult<DocumentRevisionSummary> & { hidden?: number; keep?: number }>(`/documents/${document.id}/revisions`, { params: { page_size: 50 } })
      setResult(next)
      setSelectedId((currentId) => next.items.some((item) => item.id === currentId) ? currentId : next.items[0]?.id ?? null)
    } catch (e) {
      setError((e as Error).message)
      setResult({ items: [], total: 0, page: 1, page_size: 50 })
    } finally {
      setListLoading(false)
    }
  }, [document])

  useEffect(() => {
    if (!open || !document) return
    setDetail(null)
    setSelectedId(null)
    setCompareId('current')
    loadList()
  }, [open, document, loadList])

  // 对比对象若与当前查看的版本相同，回退为“当前编辑内容”，避免自我对比
  useEffect(() => {
    if (compareId !== 'current' && compareId === selectedId) setCompareId('current')
  }, [selectedId, compareId])

  // 解析对比对象的内容：'current' 跟随编辑区，其它取对应版本正文
  useEffect(() => {
    if (!open || !document) return
    if (compareId === 'current') { setCompareContent(currentContent); setCompareLoading(false); return }
    let active = true
    setCompareLoading(true)
    api<DocumentRevision>(`/documents/${document.id}/revisions/${compareId}`)
      .then((value) => { if (active) setCompareContent(value.content || '') })
      .catch(() => { if (active) setCompareContent('') })
      .finally(() => { if (active) setCompareLoading(false) })
    return () => { active = false }
  }, [open, document, compareId, currentContent])

  useEffect(() => {
    if (!open || !document || selectedId == null) { setDetail(null); return }
    let active = true
    setDetailLoading(true)
    setError('')
    api<DocumentRevision>(`/documents/${document.id}/revisions/${selectedId}`)
      .then((value) => { if (active) setDetail(value) })
      .catch((e) => { if (active) { setDetail(null); setError((e as Error).message) } })
      .finally(() => { if (active) setDetailLoading(false) })
    return () => { active = false }
  }, [open, document, selectedId])

  useEffect(() => {
    if (!open) return
    function onKey(event: KeyboardEvent) { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  // 差异：detail(历史版本) 为旧侧，compareContent(对比对象) 为新侧
  const diffRows = useMemo(() => (detail ? diffLines(detail.content, compareContent) : []), [detail, compareContent])
  const stats = useMemo(() => diffStats(diffRows), [diffRows])
  const compareOptions = useMemo(() => {
    const opts = [{ value: 'current', label: t('writer.currentEdit') }]
    for (const revision of result?.items || []) {
      if (revision.id === selectedId) continue
      opts.push({ value: String(revision.id), label: `${t(REVISION_REASON_LABEL[revision.reason])} · ${formatDate(revision.created_at)}` })
    }
    return opts
  }, [result, selectedId, t])

  if (!open || !document) return null

  async function restore() {
    if (hasUnsavedChanges) {
      setError(t('writer.unsavedRestoreWarn'))
      return
    }
    if (!detail || !await confirmAction({
      title: t('writer.restoreVersionTitle'),
      message: t('writer.restoreVersionMsg', { time: formatDate(detail.created_at) }),
      confirmLabel: t('writer.restoreVersionConfirm'),
    })) return
    setRestoring(true)
    setError('')
    try {
      const restored = await api<Document>(`/documents/${document!.id}/revisions/${detail.id}/restore`, { method: 'POST' })
      await onRestored(restored)
      await loadList()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setRestoring(false)
    }
  }

  async function loadMore() {
    if (!document || !result || result.items.length >= result.total) return
    setLoadingMore(true)
    setError('')
    try {
      const next = await api<PageResult<DocumentRevisionSummary> & { hidden?: number; keep?: number }>(`/documents/${document.id}/revisions`, {
        params: { page: result.page + 1, page_size: 50 },
      })
      setResult({ ...next, items: [...result.items, ...next.items] })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoadingMore(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-slate-950/25 backdrop-blur-[1px]" role="dialog" aria-modal="true" aria-label={t('writer.versionHistoryAria')} onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
      <section className="flex h-full w-full max-w-5xl flex-col bg-white shadow-2xl">
        <header className="flex h-16 shrink-0 items-center justify-between border-b border-slate-200 px-5">
          <div className="flex min-w-0 items-center gap-3">
            <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600">
              <HistoryIcon className="h-5 w-5" />
            </span>
            <div className="min-w-0">
              <h2 className="truncate font-bold text-slate-900">{t('writer.versionHistory')}</h2>
              <p className="truncate text-xs text-slate-500">{document.title} · {t('writer.everyManualSave')}</p>
            </div>
          </div>
          <button type="button" aria-label={t('writer.closeVersionHistory')} onClick={onClose}
            className="flex items-center justify-center rounded-lg text-slate-500 transition-colors hover:bg-slate-100 hover:text-slate-900"
            style={{ width: 'var(--control-height-sm)', height: 'var(--control-height-sm)' }}>
            <CloseIcon className="h-5 w-5" />
          </button>
        </header>

        {error && <div className="border-b border-rose-100 bg-rose-50 px-5 py-2.5 text-sm text-rose-700">{error}</div>}

        <div className="flex min-h-0 flex-1 flex-col md:flex-row">
          <aside className="h-52 shrink-0 overflow-y-auto border-b border-slate-200 bg-slate-50/70 p-3 md:h-auto md:w-72 md:border-b-0 md:border-r">
            {listLoading ? (
              <Loading className="h-full py-8" label={t('writer.loadingVersions')} />
            ) : !result?.items.length ? (
              <EmptyState>{t('writer.noVersions')}</EmptyState>
            ) : (
              <div className="space-y-1.5">
                {result.items.map((revision) => (
                  <button key={revision.id} type="button" onClick={() => setSelectedId(revision.id)}
                    className={`w-full rounded-lg border px-3 py-2.5 text-left transition-colors ${selectedId === revision.id ? 'border-primary-200 bg-white shadow-sm ring-1 ring-primary-100' : 'border-transparent hover:border-slate-200 hover:bg-white'}`}>
                    <span className="flex items-center justify-between gap-2">
                      <span className={`text-sm font-semibold ${selectedId === revision.id ? 'text-primary-700' : 'text-slate-800'}`}>{t(REVISION_REASON_LABEL[revision.reason])}</span>
                      <span className="text-[11px] text-slate-400">{t('writer.charCount', { n: revision.content_length })}</span>
                    </span>
                    <span className="mt-1 block text-xs text-slate-500">{formatDate(revision.created_at)}</span>
                    <span className="mt-0.5 block truncate text-xs text-slate-400">{displayName(revision.author) || t('writer.unknownUser')}</span>
                  </button>
                ))}
                {result.items.length < result.total && (
                  <Button variant="ghost" size="sm" className="w-full" loading={loadingMore} onClick={loadMore}>
                    {t('writer.loadEarlier')}
                  </Button>
                )}
                {!!result.hidden && result.items.length >= result.total && (
                  <p className="rounded-lg bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-700">{t('writer.versionsHidden', { n: result.hidden, keep: result.keep ?? 0 })}</p>
                )}
              </div>
            )}
          </aside>

          <main className="flex min-h-0 min-w-0 flex-1 flex-col">
            {detailLoading ? (
              <Loading className="h-full" label={t('writer.loadingVersionContent')} />
            ) : detail ? (
              <>
                <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 px-5 py-3">
                  <div>
                    <div className="flex items-center gap-2">
                      <span className="font-semibold text-slate-900">{t(REVISION_REASON_LABEL[detail.reason])}</span>
                      <Badge tone={STATUS_META[detail.status].tone}>{t(STATUS_META[detail.status].labelKey)}</Badge>
                    </div>
                    <p className="mt-1 text-xs text-slate-500">{formatDate(detail.created_at)} · {displayName(detail.author) || t('writer.unknownUser')}</p>
                  </div>
                  <Button variant="outline" loading={restoring} disabled={hasUnsavedChanges} onClick={restore}>
                    <HistoryIcon className="h-4 w-4" /> {t('writer.restoreThisVersion')}
                  </Button>
                </div>
                {hasUnsavedChanges && (
                  <div className="border-b border-amber-100 bg-amber-50 px-5 py-2.5 text-xs text-amber-800">
                    {t('writer.unsavedRestoreWarn2')}
                  </div>
                )}
                {/* 差异工具条：选择对比对象、切换统一/并排、显示增删行数 */}
                <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-slate-100 bg-slate-50/60 px-5 py-2.5">
                  <div className="flex items-center gap-2 text-xs text-slate-500">
                    <span className="shrink-0">{t('writer.thisVersionCompareTo')}</span>
                    <Select className="w-52" size="sm" value={compareId === 'current' ? 'current' : String(compareId)}
                      options={compareOptions} onChange={(value) => setCompareId(value === 'current' ? 'current' : Number(value))} />
                  </div>
                  <SegmentedTabs size="sm" value={viewMode} ariaLabel={t('writer.diffViewAria')} onChange={(value) => setViewMode(value as 'unified' | 'split')}
                    items={[{ value: 'unified', label: t('writer.diffUnified') }, { value: 'split', label: t('writer.diffSplit') }]} />
                  <span className="ml-auto flex items-center gap-2 font-mono text-xs">
                    <span className="text-emerald-600">+{stats.added}</span>
                    <span className="text-rose-500">−{stats.removed}</span>
                  </span>
                </div>
                {compareLoading ? (
                  <Loading className="h-full" label={t('writer.loadingCompare')} />
                ) : stats.added === 0 && stats.removed === 0 ? (
                  <div className="flex flex-1 items-center justify-center p-8"><EmptyState>{t('writer.sameContent')}</EmptyState></div>
                ) : (
                  <RevisionDiff rows={diffRows} mode={viewMode} />
                )}
              </>
            ) : (
              <EmptyState>{t('writer.selectLeftVersion')}</EmptyState>
            )}
          </main>
        </div>
      </section>
    </div>
  )
}

// RevisionDiff git diff 式差异视图：统一（单栏 +/-）或并排（左删右增）
function RevisionDiff({ rows, mode }: { rows: DiffRow[]; mode: 'unified' | 'split' }) {
  if (mode === 'split') return <RevisionDiffSplit rows={rows} />
  return (
    <div className="min-h-0 flex-1 overflow-auto bg-white font-mono text-xs leading-6">
      <table className="w-full border-collapse">
        <tbody>
          {rows.map((row, i) => (
            <tr key={i} className={row.type === 'add' ? 'bg-emerald-50' : row.type === 'del' ? 'bg-rose-50' : ''}>
              <td className="w-10 select-none border-r border-slate-100 px-2 text-right align-top text-slate-300">{row.oldNo ?? ''}</td>
              <td className="w-10 select-none border-r border-slate-100 px-2 text-right align-top text-slate-300">{row.newNo ?? ''}</td>
              <td className={`whitespace-pre-wrap break-words px-2 ${row.type === 'add' ? 'text-emerald-700' : row.type === 'del' ? 'text-rose-700' : 'text-slate-700'}`}>
                <span className="mr-2 inline-block w-3 select-none text-center text-slate-400">{row.type === 'add' ? '+' : row.type === 'del' ? '−' : ''}</span>
                {row.text || ' '}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// RevisionDiffSplit 并排差异：把连续的删除/新增按行配对，尽量左右对齐
function RevisionDiffSplit({ rows }: { rows: DiffRow[] }) {
  const pairs: { left?: DiffRow; right?: DiffRow }[] = []
  let dels: DiffRow[] = []
  let adds: DiffRow[] = []
  const flush = () => {
    const max = Math.max(dels.length, adds.length)
    for (let k = 0; k < max; k++) pairs.push({ left: dels[k], right: adds[k] })
    dels = []
    adds = []
  }
  for (const row of rows) {
    if (row.type === 'del') dels.push(row)
    else if (row.type === 'add') adds.push(row)
    else { flush(); pairs.push({ left: row, right: row }) }
  }
  flush()
  return (
    <div className="min-h-0 flex-1 overflow-auto bg-white font-mono text-xs leading-6">
      <table className="w-full table-fixed border-collapse">
        <tbody>
          {pairs.map((pair, i) => (
            <tr key={i}>
              <td className="w-10 select-none border-r border-slate-100 px-2 text-right align-top text-slate-300">{pair.left?.oldNo ?? ''}</td>
              <td className={`w-[calc(50%-2.5rem)] whitespace-pre-wrap break-words border-r border-slate-200 px-2 align-top ${pair.left?.type === 'del' ? 'bg-rose-50 text-rose-700' : 'text-slate-700'}`}>{pair.left ? (pair.left.text || ' ') : ''}</td>
              <td className="w-10 select-none border-r border-slate-100 px-2 text-right align-top text-slate-300">{pair.right?.newNo ?? ''}</td>
              <td className={`w-[calc(50%-2.5rem)] whitespace-pre-wrap break-words px-2 align-top ${pair.right?.type === 'add' ? 'bg-emerald-50 text-emerald-700' : 'text-slate-700'}`}>{pair.right ? (pair.right.text || ' ') : ''}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function VisibilityCard({ active, onClick, title, desc }: { active: boolean; onClick: () => void; title: string; desc: string }) {
  return (
    <button type="button" onClick={onClick}
      className={`rounded-lg border px-3 py-2 text-left transition-colors ${
        active ? 'border-primary-500 bg-primary-50/60 ring-1 ring-inset ring-primary-200' : 'border-slate-200 hover:border-slate-300'
      }`}>
      <span className={`block text-sm font-medium ${active ? 'text-primary-700' : 'text-slate-900'}`}>{title}</span>
      <span className="block text-xs text-slate-500">{desc}</span>
    </button>
  )
}

function ToolbarButton({ title, onClick, children }: { title: string; onClick: () => void; children: ReactNode }) {
  return (
    <Tooltip content={title}>
      <button type="button" aria-label={title} onClick={onClick}
        className="flex items-center justify-center rounded-md text-slate-600 transition-colors hover:bg-slate-100 hover:text-slate-900"
        style={{ width: 'var(--control-height-sm)', height: 'var(--control-height-sm)' }}>
        {children}
      </button>
    </Tooltip>
  )
}

function ToolbarDivider() {
  return <span className="mx-1 h-5 w-px bg-slate-200" />
}

// FindBtn 查找替换栏里的紧凑按钮
function FindBtn({ onClick, disabled, children }: { onClick: () => void; disabled?: boolean; children: ReactNode }) {
  return (
    <button type="button" onClick={onClick} disabled={disabled}
      className="h-8 rounded-md border border-slate-200 bg-white px-2.5 text-xs text-slate-600 transition-colors hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-40">
      {children}
    </button>
  )
}

// H ▾ 标题级别下拉
function ToolbarSelect({ onPick }: { onPick: (prefix: string) => void }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="relative">
      <button type="button" onClick={() => setOpen(!open)}
        className="flex items-center gap-0.5 rounded-md px-2 text-sm font-bold text-slate-600 hover:bg-slate-100"
        style={{ height: 'var(--control-height-sm)' }}>
        H <ChevronDownIcon className="h-3.5 w-3.5" />
      </button>
      {open && (
        <div className="absolute left-0 top-9 z-20 w-36 overflow-hidden rounded-lg border border-slate-200 bg-white py-1 shadow-lg">
          {HEADING_LEVELS.map(({ prefix, label, level }) => (
            <button key={level} onClick={() => { onPick(prefix); setOpen(false) }}
              className="flex w-full items-baseline justify-between px-3 py-1.5 text-left hover:bg-slate-50">
              <span className="text-sm text-slate-700">{label}</span>
              <span className="font-mono text-[11px] text-slate-400">H{level}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

// 大纲 ▾ 当前章节标题跳转
function ToolbarOutline({ outline, onJump }: { outline: { level: number; text: string; offset: number }[]; onJump: (offset: number) => void }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <div className="relative">
      <Tooltip content={t('writer.outline')}>
        <button type="button" aria-label={t('writer.outline')} onClick={() => setOpen(!open)}
          className="flex items-center justify-center rounded-md text-slate-600 transition-colors hover:bg-slate-100 hover:text-slate-900"
          style={{ width: 'var(--control-height-sm)', height: 'var(--control-height-sm)' }}>
          <OutlineIcon className="h-4 w-4" />
        </button>
      </Tooltip>
      {open && (
        <div className="absolute left-0 top-9 z-20 max-h-80 w-64 overflow-y-auto rounded-lg border border-slate-200 bg-white py-1 shadow-lg">
          {outline.length === 0 ? (
            <p className="px-3 py-2 text-xs text-slate-400">{t('writer.noHeadings')}</p>
          ) : outline.map((h, i) => (
            <button key={`${h.offset}-${i}`} type="button" onClick={() => { onJump(h.offset); setOpen(false) }}
              className="block w-full truncate text-left text-sm text-slate-700 hover:bg-slate-50"
              style={{ paddingLeft: `${(h.level - 1) * 12 + 12}px`, paddingRight: '12px', paddingTop: '6px', paddingBottom: '6px' }}>
              {h.text}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

interface TreeProps {
  items: Document[]
  search: string
  expanded: Set<number>
  setExpanded: (s: Set<number>) => void
  currentId?: number
  chapterPrefix: string
  onSelect: (slug: string) => void
  onMove: (doc: Document, delta: -1 | 1) => void
  onDelete: (doc: Document) => void
  menuFor: number | null
  onOpenMenu: (doc: Document, x: number, y: number, align?: 'start' | 'end', flipY?: number) => void
  onCloseMenu: () => void
  dragEnabled: boolean
  dragId: number | null
  reorderingId: number | null
  dragBlocked: Set<number> | null
  dropTarget: { id: number; pos: 'before' | 'inside' | 'after' } | null
  onDragStartItem: (doc: Document) => void
  onDragOverItem: (doc: Document, pos: 'before' | 'inside' | 'after') => void
  onDropItem: (doc: Document) => void
  onDragEndItem: () => void
  presence?: Map<number, Presence[]> // 其他协作者正在编辑的章节
  commentCounts?: Record<string, number> // 各章节未解决的批注数
  suggestionCounts?: Record<string, number> // 各章节待处理的修改建议数
  readOnly?: boolean // 建议者：不能调整目录（无拖拽与章节菜单）
}

// TreeItems 章节树：文件夹/文件图标、展开折叠、搜索过滤、行内菜单、同级拖拽排序
function TreeItems(props: TreeProps) {
  return (
    <ul className="min-w-max space-y-0.5">
      {props.items.map((item) => <TreeItem key={item.id} {...props} item={item} depth={0} />)}
    </ul>
  )
}

function TreeItem(props: TreeProps & { item: Document; depth: number }) {
  const { t } = useTranslation()
  const {
    item, depth, search, expanded, setExpanded, currentId, chapterPrefix, onSelect, menuFor, onOpenMenu, onCloseMenu,
    dragEnabled, dragId, reorderingId, dragBlocked, dropTarget, onDragStartItem, onDragOverItem, onDropItem, onDragEndItem,
  } = props
  function toggleExpand() {
    const next = new Set(expanded)
    if (next.has(item.id)) next.delete(item.id); else next.add(item.id)
    setExpanded(next)
  }
  const hasChildren = !!item.children?.length
  const isExpanded = search !== '' || expanded.has(item.id)
  const active = currentId === item.id
  const dragging = dragId === item.id
  const reordering = reorderingId === item.id
  const dropHere = dropTarget?.id === item.id

  return (
    <li>
      <div
        {...(active ? { 'data-toc-active': '1' } : {})}
        draggable={dragEnabled && reorderingId == null}
        onContextMenu={(event) => {
          event.preventDefault()
          event.stopPropagation()
          onOpenMenu(item, event.clientX, event.clientY)
        }}
        onDragStart={(e) => { e.dataTransfer.effectAllowed = 'move'; onDragStartItem(item) }}
        onDragEnd={onDragEndItem}
        onDragOver={(e) => {
          if (!dragEnabled || dragId == null || dragId === item.id) return
          if (dragBlocked?.has(item.id)) return // 不能拖进自身子树
          e.preventDefault()
          const rect = e.currentTarget.getBoundingClientRect()
          const y = e.clientY - rect.top
          const pos = y < rect.height * 0.3 ? 'before' : y > rect.height * 0.7 ? 'after' : 'inside'
          onDragOverItem(item, pos)
        }}
        onDrop={(e) => { e.preventDefault(); onDropItem(item) }}
        className={`group relative flex items-center rounded-lg text-sm ${active ? 'bg-primary-50 ring-1 ring-inset ring-primary-100' : 'hover:bg-slate-50'} ${dragging ? 'opacity-40' : ''} ${reordering ? 'opacity-60' : ''}`}>
        {active && <span className="absolute left-0 top-1.5 h-[calc(100%-12px)] w-0.5 rounded-full bg-primary-500" />}
        {reordering && <span className="pointer-events-none absolute right-2 top-1/2 z-10 h-3.5 w-3.5 -translate-y-1/2 animate-spin rounded-full border-2 border-slate-200 border-t-primary-500" />}
        {dropHere && dropTarget!.pos !== 'inside' && <span className={`pointer-events-none absolute inset-x-1.5 z-10 h-0.5 rounded-full bg-primary-500 ${dropTarget!.pos === 'before' ? 'top-0' : 'bottom-0'}`} />}
        {dropHere && dropTarget!.pos === 'inside' && <span className="pointer-events-none absolute inset-0 z-10 rounded-lg ring-2 ring-inset ring-primary-400" />}
        {hasChildren ? (
          <button type="button" aria-label={isExpanded ? t('writer.collapse') : t('writer.expand')}
            onClick={(e) => { e.stopPropagation(); toggleExpand() }}
            className="ml-1 flex h-6 w-5 shrink-0 items-center justify-center rounded text-slate-400 hover:bg-slate-200 hover:text-slate-600">
            {isExpanded ? <ChevronDownIcon className="h-3.5 w-3.5" /> : <ChevronRightIcon className="h-3.5 w-3.5" />}
          </button>
        ) : (
          <span className="ml-1 w-5 shrink-0" />
        )}
        <button type="button" onClick={() => onSelect(item.slug)}
          className="flex flex-1 items-center gap-1.5 py-2 pl-1 pr-1 text-left">
          <DocTreeIcon icon={item.icon} hasChildren={hasChildren} colorClass={active ? 'text-primary-500' : 'text-slate-400'} />
          <span className={`whitespace-nowrap ${active ? 'font-medium text-primary-700' : 'text-slate-700'}`}>{chapterPrefix}{item.title}</span>
          {!!props.suggestionCounts?.[item.id] && (
            <span className="ml-1 inline-flex items-center gap-0.5 rounded-full bg-sky-100 px-1.5 text-[10px] font-medium text-sky-700" data-testid="tree-suggestions">
              <i className="fa-solid fa-code-pull-request" aria-hidden="true" />{props.suggestionCounts[item.id]}
            </span>
          )}
          {!!props.commentCounts?.[item.id] && (
            <span className="ml-1 inline-flex items-center gap-0.5 rounded-full bg-amber-100 px-1.5 text-[10px] font-medium text-amber-700" data-testid="tree-comments">
              <i className="fa-regular fa-comment" aria-hidden="true" />{props.commentCounts[item.id]}
            </span>
          )}
          {!!props.presence?.get(item.id)?.length && (
            <span className="ml-1 flex -space-x-1" data-testid="tree-presence">
              {props.presence.get(item.id)!.slice(0, 3).map((p) => <CollabAvatar key={p.conn_id} user={p.user} size="sm" ring={p.dirty} />)}
            </span>
          )}
        </button>
        <span className={`mr-1 hidden shrink-0 items-center ${props.readOnly ? '' : 'group-hover:flex'}`}>
          {dragEnabled ? (
            <Tooltip content={t('writer.dragReorder')}>
              <span className="flex h-6 w-6 cursor-grab items-center justify-center rounded text-slate-400 hover:bg-slate-200 hover:text-slate-700"><GripIcon className="h-4 w-4" /></span>
            </Tooltip>
          ) : <span className="flex h-6 w-6 cursor-default items-center justify-center rounded text-slate-400"><GripIcon className="h-4 w-4" /></span>}
          <button type="button" aria-label={t('writer.chapterActions')} aria-haspopup="menu" aria-expanded={menuFor === item.id}
            onClick={(event) => {
              event.stopPropagation()
              if (menuFor === item.id) {
                onCloseMenu()
                return
              }
              const rect = event.currentTarget.getBoundingClientRect()
              onOpenMenu(item, rect.right, rect.bottom + 4, 'end', rect.top - 4)
            }}
            className="flex h-6 w-6 items-center justify-center rounded text-slate-400 hover:bg-slate-200 hover:text-slate-700">
            <MoreIcon className="h-4 w-4" />
          </button>
        </span>
      </div>
      {hasChildren && isExpanded && (
        <ul className="ml-4 border-l border-slate-200 pl-1">
          {item.children!.map((child) => (
            <TreeItem key={child.id} {...props} item={child} depth={depth + 1} />
          ))}
        </ul>
      )}
    </li>
  )
}

// filterTree 按关键词过滤章节树（保留命中节点及其祖先链）
function filterTree(items: Document[], q: string): Document[] {
  const lower = q.toLowerCase()
  const result: Document[] = []
  for (const item of items) {
    const children = item.children ? filterTree(item.children, q) : []
    if (item.title.toLowerCase().includes(lower) || children.length > 0) {
      result.push({ ...item, children: children.length > 0 ? children : item.children })
    }
  }
  return result
}

function flatten(docs: Document[] | null | undefined): Document[] {
  return (docs || []).flatMap((d) => [d, ...flatten(d.children)])
}
