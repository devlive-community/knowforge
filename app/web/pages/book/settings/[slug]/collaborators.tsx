import type { InferGetServerSidePropsType } from 'next'
import CollaboratorManager from '@/components/CollaboratorManager'
import BookTeamCard from '@/components/teams/BookTeamCard'
import BookSettingsLayout from '@/components/BookSettingsLayout'
import { getBookSettingsProps } from '@/lib/book-settings'

export const getServerSideProps = getBookSettingsProps

// 书籍设置 · 协作者：书籍所属团队（团队空间插件）与协作成员（仅可管理者）
export default function BookSettingsCollaborators({ book }: InferGetServerSidePropsType<typeof getBookSettingsProps>) {
  return (
    <BookSettingsLayout book={book} active="collaborators">
      <BookTeamCard book={book} />
      <CollaboratorManager book={book} />
    </BookSettingsLayout>
  )
}
