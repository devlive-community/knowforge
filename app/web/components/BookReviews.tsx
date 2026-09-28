import { useCallback, useEffect, useState } from 'react'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Button, Textarea, Loading, useFeedback } from '@/components/ui'
import UserAvatar from '@/components/UserAvatar'
import type { BookReview, BookReviewSummary } from '@/lib/types'
import { displayName } from '@/lib/users'

// Stars 只读星级展示（支持半星取整为整数星，size 控制字号）
function Stars({ value, size = 'text-base' }: { value: number; size?: string }) {
  return (
    <span className={`inline-flex items-center gap-0.5 text-amber-400 ${size}`} aria-hidden="true">
      {[1, 2, 3, 4, 5].map((n) => (
        <i key={n} className={`fa-star ${n <= Math.round(value) ? 'fa-solid' : 'fa-regular text-slate-300'}`} />
      ))}
    </span>
  )
}

// StarPicker 可点击的评分选择器
function StarPicker({ value, onChange, disabled }: { value: number; onChange: (v: number) => void; disabled?: boolean }) {
  const [hover, setHover] = useState(0)
  return (
    <span className="inline-flex items-center gap-1 text-2xl text-amber-400">
      {[1, 2, 3, 4, 5].map((n) => (
        <button key={n} type="button" disabled={disabled} aria-label={`${n}`}
          onMouseEnter={() => setHover(n)} onMouseLeave={() => setHover(0)} onClick={() => onChange(n)}
          className="leading-none transition-transform hover:scale-110 disabled:cursor-not-allowed">
          <i className={`fa-star ${n <= (hover || value) ? 'fa-solid' : 'fa-regular text-slate-300'}`} />
        </button>
      ))}
    </span>
  )
}

interface ReviewsResponse {
  items: BookReview[]
  total: number
  page: number
  page_size: number
  summary: BookReviewSummary
  mine: BookReview | null
}

