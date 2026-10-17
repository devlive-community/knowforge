import { useState } from 'react'
import { useRouter } from 'next/router'
import { Button, useFeedback } from '@/components/ui'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { setFollowing, type FollowStats } from '@/lib/user-follow'

// FollowUserButton 关注 / 取消关注作者：未登录时跳转登录；已关注时悬停显示「取消关注」，互相关注时标明。
export default function FollowUserButton({ username, following, followsMe = false, onChange, size }: {
  username: string
  following: boolean
  followsMe?: boolean
  onChange: (stats: FollowStats) => void
  size?: 'sm' | 'md'
}) {
  const { t } = useTranslation()
  const { user } = useApp()
  const router = useRouter()
  const { showToast } = useFeedback()
  const [busy, setBusy] = useState(false)
  const [hover, setHover] = useState(false)

  async function toggle() {
    if (!user) { void router.push(`/login?next=${encodeURIComponent(router.asPath)}`); return }
    setBusy(true)
    try {
      onChange(await setFollowing(username, !following))
    } catch (e) {
      showToast({ title: t(following ? 'userFollow.unfollowFailed' : 'userFollow.followFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(false)
    }
  }

  if (!following) {
    return (
      <Button type="button" size={size} loading={busy} onClick={() => void toggle()} data-testid="follow-user">
        <i className="fa-solid fa-plus" aria-hidden="true" /> {t(followsMe ? 'userFollow.followBack' : 'userFollow.follow')}
      </Button>
    )
  }
  return (
    <Button type="button" size={size} variant="outline" loading={busy} onClick={() => void toggle()} data-testid="unfollow-user"
      onMouseEnter={() => setHover(true)} onMouseLeave={() => setHover(false)}
      className={hover ? 'border-rose-200 text-rose-600' : ''}>
      {hover
        ? <><i className="fa-solid fa-xmark" aria-hidden="true" /> {t('userFollow.unfollow')}</>
        : <><i className="fa-solid fa-check" aria-hidden="true" /> {t(followsMe ? 'userFollow.mutual' : 'userFollow.following')}</>}
    </Button>
  )
}
