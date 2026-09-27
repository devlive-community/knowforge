import { useCallback, useEffect, useState } from 'react'
import { api, formatDate } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { dateStamp, downloadAuthed } from '@/lib/download'
import { Badge, Button, Card, DateTimePicker, EmptyState, Field, Input, Loading, Modal, Pagination, SegmentedTabs, Select, Switch, useFeedback } from '@/components/ui'
import type { MembershipPlan } from '@/lib/membership'

interface Batch {
  id: number
  name: string
  plan_id: number
  plan_name: string
  days: number
  kind: 'cards' | 'promo'
  max_uses: number
  expires_at: string | null
  status: 'active' | 'disabled'
  codes: number
  redeemed: number
  promo_code?: string
  created_at: string
}
interface CodeRow { id: number; code: string; used_count: number; max_uses: number; disabled: boolean }

// RedeemPanel 会员兑换码：按批次生成一次性卡密或活动码，查看兑换情况、导出、停用。
export default function RedeemPanel({ plans }: { plans: { plan: MembershipPlan }[] }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [page, setPage] = useState(1)
  const [data, setData] = useState<{ items: Batch[]; total: number; page: number; page_size: number } | null>(null)
  const [creating, setCreating] = useState(false)
  const [viewing, setViewing] = useState<Batch | null>(null)
  const [busy, setBusy] = useState<number | null>(null)

  const load = useCallback(() => {
    api<{ items: Batch[]; total: number; page: number; page_size: number }>('/admin/membership/redeem/batches', { params: { page } })
      .then(setData)
      .catch((e) => showToast({ title: t('admin.membership.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [page, showToast, t])
  useEffect(() => { load() }, [load])

  async function toggle(b: Batch) {
    setBusy(b.id)
    try {
      await api(`/admin/membership/redeem/batches/${b.id}`, { method: 'PUT', body: { status: b.status === 'active' ? 'disabled' : 'active' } })
      load()
    } catch (e) { showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setBusy(null) }
  }

  async function copy(text: string) {
    try { await navigator.clipboard.writeText(text); showToast({ message: t('admin.membership.redeem.copied'), tone: 'success' }) }
    catch { showToast({ message: t('admin.membership.redeem.copyFailed'), tone: 'error' }) }
  }

  const activePlans = plans.filter(({ plan }) => plan.status === 'active')
  return (
    <>
      <div className="mb-4 flex items-center gap-3">
        <p className="flex-1 text-sm text-slate-500">{t('admin.membership.redeem.hint')}</p>
        <Button disabled={activePlans.length === 0} onClick={() => setCreating(true)}><i className="fa-solid fa-plus" aria-hidden="true" />{t('admin.membership.redeem.create')}</Button>
      </div>
      {data === null ? <Loading className="py-16" /> : data.items.length === 0 ? <EmptyState>{t('admin.membership.redeem.empty')}</EmptyState> : (
        <div className="space-y-3">
          {data.items.map((b) => {
            const expired = !!b.expires_at && new Date(b.expires_at) < new Date()
            return (
              <Card key={b.id} className="flex flex-wrap items-center gap-3 p-4 text-sm">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium text-slate-900">{b.name}</span>
                    <Badge tone={b.kind === 'promo' ? 'violet' : 'primary'}>{t(`admin.membership.redeem.kind.${b.kind}`)}</Badge>
                    {b.status === 'disabled' ? <Badge tone="slate">{t('admin.membership.redeem.disabled')}</Badge> : expired && <Badge tone="amber">{t('admin.membership.redeem.expired')}</Badge>}
                  </div>
                  <p className="mt-1 text-xs text-slate-500">
                    {t('admin.membership.redeem.summary', { plan: b.plan_name, days: b.days })}
                    {' · '}
                    {b.kind === 'cards' ? t('admin.membership.redeem.cardsUsage', { redeemed: b.redeemed, codes: b.codes }) : t('admin.membership.redeem.promoUsage', { redeemed: b.redeemed, max: b.max_uses })}
                    {b.expires_at && ` · ${t('admin.membership.redeem.until', { date: formatDate(b.expires_at) })}`}
                  </p>
                </div>
                {b.promo_code && (
                  <Button size="sm" variant="outline" onClick={() => void copy(b.promo_code!)}><span className="font-mono">{b.promo_code}</span><i className="fa-regular fa-copy" aria-hidden="true" /></Button>
                )}
                {b.kind === 'cards' && <Button size="sm" variant="outline" onClick={() => setViewing(b)}>{t('admin.membership.redeem.viewCodes')}</Button>}
                <Button size="sm" variant="ghost" loading={busy === b.id} onClick={() => void toggle(b)}>
                  {b.status === 'active' ? t('admin.membership.redeem.disable') : t('admin.membership.redeem.enable')}
                </Button>
              </Card>
            )
          })}
        </div>
      )}
      {data && data.total > data.page_size && <div className="mt-4"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
      {creating && <CreateBatchModal plans={activePlans} onClose={() => setCreating(false)} onCreated={() => { setCreating(false); setPage(1); load() }} />}
      {viewing && <CodesModal batch={viewing} onClose={() => setViewing(null)} />}
    </>
  )
}

function CreateBatchModal({ plans, onClose, onCreated }: { plans: { plan: MembershipPlan }[]; onClose: () => void; onCreated: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [kind, setKind] = useState<'cards' | 'promo'>('cards')
  const [name, setName] = useState('')
  const [planID, setPlanID] = useState(String(plans[0]?.plan.id || ''))
  const [days, setDays] = useState('30')
  const [count, setCount] = useState('10')
  const [code, setCode] = useState('')
  const [maxUses, setMaxUses] = useState('100')
  const [expires, setExpires] = useState('')
  const [saving, setSaving] = useState(false)

  async function submit() {
    setSaving(true)
    try {
      await api('/admin/membership/redeem/batches', {
        method: 'POST',
        body: {
          name: name.trim(), plan_id: Number(planID), days: Number(days), kind,
          count: kind === 'cards' ? Number(count) : undefined,
          code: kind === 'promo' ? code.trim() : undefined,
          max_uses: kind === 'promo' ? Number(maxUses) : undefined,
          expires_at: expires ? new Date(expires).toISOString() : undefined,
        },
      })
      showToast({ message: t('admin.membership.redeem.created'), tone: 'success' })
      onCreated()
    } catch (e) { showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setSaving(false) }
  }

  return (
    <Modal open onClose={onClose} title={t('admin.membership.redeem.create')}
      footer={<><Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button><Button loading={saving} disabled={!name.trim() || !planID} onClick={() => void submit()}>{t('admin.membership.redeem.generate')}</Button></>}>
      <div className="space-y-4">
        <SegmentedTabs fullWidth size="sm" value={kind} ariaLabel={t('admin.membership.redeem.kindLabel')} onChange={(v) => setKind(v as 'cards' | 'promo')}
          items={[{ value: 'cards', label: t('admin.membership.redeem.kind.cards') }, { value: 'promo', label: t('admin.membership.redeem.kind.promo') }]} />
        <p className="text-xs text-slate-500">{t(`admin.membership.redeem.kindHint.${kind}`)}</p>
        <Field label={t('admin.membership.redeem.name')}><Input value={name} maxLength={120} placeholder={t('admin.membership.redeem.namePlaceholder')} onChange={(e) => setName(e.target.value)} /></Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('admin.membership.member.col.plan')}><Select value={planID} onChange={setPlanID} options={plans.map(({ plan: p }) => ({ value: String(p.id), label: p.name }))} /></Field>
          <Field label={t('admin.membership.redeem.days')}><Input type="number" min={1} max={3650} value={days} onChange={(e) => setDays(e.target.value)} /></Field>
        </div>
        {kind === 'cards' ? (
          <Field label={t('admin.membership.redeem.count')} hint={t('admin.membership.redeem.countHint')}><Input type="number" min={1} max={10000} value={count} onChange={(e) => setCount(e.target.value)} /></Field>
        ) : (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t('admin.membership.redeem.code')} hint={t('admin.membership.redeem.codeHint')}><Input value={code} maxLength={40} placeholder="WELCOME2026" onChange={(e) => setCode(e.target.value)} /></Field>
            <Field label={t('admin.membership.redeem.maxUses')}><Input type="number" min={1} value={maxUses} onChange={(e) => setMaxUses(e.target.value)} /></Field>
          </div>
        )}
        <Field label={t('admin.membership.redeem.expiresAt')} hint={t('admin.membership.redeem.expiresHint')}>
          <DateTimePicker value={expires} onChange={setExpires} ariaLabel={t('admin.membership.redeem.expiresAt')} />
        </Field>
      </div>
    </Modal>
  )
}

function CodesModal({ batch, onClose }: { batch: Batch; onClose: () => void }) {
  const { t } = useTranslation()
  const { showToast } = useFeedback()
  const [page, setPage] = useState(1)
  const [data, setData] = useState<{ items: CodeRow[]; total: number; page: number; page_size: number } | null>(null)
  const [busy, setBusy] = useState<number | 'export' | null>(null)

  const load = useCallback(() => {
    api<{ items: CodeRow[]; total: number; page: number; page_size: number }>(`/admin/membership/redeem/batches/${batch.id}/codes`, { params: { page, page_size: 20 } })
      .then(setData)
      .catch((e) => showToast({ title: t('admin.membership.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [batch.id, page, showToast, t])
  useEffect(() => { load() }, [load])

  async function toggle(row: CodeRow) {
    setBusy(row.id)
    try {
      const next = await api<CodeRow>(`/admin/membership/redeem/codes/${row.id}`, { method: 'PUT', body: { disabled: !row.disabled } })
      setData((d) => (d ? { ...d, items: d.items.map((it) => (it.id === row.id ? next : it)) } : d))
    } catch (e) { showToast({ title: t('admin.membership.saveFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setBusy(null) }
  }

  async function exportCSV() {
    setBusy('export')
    try { await downloadAuthed(`/admin/membership/redeem/batches/${batch.id}/codes`, { format: 'csv' }, `redeem-${batch.id}-${dateStamp()}.csv`) }
    catch (e) { showToast({ title: t('admin.membership.redeem.exportFailed'), message: (e as Error).message, tone: 'error' }) }
    finally { setBusy(null) }
  }

  return (
    <Modal open onClose={onClose} title={t('admin.membership.redeem.codesTitle', { name: batch.name })}
      footer={<><Button variant="outline" loading={busy === 'export'} onClick={() => void exportCSV()}><i className="fa-solid fa-file-csv" aria-hidden="true" />{t('admin.membership.redeem.export')}</Button><Button onClick={onClose}>{t('common.actions.close')}</Button></>}>
      {data === null ? <Loading className="py-8" /> : (
        <>
          <ul className="divide-y divide-slate-100 rounded-lg border border-slate-200">
            {data.items.map((row) => (
              <li key={row.id} className="flex items-center gap-3 px-3 py-2 text-sm">
                <span className={`font-mono ${row.disabled ? 'text-slate-400 line-through' : 'text-slate-800'}`}>{row.code}</span>
                <Badge tone={row.used_count >= row.max_uses ? 'emerald' : 'slate'}>{row.used_count >= row.max_uses ? t('admin.membership.redeem.used') : t('admin.membership.redeem.unused')}</Badge>
                <span className="ml-auto flex items-center gap-2 text-xs text-slate-500">
                  {t('admin.membership.redeem.enabled')}
                  <Switch checked={!row.disabled} disabled={busy === row.id} onChange={() => void toggle(row)} ariaLabel={t('admin.membership.redeem.enabled')} />
                </span>
              </li>
            ))}
          </ul>
          {data.total > data.page_size && <div className="mt-3"><Pagination page={data.page} pageSize={data.page_size} total={data.total} onChange={setPage} /></div>}
        </>
      )}
    </Modal>
  )
}
