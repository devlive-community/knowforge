import { useEffect, useState } from 'react'
import { api } from '@/lib/api'
import SettingsLayout from '@/components/SettingsLayout'
import { Button, Field, Textarea } from '@/components/ui'
import { useTranslation } from '@/lib/i18n'

// 系统设置 · 自定义 HTML：每个页面 <head> 中追加的内容（统计脚本、站点验证 meta、额外样式等）与 </body> 前追加的内容（客服、统计等脚本）。
// 保存后对新打开或刷新的页面生效（服务端渲染时注入）。
export default function SettingsCustomHtml() {
  const { t } = useTranslation()
  const [head, setHead] = useState('')
  const [footer, setFooter] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState<{ tone: 'ok' | 'error'; text: string } | null>(null)

  // 从 /site 读取当前值；加载完成前禁用保存，避免用空值覆盖
  useEffect(() => {
    api<Record<string, unknown>>('/site')
      .then((cfg) => {
        setHead(typeof cfg.custom_head_html === 'string' ? cfg.custom_head_html : '')
        setFooter(typeof cfg.custom_footer_html === 'string' ? cfg.custom_footer_html : '')
        setLoaded(true)
      })
      .catch(() => setLoaded(true))
  }, [])

  async function save() {
    setSaving(true)
    setMessage(null)
    try {
      await api('/site', { method: 'PUT', body: { custom_head_html: head, custom_footer_html: footer } })
      setMessage({ tone: 'ok', text: t('admin.settings.customHtml.saved') })
    } catch (e) {
      setMessage({ tone: 'error', text: (e as Error).message })
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingsLayout active="custom-html" description={t('admin.settings.customHtml.description')}>
      <div className="max-w-3xl space-y-5 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
        <Field label={t('admin.settings.customHtml.head')} hint={t('admin.settings.customHtml.headHint')}>
          <Textarea rows={8} spellCheck={false} className="font-mono text-xs" value={head} onChange={(e) => setHead(e.target.value)}
            placeholder={'<meta name="google-site-verification" content="..." />\n<script async src="https://..."></script>'} />
        </Field>
        <Field label={t('admin.settings.customHtml.footer')} hint={t('admin.settings.customHtml.footerHint')}>
          <Textarea rows={8} spellCheck={false} className="font-mono text-xs" value={footer} onChange={(e) => setFooter(e.target.value)}
            placeholder={'<script>\n  // ...\n</script>'} />
        </Field>
        <p className="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700">{t('admin.settings.customHtml.warning')}</p>
        {message && <div className={`rounded-lg px-4 py-3 text-sm ${message.tone === 'ok' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-600'}`}>{message.text}</div>}
        <div className="flex justify-end">
          <Button loading={saving} disabled={!loaded} onClick={save}>{t('admin.settings.customHtml.save')}</Button>
        </div>
      </div>
    </SettingsLayout>
  )
}
