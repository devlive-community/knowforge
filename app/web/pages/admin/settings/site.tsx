import { useEffect, useState } from 'react'
import { api, API_BASE, getToken } from '@/lib/api'
import type { MailConfig } from '@/lib/admin'
import { resolveMediaUrl } from '@/lib/media'
import { useApp } from '@/lib/auth'
import SettingsLayout from '@/components/SettingsLayout'
import ChapterLinkPicker from '@/components/ChapterLinkPicker'
import { Button, Input, Textarea, Field, Switch, Select } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'

// 系统设置 · 站点设置：站点名称、描述、Logo 与全站公告（仅管理员；页脚链接见独立的「页脚链接」Tab）
export default function SettingsSite() {
  const { site } = useApp()
  const { t } = useTranslation()
  const [siteName, setSiteName] = useState(site.site_name || '')
  const [siteDesc, setSiteDesc] = useState(site.site_description || '')
  const [siteLogo, setSiteLogo] = useState(site.site_logo || '')
  const [siteFavicon, setSiteFavicon] = useState(site.site_favicon || '')
  const [siteKeywords, setSiteKeywords] = useState(site.site_keywords || '')
  const [siteFooterText, setSiteFooterText] = useState(site.site_footer_text || '')
  const [siteBeian, setSiteBeian] = useState(site.site_beian || '')
  const [helpDocUrl, setHelpDocUrl] = useState(site.help_doc_url || '')
  const [termsUrl, setTermsUrl] = useState(site.terms_url || '')
  const [privacyUrl, setPrivacyUrl] = useState(site.privacy_url || '')
  const [siteUrl, setSiteUrl] = useState('')
  const [uploading, setUploading] = useState(false)
  const [uploadingFavicon, setUploadingFavicon] = useState(false)
  const [annEnabled, setAnnEnabled] = useState(site.announcement_enabled === 'true')
  const [annText, setAnnText] = useState(site.announcement_text || '')
  const [annTone, setAnnTone] = useState(site.announcement_tone === 'warning' ? 'warning' : 'info')
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const [loaded, setLoaded] = useState(false)

  // 直接刷新 /admin/settings/site 时 pageProps 不含 site，context.site 为空，
  // 若以空值初始化表单再保存，会把服务端已存在的配置整体覆盖丢失。
  // 因此挂载后始终从 /site 拉取权威配置并回填，且未加载完成前禁用保存，杜绝「覆盖清空」。
  useEffect(() => {
    api<MailConfig>('/mail')
      .then((m) => setSiteUrl(m.site_url || ''))
      .catch(() => {})
    api<Record<string, unknown>>('/site')
      .then((cfg) => {
        const str = (k: string) => (typeof cfg[k] === 'string' ? (cfg[k] as string) : '')
        setSiteName(str('site_name'))
        setSiteDesc(str('site_description'))
        setSiteLogo(str('site_logo'))
        setSiteFavicon(str('site_favicon'))
        setSiteKeywords(str('site_keywords'))
        setSiteFooterText(str('site_footer_text'))
        setSiteBeian(str('site_beian'))
        setHelpDocUrl(str('help_doc_url'))
        setTermsUrl(str('terms_url'))
        setPrivacyUrl(str('privacy_url'))
        setAnnEnabled(str('announcement_enabled') === 'true')
        setAnnText(str('announcement_text'))
        setAnnTone(str('announcement_tone') === 'warning' ? 'warning' : 'info')
        setLoaded(true)
      })
      .catch(() => setLoaded(true))
  }, [])

  async function saveSiteUrl() {
    try {
      await api('/mail', { method: 'PUT', body: { site_url: siteUrl } })
    } catch (e) {
      setMessage((e as Error).message)
    }
  }

  async function uploadLogo(file: File | undefined) {
    if (!file) return
    setUploading(true)
    setMessage('')
    try {
      const fd = new FormData()
      fd.append('file', file)
      const res = await fetch(`${API_BASE}/api/v1/upload`, { method: 'POST', headers: { Authorization: `Bearer ${getToken()}` }, body: fd })
      const payload = await res.json().catch(() => ({}))
      if (!res.ok || payload.success === false) throw new Error(payload.message || t('admin.settings.uploadFailed'))
      setSiteLogo(payload.data.url)
    } catch (e) {
      setMessage((e as Error).message)
    } finally {
      setUploading(false)
    }
  }

  async function uploadFavicon(file: File | undefined) {
    if (!file) return
    setUploadingFavicon(true)
    setMessage('')
    try {
      const fd = new FormData()
      fd.append('file', file)
      const res = await fetch(`${API_BASE}/api/v1/upload`, { method: 'POST', headers: { Authorization: `Bearer ${getToken()}` }, body: fd })
      const payload = await res.json().catch(() => ({}))
      if (!res.ok || payload.success === false) throw new Error(payload.message || t('admin.settings.uploadFailed'))
      setSiteFavicon(payload.data.url)
    } catch (e) {
      setMessage((e as Error).message)
    } finally {
      setUploadingFavicon(false)
    }
  }

  async function save() {
    setSaving(true)
    setMessage('')
    try {
      await saveSiteUrl()
      await api('/site', { method: 'PUT', body: {
        site_name: siteName, site_description: siteDesc, site_logo: siteLogo,
        site_favicon: siteFavicon, site_keywords: siteKeywords, site_footer_text: siteFooterText, site_beian: siteBeian,
        help_doc_url: helpDocUrl, terms_url: termsUrl, privacy_url: privacyUrl,
        announcement_enabled: annEnabled, announcement_text: annText, announcement_tone: annTone,
      } })
      setMessage(t('admin.settings.site.saved'))
    } catch (e) {
      setMessage((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingsLayout active="site" description={t('admin.settings.site.description')}>
      <div className="max-w-2xl rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
        <div className="space-y-4">
          <Field label={t('admin.settings.site.siteName')}>
            <Input value={siteName} onChange={(e) => setSiteName(e.target.value)} placeholder="KnowForge" />
          </Field>
          <Field label={t('admin.settings.mail.siteUrl')} hint={t('admin.settings.mail.siteUrlHint')}>
            <Input value={siteUrl} onChange={(e) => setSiteUrl(e.target.value)} placeholder="https://kb.example.com" />
          </Field>
          <Field label={t('admin.settings.site.siteDescription')} hint={t('admin.settings.site.siteDescriptionHint')}>
            <Textarea rows={3} value={siteDesc} onChange={(e) => setSiteDesc(e.target.value)}
              placeholder={t('admin.settings.site.siteDescriptionPlaceholder')} />
          </Field>
          <Field label={t('admin.settings.site.siteLogo')} hint={t('admin.settings.site.siteLogoHint')}>
            <div className="flex items-center gap-3">
              <span className="flex h-12 w-12 shrink-0 items-center justify-center overflow-hidden rounded-lg border border-slate-200 bg-slate-50">
                <img src={siteLogo ? resolveMediaUrl(siteLogo) : '/logo.png'} alt={t('admin.settings.site.siteLogo')} className="h-full w-full object-contain" />
              </span>
              <label className="cursor-pointer rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-600 transition-colors hover:bg-slate-50">
                {uploading ? t('admin.settings.uploading') : t('admin.settings.site.uploadLogo')}
                <input type="file" accept="image/*" hidden disabled={uploading}
                  onChange={(e) => { void uploadLogo(e.target.files?.[0]); e.target.value = '' }} />
              </label>
              {siteLogo && <Button type="button" variant="ghost" className="text-slate-500" onClick={() => setSiteLogo('')}>{t('admin.settings.site.restoreDefault')}</Button>}
            </div>
          </Field>
          <Field label={t('admin.settings.site.favicon')} hint={t('admin.settings.site.faviconHint')}>
            <div className="flex items-center gap-3">
              <span className="flex h-12 w-12 shrink-0 items-center justify-center overflow-hidden rounded-lg border border-slate-200 bg-slate-50">
                <img src={siteFavicon ? resolveMediaUrl(siteFavicon) : '/favicon.png'} alt={t('admin.settings.site.favicon')} className="h-8 w-8 object-contain" />
              </span>
              <label className="cursor-pointer rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-600 transition-colors hover:bg-slate-50">
                {uploadingFavicon ? t('admin.settings.uploading') : t('admin.settings.site.uploadFavicon')}
                <input type="file" accept="image/png,image/x-icon,image/vnd.microsoft.icon,image/svg+xml" hidden disabled={uploadingFavicon}
                  onChange={(e) => { void uploadFavicon(e.target.files?.[0]); e.target.value = '' }} />
              </label>
              {siteFavicon && <Button type="button" variant="ghost" className="text-slate-500" onClick={() => setSiteFavicon('')}>{t('admin.settings.site.restoreDefault')}</Button>}
            </div>
          </Field>
          <Field label={t('admin.settings.site.keywords')} hint={t('admin.settings.site.keywordsHint')}>
            <Input value={siteKeywords} onChange={(e) => setSiteKeywords(e.target.value)} placeholder={t('admin.settings.site.keywordsPlaceholder')} />
          </Field>
          <Field label={t('admin.settings.site.footerText')} hint={t('admin.settings.site.footerTextHint')}>
            <Textarea rows={2} value={siteFooterText} onChange={(e) => setSiteFooterText(e.target.value)}
              placeholder={t('admin.settings.site.footerTextPlaceholder')} />
          </Field>
          <Field label={t('admin.settings.site.beian')} hint={t('admin.settings.site.beianHint')}>
            <Input value={siteBeian} onChange={(e) => setSiteBeian(e.target.value)} placeholder={t('admin.settings.site.beianPlaceholder')} />
          </Field>

          <div className="border-t border-slate-100 pt-4">
            <h3 className="mb-1 text-sm font-semibold text-slate-700">{t('admin.settings.site.legalDocs')}</h3>
            <p className="mb-3 text-xs text-slate-400">{t('admin.settings.site.legalDocsHint')}</p>
            <div className="space-y-4">
              <Field label={t('admin.settings.site.helpDoc')} hint={t('admin.settings.site.helpDocHint')}>
                <ChapterLinkPicker value={helpDocUrl} onChange={setHelpDocUrl} loading={!loaded} />
              </Field>
              <Field label={t('admin.settings.site.terms')} hint={t('admin.settings.site.termsHint')}>
                <ChapterLinkPicker value={termsUrl} onChange={setTermsUrl} loading={!loaded} />
              </Field>
              <Field label={t('admin.settings.site.privacy')} hint={t('admin.settings.site.privacyHint')}>
                <ChapterLinkPicker value={privacyUrl} onChange={setPrivacyUrl} loading={!loaded} />
              </Field>
            </div>
          </div>

          <div className="border-t border-slate-100 pt-4">
            <h3 className="mb-3 text-sm font-semibold text-slate-700">{t('admin.settings.site.announcement')}</h3>
            <div className="space-y-4">
              <Field label={t('admin.settings.site.announcementBanner')} hint={t('admin.settings.site.announcementBannerHint')}>
                <Switch ariaLabel={t('admin.settings.site.announcementBanner')} checked={annEnabled} onChange={setAnnEnabled} />
              </Field>
              <Field label={t('admin.settings.site.announcementContent')}>
                <Textarea rows={2} value={annText} onChange={(e) => setAnnText(e.target.value)} placeholder={t('admin.settings.site.announcementPlaceholder')} />
              </Field>
              <Field label={t('admin.settings.site.style')}>
                <Select options={[{ value: 'info', label: t('admin.settings.site.styleInfo') }, { value: 'warning', label: t('admin.settings.site.styleWarning') }]}
                  value={annTone} onChange={setAnnTone} />
              </Field>
            </div>
          </div>
        </div>
        {message && <div className="mt-4 rounded-lg bg-slate-100 px-4 py-3 text-sm text-slate-600">{message}</div>}
        <div className="mt-5 flex justify-end">
          <Button loading={saving} disabled={!loaded} onClick={save}>{t('admin.settings.site.save')}</Button>
        </div>
      </div>
    </SettingsLayout>
  )
}
