// One conversation: the messages (older ones load as you scroll up, new
// ones arrive live), the composer, the pinned message, the catch-up summary
// and the details panel.
import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useOutletContext, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useLive, useLiveEvents } from '../lib/live'
import { dayKey, dayLine, newClientId, preview, seenAgo } from '../lib/chat'
import type { Attachment, Catchup, Conversation as Conv, Made, MakeTarget, Message, Person } from '../lib/types'
import { MessageItem, type MsgActions, RichText } from '../components/MessageItem'
import { Composer, type ComposerHandle } from '../components/Composer'
import { MakeSheet, PollSheet, ShareSheet, TodoSheet } from '../components/Sheets'
import { ConvAvatar } from '../components/ConvList'
import { InfoPanel } from '../components/InfoPanel'
import { AskButton } from '../components/AskButton'
import { Confirm, Loading, Modal, useIsMobile, useMediaQuery, useToast } from '../components/ui'
import { Icon } from '../components/icons'

const PAGE = 60

export default function ConversationPage() {
  const { id: raw } = useParams()
  const id = Number(raw)
  const { conversations } = useLive()
  const conv = conversations?.find(c => c.id === id)
  const [missing, setMissing] = useState(false)
  const { reload } = useLive()
  useEffect(() => {
    setMissing(false)
    if (conversations && !conv) api.get<Conv>(`${V1}/conversations/${id}`).then(() => reload()).catch(() => setMissing(true))
  }, [id, !!conv]) // eslint-disable-line react-hooks/exhaustive-deps
  if (missing) return <div className="chat-missing"><Icon name="message" size={28} /><h3>This conversation is not here</h3><p className="muted">It was deleted, or you are no longer in it.</p></div>
  if (!conv) return <Loading />
  return <Chat key={conv.id} conv={conv} />
}

