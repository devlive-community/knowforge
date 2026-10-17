import { useCallback, useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import Link from 'next/link'
import UserAvatar from '@/components/UserAvatar'
import { Button, EmptyState, Loading, Modal, SegmentedTabs } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'
import { fetchFollowList, type FollowListItem, type FollowListKind } from '@/lib/user-follow'
import FollowUserButton from './FollowUserButton'

const PAGE_SIZE = 20

// FollowList 某位用户的关注者 / 关注列表（分页加载更多），每项可直接关注或取消关注。
export function FollowList({ username, kind, onNavigate, onChanged, className = '' }: {
  username: string
  kind: FollowListKind
  onNavigate?: () => void // 点击用户跳转前（弹窗中用于关闭）
  onChanged?: () => void // 列表中的关注状态变化
  className?: string
}) {
  const { t } = useTranslation()
  const [items, setItems] = useState<FollowListItem[] | null>(null)
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loadingMore, setLoadingMore] = useState(false)
  const [failed, setFailed] = useState(false)

  const load = useCallback(async (next: number) => {
    const r = await fetchFollowList(username, kind, next, PAGE_SIZE)
    setTotal(r.total)
    setPage(r.page)
    setItems((prev) => (next === 1 || !prev ? r.items : [...prev, ...r.items]))
  }, [username, kind])

  useEffect(() => {
    setItems(null)
    setFailed(false)
    load(1).catch(() => { setItems([]); setFailed(true) })
  }, [load])

  async function more() {
    setLoadingMore(true)
    try { await load(page + 1) } catch { /* 保留已加载的列表 */ } finally { setLoadingMore(false) }
  }

  if (items === null) return <Loading className="py-10" />
  if (items.length === 0) {
    return <EmptyState>{failed ? t('userFollow.loadFailed') : t(kind === 'followers' ? 'userFollow.noFollowers' : 'userFollow.noFollowing')}</EmptyState>
  }
  return (
    <div className={className}>
      <ul className="divide-y divide-slate-100" data-testid="follow-list">
        {items.map((it) => (
          <li key={it.user.id} className="flex items-center gap-3 px-2 py-3">
            <UserAvatar user={it.user} size="h-10 w-10 text-sm" tooltip={false} />
            <Link href={`/user/${encodeURIComponent(it.user.username)}`} onClick={onNavigate} className="min-w-0 flex-1">
              <span className="block truncate text-sm font-medium text-slate-800 hover:text-primary-600">{it.user.display_name || it.user.username}</span>
              <span className="block truncate text-xs text-slate-400">{it.user.bio || `@${it.user.username}`}</span>
            </Link>
            {!it.is_me && (
              <FollowUserButton size="sm" username={it.user.username} following={it.followed_by_me}
                onChange={(s) => {
                  setItems((list) => list && list.map((x) => (x.user.id === it.user.id ? { ...x, followed_by_me: s.followed_by_me } : x)))
                  onChanged?.()
                }} />
            )}
          </li>
        ))}
      </ul>
      {items.length < total && (
        <div className="mt-3 text-center">
          <Button type="button" variant="ghost" size="sm" loading={loadingMore} onClick={() => void more()}>{t('userFollow.loadMore')}</Button>
        </div>
      )}
    </div>
  )
}

// FollowListDialog 弹窗查看关注者 / 关注列表，顶部切换两类。
export default function FollowListDialog({ username, kind, counts, onKindChange, onClose, onChanged }: {
  username: string
  kind: FollowListKind
  counts: { followers: number; following: number }
  onKindChange: (kind: FollowListKind) => void
  onClose: () => void
  onChanged?: () => void
}) {
  const { t } = useTranslation()
  return createPortal(
    <Modal open onClose={onClose} title={t('userFollow.listTitle')} className="max-w-lg">
      <SegmentedTabs className="mb-2" fullWidth value={kind} ariaLabel={t('userFollow.listTitle')} onChange={(v) => onKindChange(v as FollowListKind)}
        items={[
          { value: 'followers', label: t('userFollow.followersCount', { count: counts.followers }) },
          { value: 'following', label: t('userFollow.followingCount', { count: counts.following }) },
        ]} />
      <FollowList username={username} kind={kind} onNavigate={onClose} onChanged={onChanged} className="-mx-2 max-h-[60vh] overflow-y-auto" />
    </Modal>,
    document.body,
  )
}
