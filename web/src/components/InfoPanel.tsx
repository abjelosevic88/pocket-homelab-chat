// A conversation's details: name and picture, who is in it, your own
// notifications, Pocket AI and suggestions, pinned messages and what was shared.
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useLive } from '../lib/live'
import { appName, fmtBytesShort, listTime, preview, seenAgo } from '../lib/chat'
import type { Attachment, Conversation, Message } from '../lib/types'
import { Avatar, Confirm, Field, MenuButton, Modal, Segmented, Switch, useToast } from './ui'
import { Icon } from './icons'
import { ConvAvatar } from './ConvList'

const MUTES: { label: string; hours: number }[] = [{ label: '1 hour', hours: 1 }, { label: '8 hours', hours: 8 }, { label: '1 week', hours: 24 * 7 }, { label: 'Until I turn it back', hours: 24 * 365 * 10 }]

export function InfoPanel({ conv, onClose, onJump, embedded = false }: { conv: Conversation; onClose: () => void; onJump: (id: number) => void; embedded?: boolean }) {
  const { user } = useApp()
  const { people, reload, patch } = useLive()
  const { toast } = useToast()
  const navigate = useNavigate()
  const me = user?.id || 0
  const [tab, setTab] = useState<'photos' | 'files' | 'links' | 'cards'>('photos')
  const [shared, setShared] = useState<(Attachment | Message)[] | null>(null)
  const [pinned, setPinned] = useState<Message[]>([])
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState(false)
  const [confirm, setConfirm] = useState<'leave' | 'delete' | null>(null)
  const [f, setF] = useState({ title: conv.title, about: conv.about, emoji: conv.emoji })
  const muted = !!conv.muted_until && new Date(conv.muted_until) > new Date()
  const owner = conv.role === 'owner'

  useEffect(() => { setShared(null); api.get<(Attachment | Message)[]>(`${V1}/conversations/${conv.id}/shared?type=${tab}`).then(setShared).catch(() => setShared([])) }, [conv.id, tab, conv.last_message_id])
  useEffect(() => { api.get<Message[]>(`${V1}/conversations/${conv.id}/pinned`).then(setPinned).catch(() => {}) }, [conv.id, conv.last_message_id])

  const mine = async (body: Record<string, unknown>) => {
    try { await api.put(`${V1}/conversations/${conv.id}/me`, body); patch(conv.id, body as Partial<Conversation>) } catch (e) { toast((e as Error).message, 'err') }
  }
  const update = async (body: Record<string, unknown>) => {
    try { await api.put(`${V1}/conversations/${conv.id}`, body); await reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  const remove = async (uid: number) => {
    try { await api.del(`${V1}/conversations/${conv.id}/members/${uid}`); if (uid === me) { await reload(); navigate('/') } else reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  const role = async (uid: number, r: string) => { try { await api.put(`${V1}/conversations/${conv.id}/members/${uid}`, { role: r }); reload() } catch (e) { toast((e as Error).message, 'err') } }
  const del = async () => { try { await api.del(`${V1}/conversations/${conv.id}`); await reload(); navigate('/') } catch (e) { toast((e as Error).message, 'err') } }

  const other = conv.kind === 'direct' ? conv.members.find(m => m.user_id !== me) : undefined
  const op = other ? people.find(p => p.id === other.user_id) : undefined
  return (
    <aside className={`info-panel ${embedded ? 'embedded' : ''}`} aria-label="Details">
      {!embedded && <div className="info-head"><b>Details</b><span className="spacer" /><button type="button" className="btn ghost icon-only sm" aria-label="Close" onClick={onClose}><Icon name="x" size={16} /></button></div>}
      <div className="info-body">
        <div className="info-hero">
          <ConvAvatar c={conv} size={64} />
          <h3>{conv.name}</h3>
          <p className="muted small">{conv.kind === 'direct' ? (op?.online ? 'online now' : op?.seen ? `seen ${seenAgo(op.seen)}` : other?.email) : conv.kind === 'self' ? 'Only you see this space' : `Group · ${conv.members.length} people`}</p>
          {conv.about && <p className="small">{conv.about}</p>}
          {conv.kind === 'group' && <button type="button" className="btn sm" onClick={() => { setF({ title: conv.title, about: conv.about, emoji: conv.emoji }); setEditing(true) }}><Icon name="edit" size={13} />Name and picture</button>}
        </div>

        {conv.kind !== 'self' && <section className="info-sec">
          <h4>Notifications</h4>
          <Segmented size="sm" value={conv.notify} onChange={v => mine({ notify: v })} options={[{ value: 'default', label: 'Default' }, { value: 'all', label: 'All' }, { value: 'mentions', label: '@ only' }, { value: 'off', label: 'Off' }]} />
          <div className="info-row">
            <span className="grow">{muted ? `Muted until ${new Date(conv.muted_until).getFullYear() > new Date().getFullYear() + 5 ? 'you turn it back' : listTime(conv.muted_until)}` : 'Mute'}</span>
            {muted ? <button type="button" className="btn xs" onClick={() => mine({ muted_until: '' })}>Unmute</button>
              : <MenuButton label="Mute for…" className="btn xs" align="right" items={MUTES.map(x => ({ label: x.label, onSelect: () => mine({ muted_until: new Date(Date.now() + x.hours * 3600e3).toISOString().replace(/\.\d+Z$/, 'Z') }) }))}><Icon name="bell-off" size={12} />Mute…</MenuButton>}
          </div>
        </section>}

        <section className="info-sec">
          <h4>This conversation</h4>
          <div className="info-row"><span className="grow">Pin to the top</span><Switch checked={conv.pinned} onChange={v => mine({ pinned: v })} label="Pin" /></div>
          <div className="info-row"><span className="grow">Archive<span className="muted small block-line">Out of the list until you open it; new messages still arrive.</span></span><Switch checked={conv.archived} onChange={v => mine({ archived: v })} label="Archive" /></div>
          <div className="info-row"><span className="grow">Pocket AI helpers<span className="muted small block-line">Catch-up summaries and reply ideas, only for whoever asks. Pocket AI never writes here.</span></span><Switch checked={conv.ai} onChange={v => update({ ai: v })} label="Pocket AI" /></div>
          <div className="info-row"><span className="grow">Suggestions<span className="muted small block-line">“Add to calendar”, “Split” and “Make a task” under messages.</span></span><Switch checked={conv.smart} onChange={v => update({ smart: v })} label="Suggestions" /></div>
        </section>

        {conv.kind === 'group' && <section className="info-sec">
          <div className="row"><h4 className="grow">People</h4><button type="button" className="btn xs" onClick={() => setAdding(true)}><Icon name="plus" size={12} />Add</button></div>
          {conv.members.map(m => {
            const p = people.find(x => x.id === m.user_id)
            return (
              <div key={m.user_id} className="info-person">
                <span className="conv-ava-wrap" style={{ width: 30, height: 30 }}><Avatar name={m.name} email={m.email} size={30} />{p?.online && m.user_id !== me && <span className="online-dot" />}</span>
                <span className="grow"><b>{m.user_id === me ? `${m.name} (you)` : m.name || m.email}</b>{m.role === 'owner' && <span className="badge">owner</span>}</span>
                {owner && m.user_id !== me && <span className="row">
                  <button type="button" className="btn ghost xs" onClick={() => role(m.user_id, m.role === 'owner' ? 'member' : 'owner')}>{m.role === 'owner' ? 'Member' : 'Make owner'}</button>
                  <button type="button" className="btn ghost xs danger-text" onClick={() => remove(m.user_id)}>Remove</button>
                </span>}
              </div>
            )
          })}
        </section>}

        {pinned.length > 0 && <section className="info-sec">
          <h4>Pinned</h4>
          {pinned.map(m => <button key={m.id} type="button" className="info-pinned" onClick={() => onJump(m.id)}><Icon name="pin" size={13} /><span className="ellipsis">{preview(m)}</span></button>)}
        </section>}

        <section className="info-sec">
          <h4>Shared here</h4>
          <Segmented size="sm" value={tab} onChange={setTab} options={[{ value: 'photos', label: 'Photos' }, { value: 'files', label: 'Files' }, { value: 'links', label: 'Links' }, { value: 'cards', label: 'From apps' }]} />
          {shared === null ? <p className="muted small">Loading…</p> : shared.length === 0 ? <p className="muted small">Nothing yet.</p>
            : tab === 'photos' ? <div className="info-photos">{(shared as Attachment[]).map(a => <a key={a.id} href={a.url} target="_blank" rel="noreferrer"><img src={a.url} alt={a.name} loading="lazy" /></a>)}</div>
            : tab === 'files' ? (shared as Attachment[]).map(a => <a key={a.id} className="info-file" href={`${a.url}?download=1`}><Icon name={a.mime.startsWith('audio/') ? 'mic' : 'file'} size={14} /><span className="grow ellipsis">{a.name}</span><span className="muted small">{fmtBytesShort(a.bytes)}</span></a>)
            : (shared as Message[]).map(m => <button key={m.id} type="button" className="info-pinned" onClick={() => onJump(m.id)}>
              <span>{m.data.card?.icon || '🔗'}</span><span className="grow ellipsis">{m.data.card ? `${m.data.card.title} · ${appName(m.data.card.app)}` : m.data.preview?.title || m.body}</span><span className="muted small">{listTime(m.created_at)}</span></button>)}
        </section>

        <section className="info-sec">
          <a className="btn sm" href={`${V1}/conversations/${conv.id}/export.txt`}><Icon name="download" size={13} />Save as text</a>
          {conv.kind !== 'self' && <button type="button" className="btn sm danger-text" onClick={() => setConfirm('leave')}><Icon name="log-out" size={13} />{conv.kind === 'group' ? 'Leave the group' : 'Hide this chat'}</button>}
          {conv.kind === 'group' && owner && <button type="button" className="btn sm danger-text" onClick={() => setConfirm('delete')}><Icon name="trash" size={13} />Delete for everyone</button>}
        </section>
      </div>

      {adding && <AddPeople conv={conv} onClose={() => setAdding(false)} />}
      {editing && <Modal title="Name and picture" onClose={() => setEditing(false)} size="sm" footer={<div className="form-actions"><button type="button" className="btn" onClick={() => setEditing(false)}>Cancel</button><button type="button" className="btn primary" onClick={async () => { await update(f); setEditing(false) }}>Save</button></div>}>
        <div className="stack">
          <div className="newgroup-head"><Field label="Emoji"><input value={f.emoji} onChange={e => setF({ ...f, emoji: e.target.value })} maxLength={8} style={{ width: 70, fontSize: 20, textAlign: 'center' }} /></Field>
            <Field label="Name" className="grow"><input value={f.title} onChange={e => setF({ ...f, title: e.target.value })} maxLength={80} /></Field></div>
          <Field label="What it is for"><textarea data-gramm="false" data-gramm_editor="false" data-enable-grammarly="false" rows={2} value={f.about} onChange={e => setF({ ...f, about: e.target.value })} maxLength={500} /></Field>
        </div>
      </Modal>}
      {confirm === 'leave' && <Confirm title={conv.kind === 'group' ? `Leave ${conv.name}?` : 'Hide this chat?'} confirmLabel={conv.kind === 'group' ? 'Leave' : 'Hide'}
        message={conv.kind === 'group' ? 'You stop getting its messages. Someone in it can add you back.' : 'It comes back with the next message either of you sends.'} onCancel={() => setConfirm(null)} onConfirm={() => { setConfirm(null); remove(me) }} />}
      {confirm === 'delete' && <Confirm title={`Delete ${conv.name} for everyone?`} confirmLabel="Delete" message="Every message, photo and file in it is gone for everyone in the group." onCancel={() => setConfirm(null)} onConfirm={() => { setConfirm(null); del() }} />}
    </aside>
  )
}

function AddPeople({ conv, onClose }: { conv: Conversation; onClose: () => void }) {
  const { people, reload } = useLive()
  const { toast } = useToast()
  const [picked, setPicked] = useState<number[]>([])
  const out = people.filter(p => !conv.members.some(m => m.user_id === p.id))
  const add = async () => { try { await api.post(`${V1}/conversations/${conv.id}/members`, { members: picked }); await reload(); onClose() } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Modal title={`Add people to ${conv.name}`} onClose={onClose} size="sm" footer={<div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" disabled={!picked.length} onClick={add}>Add {picked.length || ''}</button></div>}>
      <div className="people-pick">
        {out.map(p => <button key={p.id} type="button" className={`person-row ${picked.includes(p.id) ? 'on' : ''}`} onClick={() => setPicked(x => x.includes(p.id) ? x.filter(y => y !== p.id) : [...x, p.id])}>
          <Avatar name={p.name} email={p.email} size={32} /><span className="grow"><b>{p.name || p.email}</b><span className="muted small">{p.email}</span></span>
          <span className={`check ${picked.includes(p.id) ? 'on' : ''}`}><Icon name="check" size={13} strokeWidth={2.5} /></span>
        </button>)}
        {out.length === 0 && <p className="muted small">Everyone on this box is already here.</p>}
      </div>
    </Modal>
  )
}
