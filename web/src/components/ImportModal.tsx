// Bring a chat exported from WhatsApp (Export chat → .txt, or .zip with the photos).
import { useState } from 'react'
import { ApiError, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useLive } from '../lib/live'
import { Field, Modal, useToast } from './ui'
import { Select } from './Select'
import { Icon } from './icons'

interface PreviewT { messages: number; names: { name: string; count: number }[]; files: number; first: string; last: string }

async function post<T>(fields: Record<string, string>, file: File): Promise<T> {
  const fd = new FormData()
  fd.append('file', file)
  for (const [k, v] of Object.entries(fields)) fd.append(k, v)
  const res = await fetch(`${V1}/import/whatsapp`, { method: 'POST', body: fd, credentials: 'same-origin' })
  const j = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, j.error || res.statusText, j)
  return j as T
}

export function ImportModal({ onClose, onDone }: { onClose: () => void; onDone: (id: number) => void }) {
  const { user } = useApp()
  const { people, conversations, reload } = useLive()
  const { toast } = useToast()
  const [file, setFile] = useState<File | null>(null)
  const [pre, setPre] = useState<PreviewT | null>(null)
  const [who, setWho] = useState<Record<string, string>>({})
  const [title, setTitle] = useState('')
  const [into, setInto] = useState('')
  const [busy, setBusy] = useState(false)

  const pick = async (f: File | null) => {
    setFile(f); setPre(null)
    if (!f) return
    setBusy(true)
    try {
      const p = await post<PreviewT>({ preview: '1' }, f)
      setPre(p)
      const guess: Record<string, string> = {}
      for (const n of p.names) {
        const m = people.find(x => x.name && (x.name.toLowerCase() === n.name.toLowerCase() || x.name.split(' ')[0].toLowerCase() === n.name.split(' ')[0].toLowerCase()))
        if (m) guess[n.name] = String(m.id)
      }
      setWho(guess)
      setTitle(f.name.replace(/\.(txt|zip)$/i, '').replace(/^WhatsApp Chat (with|-)\s*/i, '') || 'WhatsApp chat')
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const run = async () => {
    if (!file) return
    setBusy(true)
    try {
      const map: Record<string, number> = {}
      for (const [n, id] of Object.entries(who)) if (id) map[n] = Number(id)
      const r = await post<{ conversation_id: number; messages: number; skipped: number; files: number }>({ people: JSON.stringify(map), title, conversation: into }, file)
      toast(`${r.messages} messages brought in${r.files ? `, ${r.files} photos and files` : ''}${r.skipped ? ` (${r.skipped} were already here)` : ''}`)
      await reload()
      onDone(r.conversation_id)
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const options = [{ value: '', label: 'Nobody here (keep the name)' }, ...people.map(p => ({ value: String(p.id), label: p.id === user?.id ? `${p.name} (you)` : p.name || p.email }))]
  const groups = (conversations || []).filter(c => c.kind !== 'self')
  return (
    <Modal title="Bring a WhatsApp chat" onClose={onClose} size=""
      footer={<div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" disabled={!pre || busy} onClick={run}>Bring {pre ? pre.messages : ''} messages</button></div>}>
      <div className="stack">
        <p className="muted small">In WhatsApp open the chat → ⋮ or the name → <b>Export chat</b> (with media for the photos) and choose the file here. Bringing the same file again adds nothing twice.</p>
        <label className="file-drop">
          <Icon name="upload" size={18} />
          <span>{file ? file.name : 'Choose the .txt or .zip'}</span>
          <input type="file" accept=".txt,.zip,text/plain,application/zip" onChange={e => pick(e.target.files?.[0] || null)} />
        </label>
        {pre && <>
          <p className="small"><b>{pre.messages}</b> messages from {pre.first} to {pre.last}{pre.files ? `, ${pre.files} files` : ''}.</p>
          <Field group label="Who is who" help="Messages from someone here show as theirs; everyone else keeps their WhatsApp name.">
            <div className="who-map">{pre.names.map(n => (
              <div key={n.name} className="who-row"><span className="grow ellipsis"><b>{n.name}</b> <span className="muted small">{n.count}</span></span>
                <Select ariaLabel={`Who is ${n.name}`} value={who[n.name] || ''} onChange={v => setWho(w => ({ ...w, [n.name]: v }))} options={options} /></div>
            ))}</div>
          </Field>
          <Field group label="Where to">
            <Select ariaLabel="Where to" value={into} onChange={setInto} options={[{ value: '', label: 'A new group' }, ...groups.map(c => ({ value: String(c.id), label: c.name }))]} />
          </Field>
          {!into && <Field label="Group name"><input value={title} onChange={e => setTitle(e.target.value)} maxLength={80} /></Field>}
        </>}
      </div>
    </Modal>
  )
}
