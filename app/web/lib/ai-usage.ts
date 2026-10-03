// AI 用量展示工具：tokens 紧凑格式与估算费用（cost_micros 为货币单位的百万分之一）

export function formatTokens(n: number | null | undefined): string {
  return new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(n || 0)
}

export function formatCost(micros: number | null | undefined, currency: string): string {
  const value = (micros || 0) / 1_000_000
  try {
    return new Intl.NumberFormat(undefined, { style: 'currency', currency, maximumFractionDigits: value > 0 && value < 1 ? 4 : 2 }).format(value)
  } catch {
    return `${value.toFixed(4)} ${currency}`
  }
}

export interface UsageAgg {
  calls: number
  errors: number
  input_tokens: number
  output_tokens: number
  characters: number // 机器翻译按字符计量
  cost_micros: number
}

export interface MyAIUsage {
  month_start: string
  used_tokens: number
  calls: number
  limit: number // -1 不限
  by_feature: { feature: string; calls: number; tokens: number; cost_micros?: number }[]
  translate_chars: number // 本月已翻译字数
  translate_limit: number // -1 不限
  daily: { date: string; tokens: number; characters: number }[]
  show_cost?: boolean // 站点是否向用户展示估算费用
  cost_micros?: number // 本月估算费用（show_cost 时返回）
  currency?: string
}

type TFn = (key: string, vars?: Record<string, string | number>) => string

// aiFeatureLabel 调用功能名称（i18n：ai.feature.<key>，缺失时回退键名，便于新插件先上线再补文案）
export function aiFeatureLabel(t: TFn, feature: string): string {
  const k = `ai.feature.${feature}`
  const v = t(k)
  return v === k ? feature : v
}

// AIUsageRef AI 调用记录的关联对象（服务端按功能解析，不可见或无法解析时为 null）。
export interface AIUsageRef { kind: 'translation' | 'qa' | 'book' | 'moderation_case' | string; title: string; link: string }
