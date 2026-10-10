// 资源图标的取值约定（icon_type + icon_value，标签、分类、会员方案、等级等共用）：
//   fa    → icon_value 为 Font Awesome 类名（如 fa-folder）
//   image → 上传的图片地址
//   svg   → 上传的 SVG 地址
// 可选颜色追加在 icon_value 末尾：「值|#rrggbb」（fa 为图标颜色，svg 为单色着色；image 不支持）。旧数据没有颜色时照常显示。

export interface IconSpec { type: string; value: string; color: string }

const COLOR = /^#[0-9a-fA-F]{6}$/

export function parseIcon(type: string | undefined, raw: string | undefined): IconSpec {
  const text = raw || ''
  const i = text.lastIndexOf('|')
  if (i > 0 && COLOR.test(text.slice(i + 1))) return { type: type || '', value: text.slice(0, i), color: text.slice(i + 1).toLowerCase() }
  return { type: type || '', value: text, color: '' }
}

export function formatIconValue(value: string, color: string): string {
  return value && color && COLOR.test(color) ? `${value}|${color.toLowerCase()}` : value
}

export function isValidColor(color: string): boolean {
  return COLOR.test(color)
}

/** 图标调色板（与站点主题无关的固定色，便于分类等区分） */
export const ICON_COLORS = ['#ef4444', '#f97316', '#f59e0b', '#eab308', '#22c55e', '#10b981', '#14b8a6', '#0ea5e9', '#3b82f6', '#6366f1', '#8b5cf6', '#ec4899', '#64748b', '#1e293b']

/** 选择器分组（与 scripts/gen_fa_icons.py 一致）；all 为全部图标 */
export const ICON_GROUPS = ['common', 'files', 'users', 'system', 'media', 'editing', 'education', 'all'] as const
export type IconGroup = typeof ICON_GROUPS[number]

export interface IconCatalog { icons: [string, string][]; groups: Record<string, string[]> }

let catalog: Promise<IconCatalog> | null = null

/** loadIconCatalog 按需加载 Font Awesome 图标目录（打开选择器时才下载） */
export function loadIconCatalog(): Promise<IconCatalog> {
  if (!catalog) catalog = import('./fa-icons.json').then((m) => (m.default || m) as unknown as IconCatalog)
  return catalog
}

/** searchIcons 按名称与搜索词匹配（多个词需全部命中） */
export function searchIcons(cat: IconCatalog, group: IconGroup, query: string): string[] {
  const words = query.trim().toLowerCase().replace(/^fa-/, '').split(/\s+/).filter(Boolean)
  if (words.length === 0) return group === 'all' ? cat.icons.map(([n]) => n) : cat.groups[group] || []
  // 搜索时在全部图标中查找（不限分组）
  return cat.icons.filter(([name, terms]) => words.every((w) => name.includes(w) || terms.includes(w))).map(([n]) => n)
}
