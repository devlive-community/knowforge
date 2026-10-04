import Container from '@/components/Container'
import FeatureGate from '@/components/FeatureGate'
import Seo from '@/components/Seo'
import TemplateManager from '@/components/templates/TemplateManager'
import { useApp, useRequireAuth } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { Loading } from '@/components/ui'

// 我的模板：个人的章节模板与书籍模板（只有自己可见），写作台与新建书籍时可以选用。
export default function MyTemplatesPage() {
  const user = useRequireAuth()
  const { site } = useApp()
  const { t } = useTranslation()
  if (!user) return <Loading className="min-h-[60vh]" />
  return (
    <FeatureGate feature="templates">
      <Seo siteName={site.site_name || 'KnowForge'} title={t('templates.page.title')} noindex />
      <Container>
        <div className="pb-6">
          <h1 className="text-3xl font-bold text-ink">{t('templates.page.title')}</h1>
          <p className="mt-2 text-[15px] text-slate-500">{t('templates.page.description')}</p>
        </div>
        <TemplateManager scope="mine" basePath="/user/templates" />
      </Container>
    </FeatureGate>
  )
}
