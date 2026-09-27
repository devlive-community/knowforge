// formatBytes 文件大小的可读形式（B / KB / MB / GB）。
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
}

// storagePercent 个人存储已用百分比（不限时为 0，超出按 100）。
export function storagePercent(usedBytes: number, limitMB: number): number {
  if (limitMB < 0) return 0
  if (limitMB === 0) return usedBytes > 0 ? 100 : 0
  return Math.min(100, Math.round((usedBytes / (limitMB * 1024 * 1024)) * 100))
}
