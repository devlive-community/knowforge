import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/lib/server-api', () => ({ serverApi: vi.fn() }))

import { serverApi } from '@/lib/server-api'
import { getServerSideProps } from '@/pages/[key].txt'

const key = '0123456789abcdef0123456789abcdef'

describe('IndexNow root verification file', () => {
  beforeEach(() => vi.clearAllMocks())

  it('serves the matching key as plain text for the .txt route parameter', async () => {
    vi.mocked(serverApi).mockResolvedValueOnce({ key })
    const res = { statusCode: 200, end: vi.fn(), setHeader: vi.fn() }

    await getServerSideProps({ params: { key: `${key}.txt` }, res } as never)

    expect(serverApi).toHaveBeenCalledWith('/indexnow/key')
    expect(res.setHeader).toHaveBeenCalledWith('Content-Type', 'text/plain; charset=utf-8')
    expect(res.end).toHaveBeenCalledWith(key)
  })

  it('returns 404 for malformed, missing-suffix, or mismatched keys', async () => {
    const invalidRes = { statusCode: 200, end: vi.fn(), setHeader: vi.fn() }
    await getServerSideProps({ params: { key }, res: invalidRes } as never)
    expect(invalidRes.statusCode).toBe(404)
    expect(invalidRes.end).toHaveBeenCalled()
    expect(serverApi).not.toHaveBeenCalled()

    vi.mocked(serverApi).mockResolvedValueOnce({ key: 'fedcba9876543210fedcba9876543210' })
    const mismatchRes = { statusCode: 200, end: vi.fn(), setHeader: vi.fn() }
    await getServerSideProps({ params: { key: `${key}.txt` }, res: mismatchRes } as never)
    expect(mismatchRes.statusCode).toBe(404)
    expect(mismatchRes.end).toHaveBeenCalled()
  })
})
