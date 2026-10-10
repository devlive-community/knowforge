import { resolveMediaUrl } from '@/lib/media'
import { parseIcon } from '@/lib/icons'

// ResourceIcon 通用图标展示：支持 fa 图标 / 上传的 image / svg，缺省回退。
// 供标签、成就等任意资源复用（icon_type: '' | 'fa' | 'image' | 'svg'）；icon_value 可带颜色（见 lib/icons.ts）。
export default function ResourceIcon({ iconType, iconValue, name, className, fallback = 'fa-hashtag' }: {
  iconType?: string
  iconValue?: string
  name?: string
  className?: string
  fallback?: string
}) {
  const base = className || 'flex h-10 w-10 shrink-0 items-center justify-center overflow-hidden rounded-xl border border-primary-100 bg-primary-50 text-primary-600'
  const icon = parseIcon(iconType, iconValue)
  if (icon.type === 'fa' && icon.value) {
    return <span className={base}><i className={`fa-solid ${icon.value}`} style={icon.color ? { color: icon.color } : undefined} aria-hidden="true" /></span>
  }
  if ((icon.type === 'image' || icon.type === 'svg') && icon.value) {
    // 图片/SVG logo 不套底色框：去掉 bg/border/ring/text 等装饰类，只保留尺寸与圆角，logo 干净展示。
    const clean = base.replace(/\b(?:bg|border|ring|text)-[^\s]+/g, '').replace(/\bborder\b/g, '').replace(/\s+/g, ' ').trim()
    const src = resolveMediaUrl(icon.value)
    if (icon.type === 'svg' && icon.color) {
      // 单色 SVG 着色：以 SVG 为遮罩填充所选颜色
      const mask = `url("${src}") center / contain no-repeat`
      return <span className={clean}><span role="img" aria-label={name || ''} className="block h-full w-full" style={{ backgroundColor: icon.color, WebkitMask: mask, mask }} /></span>
    }
    return <span className={clean}><img src={src} alt={name || ''} className="h-full w-full object-contain" /></span>
  }
  return <span className={base}><i className={`fa-solid ${fallback}`} aria-hidden="true" /></span>
}
