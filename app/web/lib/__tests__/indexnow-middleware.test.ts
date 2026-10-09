import { afterEach, describe, expect, it, vi } from 'vitest'
import { NextRequest } from 'next/server'
import { middleware } from '@/middleware'

const key = 'f581230d8e8ef0c5b36c5eb6c557c4dc'

describe('IndexNow verification middleware fallback', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('serves the matching configured key as plain text before Next page routing', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ success: true, data: { key } }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)

    const response = await middleware(new NextRequest(`https://knowforge.example/${key}.txt`))

    expect(fetchMock).toHaveBeenCalledWith('http://127.0.0.1:6969/api/v1/indexnow/key', { cache: 'no-store' })
    expect(response.status).toBe(200)
    expect(response.headers.get('content-type')).toBe('text/plain; charset=utf-8')
    expect(response.headers.get('cache-control')).toBe('public, no-cache, must-revalidate')
    await expect(response.text()).resolves.toBe(key)
  })

  it('returns 404 when the key does not match the API response or the API is unavailable', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ success: true, data: { key: '0123456789abcdef0123456789abcdef' } }), { status: 200 }))
      .mockRejectedValueOnce(new Error('API unavailable'))
    vi.stubGlobal('fetch', fetchMock)

    const mismatch = await middleware(new NextRequest(`https://knowforge.example/${key}.txt`))
    expect(mismatch.status).toBe(404)
    await expect(mismatch.text()).resolves.toBe('')

    const unavailable = await middleware(new NextRequest(`https://knowforge.example/${key}.txt`))
    expect(unavailable.status).toBe(404)
  })

  it('does not intercept other text files', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ success: true, data: { installed: true } }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    const response = await middleware(new NextRequest('https://knowforge.example/robots.txt'))
    expect(response.headers.get('x-middleware-next')).toBe('1')
    expect(fetchMock).toHaveBeenCalledWith('http://127.0.0.1:6969/api/v1/setup/status', { cache: 'no-store' })
  })
})
