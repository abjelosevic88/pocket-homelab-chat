// The sheets a conversation opens: make something from a message in your
// other apps, share something from them, start a poll or a to-do list.
import React, { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { APP_INFO, appName, MAKE } from '../lib/chat'
import type { Conversation, MakeTarget, Made, Message, ShareApp, ShareItem } from '../lib/types'
import { Field, Loading, Modal, Switch, useDebounce, useToast } from './ui'
import { Select } from './Select'
import { Icon } from './icons'

type Options = Record<MakeTarget, { connected: boolean; detail: string; app: string; label: string }>
interface SplitGroup { id: number; name: string; currency: string; my_member_id: number; members: { id: number; name: string }[] }

const DURATIONS = [15, 30, 45, 60, 90, 120, 180, 240].map(n => ({ value: String(n), label: n < 60 ? `${n} min` : `${n / 60} h` }))
const PLACE_KINDS = ['restaurant', 'cafe', 'bar', 'bakery', 'street_food', 'dessert', 'market', 'shop', 'museum', 'sight', 'park', 'viewpoint', 'beach', 'nature', 'activity', 'hotel', 'other'].map(k => ({ value: k, label: k.replace('_', ' ').replace(/^./, c => c.toUpperCase()) }))

export function MakeSheet({ m, target, prefill, onClose, onMade }: { m: Message; target: MakeTarget; prefill?: Record<string, unknown>; onClose: () => void; onMade: (d: Made) => void }) {
  const { toast } = useToast()
  const { user } = useApp()
  const info = MAKE[target]
  const [opts, setOpts] = useState<Options | null>(null)
  const [f, setF] = useState<Record<string, string>>({})
  const [groups, setGroups] = useState<SplitGroup[] | null>(null)
  const [sharedWith, setSharedWith] = useState<number[]>([])
  const [busy, setBusy] = useState(false)
  const [aiBusy, setAiBusy] = useState(false)
  const set = (k: string, v: string) => setF(x => ({ ...x, [k]: v }))
  const merge = (o: Record<string, unknown>) => setF(x => {
    const n = { ...x }
    for (const [k, v] of Object.entries(o)) if (v !== undefined && v !== null && v !== '') n[k] = String(v)
    return n
  })

  useEffect(() => {
    api.get<Options>(`${V1}/make/options`).then(setOpts).catch(() => setOpts({} as Options))
    api.post<Record<string, unknown>>(`${V1}/messages/${m.id}/fill`, { target }).then(r => { merge(r); if (prefill) merge(prefill) }).catch(() => prefill && merge(prefill))
    if (target === 'note') set('notes', m.body || m.data.text || '')
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (target !== 'expense') return
    api.get<SplitGroup[]>(`${V1}/make/split-groups`).then(g => {
      setGroups(g)
      if (g.length) { set('group', String(g[0].id)); set('paid_by', String(g[0].my_member_id || '')); setSharedWith(g[0].members.map(x => x.id)); if (!f.currency) set('currency', g[0].currency) }
    }).catch(e => { setGroups([]); toast((e as Error).message, 'err') })
  }, [target]) // eslint-disable-line react-hooks/exhaustive-deps

  const fillAI = async () => {
    setAiBusy(true)
    try { merge(await api.post<Record<string, unknown>>(`${V1}/messages/${m.id}/fill`, { target, ai: true })) } catch (e) { toast((e as Error).message, 'err') } finally { setAiBusy(false) }
  }
  const group = groups?.find(g => String(g.id) === f.group)
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      const body: Record<string, unknown> = { target, ...f, minutes: Number(f.minutes) || 0, amount: Number(String(f.amount || '').replace(',', '.')) || 0, group: Number(f.group) || 0, paid_by: Number(f.paid_by) || 0 }
      if (target === 'expense' && group && sharedWith.length !== group.members.length) body.members = sharedWith
      const d = await api.post<Made>(`${V1}/messages/${m.id}/make`, body)
      toast(`${info.label} added to ${appName(info.app)}`)
      onMade(d)
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const o = opts?.[target]
  const ready = !!o?.connected
  return (
    <Modal title={<span className="row" style={{ gap: 8 }}><Icon name={info.icon} size={17} />{info.verb}</span>} onClose={onClose} size="sm"
      footer={ready ? <div className="form-actions">
        <button type="button" className="btn ghost" onClick={fillAI} disabled={aiBusy} title="Pocket AI reads the message (and the ones around it) and fills in the details"><Icon name="sparkles" size={14} />{aiBusy ? 'Reading…' : 'Fill in with AI'}</button>
        <span className="spacer" />
        <button type="button" className="btn" onClick={onClose}>Cancel</button>
        <button type="submit" form="make-form" className="btn primary" disabled={busy || (target === 'expense' && !group)}>Add to {appName(info.app).replace('Pocket ', '')}</button>
      </div> : undefined}>
      {!opts ? <Loading /> : !ready ? (
        <div className="stack">
          <div className="callout info"><Icon name={APP_INFO[info.app].icon} size={15} /><div><b>{appName(info.app)}</b> is not connected{o?.detail ? `: ${o.detail}` : ''}.</div></div>
          <Link to="/settings/apps" className="btn" onClick={onClose}><Icon name="layers" size={15} />Connected apps</Link>
        </div>
      ) : (
        <form id="make-form" onSubmit={submit} className="stack">
          <blockquote className="make-quote"><b>{m.user_id === user?.id ? 'You' : m.author_name}</b> {m.body || m.data.text || m.data.card?.title || ''}</blockquote>
          {target === 'expense' && (groups === null ? <Loading label="Your Split groups…" /> : groups.length === 0 ? <p className="muted">You have no groups in Pocket Split yet.</p> : <>
            <Field group label="Group"><Select ariaLabel="Split group" value={f.group || ''} onChange={v => { const g = groups.find(x => String(x.id) === v); set('group', v); if (g) { set('paid_by', String(g.my_member_id)); setSharedWith(g.members.map(x => x.id)); set('currency', g.currency) } }} options={groups.map(g => ({ value: String(g.id), label: g.name }))} /></Field>
          </>)}
          <Field label={target === 'place' ? 'Name' : target === 'expense' ? 'What for' : target === 'wish' ? 'What (empty: from the shop page)' : 'Title'}>
            <input value={f.title || ''} onChange={e => set('title', e.target.value)} required={target !== 'wish' && target !== 'note'} autoFocus maxLength={200} />
          </Field>
          {(target === 'task' || target === 'event' || target === 'expense') && <div className="two-col tight">
            <Field label={target === 'task' ? 'Due (optional)' : 'Day'}><input type="date" value={f.date || ''} onChange={e => set('date', e.target.value)} required={target === 'event'} /></Field>
            {target !== 'expense' && <Field label="Time (optional)"><input type="time" value={f.time || ''} onChange={e => set('time', e.target.value)} /></Field>}
          </div>}
          {target === 'event' && <div className="two-col tight">
            {f.time && <Field group label="How long"><Select ariaLabel="How long" value={f.minutes || '60'} onChange={v => set('minutes', v)} options={DURATIONS} /></Field>}
            <Field label="Where"><input value={f.location || ''} onChange={e => set('location', e.target.value)} placeholder="Optional" /></Field>
          </div>}
          {target === 'expense' && group && <>
            <div className="two-col tight">
              <Field label="Amount"><input inputMode="decimal" value={f.amount || ''} onChange={e => set('amount', e.target.value)} required placeholder="0.00" /></Field>
              <Field label="Currency"><input value={f.currency || group.currency} onChange={e => set('currency', e.target.value.toUpperCase())} maxLength={3} /></Field>
            </div>
            <Field group label="Who paid"><Select ariaLabel="Who paid" value={f.paid_by || ''} onChange={v => set('paid_by', v)} options={group.members.map(x => ({ value: String(x.id), label: x.name }))} /></Field>
            <Field group label="Shared by" help="Split evenly.">
              <div className="chip-pick">{group.members.map(x => <button key={x.id} type="button" className={sharedWith.includes(x.id) ? 'on' : ''} onClick={() => setSharedWith(s => s.includes(x.id) ? s.filter(y => y !== x.id) : [...s, x.id])}>{x.name}</button>)}</div>
            </Field>
          </>}
          {(target === 'wish' || target === 'place') && <Field label={target === 'wish' ? 'Shop link' : 'Website (optional)'}><input type="url" value={f.url || ''} onChange={e => set('url', e.target.value)} placeholder="https://…" /></Field>}
          {target === 'place' && <>
            <Field group label="Kind"><Select ariaLabel="Kind" value={f.kind || 'restaurant'} onChange={v => set('kind', v)} options={PLACE_KINDS} /></Field>
            <div className="two-col tight">
              <Field label="City"><input value={f.city || ''} onChange={e => set('city', e.target.value)} /></Field>
              <Field label="Address"><input value={f.address || ''} onChange={e => set('address', e.target.value)} /></Field>
            </div>
          </>}
          {target !== 'expense' && <Field label={target === 'note' ? 'The note' : 'Notes (optional)'} help={target === 'note' ? undefined : 'Who said it and where is added by itself, with a link back.'}>
            <textarea data-gramm="false" data-gramm_editor="false" data-enable-grammarly="false" rows={target === 'note' ? 6 : 2} value={f.notes || ''} onChange={e => set('notes', e.target.value)} />
          </Field>}
        </form>
      )}
    </Modal>
  )
}

export function ShareSheet({ conv, onClose, onShared }: { conv: Conversation; onClose: () => void; onShared: (m: Message) => void }) {
  const { toast } = useToast()
  const [apps, setApps] = useState<ShareApp[] | null>(null)
  const [app, setApp] = useState<ShareApp | null>(null)
  const [q, setQ] = useState('')
  const dq = useDebounce(q, 300)
  const [items, setItems] = useState<ShareItem[] | null>(null)
  const [err, setErr] = useState('')
  const [pick, setPick] = useState<ShareItem | null>(null)
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => { api.get<ShareApp[]>(`${V1}/share/apps`).then(setApps).catch(() => setApps([])) }, [])
  useEffect(() => {
    if (!app) return
    setItems(null); setErr('')
    api.get<ShareItem[]>(`${V1}/share/${app.slug}${dq ? `?q=${encodeURIComponent(dq)}` : ''}`).then(setItems).catch(e => { setErr((e as Error).message); setItems([]) })
  }, [app, dq])
  const share = async () => {
    if (!pick || !app) return
    setBusy(true)
    try { onShared(await api.post<Message>(`${V1}/conversations/${conv.id}/share`, { app: app.slug, id: pick.id, text })) } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={pick ? 'Share this' : app ? <span className="row" style={{ gap: 8 }}><button type="button" className="btn ghost icon-only sm" aria-label="Back" onClick={() => { setApp(null); setQ('') }}><Icon name="chevron-left" size={16} /></button>{app.label}</span> : 'Share from your apps'} onClose={onClose} size="sm"
      footer={pick ? <div className="form-actions"><button type="button" className="btn" onClick={() => setPick(null)}>Back</button><button type="button" className="btn primary" onClick={share} disabled={busy}><Icon name="send" size={14} />Share in {conv.name}</button></div> : undefined}>
      {pick ? (
        <div className="stack">
          <div className="share-preview"><span className="share-ico">{pick.card.icon}</span><div><b>{pick.title}</b>{pick.subtitle && <div className="muted small">{pick.subtitle}</div>}</div></div>
          <Field label="Add a few words (optional)"><textarea data-gramm="false" data-gramm_editor="false" data-enable-grammarly="false" rows={2} value={text} onChange={e => setText(e.target.value)} autoFocus placeholder="Let's do this on Sunday?" /></Field>
          <p className="muted small">Everyone in this conversation sees the card as it is now. The link back to {appName(app?.slug)} opens only for you.</p>
        </div>
      ) : !app ? (
        apps === null ? <Loading /> : <div className="share-apps">
          {apps.map(a => (
            <button key={a.slug} type="button" className="share-app" disabled={!a.connected} title={a.connected ? a.name : `${a.name}: ${a.detail || 'not connected'}`} onClick={() => setApp(a)}>
              <span className="share-app-ico"><Icon name={APP_INFO[a.slug]?.icon || 'layers'} size={18} /></span>
              <span className="grow"><b>{a.label}</b><span className="muted small ellipsis">{a.connected ? a.name : 'not connected'}</span></span>
            </button>
          ))}
          {apps.some(a => !a.connected) && <p className="muted small">Apps that are not connected can be switched on in <Link to="/settings/apps" onClick={onClose}>Settings → Connected apps</Link>.</p>}
        </div>
      ) : (
        <div className="stack">
          {app.slug !== 'weather' && <label className="search-field"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Find…" autoFocus /></label>}
          {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
          {items === null ? <Loading /> : items.length === 0 && !err ? <p className="muted">Nothing found.</p> : (
            <div className="share-items">{items.map(it => (
              <button key={it.id} type="button" className="share-item" onClick={() => setPick(it)}>
                <span className="share-ico">{it.card.icon}</span>
                <span className="grow"><b className="ellipsis">{it.title}</b>{it.subtitle && <span className="muted small ellipsis">{it.subtitle}</span>}</span>
              </button>
            ))}</div>
          )}
        </div>
      )}
    </Modal>
  )
}

export function PollSheet({ onClose, onSend }: { onClose: () => void; onSend: (poll: { question: string; options: string[]; multi: boolean }) => Promise<void> }) {
  const [question, setQuestion] = useState('')
  const [options, setOptions] = useState(['', ''])
  const [multi, setMulti] = useState(false)
  const [busy, setBusy] = useState(false)
  const filled = options.map(o => o.trim()).filter(Boolean)
  const submit = async (e: React.FormEvent) => { e.preventDefault(); setBusy(true); try { await onSend({ question, options: filled, multi }) } finally { setBusy(false) } }
  return (
    <Modal title="Ask the group" onClose={onClose} size="sm" footer={<div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="submit" form="poll-form" className="btn primary" disabled={busy || !question.trim() || filled.length < 2}>Send the poll</button></div>}>
      <form id="poll-form" onSubmit={submit} className="stack">
        <Field label="Question"><input value={question} onChange={e => setQuestion(e.target.value)} autoFocus placeholder="Where for Saturday lunch?" maxLength={300} /></Field>
        <Field group label="Options">
          <div className="stack tight">{options.map((o, i) => (
            <div key={i} className="row"><input value={o} onChange={e => setOptions(xs => { const n = [...xs]; n[i] = e.target.value; if (i === n.length - 1 && e.target.value && n.length < 12) n.push(''); return n })} placeholder={`Option ${i + 1}`} maxLength={120} />
              {options.length > 2 && <button type="button" className="btn ghost icon-only sm" aria-label="Remove" onClick={() => setOptions(xs => xs.filter((_, j) => j !== i))}><Icon name="x" size={14} /></button>}</div>
          ))}</div>
        </Field>
        <Switch checked={multi} onChange={setMulti} label="People may pick more than one" />
      </form>
    </Modal>
  )
}

export function TodoSheet({ conv, onClose, onSend }: { conv: Conversation; onClose: () => void; onSend: (todo: { title: string; items: { text: string; assignee: number }[] }) => Promise<void> }) {
  const [title, setTitle] = useState('')
  const [items, setItems] = useState<{ text: string; assignee: number }[]>([{ text: '', assignee: 0 }, { text: '', assignee: 0 }])
  const [busy, setBusy] = useState(false)
  const filled = items.filter(i => i.text.trim())
  const people = [{ value: '0', label: 'Anyone' }, ...conv.members.map(m => ({ value: String(m.user_id), label: m.name || m.email }))]
  const submit = async (e: React.FormEvent) => { e.preventDefault(); setBusy(true); try { await onSend({ title, items: filled }) } finally { setBusy(false) } }
  return (
    <Modal title="A to-do list for everyone" onClose={onClose} size="sm" footer={<div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="submit" form="todo-form" className="btn primary" disabled={busy || filled.length === 0}>Send the list</button></div>}>
      <form id="todo-form" onSubmit={submit} className="stack">
        <Field label="Title"><input value={title} onChange={e => setTitle(e.target.value)} autoFocus placeholder="Weekend at the cottage" maxLength={120} /></Field>
        <Field group label="Items" help="Anyone in the conversation can tick them off and add more.">
          <div className="stack tight">{items.map((it, i) => (
            <div key={i} className="todo-edit-row">
              <input value={it.text} onChange={e => setItems(xs => { const n = [...xs]; n[i] = { ...n[i], text: e.target.value }; if (i === n.length - 1 && e.target.value && n.length < 50) n.push({ text: '', assignee: 0 }); return n })} placeholder={`Item ${i + 1}`} maxLength={200} />
              {conv.kind === 'group' && <Select ariaLabel="For whom" value={String(it.assignee)} onChange={v => setItems(xs => { const n = [...xs]; n[i] = { ...n[i], assignee: Number(v) }; return n })} options={people} />}
            </div>
          ))}</div>
        </Field>
      </form>
    </Modal>
  )
}
