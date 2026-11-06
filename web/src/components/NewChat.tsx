// Start a chat with someone on this box, or a group.
import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useLive } from '../lib/live'
import type { Conversation } from '../lib/types'
import { seenAgo } from '../lib/chat'
import { Avatar, Field, Modal, Segmented, useToast } from './ui'
import { Icon } from './icons'

const EMOJI = ['🏡', '👨‍👩‍👧', '🥾', '⚽', '🎉', '✈️', '🍕', '💼', '🎓', '❤️', '🎸', '🐶']
const COLORS = ['#128c7e', '#2563eb', '#7c3aed', '#db2777', '#ea580c', '#ca8a04', '#0891b2', '#4d7c0f']

export function NewChat({ mode: initial, onClose, onDone }: { mode: 'chat' | 'group'; onClose: () => void; onDone: (id: number) => void }) {
  const { user } = useApp()
  const { people, reload } = useLive()
  const { toast } = useToast()
  const [mode, setMode] = useState(initial)
  const [q, setQ] = useState('')
  const [picked, setPicked] = useState<number[]>([])
  const [title, setTitle] = useState('')
  const [emoji, setEmoji] = useState('🏡')
  const [color, setColor] = useState(COLORS[0])
  const [busy, setBusy] = useState(false)
  const others = people.filter(p => p.id !== user?.id && (!q || (p.name + ' ' + p.email).toLowerCase().includes(q.toLowerCase())))

  const start = async (body: Record<string, unknown>) => {
    setBusy(true)
    try { const c = await api.post<Conversation>(`${V1}/conversations`, body); await reload(); onDone(c.id) } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const toggle = (id: number) => setPicked(p => p.includes(id) ? p.filter(x => x !== id) : [...p, id])

  return (
    <Modal title={mode === 'chat' ? 'Chat with someone' : 'New group'} onClose={onClose} size="sm"
      footer={mode === 'group' ? <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button>
        <button type="button" className="btn primary" disabled={busy || !title.trim()} onClick={() => start({ kind: 'group', title, emoji, color, members: picked })}>Make the group</button></div> : undefined}>
      <div className="stack">
        <Segmented value={mode} onChange={v => setMode(v)} options={[{ value: 'chat', label: 'One person' }, { value: 'group', label: 'Group' }]} />
        {mode === 'group' && <>
          <div className="newgroup-head">
            <span className="conv-ava group" style={{ width: 48, height: 48, background: color, fontSize: 24 }}>{emoji}</span>
            <Field label="Name" className="grow"><input value={title} onChange={e => setTitle(e.target.value)} placeholder="Family, Weekend hikers…" autoFocus maxLength={80} /></Field>
          </div>
          <div className="emoji-pick" role="group" aria-label="Picture">{EMOJI.map(e => <button key={e} type="button" className={e === emoji ? 'on' : ''} onClick={() => setEmoji(e)}>{e}</button>)}</div>
          <div className="color-pick" role="group" aria-label="Colour">{COLORS.map(c => <button key={c} type="button" aria-label={c} className={c === color ? 'on' : ''} style={{ background: c }} onClick={() => setColor(c)} />)}</div>
        </>}
        <label className="search-field"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Find a person" autoFocus={mode === 'chat'} /></label>
        <div className="people-pick">
          {others.map(p => (
            <button key={p.id} type="button" className={`person-row ${picked.includes(p.id) ? 'on' : ''}`} disabled={busy}
              onClick={() => mode === 'chat' ? start({ kind: 'direct', with: p.id }) : toggle(p.id)}>
              <span className="conv-ava-wrap" style={{ width: 34, height: 34 }}><Avatar name={p.name} email={p.email} size={34} />{p.online && <span className="online-dot" />}</span>
              <span className="grow"><b>{p.name || p.email}</b><span className="muted small">{p.online ? 'online now' : p.seen ? `seen ${seenAgo(p.seen)}` : p.email}</span></span>
              {mode === 'group' && <span className={`check ${picked.includes(p.id) ? 'on' : ''}`}><Icon name="check" size={13} strokeWidth={2.5} /></span>}
            </button>
          ))}
          {others.length === 0 && <p className="muted small">{q ? 'Nobody matches.' : 'Nobody else is on this Pocket Chat yet. An admin adds people in Settings → People; with Pocket Core everyone who signs in once is here.'}</p>}
        </div>
      </div>
    </Modal>
  )
}
