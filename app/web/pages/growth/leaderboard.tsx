import type { GetServerSideProps } from 'next'

// 旧地址：经验排行榜已并入排行榜插件（/leaderboards?board=xp），保留跳转以兼容已分享的链接。
export const getServerSideProps: GetServerSideProps = async ({ query }) => {
  const q = new URLSearchParams({ board: 'xp' })
  if (typeof query.period === 'string' && query.period) q.set('period', query.period)
  return { redirect: { destination: `/leaderboards?${q.toString()}`, permanent: true } }
}

export default function LegacyLeaderboard() {
  return null
}
