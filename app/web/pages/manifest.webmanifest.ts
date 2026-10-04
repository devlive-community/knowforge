import type { GetServerSideProps } from 'next'
import { getSiteConfig } from '@/lib/server-api'

export default function Manifest() {
  return null
}

// Web 应用清单：站点名称与说明取自站点设置，供浏览器「安装应用 / 添加到主屏幕」。
export const getServerSideProps: GetServerSideProps = async ({ res }) => {
  const site = await getSiteConfig()
  const name = site.site_name || 'KnowForge'
  const manifest = {
    name,
    short_name: name,
    description: site.site_description || '',
    start_url: '/',
    scope: '/',
    display: 'standalone',
    background_color: '#F7F6F2',
    theme_color: '#4169E1',
    icons: [
      { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
      { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
    ],
  }
  res.setHeader('Content-Type', 'application/manifest+json; charset=utf-8')
  res.setHeader('Cache-Control', 'public, max-age=3600')
  res.write(JSON.stringify(manifest))
  res.end()
  return { props: {} }
}
