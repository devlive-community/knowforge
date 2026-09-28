import { useEffect, useState } from 'react'
import { useRouter } from 'next/router'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, ButtonLink } from '@/components/ui'
import { checkoutAvailable, checkoutHref, formatPrice } from '@/lib/commerce'
import { freeReadersText, type PaidBookInfo } from '@/lib/paid'

// PaidBookCard 书籍详情页侧栏：付费书籍的价格、免费试读章节与购买/已解锁状态（付费内容插件启用且本书开启付费时显示）。
export default function PaidBookCard({ bookId }: { bookId: number }) {
  const { t, locale } = useTranslation()
  const { site, user } = useApp()
  const router = useRouter()
  const [info, setInfo] = useState<PaidBookInfo | null>(null)
  const enabled = (site.feature_plugins || []).includes('paid-content')
  useEffect(() => {
    if (!enabled) return
    api<PaidBookInfo>(`/paid/books/${bookId}`).then(setInfo).catch(() => {})
  }, [enabled, bookId, user?.id])
  if (!info || !info.enabled) return null
  const price = (final: number, original: number) => (
    <>{formatPrice(final, info.currency, locale)}{final < original && <span className="ml-1.5 text-xs line-through opacity-70">{formatPrice(original, info.currency, locale)}</span>}</>
  )
  return (
    <div className="rounded-2xl border border-amber-200 bg-amber-50/50 p-5 shadow-sm">
      <div className="flex items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 font-bold text-slate-900"><i className="fa-solid fa-coins text-amber-500" aria-hidden="true" />{t('paid.book.title')}</h2>
        {info.is_author ? <Badge>{t('paid.book.author')}</Badge> : info.can_read_all ? <Badge tone="emerald">{t('paid.book.unlocked')}</Badge> : null}
      </div>
      <ul className="mt-3 space-y-1.5 text-sm text-slate-600">
        {info.book_price_cents > 0 && <li>{t('paid.book.bookPrice')}：<span className="font-medium text-slate-900">{price(info.book_final_cents, info.book_price_cents)}</span></li>}
        {info.chapter_price_cents > 0 && <li>{t('paid.book.chapterPrice')}：<span className="font-medium text-slate-900">{formatPrice(info.chapter_price_cents, info.currency, locale)}</span></li>}
        {info.free_chapters > 0 && <li>{t('paid.book.freeChapters', { n: info.free_chapters })}</li>}
        {info.discount_percent > 0 && <li className="text-amber-700">{t('paid.paywall.discount', { n: info.discount_percent })}</li>}
        {info.free_tier > 0 && <li className="text-xs text-slate-500">{info.free_for?.length ? t('paid.paywall.freeFor', { who: freeReadersText(t, locale, info.free_for, info.free_tier) }) : t('paid.paywall.freeTier', { n: info.free_tier })}{info.upgrade_link && <> · <a href={info.upgrade_link} className="text-primary-600 hover:underline">{t('paid.paywall.upgrade')}</a></>}</li>}
      </ul>
      {!info.is_author && !info.can_read_all && info.book_price_cents > 0 && (
        <div className="mt-4">
          {!user ? <ButtonLink className="w-full" href={`/login?next=${encodeURIComponent(router.asPath)}`}>{t('paid.paywall.login')}</ButtonLink>
            : checkoutAvailable(site) ? <ButtonLink className="w-full" href={checkoutHref('paid-book', bookId)}>{t('paid.paywall.buyBook')} · {price(info.book_final_cents, info.book_price_cents)}</ButtonLink>
              : <p className="text-xs text-slate-500">{t('paid.paywall.noCheckout')}</p>}
        </div>
      )}
    </div>
  )
}
