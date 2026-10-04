import type { GetServerSideProps } from 'next'
import { authHeaderFrom, getSSRUser, isInstalled, serverApi } from '@/lib/server-api'
import WriterWorkbench from '@/components/WriterWorkbench'
import type { BookAccess, User } from '@/lib/types'

interface Props {
  user: User | null
  slug: string
  suggestOnly: boolean // 「建议者」：修改以建议提交，不能直接保存
}

// 写作工作台：`/book/writer/:slug` 与 `/book/writer/:slug/:doc` 走同一页面（可选 catch-all），
// 这样选中/取消选中章节可用浅路由（shallow），不再整页重挂载刷新。
export const getServerSideProps: GetServerSideProps<Props> = async ({ req, params, resolvedUrl }) => {
  if (!(await isInstalled())) {
    return { redirect: { destination: '/install', permanent: false } }
  }
  const user = await getSSRUser(req)
  if (!user) {
    return { redirect: { destination: `/login?next=${encodeURIComponent(resolvedUrl)}`, permanent: false } }
  }
  const slug = typeof params?.slug === 'string' ? params.slug : ''
  if (!slug) return { notFound: true }
  let suggestOnly = false
  try {
    const access = await serverApi<BookAccess>(`/books/slug/${encodeURIComponent(slug)}/access`, { headers: authHeaderFrom(req) })
    if (!access.can_edit_content && !access.can_suggest) return { notFound: true }
    suggestOnly = !access.can_edit_content
  } catch {
    return { notFound: true }
  }
  return { props: { user, slug, suggestOnly } }
}

export default function WriterPage(props: Props) {
  return <WriterWorkbench {...props} />
}
