import { useEffect, useState } from 'react'
import AdminLayout from '@/components/AdminLayout'
import FeatureGate from '@/components/FeatureGate'
import { api } from '@/lib/api'
import { useTranslation } from '@/lib/i18n'
import { Badge, Button, EmptyState, Field, Input, Loading, Modal, Select, Switch, useFeedback } from '@/components/ui'

type RuleType = 'remove_element' | 'remove_line' | 'replace'
interface CustomRule { id: string; name: string; enabled: boolean; type: RuleType; pattern: string; replacement: string; hosts: string }
interface BuiltinRule { key: string; enabled: boolean }
interface Rules { builtin: BuiltinRule[]; custom: CustomRule[] }

const RULE_TYPES: RuleType[] = ['remove_element', 'remove_line', 'replace']
const EMPTY_RULE: CustomRule = { id: '', name: '', enabled: true, type: 'remove_line', pattern: '', replacement: '', hosts: '' }

export default function AdminCollect() {
  return <FeatureGate feature="content-collect"><Inner /></FeatureGate>
}

// 内容采集 · 采集规则：内置清理规则的开关、自定义规则（删除元素 / 删除行 / 替换文本，可限定站点），
// 以及用当前（未保存的）规则试采一个网页预览结果。规则对所有用户的单页采集、导入与整站采集生效。
function Inner() {
  const { t } = useTranslation()
  const { showToast, confirmAction } = useFeedback()
  const [rules, setRules] = useState<Rules | null>(null)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [editing, setEditing] = useState<{ index: number; rule: CustomRule } | null>(null)
  const [testURL, setTestURL] = useState('')
  const [testing, setTesting] = useState(false)
  const [preview, setPreview] = useState<{ title: string; markdown: string } | null>(null)

  useEffect(() => {
    api<Rules>('/admin/collect/rules').then((r) => { setRules(r); setDirty(false) })
      .catch((e) => showToast({ title: t('admin.collect.loadFailed'), message: (e as Error).message, tone: 'error' }))
  }, [showToast, t])

  const change = (next: Rules) => { setRules(next); setDirty(true) }

  async function save() {
    if (!rules) return
    setSaving(true)
    try {
      setRules(await api<Rules>('/admin/collect/rules', { method: 'PUT', body: rules }))
      setDirty(false)
      showToast({ message: t('admin.collect.saved'), tone: 'success' })
    } catch (e) {
      showToast({ title: t('admin.collect.saveFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setSaving(false)
    }
  }

  async function runTest() {
    if (!rules || !testURL.trim()) return
    setTesting(true)
    setPreview(null)
    try {
      setPreview(await api<{ title: string; markdown: string }>('/admin/collect/rules/preview', { method: 'POST', body: { ...rules, url: testURL.trim() } }))
    } catch (e) {
      showToast({ title: t('admin.collect.testFailed'), message: (e as Error).message, tone: 'error' })
    } finally {
      setTesting(false)
    }
  }

  function saveRule(rule: CustomRule) {
    if (!rules || !editing) return
    const custom = [...rules.custom]
    if (editing.index < 0) custom.push(rule)
    else custom[editing.index] = rule
    change({ ...rules, custom })
    setEditing(null)
  }

  async function removeRule(i: number) {
    if (!rules) return
    const ok = await confirmAction({ title: t('admin.collect.deleteTitle'), message: t('admin.collect.deleteMessage', { name: rules.custom[i].name || rules.custom[i].pattern }), confirmLabel: t('common.actions.delete'), danger: true })
    if (ok) change({ ...rules, custom: rules.custom.filter((_, j) => j !== i) })
  }

  return (
    <AdminLayout current="collect" breadcrumb={t('admin.nav.collect')}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold text-slate-900">{t('admin.collect.title')}</h1>
          <p className="mt-1 text-sm text-slate-500">{t('admin.collect.description')}</p>
        </div>
        <div className="flex items-center gap-3">
          {dirty && <span className="text-xs text-amber-600">{t('admin.collect.unsaved')}</span>}
          <Button loading={saving} disabled={!rules || !dirty} onClick={() => void save()}>{t('admin.collect.save')}</Button>
        </div>
      </div>

      {!rules ? <Loading className="py-16" /> : (
        <div className="mt-6 grid gap-6 xl:grid-cols-[1fr_26rem]">
          <div className="space-y-6">
            <section className="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
              <h2 className="font-semibold text-slate-900">{t('admin.collect.builtinTitle')}</h2>
              <p className="mt-1 text-xs text-slate-500">{t('admin.collect.builtinHint')}</p>
              <ul className="mt-4 divide-y divide-slate-100">
                {rules.builtin.map((b, i) => (
                  <li key={b.key} className="flex items-start justify-between gap-4 py-3">
                    <span className="min-w-0">
                      <span className="block text-sm font-medium text-slate-800">{t(`admin.collect.builtin.${b.key}.name`)}</span>
                      <span className="mt-0.5 block text-xs text-slate-500">{t(`admin.collect.builtin.${b.key}.hint`)}</span>
                    </span>
                    <Switch checked={b.enabled} ariaLabel={t(`admin.collect.builtin.${b.key}.name`)}
                      onChange={(v) => change({ ...rules, builtin: rules.builtin.map((x, j) => (j === i ? { ...x, enabled: v } : x)) })} />
                  </li>
                ))}
              </ul>
            </section>

            <section className="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="mr-auto font-semibold text-slate-900">{t('admin.collect.customTitle')}</h2>
                <Button size="sm" variant="outline" onClick={() => setEditing({ index: -1, rule: { ...EMPTY_RULE } })}>
                  <i className="fa-solid fa-plus" aria-hidden="true" />{t('admin.collect.addRule')}
                </Button>
              </div>
              <p className="mt-1 text-xs text-slate-500">{t('admin.collect.customHint')}</p>
              {rules.custom.length === 0 ? (
                <div className="mt-4"><EmptyState>{t('admin.collect.noCustom')}</EmptyState></div>
              ) : (
                <ul className="mt-4 space-y-2">
                  {rules.custom.map((c, i) => (
                    <li key={c.id || i} className="flex flex-wrap items-center gap-3 rounded-xl border border-slate-200 px-4 py-3" data-testid="collect-rule">
                      <Switch checked={c.enabled} ariaLabel={c.name || c.pattern}
                        onChange={(v) => change({ ...rules, custom: rules.custom.map((x, j) => (j === i ? { ...x, enabled: v } : x)) })} />
                      <span className="min-w-0 flex-1">
                        <span className="flex flex-wrap items-center gap-2">
                          <span className="text-sm font-medium text-slate-800">{c.name || t('admin.collect.unnamed')}</span>
                          <Badge tone="slate">{t(`admin.collect.type.${c.type}`)}</Badge>
                          {c.hosts && <span className="text-xs text-slate-400">{t('admin.collect.onlyHosts', { hosts: c.hosts })}</span>}
                        </span>
                        <code className="mt-1 block truncate font-mono text-xs text-slate-500">
                          {c.pattern}{c.type === 'replace' ? ` → ${c.replacement || t('admin.collect.emptyReplacement')}` : ''}
                        </code>
                      </span>
                      <Button size="sm" variant="ghost" onClick={() => setEditing({ index: i, rule: { ...c } })}>{t('common.actions.edit')}</Button>
                      <Button size="sm" variant="ghost" className="text-rose-600" onClick={() => void removeRule(i)}>{t('common.actions.delete')}</Button>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </div>

          <section className="h-fit rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
            <h2 className="font-semibold text-slate-900">{t('admin.collect.testTitle')}</h2>
            <p className="mt-1 text-xs text-slate-500">{t('admin.collect.testHint')}</p>
            <form className="mt-3 flex items-center gap-2" onSubmit={(e) => { e.preventDefault(); void runTest() }}>
              <Input className="min-w-0 flex-1" value={testURL} onChange={(e) => setTestURL(e.target.value)} placeholder="https://" />
              <Button type="submit" variant="outline" className="shrink-0 whitespace-nowrap" loading={testing} disabled={!testURL.trim()}>{t('admin.collect.test')}</Button>
            </form>
            {preview && (
              <div className="mt-3">
                <div className="truncate text-sm font-medium text-slate-800">{preview.title}</div>
                <pre className="mt-2 max-h-[32rem] overflow-auto whitespace-pre-wrap break-words rounded-lg bg-slate-50 p-3 font-mono text-xs leading-5 text-slate-700" data-testid="collect-preview">{preview.markdown}</pre>
              </div>
            )}
          </section>
        </div>
      )}

      {editing && <RuleModal initial={editing.rule} isNew={editing.index < 0} onClose={() => setEditing(null)} onSave={saveRule} />}
    </AdminLayout>
  )
}

function RuleModal({ initial, isNew, onClose, onSave }: { initial: CustomRule; isNew: boolean; onClose: () => void; onSave: (rule: CustomRule) => void }) {
  const { t } = useTranslation()
  const [rule, setRule] = useState(initial)
  const set = (patch: Partial<CustomRule>) => setRule((r) => ({ ...r, ...patch }))
  return (
    <Modal open onClose={onClose} title={t(isNew ? 'admin.collect.addRule' : 'admin.collect.editRule')}
      footer={<>
        <Button variant="outline" onClick={onClose}>{t('common.actions.cancel')}</Button>
        <Button disabled={!rule.pattern.trim()} onClick={() => onSave({ ...rule, name: rule.name.trim(), pattern: rule.pattern.trim(), hosts: rule.hosts.trim() })}>{t('admin.collect.applyRule')}</Button>
      </>}>
      <div className="space-y-4">
        <Field label={t('admin.collect.field.name')}>
          <Input value={rule.name} maxLength={60} onChange={(e) => set({ name: e.target.value })} placeholder={t('admin.collect.field.namePlaceholder')} />
        </Field>
        <Field label={t('admin.collect.field.type')}>
          <Select value={rule.type} onChange={(v) => set({ type: v as RuleType })}
            options={RULE_TYPES.map((v) => ({ value: v, label: t(`admin.collect.type.${v}`) }))} />
        </Field>
        <Field label={t('admin.collect.field.pattern')} hint={t(`admin.collect.patternHint.${rule.type}`)}>
          <Input className="font-mono" value={rule.pattern} maxLength={500} onChange={(e) => set({ pattern: e.target.value })}
            placeholder={rule.type === 'remove_element' ? 'div.feedback, a[aria-label=Edit]' : rule.type === 'remove_line' ? '^Edit this page' : '\\[\\[(.*?)\\]\\]'} />
        </Field>
        {rule.type === 'replace' && (
          <Field label={t('admin.collect.field.replacement')} hint={t('admin.collect.field.replacementHint')}>
            <Input className="font-mono" value={rule.replacement} maxLength={500} onChange={(e) => set({ replacement: e.target.value })} />
          </Field>
        )}
        <Field label={t('admin.collect.field.hosts')} hint={t('admin.collect.field.hostsHint')}>
          <Input value={rule.hosts} maxLength={500} onChange={(e) => set({ hosts: e.target.value })} placeholder="docs.example.com, example.org" />
        </Field>
      </div>
    </Modal>
  )
}
