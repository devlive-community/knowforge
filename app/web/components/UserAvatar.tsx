import Link from 'next/link'
import Tooltip from '@/components/ui/Tooltip'
import { API_BASE } from '@/lib/api'
import { displayName } from '@/lib/users'

export interface UserAvatarUser {
  username: string
  nickname?: string
  display_name?: string
  avatar?: string | null
}

interface UserAvatarProps {
  user?: UserAvatarUser | null
  /** 尺寸：h-5/h-6/h-7/h-9 等任意 tailwind 尺寸类，默认 h-7 w-7 */
  size?: string
  /** hover 显示用户名 tooltip，默认 true */
  tooltip?: boolean
  /** 点击跳转用户主页，默认 true；传 false 则为纯展示 */
  link?: boolean
  className?: string
}

function src(avatar?: string | null): string {
  if (!avatar) return ''
  return /^https?:\/\//.test(avatar) ? avatar : API_BASE + avatar
}

// UserAvatar 通用用户头像：无头像回退首字母，hover tooltip 用户名，点击跳转用户主页。
// 所有展示用户头像的地方一律使用本组件，保证样式与行为只改一处。
export default function UserAvatar({ user, size = 'h-7 w-7', tooltip = true, link = true, className = '' }: UserAvatarProps) {
  if (!user?.username) return null
  const inner = (
    <span
      className={`inline-flex shrink-0 items-center justify-center overflow-hidden rounded-full bg-primary-500 align-middle ${size} ${className}`.trim()}
    >
      {src(user.avatar)
        ? <img src={src(user.avatar)} alt={displayName(user)} className="h-full w-full object-cover" />
        : <span className="font-bold text-white" style={{ fontSize: '0.5em' }}>{displayName(user).slice(0, 1).toUpperCase()}</span>}
    </span>
  )
  if (!link) return inner
  return (
    <Tooltip content={displayName(user)}>
      <Link href={`/user/${encodeURIComponent(user.username)}`} className="inline-flex shrink-0">
        {inner}
      </Link>
    </Tooltip>
  )
}