// BookReviews 书籍评价区：平均分与分布概览 + 评论列表 + 当前用户评分/评论表单。
export default function BookReviews({ bookId, authorId }: { bookId: number; authorId: number }) {
  const { t } = useTranslation()
  const { user } = useApp()
  const { showToast } = useFeedback()
  const [loading, setLoading] = useState(true)
  const [items, setItems] = useState<BookReview[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [summary, setSummary] = useState<BookReviewSummary>({ average: 0, count: 0, distribution: {} })
  const [mine, setMine] = useState<BookReview | null>(null)
  const [rating, setRating] = useState(0)
  const [content, setContent] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [deletingId, setDeletingId] = useState<number | null>(null)
  const [loadingMore, setLoadingMore] = useState(false)

  const load = useCallback(async (targetPage: number, append: boolean) => {
    try {
      const d = await api<ReviewsResponse>(`/books/${bookId}/reviews?page=${targetPage}&page_size=10`)
      setItems((prev) => (append ? [...prev, ...d.items] : d.items))
      setTotal(d.total)
      setSummary(d.summary)
      setPage(d.page)
      setMine(d.mine)
      if (d.mine) { setRating(d.mine.rating); setContent(d.mine.content) }
    } catch { /* 忽略：区块加载失败不阻塞详情页 */ }
  }, [bookId])

  useEffect(() => {
    setLoading(true)
    load(1, false).finally(() => setLoading(false))
  }, [load])

  async function submit() {
    if (rating < 1) { showToast({ message: t('review.rateFirst'), tone: 'error' }); return }
    setSubmitting(true)
    try {
      const d = await api<{ review: BookReview; summary: BookReviewSummary }>(`/books/${bookId}/reviews`, { method: 'POST', body: { rating, content: content.trim() } })
      setSummary(d.summary)
      setMine(d.review)
      // 用最新的自评替换列表中的旧条目，或插入到最前
      setItems((prev) => {
        const without = prev.filter((r) => r.id !== d.review.id)
        return [d.review, ...without]
      })
      setTotal((prev) => (mine ? prev : prev + 1))
      showToast({ message: t('review.submitted'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('review.submitFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSubmitting(false)
    }
  }

  async function remove(id: number) {
    setDeletingId(id)
    try {
      const d = await api<{ summary: BookReviewSummary }>(`/reviews/${id}`, { method: 'DELETE' })
      setItems((prev) => prev.filter((r) => r.id !== id))
      setTotal((prev) => Math.max(0, prev - 1))
      setSummary(d.summary)
      if (mine?.id === id) { setMine(null); setRating(0); setContent('') }
      showToast({ message: t('review.deleted'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('review.deleteFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setDeletingId(null)
    }
  }

  async function loadMore() {
    setLoadingMore(true)
    await load(page + 1, true)
    setLoadingMore(false)
  }

  const canReview = !!user && user.id !== authorId
  const total5 = summary.count || 0

  return (
    <div className="max-w-3xl">
      <div className="mb-6 flex flex-wrap items-center gap-x-8 gap-y-4">
        <div className="flex items-center gap-3">
          <span className="text-4xl font-bold text-slate-900">{summary.average ? summary.average.toFixed(1) : '—'}</span>
          <div>
            <Stars value={summary.average} />
            <div className="mt-0.5 text-xs text-slate-400">{t('review.count', { n: summary.count })}</div>
          </div>
        </div>
        {total5 > 0 && (
          <div className="min-w-[180px] flex-1 space-y-1">
            {[5, 4, 3, 2, 1].map((star) => {
              const n = summary.distribution?.[String(star)] || 0
              const pct = total5 ? Math.round((n / total5) * 100) : 0
              return (
                <div key={star} className="flex items-center gap-2 text-xs text-slate-500">
                  <span className="w-3 shrink-0 text-right">{star}</span>
                  <i className="fa-solid fa-star text-amber-400" aria-hidden="true" />
                  <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-slate-100">
                    <span className="block h-full rounded-full bg-amber-400" style={{ width: `${pct}%` }} />
                  </span>
                  <span className="w-6 shrink-0 text-right tabular-nums">{n}</span>
                </div>
              )
            })}
          </div>
        )}
      </div>

      {/* 评价表单 */}
      {canReview ? (
        <div className="mb-6 rounded-xl border border-slate-200 bg-white p-4">
          <div className="flex items-center gap-3">
            <span className="text-sm font-medium text-slate-700">{t('review.yourRating')}</span>
            <StarPicker value={rating} onChange={setRating} disabled={submitting} />
          </div>
          <Textarea rows={3} className="mt-3" value={content} maxLength={2000}
            onChange={(e) => setContent(e.target.value)} placeholder={t('review.placeholder')} />
          <div className="mt-3 flex justify-end">
            <Button loading={submitting} onClick={submit}>{mine ? t('review.update') : t('review.submit')}</Button>
          </div>
        </div>
      ) : (
        <p className="mb-6 text-sm text-slate-400">{user ? t('review.authorCannot') : t('review.loginToReview')}</p>
      )}

      {/* 评价列表 */}
      {loading ? (
        <Loading />
      ) : items.length === 0 ? (
        <p className="py-6 text-center text-sm text-slate-400">{t('review.noReviews')}</p>
      ) : (
        <ul className="space-y-4">
          {items.map((r) => {
            const canDelete = !!user && (user.id === r.user_id || user.id === authorId || user.role === 'admin')
            return (
              <li key={r.id} className="flex gap-3 border-b border-slate-100 pb-4 last:border-0">
                <UserAvatar user={r.user} size="h-9 w-9" />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-slate-800">{displayName(r.user) || '—'}</span>
                    <Stars value={r.rating} size="text-xs" />
                    <span className="ml-auto text-xs text-slate-400">{new Date(r.updated_at).toLocaleDateString()}</span>
                  </div>
                  {r.content && <p className="mt-1.5 whitespace-pre-wrap text-sm leading-6 text-slate-600 [overflow-wrap:anywhere]">{r.content}</p>}
                  {canDelete && (
                    <button type="button" disabled={deletingId === r.id} onClick={() => remove(r.id)}
                      className="mt-1.5 text-xs text-slate-400 transition-colors hover:text-rose-500 disabled:opacity-50">
                      {deletingId === r.id ? t('review.deleting') : t('review.delete')}
                    </button>
                  )}
                </div>
              </li>
            )
          })}
        </ul>
      )}

      {items.length < total && (
        <div className="mt-4 flex justify-center">
          <Button variant="ghost" loading={loadingMore} onClick={loadMore}>{t('review.loadMore')}</Button>
        </div>
      )}
    </div>
  )
}
