import { useEffect, useState } from 'react'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { durationLabel } from '@/lib/commerce'
import type { MembershipGift } from '@/lib/membership'
import { Badge, Button, Card, Pagination, useFeedback } from '@/components/ui'
import { useUrlPage } from '@/lib/use-url-page'

const statusTone = { unused: 'emerald', redeemed: 'slate', void: 'rose' } as const

// GiftsCard 我购买的礼品卡：复制兑换码送给他人，查看是否已被兑换（没有礼品卡时不显示）。
export default function GiftsCard({ reloadKey }: { reloadKey?: number }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [page, setPage] = useUrlPage('giftsPage')
  const [data, setData] = useState<{ items: MembershipGift[]; total: number; page: number; page_size: number } | null>(null)

  useEffect(() => {
    api<{ items: MembershipGift[]; total: number; page: number; page_size: number }>(`/users/me/membership/gifts?page=${page}&page_size=10`).then(setData).catch(() => {})
  }, [page, reloadKey])

  async function copy(code: string) {
    try {
      await navigator.clipboard.writeText(code)
      showToast({ message: t('membership.gift.copied'), tone: 'success' })
    } catch {
      showToast({ message: t('membership.gift.copyFailed'), tone: 'error' })
    }
  }

  if (!data || data.total === 0) return null
  return (
    <Card className="mt-6 p-5">
      <h2 className="font-bold text-slate-900"><i className="fa-solid fa-gift mr-1.5 text-rose-500" aria-hidden="true" />{t('membership.gift.title')}</h2>
      <p className="mt-1 text-xs text-slate-500">{t('membership.gift.hint')}</p>
      <ul className="mt-3 divide-y divide-slate-100">
        {data.items.map((g) => (
          <li key={g.id} className="flex flex-wrap items-center justify-between gap-3 py-3 text-sm">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium text-slate-900">{g.plan_name} · {durationLabel(t, g.days)}</span>
                <Badge tone={statusTone[g.status]}>{t(`membership.gift.status.${g.status}`)}</Badge>
              </div>
              <div className="mt-1 text-xs text-slate-400">
                {t('membership.gift.boughtOn', { date: formatDate(g.created_at) })}
                {g.status === 'redeemed' && g.redeemed_at && ` · ${t(g.redeemed_by_me ? 'membership.gift.redeemedByMe' : 'membership.gift.redeemedOn', { date: formatDate(g.redeemed_at) })}`}
              </div>
            </div>
            {g.status === 'unused' && (
              <div className="flex shrink-0 items-center gap-2">
                <code className="rounded-md bg-slate-50 px-2 py-1 font-mono text-xs tracking-wider text-slate-700">{g.code}</code>
                <Button size="sm" variant="outline" className="whitespace-nowrap" onClick={() => void copy(g.code)}><i className="fa-regular fa-copy mr-1" aria-hidden="true" />{t('membership.gift.copy')}</Button>
              </div>
            )}
          </li>
        ))}
      </ul>
      {data.total > data.page_size && <div className="mt-4"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
    </Card>
  )
}