function Chat({ conv }: { conv: Conv }) {
  const { user, settings } = useApp()
  const { people, typing, patch, setActive } = useLive()
  const { toast } = useToast()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const { onMenu } = useOutletContext<{ onMenu: () => void }>()
  const mobile = useIsMobile()
  const wide = useMediaQuery('(min-width: 1180px)')
  const me = user?.id || 0

  const [msgs, setMsgs] = useState<Message[] | null>(null)
  const [older, setOlder] = useState(true)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [newer, setNewer] = useState(false) // opened around a message: there is more below
  const [below, setBelow] = useState(0)
  const [highlight, setHighlight] = useState(0)
  const [replyTo, setReplyTo] = useState<Message | null>(null)
  const [editing, setEditing] = useState<Message | null>(null)
  const [make, setMake] = useState<{ m: Message; target: MakeTarget; prefill?: Record<string, unknown> } | null>(null)
  const [sheet, setSheet] = useState<'share' | 'poll' | 'todo' | null>(null)
  const [del, setDel] = useState<Message | null>(null)
  const [photo, setPhoto] = useState<Attachment | null>(null)
  const [info, setInfo] = useState(() => wide && localStorage.getItem('pch-info') === '1')
  const [translations, setTranslations] = useState<Record<number, string>>({})
  const [catchup, setCatchup] = useState<Catchup | null>(null)
  const [catchupBusy, setCatchupBusy] = useState(false)
  const [dragging, setDragging] = useState(false)
  const scroller = useRef<HTMLDivElement>(null)
  const composer = useRef<ComposerHandle>(null)
  const atBottom = useRef(true)
  const firstUnread = useRef(0)
  const unreadAtOpen = useRef(conv.unread)
  const readSent = useRef(conv.last_read_id)
  const stick = useRef<'bottom' | 'unread' | 'keep' | number>('bottom')
  const keepFrom = useRef(0)

  const peopleMap = useMemo(() => { const m = new Map<number, Person>(); people.forEach(p => m.set(p.id, p)); conv.members.forEach(x => { if (!m.has(x.user_id)) m.set(x.user_id, { id: x.user_id, name: x.name, email: x.email, online: false }) }); return m }, [people, conv.members])
  const names = useMemo(() => { const m = new Map<string, number>(); conv.members.forEach(x => { const f = (x.name || x.email).split(/\s+/)[0].toLowerCase(); m.set(f, x.user_id); m.set((x.name || '').replace(/\s+/g, '').toLowerCase(), x.user_id) }); m.set('all', 0); m.set('everyone', 0); return m }, [conv.members])

  useEffect(() => { setActive(conv.id); return () => setActive(0) }, [conv.id, setActive])

  // the first page: around a message (?m=), else the newest, scrolled to the first unread
  useEffect(() => {
    const around = Number(params.get('m')) || 0
    const url = around ? `${V1}/conversations/${conv.id}/messages?around=${around}` : `${V1}/conversations/${conv.id}/messages?limit=${PAGE}`
    api.get<Message[]>(url).then(list => {
      firstUnread.current = list.find(m => m.id > conv.last_read_id && m.user_id !== me && m.kind !== 'system')?.id || 0
      stick.current = around ? around : firstUnread.current && unreadAtOpen.current > 3 ? 'unread' : 'bottom'
      setOlder(list.length >= (around ? 20 : PAGE))
      setNewer(!!around && list.length > 0 && list[list.length - 1].id < conv.last_message_id)
      if (around) { setHighlight(around); window.setTimeout(() => setHighlight(0), 2600); params.delete('m'); setParams(params, { replace: true }) }
      setMsgs(list)
    }).catch(e => { toast((e as Error).message, 'err'); setMsgs([]) })
  }, [conv.id]) // eslint-disable-line react-hooks/exhaustive-deps

  // keep the place: at the bottom, at the first unread, at a message, or where it was when older ones came in
  useLayoutEffect(() => {
    const el = scroller.current
    if (!el || !msgs) return
    const s = stick.current
    if (s === 'bottom') el.scrollTop = el.scrollHeight
    else if (s === 'unread') { const d = el.querySelector('.unread-line') as HTMLElement | null; el.scrollTop = d ? d.offsetTop - 80 : el.scrollHeight }
    else if (s === 'keep') el.scrollTop = el.scrollHeight - keepFrom.current
    else if (typeof s === 'number') { const t = el.querySelector(`#m${s}`) as HTMLElement | null; if (t) el.scrollTop = t.offsetTop - el.clientHeight / 3 }
    stick.current = atBottom.current ? 'bottom' : 'keep'
    if (stick.current === 'keep') keepFrom.current = el.scrollHeight - el.scrollTop
    markRead()
  }, [msgs]) // eslint-disable-line react-hooks/exhaustive-deps

  const lastReal = (list: Message[] | null) => { if (!list) return 0; for (let i = list.length - 1; i >= 0; i--) if (list[i].id > 0) return list[i].id; return 0 }
  const markRead = useCallback(() => {
    const last = lastReal(msgs)
    if (!last || last <= readSent.current || document.visibilityState !== 'visible' || newer) return
    const el = scroller.current
    if (el && el.scrollHeight - el.scrollTop - el.clientHeight > 160) return
    readSent.current = last
    patch(conv.id, { unread: 0, mentioned: 0, last_read_id: last })
    api.post(`${V1}/conversations/${conv.id}/read`, { upto: last }).catch(() => {})
  }, [msgs, conv.id, patch, newer])
  useEffect(() => {
    const v = () => markRead()
    document.addEventListener('visibilitychange', v)
    window.addEventListener('focus', v)
    return () => { document.removeEventListener('visibilitychange', v); window.removeEventListener('focus', v) }
  }, [markRead])

  const loadOlder = async () => {
    if (!msgs || loadingOlder || !older) return
    const first = msgs.find(m => m.id > 0)
    if (!first) return
    setLoadingOlder(true)
    try {
      const list = await api.get<Message[]>(`${V1}/conversations/${conv.id}/messages?before=${first.id}&limit=${PAGE}`)
      const el = scroller.current
      keepFrom.current = el ? el.scrollHeight - el.scrollTop : 0
      stick.current = 'keep'
      setOlder(list.length >= PAGE)
      setMsgs(cur => [...list.filter(m => !cur?.some(x => x.id === m.id)), ...(cur || [])])
    } catch (e) { toast((e as Error).message, 'err') } finally { setLoadingOlder(false) }
  }
  const loadNewer = async () => {
    const last = lastReal(msgs)
    try {
      const list = await api.get<Message[]>(`${V1}/conversations/${conv.id}/messages?after=${last}&limit=200`)
      setNewer(list.length >= 200)
      stick.current = atBottom.current ? 'bottom' : 'keep'
      setMsgs(cur => [...(cur || []), ...list.filter(m => !cur?.some(x => x.id === m.id))])
    } catch { /* the next event tries again */ }
  }
  const onScroll = () => {
    const el = scroller.current
    if (!el) return
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80
    if (atBottom.current) { if (below) setBelow(0); if (newer) loadNewer(); markRead() }
    if (el.scrollTop < 300 && older && !loadingOlder) loadOlder()
  }
  const toBottom = () => {
    if (newer) { api.get<Message[]>(`${V1}/conversations/${conv.id}/messages?limit=${PAGE}`).then(list => { stick.current = 'bottom'; setNewer(false); setOlder(true); setMsgs(list) }); return }
    const el = scroller.current
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' })
    setBelow(0)
  }

  // live: new messages, changes, catching up after a gap
  const upsert = (m: Message) => setMsgs(cur => {
    if (!cur) return cur
    const i = cur.findIndex(x => x.id === m.id || (!!m.client_id && x.client_id === m.client_id && x.pending))
    if (i >= 0) { const n = [...cur]; n[i] = { ...m, starred: cur[i].starred, made: cur[i].made, pending: false, failed: undefined }; return n }
    return [...cur, m]
  })
  useLiveEvents(e => {
    if (e.type === 'message' && e.data.conversation_id === conv.id) {
      if (newer) return
      const mine = e.data.user_id === me
      stick.current = mine || atBottom.current ? 'bottom' : 'keep'
      if (stick.current === 'keep' && scroller.current) keepFrom.current = scroller.current.scrollHeight - scroller.current.scrollTop
      if (!mine && !atBottom.current && e.data.kind !== 'system') setBelow(b => b + 1)
      upsert(e.data)
    } else if (e.type === 'message_update' && e.data.conversation_id === conv.id) {
      stick.current = atBottom.current ? 'bottom' : 'keep'
      if (scroller.current && stick.current === 'keep') keepFrom.current = scroller.current.scrollHeight - scroller.current.scrollTop
      setMsgs(cur => cur && cur.map(x => x.id === e.data.id ? { ...e.data, starred: x.starred, made: x.made } : x))
    } else if (e.type === 'resync') loadNewer()
  })

  // ---- sending ----
  const sendNow = async (pending: Message, body: Record<string, unknown>) => {
    try {
      const m = await api.post<Message>(`${V1}/conversations/${conv.id}/messages`, body)
      upsert({ ...m, client_id: pending.client_id })
    } catch (e) { setMsgs(cur => cur && cur.map(x => x.client_id === pending.client_id ? { ...x, failed: (e as Error).message } : x)) }
  }
  const send = (text: string, atts: Attachment[], kind?: 'voice', extra: Record<string, unknown> = {}) => {
    const client_id = newClientId()
    const pending: Message = { id: -Date.now(), conversation_id: conv.id, user_id: me, kind: kind || (extra.kind as Message['kind']) || 'text', body: text, data: {}, reply_to: replyTo?.id || 0, mentions: [], author_name: user?.name || '', source: '', source_ref: '',
      client_id, deleted: false, created_at: new Date().toISOString(), attachments: atts, reactions: [], starred: false, pending: true,
      reply: replyTo ? { id: replyTo.id, user_id: replyTo.user_id, author_name: replyTo.author_name, kind: replyTo.kind, body: replyTo.body, deleted: false } : undefined }
    if (extra.poll) pending.data = { ...(extra.poll as object), closed: false } as Message['data']
    if (extra.todo) pending.data = { title: (extra.todo as { title: string }).title, items: [] }
    stick.current = 'bottom'
    atBottom.current = true
    setMsgs(cur => [...(cur || []), pending])
    setReplyTo(null)
    patch(conv.id, { draft: '' })
    sendNow(pending, { text, attachments: atts.map(a => a.id), reply_to: pending.reply_to || undefined, client_id, kind, ...extra })
  }
  const retry = (m: Message) => {
    setMsgs(cur => cur && cur.map(x => x.client_id === m.client_id ? { ...x, failed: undefined } : x))
    sendNow(m, { text: m.body, attachments: m.attachments.map(a => a.id), reply_to: m.reply_to || undefined, client_id: m.client_id, kind: m.kind === 'voice' ? 'voice' : undefined })
  }
  const saveEdit = async (m: Message, text: string) => {
    setEditing(null)
    try { upsert(await api.put<Message>(`${V1}/messages/${m.id}`, { text })) } catch (e) { toast((e as Error).message, 'err') }
  }

  const act: MsgActions = useMemo(() => ({
    react: (m, emoji) => api.post<Message>(`${V1}/messages/${m.id}/react`, { emoji }).then(x => setMsgs(cur => cur && cur.map(y => y.id === x.id ? { ...x, starred: y.starred, made: y.made } : y))).catch(e => toast((e as Error).message, 'err')),
    reply: m => { setEditing(null); setReplyTo(m) },
    edit: m => { setReplyTo(null); setEditing(m) },
    remove: m => setDel(m),
    star: m => api.post(`${V1}/messages/${m.id}/star`, { on: !m.starred }).then(() => { setMsgs(cur => cur && cur.map(y => y.id === m.id ? { ...y, starred: !m.starred } : y)); toast(m.starred ? 'Removed from Saved' : 'Saved') }).catch(e => toast((e as Error).message, 'err')),
    pin: m => api.post(`${V1}/messages/${m.id}/pin`, { on: !m.pinned_at }).catch(e => toast((e as Error).message, 'err')),
    make: (m, target, prefill) => setMake({ m, target, prefill }),
    translate: m => {
      setTranslations(t => ({ ...t, [m.id]: '…' }))
      api.post<{ text: string }>(`${V1}/messages/${m.id}/translate`, {}).then(r => setTranslations(t => ({ ...t, [m.id]: r.text }))).catch(e => { setTranslations(t => { const n = { ...t }; delete n[m.id]; return n }); toast((e as Error).message, 'err') })
    },
    vote: (m, options) => api.post<Message>(`${V1}/messages/${m.id}/vote`, { options }).then(x => setMsgs(cur => cur && cur.map(y => y.id === x.id ? { ...x, starred: y.starred, made: y.made } : y))).catch(e => toast((e as Error).message, 'err')),
    closePoll: (m, closed) => api.post(`${V1}/messages/${m.id}/close`, { closed }).catch(e => toast((e as Error).message, 'err')),
    todo: (m, body) => api.post<Message>(`${V1}/messages/${m.id}/todo`, body).then(x => setMsgs(cur => cur && cur.map(y => y.id === x.id ? { ...x, starred: y.starred, made: y.made } : y))).catch(e => toast((e as Error).message, 'err')),
    retry,
    open: a => setPhoto(a),
    jump: mid => {
      const el = scroller.current?.querySelector(`#m${mid}`) as HTMLElement | null
      if (el) { el.scrollIntoView({ block: 'center', behavior: 'smooth' }); setHighlight(mid); window.setTimeout(() => setHighlight(0), 2000) }
      else api.get<Message[]>(`${V1}/conversations/${conv.id}/messages?around=${mid}`).then(list => { stick.current = mid; setNewer(true); setOlder(true); setMsgs(list); setHighlight(mid); window.setTimeout(() => setHighlight(0), 2600) })
    },
  }), [conv.id, toast]) // eslint-disable-line react-hooks/exhaustive-deps

  const madeOne = (d: Made) => { setMsgs(cur => cur && cur.map(y => y.id === d.message_id ? { ...y, made: [...(y.made || []), d] } : y)); setMake(null) }

  const runCatchup = async () => {
    setCatchupBusy(true)
    try { setCatchup(await api.get<Catchup>(`${V1}/conversations/${conv.id}/catchup`)) } catch (e) { toast((e as Error).message, 'err') } finally { setCatchupBusy(false) }
  }
  const keepCatchup = async () => {
    if (!catchup) return
    const content = [catchup.summary, catchup.for_you.length ? '\n**For you**\n' + catchup.for_you.map(x => '- ' + x).join('\n') : '', catchup.plans.length ? '\n**Plans**\n' + catchup.plans.map(x => '- ' + x).join('\n') : ''].join('\n')
    try { const r = await api.post<{ url: string }>(`${V1}/conversations/${conv.id}/note`, { content }); toast('Kept in Pocket Notes'); if (r.url) window.open(r.url, '_blank', 'noreferrer') } catch (e) { toast((e as Error).message, 'err') }
  }

  // read receipts: my messages others have read
  const others = conv.members.filter(m => m.user_id !== me)
  const lastMine = msgs ? [...msgs].reverse().find(m => m.user_id === me && m.id > 0 && m.kind !== 'system')?.id || 0 : 0
  const seenFor = (m: Message): string | undefined => {
    if (m.user_id !== me || m.id <= 0 || conv.kind === 'self' || !(settings?.read_receipts ?? true)) return undefined
    const readers = others.filter(o => o.last_read_id >= m.id)
    if (conv.kind === 'direct') return readers.length ? 'Seen' : ''
    if (m.id !== lastMine) return readers.length === others.length && others.length ? 'Seen by everyone' : ''
    if (!readers.length) return ''
    return readers.length === others.length ? 'Seen by everyone' : 'Seen by ' + readers.map(r => (r.name || r.email).split(' ')[0]).join(', ')
  }

  const t = (typing[conv.id] || []).filter(x => x.user_id !== me)
  const other = conv.kind === 'direct' ? others[0] : undefined
  const otherP = other ? peopleMap.get(other.user_id) : undefined
  const sub = t.length ? `${t.map(x => x.name.split(' ')[0]).join(', ')} ${t.length > 1 ? 'are' : 'is'} writing…`
    : conv.kind === 'self' ? 'Only you see this'
    : conv.kind === 'direct' ? (otherP?.online ? 'online' : otherP?.seen ? `seen ${seenAgo(otherP.seen)}` : other?.email || '')
    : conv.members.map(m => m.user_id === me ? 'you' : (m.name || m.email).split(' ')[0]).join(', ')
  const pinned = msgs?.filter(m => m.pinned_at && !m.deleted).sort((a, b) => (b.pinned_at || '').localeCompare(a.pinned_at || ''))[0]
  const smart = (settings?.smart_actions ?? true) && conv.smart
  const toggleInfo = () => setInfo(v => { const n = !v; if (wide) localStorage.setItem('pch-info', n ? '1' : '0'); return n })

  let lastDay = ''
  return (
    <div className={`chat ${info && wide ? 'with-info' : ''}`}>
      <div className="chat-main" onDragOver={e => { if (e.dataTransfer.types.includes('Files')) { e.preventDefault(); setDragging(true) } }} onDragLeave={e => { if (e.currentTarget === e.target) setDragging(false) }}
        onDrop={e => { if (e.dataTransfer.files.length) { e.preventDefault(); setDragging(false); composer.current?.addFiles(Array.from(e.dataTransfer.files)) } }}>
        <header className="chat-head">
          {mobile && <button type="button" className="btn ghost icon-only" aria-label="Chats" onClick={() => navigate('/')}><Icon name="chevron-left" size={20} /></button>}
          {mobile && <button type="button" className="btn ghost icon-only chat-head-menu" aria-label="Menu" onClick={onMenu}><Icon name="menu" size={18} /></button>}
          <button type="button" className="chat-head-who" onClick={toggleInfo} title="Details">
            <ConvAvatar c={conv} size={36} />
            <span className="grow chat-head-text"><b className="ellipsis">{conv.name}</b><span className={`ellipsis small ${t.length ? 'typing' : 'muted'}`}>{sub}</span></span>
          </button>
          <span className="spacer" />
          {conv.ai && conv.kind !== 'self' && <button type="button" className="btn ghost sm catchup-btn" onClick={runCatchup} disabled={catchupBusy} title="What did I miss? A short summary by Pocket AI">
            <Icon name="sparkles" size={15} className={catchupBusy ? 'pulse' : ''} />{!mobile && 'Catch up'}
          </button>}
          <button type="button" className="btn ghost icon-only" aria-label="Search in this chat" title="Search in this chat" onClick={() => navigate(`/search?conversation=${conv.id}`)}><Icon name="search" size={17} /></button>
          <button type="button" className={`btn ghost icon-only ${info ? 'on' : ''}`} aria-label="Details" title="Details" onClick={toggleInfo}><Icon name="info" size={17} /></button>
          {mobile && <AskButton />}
        </header>
        {pinned && <button type="button" className="pinned-bar" onClick={() => act.jump(pinned.id)}><Icon name="pin" size={14} /><span className="ellipsis"><b>Pinned</b> {preview(pinned)}</span></button>}

        <div className="chat-scroll" ref={scroller} onScroll={onScroll}>
          <div className="chat-list">
            {msgs === null ? <Loading /> : <>
              {older ? <div className="chat-older">{loadingOlder ? <Loading label="Older messages…" /> : <button type="button" className="btn ghost sm" onClick={loadOlder}>Older messages</button>}</div>
                : <div className="chat-start"><ConvAvatar c={conv} size={56} /><h3>{conv.name}</h3>
                  <p className="muted small">{conv.kind === 'self' ? 'Your own space: notes to yourself, links, photos and things to file later. Write “ai …” to ask Pocket AI.' : conv.kind === 'direct' ? `The start of your chat with ${conv.name}.` : conv.about || `A group of ${conv.members.length}.`}</p>
                  {conv.ai && conv.kind !== 'self' && <p className="muted small">Write <b>@ai</b> to ask Pocket AI here — everyone sees the answer.</p>}</div>}
              {catchup && <div className="catchup-card">
                <div className="row"><Icon name="sparkles" size={15} /><b className="grow">{catchup.unread ? `What you missed (${catchup.count})` : 'Lately'}</b>
                  <button type="button" className="btn ghost xs" onClick={keepCatchup} title="Keep it in Pocket Notes"><Icon name="note" size={13} />Keep</button>
                  <button type="button" className="btn ghost icon-only xs" aria-label="Close" onClick={() => setCatchup(null)}><Icon name="x" size={13} /></button></div>
                <p><RichText text={catchup.summary} /></p>
                {catchup.for_you.length > 0 && <><h5>For you</h5><ul>{catchup.for_you.map((x, i) => <li key={i}>{x}</li>)}</ul></>}
                {catchup.plans.length > 0 && <><h5>Plans</h5><ul>{catchup.plans.map((x, i) => <li key={i}>{x}</li>)}</ul></>}
              </div>}
              {msgs.map((m, i) => {
                const k = dayKey(m.created_at)
                const day = k !== lastDay ? <div className="day-line" key={`d${k}`}><span>{dayLine(m.created_at)}</span></div> : null
                lastDay = k
                const prev = i > 0 && !day ? msgs[i - 1] : undefined
                return <React.Fragment key={m.client_id && m.pending ? m.client_id : m.id}>
                  {day}
                  {m.id === firstUnread.current && unreadAtOpen.current > 0 && <div className="unread-line"><span>New messages</span></div>}
                  <MessageItem m={m} prev={prev} conv={conv} act={act} people={peopleMap} names={names} seen={seenFor(m)} seenLine={m.id === lastMine && conv.kind === 'group'}
                    translation={translations[m.id]} highlight={highlight === m.id} smart={smart} />
                </React.Fragment>
              })}
              {t.length > 0 && <div className="typing-row"><span className="typing-dots"><i /><i /><i /></span>{t.map(x => x.name.split(' ')[0]).join(', ')}</div>}
            </>}
          </div>
        </div>
        {(below > 0 || newer) && <button type="button" className="to-bottom" onClick={toBottom} aria-label="To the newest messages"><Icon name="arrow-down" size={16} />{below > 0 && <span className="tnum">{below}</span>}</button>}
        {!catchup && unreadAtOpen.current >= 8 && conv.ai && <button type="button" className="catchup-hint" onClick={() => { unreadAtOpen.current = 0; runCatchup() }}><Icon name="sparkles" size={14} />{unreadAtOpen.current} new — summarise?</button>}
        <Composer ref={composer} conv={conv} replyTo={replyTo} editing={editing} onCancelReply={() => setReplyTo(null)} onCancelEdit={() => setEditing(null)}
          onSend={(text, atts, kind) => send(text, atts, kind)} onEditSave={saveEdit} onShare={() => setSheet('share')} onPoll={() => setSheet('poll')} onTodo={() => setSheet('todo')} />
        {dragging && <div className="drop-hint"><Icon name="upload" size={22} />Drop to send</div>}
      </div>
      {info && (wide ? <InfoPanel conv={conv} onClose={toggleInfo} onJump={act.jump} /> : <Modal title="Details" onClose={toggleInfo} size="sm" className="info-modal"><InfoPanel conv={conv} onClose={toggleInfo} onJump={mid => { toggleInfo(); act.jump(mid) }} embedded /></Modal>)}
      {make && <MakeSheet m={make.m} target={make.target} prefill={make.prefill} onClose={() => setMake(null)} onMade={madeOne} />}
      {sheet === 'share' && <ShareSheet conv={conv} onClose={() => setSheet(null)} onShared={m => { setSheet(null); stick.current = 'bottom'; upsert(m) }} />}
      {sheet === 'poll' && <PollSheet onClose={() => setSheet(null)} onSend={async poll => { setSheet(null); send('', [], undefined, { kind: 'poll', poll }) }} />}
      {sheet === 'todo' && <TodoSheet conv={conv} onClose={() => setSheet(null)} onSend={async todo => { setSheet(null); send('', [], undefined, { kind: 'todo', todo }) }} />}
      {del && <Confirm title="Delete this message?" confirmLabel="Delete for everyone" message="It disappears for everyone in the conversation, with its photos and files." onCancel={() => setDel(null)}
        onConfirm={() => { const m = del; setDel(null); api.del(`${V1}/messages/${m.id}`).catch(e => toast((e as Error).message, 'err')) }} />}
      {photo && <Modal onClose={() => setPhoto(null)} bare className="lightbox">
        <img src={photo.url} alt={photo.name} />
        <div className="lightbox-bar"><span className="ellipsis">{photo.name}</span><a className="btn sm" href={`${photo.url}?download=1`}><Icon name="download" size={14} />Download</a><button type="button" className="btn sm" onClick={() => setPhoto(null)}>Close</button></div>
      </Modal>}
    </div>
  )
}
