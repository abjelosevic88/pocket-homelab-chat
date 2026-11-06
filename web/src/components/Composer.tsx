// Writing a message: text with @names, photos and files (pick, paste or
// drop), a voice message, the + menu (share from your apps, a poll, a to-do
// list) and Pocket AI's reply ideas.
import React, { useEffect, useImperativeHandle, useRef, useState, forwardRef } from 'react'
import { api, ApiError, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { fmtBytesShort, fmtDuration } from '../lib/chat'
import type { Attachment, Conversation, Message } from '../lib/types'
import { Avatar, type Anchor, Menu, rectOf, useIsMobile, useToast } from './ui'
import { Icon } from './icons'

export interface ComposerHandle { focus: () => void; addFiles: (files: File[]) => void; setText: (t: string) => void }

interface Upload { key: string; name: string; size: number; preview?: string; att?: Attachment; error?: string }

interface Props {
  conv: Conversation
  replyTo: Message | null
  editing: Message | null
  onCancelReply: () => void
  onCancelEdit: () => void
  onSend: (text: string, attachments: Attachment[], kind?: 'voice') => void
  onEditSave: (m: Message, text: string) => void
  onShare: () => void
  onPoll: () => void
  onTodo: () => void
}

const MENTION = /(^|\s)@([\p{L}\p{N}_.-]*)$/u

async function uploadFile(cid: number, file: Blob, name: string, extra: Record<string, string> = {}): Promise<Attachment> {
  const fd = new FormData()
  fd.append('file', file, name)
  for (const [k, v] of Object.entries(extra)) fd.append(k, v)
  const res = await fetch(`${V1}/conversations/${cid}/upload`, { method: 'POST', body: fd, credentials: 'same-origin' })
  const j = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, j.error || res.statusText, j)
  return j as Attachment
}

