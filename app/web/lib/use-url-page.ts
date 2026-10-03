import { useCallback, useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/router'

function parsePage(value: unknown): number {
  const raw = Array.isArray(value) ? value[0] : value
  const n = parseInt(String(raw ?? ''), 10)
  return Number.isFinite(n) && n > 1 ? n : 1
}

function initialPage(key: string, query: Record<string, unknown>): number {
  if (typeof window !== 'undefined') {
    // 客户端首次渲染时 router.query 可能尚未就绪：直接读地址栏，避免先按第 1 页加载再跳到目标页
    return parsePage(new URLSearchParams(window.location.search).get(key))
  }
  return parsePage(query[key])
}

type PageUpdate = number | ((current: number) => number)

// useChangeEffect 只在依赖变化时执行（首次渲染不执行）：如筛选条件变化时回到第 1 页，
// 避免页面刚打开就把地址栏里的页码重置掉。
// 按依赖值比较而不是「是否首次」，开发模式下 StrictMode 重复执行副作用时也不会误触发。
export function useChangeEffect(effect: () => void, deps: unknown[]) {
  const prev = useRef<unknown[] | null>(null)
  useEffect(() => {
    const last = prev.current
    prev.current = deps
    if (last && (last.length !== deps.length || deps.some((d, i) => !Object.is(d, last[i])))) effect()
  }, deps) // eslint-disable-line react-hooks/exhaustive-deps
}

// useUrlPage 列表的当前页保存在地址栏（?page=2；同一页面有多个列表时用不同的 key），刷新、分享链接、
// 前进后退都保持页码；第 1 页不写入地址。返回 [page, setPage]，setPage 支持函数式更新。
export function useUrlPage(key = 'page'): [number, (update: PageUpdate) => void] {
  const router = useRouter()
  const [page, setPageState] = useState(() => initialPage(key, router.query))
  const pageRef = useRef(page)
  pageRef.current = page

  // 前进/后退或外部改了地址栏：同步到状态
  const fromUrl = router.isReady ? parsePage(router.query[key]) : null
  useEffect(() => {
    if (fromUrl !== null && fromUrl !== pageRef.current) setPageState(fromUrl)
  }, [fromUrl])

  const setPage = useCallback((update: PageUpdate) => {
    const next = Math.max(1, typeof update === 'function' ? update(pageRef.current) : update)
    if (next === pageRef.current) return
    pageRef.current = next
    setPageState(next)
    const query = { ...router.query }
    if (next === 1) delete query[key]
    else query[key] = String(next)
    void router.push({ pathname: router.pathname, query }, undefined, { shallow: true, scroll: false })
  }, [router, key])

  return [page, setPage]
}
