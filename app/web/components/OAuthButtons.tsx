import { useEffect, useState } from 'react'
import { API_BASE, api } from '@/lib/api'
import { Button, Loading } from '@/components/ui'
import { resolveMediaUrl } from '@/lib/media'
import { parseIcon } from '@/lib/icons'
import { useTranslation } from '@/lib/i18n'

// icon 为完整 FontAwesome 类名（品牌图标用 fa-brands，无品牌图标的国内平台用 fa-solid）。
export const PROVIDER_META: Record<string, { label: string; icon: string }> = {
  github: { label: 'GitHub', icon: 'fa-brands fa-github' },
  google: { label: 'Google', icon: 'fa-brands fa-google' },
  gitlab: { label: 'GitLab', icon: 'fa-brands fa-gitlab' },
  gitee: { label: 'Gitee', icon: 'fa-solid fa-code-branch' },
  gitcode: { label: 'GitCode', icon: 'fa-solid fa-code' },
}

interface Provider { provider: string; enabled: boolean; icon_type?: string; icon_value?: string }

// providerIcon 优先用管理员自定义图标（image/svg 用图片，fa 用类名），否则回退品牌默认图标。供登录页与后台复用。
export function providerIcon(p: { provider: string; icon_type?: string; icon_value?: string }, className: string) {
  const icon = parseIcon(p.icon_type, (p.icon_value || '').trim())
  const iv = icon.value
  if (iv && (p.icon_type === 'image' || p.icon_type === 'svg')) {
    return <img src={resolveMediaUrl(iv)} alt="" className={`${className} object-contain`} />
  }
  const cls = iv ? `fa-solid ${iv}` : (PROVIDER_META[p.provider]?.icon || 'fa-solid fa-right-to-bracket')
  return <i className={`${cls} ${className}`} style={iv && icon.color ? { color: icon.color } : undefined} aria-hidden="true" />
}

// OAuthButtons 第三方登录入口：拉取启用中的 provider，按显示方式（按钮/图标）渲染（登录/注册页共用）
export default function OAuthButtons({ label }: { label: string }) {
  const { t } = useTranslation()
  const [providers, setProviders] = useState<Provider[]>([])
  const [mode, setMode] = useState<'button' | 'icon'>('button')
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    api<{ providers: Provider[]; display_mode?: string }>('/auth/oauth/providers')
      .then((d) => {
        setProviders((d.providers || []).filter((p) => p.enabled))
        setMode(d.display_mode === 'icon' ? 'icon' : 'button')
      })
      .catch(() => { /* providers 拉取失败时不展示入口 */ })
      .finally(() => setLoaded(true))
  }, [])

  const go = (p: string) => { window.location.href = `${API_BASE}/api/v1/auth/oauth/${p}?origin=${encodeURIComponent(window.location.origin)}` }

  if (!loaded) return <Loading className="py-4" label={t('auth.oauth.loading')} />
  if (providers.length === 0) return null
  return (
    <>
      <div className="flex items-center gap-3 py-1 text-xs text-slate-400">
        <span className="h-px flex-1 bg-slate-200" />{t('auth.oauth.divider')}<span className="h-px flex-1 bg-slate-200" />
      </div>
      {mode === 'icon' ? (
        <div className="flex flex-wrap justify-center gap-3">
          {providers.map((p) => (
            <button key={p.provider} type="button" onClick={() => go(p.provider)}
              aria-label={t('auth.oauth.button', { provider: PROVIDER_META[p.provider]?.label || p.provider, label })}
              className="flex h-11 w-11 items-center justify-center rounded-full border border-slate-200 text-lg text-slate-600 transition-colors hover:border-primary-300 hover:text-primary-600">
              {providerIcon(p, 'h-5 w-5 text-[1.05rem]')}
            </button>
          ))}
        </div>
      ) : (
        <div className="space-y-2">
          {providers.map((p) => (
            <Button key={p.provider} variant="outline" type="button" className="w-full" onClick={() => go(p.provider)}>
              {providerIcon(p, 'h-4 w-4')}{t('auth.oauth.button', { provider: PROVIDER_META[p.provider]?.label || p.provider, label })}
            </Button>
          ))}
        </div>
      )}
    </>
  )
}

const OAUTH_ERROR_CODES = new Set([
  'not_configured', 'invalid_state', 'missing_code', 'provider_unreachable', 'token_exchange_failed',
  'profile_fetch_failed', 'account_disabled', 'register_failed', 'token_issue_failed', 'unsupported_provider',
  'registration_closed', 'registration_invite_required', 'already_bound',
])

// oauthErrorKey 把回调携带的 oauth_error 代码转成 i18n 键（未知代码回退到带占位的通用键）
export function oauthErrorKey(code: string): string {
  return OAUTH_ERROR_CODES.has(code) ? `auth.oautherror.${code}` : 'auth.oautherror.unknown'
}