export const Composer = forwardRef<ComposerHandle, Props>(function Composer({ conv, replyTo, editing, onCancelReply, onCancelEdit, onSend, onEditSave, onShare, onPoll, onTodo }, ref) {
  const { settings, user } = useApp()
  const { toast } = useToast()
  const mobile = useIsMobile()
  const [text, setText] = useState(conv.draft || '')
  const [uploads, setUploads] = useState<Upload[]>([])
  const [plus, setPlus] = useState<Anchor | null>(null)
  const [mention, setMention] = useState<{ q: string; i: number } | null>(null)
  const [ideas, setIdeas] = useState<string[] | null>(null)
  const [ideasBusy, setIdeasBusy] = useState(false)
  const [rec, setRec] = useState<{ start: number; now: number } | null>(null)
  const ta = useRef<HTMLTextAreaElement>(null)
  const fileIn = useRef<HTMLInputElement>(null)
  const recorder = useRef<MediaRecorder | null>(null)
  const chunks = useRef<Blob[]>([])
  const cancelRec = useRef(false)
  const lastTyping = useRef(0)
  const draftTimer = useRef(0)
  const recRef = useRef(rec) // the start time, for onstop
  recRef.current = rec

  // a different conversation: its own draft
  useEffect(() => { setText(conv.draft || ''); setUploads([]); setIdeas(null) }, [conv.id]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { if (editing) { setText(editing.body); ta.current?.focus() } }, [editing])
  useEffect(() => { if (replyTo) ta.current?.focus() }, [replyTo])
  // grow with the text
  useEffect(() => { const el = ta.current; if (!el) return; el.style.height = 'auto'; el.style.height = Math.min(el.scrollHeight, 220) + 'px' }, [text])

  const addFiles = (files: File[]) => {
    for (const f of files) {
      const key = Math.random().toString(36).slice(2)
      const preview = f.type.startsWith('image/') ? URL.createObjectURL(f) : undefined
      setUploads(u => [...u, { key, name: f.name, size: f.size, preview }])
      uploadFile(conv.id, f, f.name).then(att => setUploads(u => u.map(x => x.key === key ? { ...x, att } : x)))
        .catch(e => setUploads(u => u.map(x => x.key === key ? { ...x, error: (e as Error).message } : x)))
    }
  }
  useImperativeHandle(ref, () => ({ focus: () => ta.current?.focus(), addFiles, setText: (t: string) => { setText(t); ta.current?.focus() } }))

  const members = conv.members.filter(m => m.user_id !== user?.id)
  const candidates = mention ? [
    ...(conv.kind === 'group' ? [{ key: 'all', label: 'Everyone', hint: 'tell all', insert: 'all' }] : []),
    ...members.map(m => ({ key: String(m.user_id), label: m.name || m.email, hint: m.email, insert: (m.name || m.email).split(/\s+/)[0] })),
  ].filter(c => !mention.q || c.label.toLowerCase().includes(mention.q.toLowerCase()) || c.insert.toLowerCase().startsWith(mention.q.toLowerCase())).slice(0, 6) : []

  const change = (v: string) => {
    setText(v)
    const el = ta.current
    const before = el ? v.slice(0, el.selectionStart) : v
    const mm = MENTION.exec(before)
    setMention(mm ? { q: mm[2], i: 0 } : null)
    if (editing) return
    const now = Date.now()
    if (v && now - lastTyping.current > 3000) { lastTyping.current = now; api.post(`${V1}/conversations/${conv.id}/typing`).catch(() => {}) }
    window.clearTimeout(draftTimer.current)
    draftTimer.current = window.setTimeout(() => api.put(`${V1}/conversations/${conv.id}/me`, { draft: v }).catch(() => {}), 1200)
  }
  const pickMention = (insert: string) => {
    const el = ta.current
    if (!el) return
    const pos = el.selectionStart
    const before = text.slice(0, pos).replace(MENTION, (_a, sp) => `${sp}@${insert} `)
    const next = before + text.slice(pos)
    setText(next)
    setMention(null)
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(before.length, before.length) })
  }

  const ready = uploads.filter(u => u.att)
  const busyUp = uploads.some(u => !u.att && !u.error)
  const canSend = !busyUp && (text.trim() !== '' || ready.length > 0)
  const send = () => {
    if (editing) { if (text.trim()) onEditSave(editing, text.trim()); setText(''); return }
    if (!canSend) return
    onSend(text.trim(), ready.map(u => u.att!))
    uploads.forEach(u => u.preview && URL.revokeObjectURL(u.preview))
    setText(''); setUploads([]); setIdeas(null)
    window.clearTimeout(draftTimer.current)
  }
  const key = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (mention && candidates.length) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); setMention(m => m && { ...m, i: (m.i + (e.key === 'ArrowDown' ? 1 : candidates.length - 1)) % candidates.length }); return }
      if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); pickMention(candidates[mention.i % candidates.length].insert); return }
      if (e.key === 'Escape') { e.preventDefault(); setMention(null); return }
    }
    if (e.key === 'Escape') { if (editing) { e.preventDefault(); setText(''); onCancelEdit() } else if (replyTo) { e.preventDefault(); onCancelReply() } return }
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && (settings?.enter_sends ?? true) && !mobile) { e.preventDefault(); send() }
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); send() }
  }
  const paste = (e: React.ClipboardEvent) => {
    const files = Array.from(e.clipboardData.files || [])
    if (files.length) { e.preventDefault(); addFiles(files) }
  }

  // ---- voice ----
  const startRec = async () => {
    if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === 'undefined') { toast('This browser cannot record', 'err'); return }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      const type = ['audio/webm;codecs=opus', 'audio/webm', 'audio/mp4', 'audio/ogg'].find(t => MediaRecorder.isTypeSupported(t)) || ''
      const mr = new MediaRecorder(stream, type ? { mimeType: type } : undefined)
      chunks.current = []
      cancelRec.current = false
      mr.ondataavailable = e => { if (e.data.size) chunks.current.push(e.data) }
      mr.onstop = async () => {
        stream.getTracks().forEach(t => t.stop())
        const started = recRef.current?.start || Date.now()
        setRec(null)
        if (cancelRec.current) return
        const blob = new Blob(chunks.current, { type: (mr.mimeType || 'audio/webm').split(';')[0] })
        const ms = Date.now() - started
        if (ms < 700) { toast('Too short — hold on a bit longer'); return }
        try {
          const ext = blob.type.includes('mp4') ? 'm4a' : blob.type.includes('ogg') ? 'ogg' : 'webm'
          const att = await uploadFile(conv.id, blob, `voice.${ext}`, { voice: '1', duration_ms: String(ms) })
          onSend('', [att], 'voice')
        } catch (e) { toast((e as Error).message, 'err') }
      }
      mr.start()
      recorder.current = mr
      const start = Date.now()
      setRec({ start, now: start })
    } catch { toast('The microphone is not allowed for this site', 'err') }
  }
  const stopRec = (cancel = false) => { cancelRec.current = cancel; recorder.current?.stop(); recorder.current = null }
  useEffect(() => {
    if (!rec) return
    const t = window.setInterval(() => setRec(r => r && { ...r, now: Date.now() }), 250)
    return () => window.clearInterval(t)
  }, [rec?.start]) // eslint-disable-line react-hooks/exhaustive-deps

  const getIdeas = async () => {
    setIdeasBusy(true)
    try { setIdeas((await api.post<{ replies: string[] }>(`${V1}/conversations/${conv.id}/replies`)).replies) } catch (e) { toast((e as Error).message, 'err') } finally { setIdeasBusy(false) }
  }

  const plusItems = [
    { label: 'Photo or file', icon: 'paperclip', onSelect: () => fileIn.current?.click() },
    { label: 'Share from your apps…', icon: 'layers', onSelect: onShare },
    'sep' as const,
    { label: 'Poll', icon: 'poll', onSelect: onPoll },
    { label: 'To-do list', icon: 'check-square', onSelect: onTodo },
  ]

  return (
    <div className="composer-wrap">
      {ideas && ideas.length > 0 && <div className="reply-ideas">
        {ideas.map((r, i) => <button key={i} type="button" className="idea-chip" onClick={() => { setText(r); setIdeas(null); ta.current?.focus() }}>{r}</button>)}
        <button type="button" className="btn ghost icon-only xs" aria-label="Hide" onClick={() => setIdeas(null)}><Icon name="x" size={13} /></button>
      </div>}
      {(replyTo || editing) && <div className="composer-context">
        <Icon name={editing ? 'edit' : 'reply'} size={15} />
        <span className="grow ellipsis"><b>{editing ? 'Editing' : `Reply to ${replyTo!.user_id === user?.id ? 'yourself' : replyTo!.author_name || 'Pocket AI'}`}</b> <span className="muted">{(editing || replyTo)!.body || '…'}</span></span>
        <button type="button" className="btn ghost icon-only xs" aria-label="Cancel" onClick={() => { if (editing) { setText(''); onCancelEdit() } else onCancelReply() }}><Icon name="x" size={14} /></button>
      </div>}
      {uploads.length > 0 && <div className="composer-files">
        {uploads.map(u => (
          <div key={u.key} className={`up ${u.error ? 'bad' : ''} ${!u.att && !u.error ? 'busy' : ''}`} title={u.error || u.name}>
            {u.preview ? <img src={u.preview} alt="" /> : <span className="up-file"><Icon name="file" size={16} /><span className="ellipsis">{u.name}</span><span className="muted">{fmtBytesShort(u.size)}</span></span>}
            {!u.att && !u.error && <span className="up-spin" />}
            <button type="button" className="up-x" aria-label="Remove" onClick={() => setUploads(x => x.filter(y => y.key !== u.key))}><Icon name="x" size={12} /></button>
          </div>
        ))}
      </div>}
      {mention && candidates.length > 0 && <div className="mention-pop" role="listbox">
        {candidates.map((c, i) => (
          <button key={c.key} type="button" role="option" aria-selected={i === mention.i % candidates.length} className={`mention-opt ${i === mention.i % candidates.length ? 'hl' : ''}`} onMouseDown={e => { e.preventDefault(); pickMention(c.insert) }}>
            {c.key === 'all' ? <span className="ai-ava sm app"><Icon name="users" size={12} /></span> : <Avatar name={c.label} email={c.hint} size={22} />}
            <b>{c.label}</b><span className="muted small">{c.hint}</span>
          </button>
        ))}
      </div>}
      {rec ? (
        <div className="composer recording">
          <button type="button" className="btn ghost icon-only" aria-label="Throw away" title="Throw away" onClick={() => stopRec(true)}><Icon name="trash" size={18} /></button>
          <span className="rec-dot" /><span className="tnum">{fmtDuration(rec.now - rec.start)}</span>
          <span className="grow muted small">Recording… {settings?.voice_via ? 'Pocket Voice writes it out on this server.' : ''}</span>
          <button type="button" className="send-btn" aria-label="Send the voice message" onClick={() => stopRec(false)}><Icon name="send" size={18} /></button>
        </div>
      ) : (
        <div className="composer">
          <button type="button" className="btn ghost icon-only" aria-label="Add" title="Photo, file, share from your apps, poll, to-do list" onClick={e => setPlus(plus ? null : rectOf(e.currentTarget))}><Icon name="plus" size={20} /></button>
          {plus && <Menu anchor={plus} items={plusItems} onClose={() => setPlus(null)} width={230} />}
          <input ref={fileIn} type="file" multiple hidden onChange={e => { addFiles(Array.from(e.target.files || [])); e.target.value = '' }} />
          <textarea data-gramm="false" data-gramm_editor="false" data-enable-grammarly="false" ref={ta} rows={1} value={text} onChange={e => change(e.target.value)} onKeyDown={key} onPaste={paste} onBlur={() => window.setTimeout(() => setMention(null), 150)}
            placeholder={editing ? 'Change your message' : conv.kind === 'self' ? 'A note to yourself…' : mobile ? 'Message' : `Message ${conv.name}`} aria-label="Message" />
          {!editing && conv.kind !== 'self' && !text && <button type="button" className="btn ghost icon-only" aria-label="Reply ideas" title="Reply ideas from Pocket AI" onClick={getIdeas} disabled={ideasBusy}><Icon name="sparkles" size={18} className={ideasBusy ? 'pulse' : ''} /></button>}
          {text.trim() || ready.length || editing ? (
            <button type="button" className="send-btn" aria-label={editing ? 'Save' : 'Send'} title={editing ? 'Save (Enter)' : 'Send (Enter)'} onClick={send} disabled={!editing && !canSend}><Icon name={editing ? 'check' : 'send'} size={18} /></button>
          ) : (
            <button type="button" className="send-btn mic" aria-label="Record a voice message" title="Record a voice message" onClick={startRec}><Icon name="mic" size={18} /></button>
          )}
        </div>
      )}
    </div>
  )
})
