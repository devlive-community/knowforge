import { useRouter } from 'next/router'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Badge, ButtonLink } from '@/components/ui'
import { checkoutAvailable, checkoutHref, formatPrice } from '@/lib/commerce'
import { freeReadersText, type Paywall } from '@/lib/paid'

// PaywallCard 章节未解锁时显示在试读内容下方：购买本章 / 购买全书、会员折扣与「免费阅读」入口。
export default function PaywallCard({ paywall }: { paywall: Paywall }) {
  const { t, locale } = useTranslation()
  const { site } = useApp()
  const router = useRouter()
  const canBuy = checkoutAvailable(site)
  const price = (final: number, original: number) => (
    <>
      {formatPrice(final, paywall.currency, locale)}
      {final < original && <span className="ml-1.5 text-xs line-through opacity-70">{formatPrice(original, paywall.currency, locale)}</span>}
    </>
  )
  return (
    <div className="relative mt-2">
      <div className="pointer-events-none absolute -top-24 left-0 right-0 h-24 bg-gradient-to-b from-transparent to-white" />
      <div className="rounded-2xl border border-amber-200 bg-amber-50/60 p-6 text-center">
        <i className="fa-solid fa-lock text-2xl text-amber-500" aria-hidden="true" />
        <h3 className="mt-2 text-lg font-bold text-slate-900">{t('paid.paywall.title')}</h3>
        <p className="mt-1 text-sm text-slate-500">{t('paid.paywall.subtitle')}</p>
        {paywall.discount_percent > 0 && <div className="mt-2"><Badge tone="amber">{t('paid.paywall.discount', { n: paywall.discount_percent })}</Badge></div>}
        <div className="mt-5 flex flex-wrap justify-center gap-3">
          {!paywall.logged_in ? (
            <ButtonLink href={`/login?next=${encodeURIComponent(router.asPath)}`}>{t('paid.paywall.login')}</ButtonLink>
          ) : canBuy ? (
            <>
              {paywall.chapter_price_cents > 0 && (
                <ButtonLink href={checkoutHref('paid-doc', paywall.doc_id)} variant="outline">{t('paid.paywall.buyChapter')} · {price(paywall.chapter_final_cents, paywall.chapter_price_cents)}</ButtonLink>
              )}
              {paywall.book_price_cents > 0 && (
                <ButtonLink href={checkoutHref('paid-book', paywall.book_id)}>{t('paid.paywall.buyBook')} · {price(paywall.book_final_cents, paywall.book_price_cents)}</ButtonLink>
              )}
            </>
          ) : <p className="text-sm text-slate-500">{t('paid.paywall.noCheckout')}</p>}
        </div>
        {paywall.free_tier > 0 && (
          <p className="mt-4 text-xs text-slate-500">
            {paywall.free_for?.length ? t('paid.paywall.freeFor', { who: freeReadersText(t, locale, paywall.free_for, paywall.free_tier) }) : t('paid.paywall.freeTier', { n: paywall.free_tier })}
            {paywall.upgrade_link && <> · <a href={paywall.upgrade_link} className="text-primary-600 hover:underline">{t('paid.paywall.upgrade')}</a></>}
          </p>
        )}
      </div>
    </div>
  )
}
