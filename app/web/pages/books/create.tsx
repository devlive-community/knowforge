import { useState } from 'react'
import { useRouter } from 'next/router'
import Seo from '@/components/Seo'
import Container from '@/components/Container'
import { api } from '@/lib/api'
import { useRequireAuth , useApp} from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import BookForm from '@/components/BookForm'
import { Loading, useFeedback } from '@/components/ui'
import type { Book } from '@/lib/types'
import BookTemplateChoice from '@/components/templates/BookTemplateChoice'
import { browserTimeZone, templatesEnabled, type TemplateSummary } from '@/lib/templates'

export default function CreateBook() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  const siteName = site.site_name || 'KnowForge'
  const router = useRouter()
  const { showToast } = useFeedback()
  const [template, setTemplate] = useState<TemplateSummary | null>(null)

  if (!user) return <Loading className="min-h-[60vh]" label={t('book.create.verifying')} />

  return (
    <>
      <Seo siteName={siteName} title={t('book.create.seoTitle')} noindex />
      <Container>
      <BookForm
        heading={t('book.create.heading')}
        subheading={t('book.create.subheading')}
        breadcrumb={t('book.create.breadcrumb')}
        submitLabel={t('book.create.submit')}
        showSaveDraft
        beforeForm={templatesEnabled(site) ? <BookTemplateChoice value={template} onChange={setTemplate} /> : undefined}
        onSubmit={async (payload) => {
          const book = await api<Book>('/books', { method: 'POST', body: payload })
          let target = `/book/writer/${encodeURIComponent(book.slug)}`
          // 选了书籍模板：书创建后按模板生成章节，并打开第一章；生成失败时书仍已创建，提示后进入写作台
          if (template) {
            try {
              const r = await api<{ created: number; first_slug: string }>(`/templates/${template.id}/apply`, { method: 'POST', body: { book_id: book.id, tz: browserTimeZone() } })
              if (r.first_slug) target += `/${encodeURIComponent(r.first_slug)}`
              showToast({ message: t('templates.create.applied', { n: r.created }), tone: 'success' })
            } catch (e) {
              showToast({ message: t('templates.create.applyFailed', { error: (e as Error).message }), tone: 'error' })
            }
          }
          router.push(target)
        }}
      />
    </Container>
  </>
  )
}
