import { describe, expect, it } from 'vitest'
import { formatBytes, storagePercent } from '@/lib/files'

describe('files', () => {
  it('formatBytes', () => {
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(700 * 1024)).toBe('700.0 KB')
    expect(formatBytes(3 * 1024 * 1024)).toBe('3.0 MB')
  })
  it('storagePercent', () => {
    expect(storagePercent(512 * 1024, 1)).toBe(50)
    expect(storagePercent(5 * 1024 * 1024, 1)).toBe(100)
    expect(storagePercent(100, -1)).toBe(0)
    expect(storagePercent(1, 0)).toBe(100)
  })
})
