// 模板（「模板」插件）：章节模板插入到写作台光标处，书籍模板在新建书籍时生成章节目录与正文。

export const TEMPLATES_PLUGIN_KEY = 'templates'

export function templatesEnabled(site: { feature_plugins?: string[] } | Record<string, unknown>): boolean {
  const list = (site as { feature_plugins?: unknown }).feature_plugins
  return Array.isArray(list) && list.includes(TEMPLATES_PLUGIN_KEY)
}

export type TemplateKind = 'chapter' | 'book'

export interface TemplateNode { title: string; content: string; children?: TemplateNode[] }

// TemplateSummary 列表中的模板：章节模板给 preview（正文开头），书籍模板给 outline（第一级章节标题）。
export interface TemplateSummary {
  id: number
  user_id: number
  official: boolean
  kind: TemplateKind
  title: string
  description: string
  chapter_count: number
  use_count: number
  sort_order: number
  created_at: string
  updated_at: string
  preview: string
  outline?: string[]
}

export interface TemplateDetail extends Omit<TemplateSummary, 'preview' | 'outline'> {
  content?: string
  chapters?: TemplateNode[]
}

export interface TemplateLists {
  official: TemplateSummary[]
  mine: TemplateSummary[]
  used: number
  limit: number // -1 不限
}

// 模板中可用的变量（使用时替换为实际值）
export const TEMPLATE_VARIABLES = ['date', 'time', 'datetime', 'year', 'month', 'day', 'book', 'chapter', 'author'] as const

export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
}

// countNodes 书籍模板目录中的章节总数。
export function countNodes(nodes: TemplateNode[]): number {
  return nodes.reduce((n, x) => n + 1 + countNodes(x.children || []), 0)
}
