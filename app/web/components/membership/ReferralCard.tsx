import { useEffect, useState } from 'react'
import Link from 'next/link'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import UserAvatar from '@/components/UserAvatar'
import type { UserLite } from '@/components/UserSearchSelect'
import { Badge, Button, Card, Input, useFeedback } from '@/components/ui'

interface MyReferral {
  enabled: boolean
  rules?: { inviter_days: number; invitee_days: number; signup_days: number; monthly_limit: number; plan_name: string }
  rewards: { kind: 'signup' | 'purchase'; days: number; capped: boolean; created_at: string; invitee?: UserLite }[]
  total_days: number
}

// ReferralCard 邀请好友得会员：奖励规则、我的邀请链接（邀请码在账号设置中开启）与获得的奖励。
export default function ReferralCard() {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [data, setData] = useState<MyReferral | null>(null)
  const [invite, setInvite] = useState<{ invite_code: string; enabled: boolean } | null>(null)

  useEffect(() => {
    api<MyReferral>('/users/me/membership/referral').then(setData).catch(() => {})
    api<{ invite_code: string; enabled: boolean }>('/auth/invite-code').then(setInvite).catch(() => {})
  }, [])

  if (!data || (!data.enabled && data.rewards.length === 0)) return null
  const link = invite?.enabled && invite.invite_code && typeof window !== 'undefined' ? `${window.location.origin}/register?invite=${encodeURIComponent(invite.invite_code)}` : ''
  async function copy() {
    try { await navigator.clipboard.writeText(link); showToast({ message: t('membership.referral.copied'), tone: 'success' }) }
    catch { showToast({ message: t('membership.gift.copyFailed'), tone: 'error' }) }
  }
  const r = data.rules
  return (
    <Card className="mt-6 p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="font-bold text-slate-900"><i className="fa-solid fa-user-plus mr-1.5 text-primary-500" aria-hidden="true" />{t('membership.referral.title')}</h2>
          {r && (
            <ul className="mt-2 space-y-1 text-sm text-slate-600">
              {r.inviter_days > 0 && <li>{t('membership.referral.rulePurchase', { days: r.inviter_days, plan: r.plan_name })}</li>}
              {r.invitee_days > 0 && <li>{t('membership.referral.ruleInvitee', { days: r.invitee_days })}</li>}
              {r.signup_days > 0 && <li>{t('membership.referral.ruleSignup', { days: r.signup_days })}</li>}
              {r.monthly_limit > 0 && <li className="text-xs text-slate-400">{t('membership.referral.ruleLimit', { n: r.monthly_limit })}</li>}
            </ul>
          )}
        </div>
        {data.total_days > 0 && <div className="shrink-0 text-right"><div className="text-2xl font-bold tabular-nums text-slate-900">{data.total_days}</div><div className="text-xs text-slate-400">{t('membership.referral.totalDays')}</div></div>}
      </div>
      {data.enabled && (
        <div className="mt-4">
          {link ? (
            <div className="flex max-w-xl gap-2">
              <Input value={link} readOnly aria-label={t('membership.referral.link')} />
              <Button variant="outline" className="shrink-0 whitespace-nowrap" onClick={() => void copy()}><i className="fa-regular fa-copy" aria-hidden="true" />{t('membership.referral.copyLink')}</Button>
            </div>
          ) : (
            <Link href="/user/invite" className="text-sm font-medium text-primary-600 hover:text-primary-700">{t('membership.referral.enableInvite')} <i className="fa-solid fa-arrow-right text-xs" aria-hidden="true" /></Link>
          )}
        </div>
      )}
      {data.rewards.length > 0 && (
        <ul className="mt-4 divide-y divide-slate-100 border-t border-slate-100">
          {data.rewards.map((w, i) => (
            <li key={i} className="flex flex-wrap items-center gap-2 py-2.5 text-sm">
              {w.invitee && <span className="flex min-w-0 items-center gap-2"><UserAvatar user={w.invitee} /><span className="truncate text-slate-700">{w.invitee.nickname || w.invitee.username}</span></span>}
              <Badge tone={w.kind === 'purchase' ? 'amber' : 'sky'}>{t(`membership.referral.kind.${w.kind}`)}</Badge>
              <span className="ml-auto text-xs text-slate-500">
                {w.capped ? t('membership.referral.capped') : t('membership.referral.gotDays', { days: w.days })} · {formatDate(w.created_at)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}
