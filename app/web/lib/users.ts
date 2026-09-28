// 用户展示名：服务端按用户的「名字显示方式」计算 display_name（优先昵称，或始终用户名）；
// 旧数据或未带该字段时按默认规则回退为昵称，没有昵称时为用户名。
export interface NamedUser { username?: string; nickname?: string; display_name?: string }

export function displayName(u: NamedUser | null | undefined): string {
  if (!u) return ''
  return u.display_name || u.nickname || u.username || ''
}
