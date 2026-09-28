import { useCallback, useEffect, useState, FormEvent } from 'react'
import Link from 'next/link'
import { api } from '@/lib/api'
import UserAvatar from '@/components/UserAvatar'
import { formatDate } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import type { User } from '@/lib/types'
import { Button, Loading, useFeedback } from '@/components/ui'
import ReportButton from '@/components/ReportButton'
import CaptchaField, { CaptchaValue } from '@/components/CaptchaField'
import { displayName } from '@/lib/users'

interface CommentItem {
  id: number
  user_id: number
  content: string
  created_at: string
  user: { username: string; avatar?: string }
  replies?: CommentItem[]
}

// Comments 章节评论区（两级）
export default function Comments({ docId, allowComments = true }: { docId: number; allowComments?: boolean }) {
  const { confirmAction, showToast } = useFeedback()
  const { user, site } = useApp()
  const { t } = useTranslation()
  const commentsOff = site.comments_enabled === 'false'
  const [comments, setComments] = useState<CommentItem[] | null>(null)
  const [content, setContent] = useState('')
  const [replyTo, setReplyTo] = useState<number | null>(null)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [captcha, setCaptcha] = useState<CaptchaValue>({ id: '', answer: '', required: false })
  const [captchaRefresh, setCaptchaRefresh] = useState(0)

  const load = useCallback(async () => {
    try {
      setComments(await api<CommentItem[]>(`/documents/${docId}/comments`))
    } catch (e) {
      setComments([])
    }
  }, [docId])

  useEffect(() => { load() }, [load])

  async function submit(e: FormEvent, parentId?: number) {
    e.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      await api(`/documents/${docId}/comments`, {
        method: 'POST',
        body: { content, parent_id: parentId, captcha_id: captcha.id, captcha_answer: captcha.answer },
      })
      setContent('')
      setReplyTo(null)
      await load()
    } catch (err) {
      setError((err as Error).message)
      if (captcha.required) setCaptchaRefresh((n) => n + 1) // 验证码一次性，失败后换新
    } finally {
      setSubmitting(false)
    }
  }

  async function remove(id: number) {
    if (!await confirmAction({ title: t('comment.deleteTitle'), message: t('comment.deleteConfirm'), confirmLabel: t('comment.delete'), danger: true })) return
    try {
      await api(`/comments/${id}`, { method: 'DELETE' })
      await load()
    } catch (e) {
      showToast({ title: t('comment.deleteFailed'), message: (e as Error).message, tone: 'error' })
    }
  }

  function CommentNode({ comment }: { comment: CommentItem }) {
    return (
      <div className="flex gap-3 py-3">
        <UserAvatar user={comment.user} size="h-8 w-8 text-xs" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 text-sm">
            <span className="font-medium text-slate-900">{displayName(comment.user) || t('comment.anonymous')}</span>
            <span className="text-xs text-slate-400">{formatDate(comment.created_at)}</span>
            {user?.id === comment.user_id && (
              <button onClick={() => remove(comment.id)} className="text-xs text-rose-400 hover:text-rose-600">{t('comment.delete')}</button>
            )}
            {user?.id !== comment.user_id && <ReportButton targetType="comment" targetId={comment.id} compact />}
          </div>
          <p className="mt-1 whitespace-pre-wrap text-sm leading-6 text-slate-700">{comment.content}</p>
          {user && (
            <button onClick={() => setReplyTo(comment.id)} className="mt-1 text-xs text-slate-400 hover:text-primary-600">{t('comment.reply')}</button>
          )}
          {(comment.replies || []).length > 0 && (
            <div className="mt-2 space-y-3 border-l border-slate-100 pl-4">
              {(comment.replies || []).map((r) => (
                <div key={r.id} className="flex gap-3">
                  <UserAvatar user={r.user} size="h-7 w-7" />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2 text-sm">
                      <span className="font-medium text-slate-900">{displayName(r.user)}</span>
                      <span className="text-xs text-slate-400">{formatDate(r.created_at)}</span>
                      {user?.id === r.user_id ? (
                        <button onClick={() => remove(r.id)} className="text-xs text-rose-400 hover:text-rose-600">{t('comment.delete')}</button>
                      ) : (
                        <ReportButton targetType="comment" targetId={r.id} compact />
                      )}
                    </div>
                    <p className="mt-0.5 whitespace-pre-wrap text-sm text-slate-700">{r.content}</p>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    )
  }

  return (
    <section className="border-t border-slate-200 pt-8">
      <h2 className="text-xl font-bold text-slate-900">{t('comment.heading')}</h2>

      {commentsOff ? (
        <p className="py-4 text-sm text-slate-400">{t('comment.siteOff')}</p>
      ) : user && allowComments ? (
        <form onSubmit={(e) => submit(e, replyTo ?? undefined)} className="mt-4">
          {replyTo && (
            <p className="mb-2 text-xs text-slate-400">
              {t('comment.replyingTo', { id: replyTo })} <button type="button" onClick={() => setReplyTo(null)} className="text-primary-600 hover:underline">{t('common.actions.cancel')}</button>
            </p>
          )}
          <textarea value={content} onChange={(e) => setContent(e.target.value)} maxLength={2000}
            placeholder={t('comment.placeholder')} className="min-h-[88px] w-full rounded-lg border border-slate-200 bg-white px-3.5 py-2.5 text-sm placeholder:text-slate-400 transition-colors hover:border-slate-300 focus:border-primary-500 focus:outline-none" />
          <div className="mt-3 max-w-sm">
            <CaptchaField scene="comment" onChange={setCaptcha} refreshSignal={captchaRefresh} />
          </div>
          {error && <p className="mt-1 text-sm text-rose-500">{error}</p>}
          <div className="mt-2 flex justify-end">
            <Button type="submit" disabled={submitting || !content.trim()}>
              {submitting ? t('comment.submitting') : t('comment.submit')}
            </Button>
          </div>
        </form>
      ) : !allowComments ? (
        <p className="py-4 text-sm text-slate-400">{t('comment.chapterOff')}</p>
      ) : (
        <p className="py-4 text-sm text-slate-400">
          <Link href="/login" className="text-primary-600 hover:underline">{t('comment.loginLink')}</Link>{t('comment.loginSuffix')}
        </p>
      )}

      <div className="mt-6 divide-y divide-slate-100">
        {comments === null ? (
          <Loading className="py-8" label={t('comment.loading')} />
        ) : comments.map((c) => <CommentNode key={c.id} comment={c} />)}
        {comments !== null && comments.length === 0 && (
          <p className="py-6 text-center text-sm text-slate-400">{t('comment.empty')}</p>
        )}
      </div>
    </section>
  )
}
