import { useCallback, useEffect, useState } from 'react'
import Link from 'next/link'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import type { GroupSubscription, MemberGroup, MembershipPlan } from '@/lib/membership'
import type { PageResult } from '@/lib/types'
import { useUrlPage } from '@/lib/use-url-page'
import { Badge, Button, Card, DateTimePicker, EmptyState, Field, Input, Loading, Modal, Pagination, Select, useFeedback } from '@/components/ui'

interface Row { group: MemberGroup; group_found: boolean; subscription: GroupSubscription | null; buyer_id: number }

// toLocalInput ISO 时间 → DateTimePicker 的本地 'YYYY-MM-DDTHH:mm'。
function toLocalInput(value: string): string {
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

// GroupsPanel 会员管理 · 团队会员：全部团队会员（含已到期），可调整方案、席位与到期时间，或立即取消。
export default function GroupsPanel({ plans }: { plans: MembershipPlan[] }) {
  const { t } = useTranslation()
  const { showToast, confirmAction } = useFeedback()
  const [page, setPage] = useUrlPage()
  const [data, setData] = useState<PageResult<Row> | null>(null)
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState<Row | null>(null)
  const [revoking, setRevoking] = useState<string | null>(null)

  const load = useCallback(() => {
    setLoading(true)
    api<PageResult<Row>>('/admin/membership/groups', { params: { page, page_size: 20 } })
      .then(setData)
      .catch((e) => showToast({ title: t('admin.membership.loadFailed'), message: (e as Error).message, tone: 'error' }))
      .finally(() => setLoading(false))
  }, [page, showToast, t])
  useEffect(() => { load() }, [load])

  async function revoke(r: Row) {
    if (!await confirmAction({ title: t('admin.membership.group.revokeTitle'), message: t('admin.membership.group.revokeConfirm', { name: r.group.name || `#${r.group.id}` }), confirmLabel: t('admin.membership.group.revoke'), danger: true })) return
    const key = `${r.group.kind}:${r.group.id}`
    setRevoking(key)
    try {
      await api(`/admin/membership/groups/${r.group.kind}/${r.group.id}/revoke`, { method: 'POST' })
      showToast({ message: t('admin.membership.group.revoked'), tone: 'success' })
      load()
    } catch (e) {
      showToast({ message: (e as Error).message, tone: 'error' })
    } finally {
      setRevoking(null)
    }
  }

  if (!data) return <Loading className="py-16" />
  if (data.items.length === 0) return <EmptyState>{t('admin.membership.group.empty')}</EmptyState>
  return (
    <>
      <p className="mb-4 text-sm text-slate-500">{t('admin.membership.group.hint')}</p>
      <Card className="overflow-x-auto">
        <table className="w-full min-w-[760px] text-sm">
          <thead className="bg-slate-50 text-left text-xs text-slate-500">
            <tr>
              <th className="px-4 py-3">{t('admin.membership.group.col.group')}</th>
              <th className="px-4 py-3">{t('admin.membership.member.col.plan')}</th>
              <th className="px-4 py-3">{t('admin.membership.group.col.seats')}</th>
              <th className="px-4 py-3">{t('admin.membership.member.col.expires')}</th>
              <th className="px-4 py-3" />
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100">
            {data.items.map((r) => {
              const s = r.subscription
              const key = `${r.group.kind}:${r.group.id}`
              return (
                <tr key={key}>
                  <td className="px-4 py-3">
                    {r.group_found ? <Link href={r.group.link} className="font-medium text-slate-900 hover:text-primary-600">{r.group.name}</Link>
                      : <span className="text-slate-400">{t('admin.membership.group.missing', { id: r.group.id })}</span>}
                    <div className="text-xs text-slate-400">{t('admin.membership.group.members', { n: r.group.member_count })}</div>
                  </td>
                  <td className="px-4 py-3 text-slate-700">{s?.plan?.name || '-'}</td>
                  <td className="px-4 py-3 tabular-nums text-slate-600">{s ? t('admin.membership.group.seatsValue', { covered: s.covered, seats: s.seats }) : '-'}</td>
                  <td className="px-4 py-3">
                    {s && <span className="mr-2 text-slate-600">{formatDate(s.expires_at).slice(0, 16)}</span>}
                    <Badge tone={s?.active ? 'emerald' : 'slate'}>{s?.active ? t('admin.membership.group.active') : t('admin.membership.group.expired')}</Badge>
                  </td>
                  <td className="whitespace-nowrap px-4 py-3 text-right">
                    {r.group_found && <Button size="sm" variant="ghost" onClick={() => setEditing(r)}>{t('admin.membership.group.adjust')}</Button>}
                    {s?.active && <Button size="sm" variant="ghost" className="text-rose-600" loading={revoking === key} disabled={revoking !== null} onClick={() => void revoke(r)}>{t('admin.membership.group.revoke')}</Button>}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </Card>
      <Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} loading={loading} />
      {editing && <AdjustGroupModal row={editing} plans={plans} onClose={() => setEditing(null)} onDone={() => { setEditing(null); load() }} />}
    </>
  )
}

function AdjustGroupModal({ row, plans, onClose, onDone }: { row: Row; plans: MembershipPlan[]; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const s = row.subscription
  const groupPlans = plans.filter((p) => p.group_enabled || p.id === s?.plan?.id)
  const [planID, setPlanID] = useState(String(s?.plan?.id || groupPlans[0]?.id || ''))
  const [seats, setSeats] = useState(String(s?.seats || row.group.member_count || 1))
  const [expires, setExpires] = useState(toLocalInput(s?.active ? s.expires_at : new Date(Date.now() + 30 * 86400000).toISOString()))
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)

  async function submit() {
    const at = new Date(expires)
    if (!planID || Number.isNaN(at.getTime()) || !(Number(seats) >= 1)) { showToast({ message: t('admin.membership.group.adjustRequired'), tone: 'error' }); return }
    setSaving(true)
    try {
      await api(`/admin/membership/groups/${row.group.kind}/${row.group.id}`, { method: 'PUT', body: { plan_id: Number(planID), seats: Number(seats), expires_at: at.toISOString(), reason } })
      showToast({ message: t('admin.membership.group.adjusted'), tone: 'success' })
      onDone()
    } catch (e) {
      showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal open onClose={onClose} title={t('admin.membership.group.adjustTitle', { name: row.group.name })}
      footer={<><Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button><Button loading={saving} onClick={() => void submit()}>{t('common.actions.save')}</Button></>}>
      <div className="space-y-4">
        <Field label={t('admin.membership.member.col.plan')}><Select value={planID} onChange={setPlanID} options={groupPlans.map((p) => ({ value: String(p.id), label: p.name }))} /></Field>
        <Field label={t('admin.membership.group.col.seats')}><Input type="number" min={1} max={1000} value={seats} onChange={(e) => setSeats(e.target.value)} /></Field>
        <Field label={t('admin.membership.member.col.expires')}><DateTimePicker value={expires} onChange={setExpires} ariaLabel={t('admin.membership.member.col.expires')} /></Field>
        <Field label={t('admin.membership.member.reason')}><Input value={reason} maxLength={255} onChange={(e) => setReason(e.target.value)} /></Field>
      </div>
    </Modal>
  )
}
