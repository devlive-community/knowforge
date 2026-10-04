import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import TemplateManager from '@/components/templates/TemplateManager'
import { useTranslation } from '@/lib/i18n'

// 站点模板：管理员维护、所有作者可用的章节模板与书籍模板。
export default function AdminTemplatesPage() {
  const { t } = useTranslation()
  return (
    <FeatureGate feature="templates">
      <AdminLayout current="templates" breadcrumb={t('admin.nav.templates')}>
        <div className="mb-6">
          <h1 className="text-2xl font-bold text-slate-900">{t('admin.nav.templates')}</h1>
          <p className="mt-1.5 text-sm text-slate-500">{t('templates.admin.description')}</p>
        </div>
        <TemplateManager scope="official" basePath="/admin/templates" />
      </AdminLayout>
    </FeatureGate>
  )
}
