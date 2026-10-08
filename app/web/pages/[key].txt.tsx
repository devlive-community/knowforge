import type { GetServerSideProps } from 'next'
import { serverApi } from '@/lib/server-api'

export default function IndexNowKeyFile() {
  return null
}

// IndexNow requires a verification key file at the root of the site's host.
export const getServerSideProps: GetServerSideProps = async ({ params, res }) => {
  // Next's dynamic Pages Router parameter includes the literal .txt suffix.
  const routeValue = String(params?.key || '')
  const key = routeValue.endsWith('.txt') ? routeValue.slice(0, -4) : ''
  if (!/^[a-f0-9]{32}$/.test(key)) {
    res.statusCode = 404
    res.end()
    return { props: {} }
  }
  const configured = await serverApi<{ key: string }>('/indexnow/key').catch(() => null)
  if (!configured || configured.key !== key) {
    res.statusCode = 404
    res.end()
    return { props: {} }
  }
  res.setHeader('Content-Type', 'text/plain; charset=utf-8')
  res.setHeader('Cache-Control', 'public, no-cache, must-revalidate')
  res.end(key)
  return { props: {} }
}
