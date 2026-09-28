import Head from 'next/head'

interface JsonLdSchema {
  [key: string]: unknown
}

export interface SeoProps {
  title?: string
  description?: string
  image?: string
  url?: string
  noindex?: boolean
  jsonLd?: JsonLdSchema | JsonLdSchema[]
  siteName?: string
  /** RSS 订阅地址（阅读器自动发现） */
  rss?: { title: string; href: string }
}

// Seo 公开页面的 SEO 头：标题、描述、OG/Twitter 卡片、canonical 与 JSON-LD 结构化数据
export default function Seo({ title, description, image, url, noindex, jsonLd, siteName, rss }: SeoProps) {
  const fullTitle = title && siteName ? `${title} - ${siteName}` : title || siteName
  const schemas = jsonLd ? (Array.isArray(jsonLd) ? jsonLd : [jsonLd]) : []
  // 未指定分享图时回退到站点 logo，保证社交卡片不缺图
  const ogImage = image || (url ? `${url}/logo.png` : undefined)
  return (
    <Head>
      {fullTitle && <title key="title">{fullTitle}</title>}
      {description && <meta key="description" name="description" content={description} />}
      {noindex && <meta key="robots" name="robots" content="noindex, nofollow" />}
      {url && <link key="canonical" rel="canonical" href={url} />}
      {/* eslint-disable-next-line no-restricted-syntax -- <link title> 是订阅源名称（供阅读器自动发现），不是悬停提示 */}
      {rss && <link key="rss" rel="alternate" type="application/rss+xml" title={rss.title} href={rss.href} />}
      {description && <meta key="og-description" property="og:description" content={description} />}
      {url && <meta key="og-url" property="og:url" content={url} />}
      {ogImage && <meta key="og-image" property="og:image" content={ogImage} />}
      {fullTitle && <meta key="og-title" property="og:title" content={fullTitle} />}
      {description && <meta key="tw-description" name="twitter:description" content={description} />}
      {fullTitle && <meta key="tw-title" name="twitter:title" content={fullTitle} />}
      {ogImage && <meta key="tw-image" name="twitter:image" content={ogImage} />}
      {schemas.map((schema, i) => (
        <script key={`jsonld-${i}`} type="application/ld+json"
          dangerouslySetInnerHTML={{ __html: JSON.stringify(schema) }} />
      ))}
    </Head>
  )
}
