import { useState } from 'react'
import { useRouter } from 'next/router'
import { api } from '@/lib/api'
import { useApp } from '@/lib/auth'
import { useTranslation } from '@/lib/i18n'
import { displayName } from '@/lib/users'
import type { TeamDetail } from '@/lib/teams'
import { Button, Card, Field, Input, Select, Textarea, useFeedback } from '@/components/ui'

// TeamSettings 团队设置：所有者与管理员修改名称与简介；所有者可转交或解散团队；其他成员可退出团队。
export default function TeamSettings({ data, reload }: { data: TeamDetail; reload: () => Promise<void> }) {
  const { t } = useTranslation()
  const router = useRouter()
  const { user } = useApp()
  const { showToast, confirmAction } = useFeedback()
  const { team } = data
  const [name, setName] = useState(team.name)
  const [desc, setDesc] = useState(team.description)
  const [target, setTarget] = useState('')
  const [busy, setBusy] = useState<'save' | 'transfer' | 'delete' | 'leave' | null>(null)
  const isOwner = team.my_role === 'owner'
  const candidates = data.members.filter((m) => m.status === 'accepted' && m.user.id !== user?.id)

  async function run(kind: NonNullable<typeof busy>, fn: () => Promise<void>) {
    setBusy(kind)
    try {
      await fn()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setBusy(null)
    }
  }

  const save = () => run('save', async () => {
    await api(`/teams/${team.id}`, { method: 'PUT', body: { name: name.trim(), description: desc.trim() } })
    showToast({ message: t('teams.settings.saved'), tone: 'success' })
    await reload()
  })

  const transfer = async () => {
    const m = candidates.find((c) => String(c.user.id) === target)
    if (!m || !await confirmAction({ title: t('teams.settings.transferTitle'), message: t('teams.settings.transferConfirm', { name: displayName(m.user) }), confirmLabel: t('teams.settings.transfer') })) return
    await run('transfer', async () => {
      await api(`/teams/${team.id}/transfer`, { method: 'POST', body: { user_id: m.user.id } })
      showToast({ message: t('teams.settings.transferred'), tone: 'success' })
      setTarget('')
      await reload()
    })
  }

  const remove = async () => {
    if (!await confirmAction({ title: t('teams.settings.deleteTitle'), message: t('teams.settings.deleteConfirm', { name: team.name }), confirmLabel: t('teams.settings.delete'), danger: true })) return
    await run('delete', async () => {
      await api(`/teams/${team.id}`, { method: 'DELETE' })
      showToast({ message: t('teams.settings.deleted'), tone: 'success' })
      await router.push('/teams')
    })
  }

  const leave = async () => {
    if (!user || !await confirmAction({ title: t('teams.settings.leaveTitle'), message: t('teams.settings.leaveConfirm', { name: team.name }), confirmLabel: t('teams.settings.leave'), danger: true })) return
    await run('leave', async () => {
      await api(`/teams/${team.id}/members/${user.id}`, { method: 'DELETE' })
      showToast({ message: t('teams.settings.left'), tone: 'success' })
      await router.push('/teams')
    })
  }

  return (
    <div className="max-w-2xl space-y-5" data-testid="team-settings">
      {data.can_manage && (
        <Card className="space-y-4 p-6">
          <h2 className="font-semibold text-slate-900">{t('teams.settings.info')}</h2>
          <Field label={t('teams.field.name')}>
            <Input value={name} maxLength={60} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label={t('teams.field.description')}>
            <Textarea value={desc} maxLength={500} rows={3} onChange={(e) => setDesc(e.target.value)} />
          </Field>
          <div className="flex justify-end">
            <Button loading={busy === 'save'} disabled={busy !== null || !name.trim() || (name === team.name && desc === team.description)} onClick={() => void save()}>
              {t('common.actions.save')}
            </Button>
          </div>
        </Card>
      )}
      {isOwner ? (
        <Card className="space-y-5 border-rose-200 p-6">
          <div>
            <h2 className="font-semibold text-slate-900">{t('teams.settings.transferTitle')}</h2>
            <p className="mt-1 text-sm text-slate-500">{t('teams.settings.transferHint')}</p>
            <div className="mt-3 flex flex-col gap-2 sm:flex-row">
              <Select className="flex-1" value={target} onChange={setTarget} disabled={!candidates.length}
                placeholder={candidates.length ? t('teams.settings.transferPick') : t('teams.settings.transferNone')}
                options={candidates.map((m) => ({ value: String(m.user.id), label: `${displayName(m.user)} (@${m.user.username})` }))} />
              <Button variant="outline" loading={busy === 'transfer'} disabled={busy !== null || !target} onClick={() => void transfer()}>{t('teams.settings.transfer')}</Button>
            </div>
          </div>
          <div className="border-t border-slate-100 pt-5">
            <h2 className="font-semibold text-rose-600">{t('teams.settings.deleteTitle')}</h2>
            <p className="mt-1 text-sm text-slate-500">{t('teams.settings.deleteHint')}</p>
            <Button className="mt-3" variant="danger" loading={busy === 'delete'} disabled={busy !== null} onClick={() => void remove()} data-testid="team-delete">{t('teams.settings.delete')}</Button>
          </div>
        </Card>
      ) : team.my_role && (
        <Card className="p-6">
          <h2 className="font-semibold text-slate-900">{t('teams.settings.leaveTitle')}</h2>
          <p className="mt-1 text-sm text-slate-500">{t('teams.settings.leaveHint')}</p>
          <Button className="mt-3" variant="danger" loading={busy === 'leave'} disabled={busy !== null} onClick={() => void leave()}>{t('teams.settings.leave')}</Button>
        </Card>
      )}
    </div>
  )
}
