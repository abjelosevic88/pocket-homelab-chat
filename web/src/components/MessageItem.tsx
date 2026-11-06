// One message as it shows in a conversation: text (links, @names, light
// bold), photos and files, a card from another app, a poll, a to-do list, a
// voice message, Pocket AI's answer, suggestions and what you made of it.
import React, { memo, useRef, useState } from 'react'
import { useApp } from '../lib/app-context'
import { appName, fmtBytesShort, fmtDuration, hhmm, MAKE, nameColor, onlyEmoji, pieces, QUICK_EMOJI } from '../lib/chat'
import type { Attachment, Card, Conversation, Hint, MakeTarget, Message, Person, TodoItem } from '../lib/types'
import { Avatar, type MenuItem, Menu, type Anchor, Popover, rectOf, copyText, useToast } from './ui'
import { Icon } from './icons'

export interface MsgActions {
  react: (m: Message, emoji: string) => void
  reply: (m: Message) => void
  edit: (m: Message) => void
  remove: (m: Message) => void
  star: (m: Message) => void
  pin: (m: Message) => void
  make: (m: Message, target: MakeTarget, prefill?: Record<string, unknown>) => void
  translate: (m: Message) => void
  vote: (m: Message, options: number[]) => void
  closePoll: (m: Message, closed: boolean) => void
  todo: (m: Message, body: Record<string, unknown>) => void
  retry: (m: Message) => void
  open: (a: Attachment) => void
  jump: (id: number) => void
}

export function RichText({ text, people }: { text: string; people?: Map<string, number> }) {
  const lines = text.split('\n')
  const out: React.ReactNode[] = []
  let list: React.ReactNode[] = []
  const flush = () => { if (list.length) { out.push(<ul key={`l${out.length}`}>{list}</ul>); list = [] } }
  lines.forEach((line, i) => {
    const li = /^\s*(?:[-•*]|\d+[.)])\s+(.*)$/.exec(line)
    const content = <Inline text={li ? li[1] : line} people={people} />
    if (li) { list.push(<li key={i}>{content}</li>); return }
    flush()
    out.push(<React.Fragment key={i}>{content}{i < lines.length - 1 && <br />}</React.Fragment>)
  })
  flush()
  return <>{out}</>
}

function Inline({ text, people }: { text: string; people?: Map<string, number> }) {
  return <>{pieces(text).map((p, i) => {
    switch (p.t) {
      case 'link': return <a key={i} href={p.v} target="_blank" rel="noreferrer noopener">{p.v.replace(/^https?:\/\/(www\.)?/, '').slice(0, 60)}{p.v.length > 70 ? '…' : ''}</a>
      case 'mention': {
        const known = people?.has(p.v.slice(1).toLowerCase())
        return <span key={i} className={known ? 'mention' : ''}>{p.v}</span>
      }
      case 'bold': return <b key={i}>{p.v}</b>
      case 'code': return <code key={i}>{p.v}</code>
      default: return <React.Fragment key={i}>{p.v}</React.Fragment>
    }
  })}</>
}

function EmojiRow({ onPick }: { onPick: (e: string) => void }) {
  return <span className="emoji-row">{QUICK_EMOJI.map(e => <span key={e} role="button" tabIndex={-1} className="emoji-btn" onClick={ev => { ev.stopPropagation(); onPick(e) }}>{e}</span>)}</span>
}

function Stars({ n }: { n: number }) {
  const r = Math.round(n)
  return <span className="stars" aria-label={`${n} of 5`}>{'★'.repeat(r)}<span className="off">{'★'.repeat(Math.max(0, 5 - r))}</span></span>
}

const CARD_SAVE: Record<string, MakeTarget> = { event: 'event', place: 'place', wish: 'wish', task: 'task' }

function CardView({ card, m, act, mine }: { card: Card; m: Message; act: MsgActions; mine: boolean }) {
  const save = card.kind ? CARD_SAVE[card.kind] : undefined
  const made = m.made?.find(d => d.target === save)
  return (
    <div className={`msg-card kind-${card.kind || 'item'}`}>
      {card.image && <img className="msg-card-img" src={card.image} alt="" loading="lazy" referrerPolicy="no-referrer" onError={e => { (e.target as HTMLElement).style.display = 'none' }} />}
      <div className="msg-card-body">
        <div className="msg-card-app"><span>{card.icon}</span>{appName(card.app)}{card.badge && <span className="badge">{card.badge}</span>}</div>
        <div className="msg-card-title">{card.title}</div>
        {card.subtitle && <div className="msg-card-sub">{card.subtitle}</div>}
        {card.rating ? <Stars n={card.rating} /> : null}
        {card.lines && card.lines.length > 0 && <ul className="msg-card-lines">{card.lines.slice(0, 8).map((l, i) => <li key={i}>{l}</li>)}</ul>}
        {card.text && <p className="msg-card-text">{card.text}</p>}
        <div className="msg-card-actions">
          {card.url && mine && <a className="btn xs" href={card.url} target="_blank" rel="noreferrer">Open<Icon name="external" size={12} /></a>}
          {card.url && !mine && card.kind === 'wish' && <a className="btn xs" href={card.url} target="_blank" rel="noreferrer">Shop page<Icon name="external" size={12} /></a>}
          {save && !mine && (made ? <a className="btn xs ok-btn" href={made.url || undefined} target="_blank" rel="noreferrer"><Icon name="check" size={12} />In your {MAKE[save].label.toLowerCase()}s</a>
            : <button type="button" className="btn xs" onClick={() => act.make(m, save)}><Icon name={MAKE[save].icon} size={12} />{save === 'event' ? 'Add to my calendar' : save === 'place' ? 'Save to my places' : save === 'wish' ? 'Add to my wishlist' : 'Add to my tasks'}</button>)}
        </div>
      </div>
    </div>
  )
}

function Poll({ m, act, me, people }: { m: Message; act: MsgActions; me: number; people: Map<number, Person> }) {
  const opts = m.data.options || []
  const votes = m.votes || []
  const mine = votes.filter(v => v.user_id === me).map(v => v.option)
  const voters = new Set(votes.map(v => v.user_id)).size
  const max = Math.max(1, ...opts.map((_, i) => votes.filter(v => v.option === i).length))
  const pick = (i: number) => {
    if (m.data.closed) return
    const next = m.data.multi ? (mine.includes(i) ? mine.filter(x => x !== i) : [...mine, i]) : (mine.includes(i) ? [] : [i])
    act.vote(m, next)
  }
  return (
    <div className="poll">
      <div className="poll-q"><Icon name="poll" size={15} />{m.data.question}</div>
      <div className="poll-opts">
        {opts.map((o, i) => {
          const who = votes.filter(v => v.option === i)
          const top = who.length > 0 && who.length === max
          return (
            <button key={i} type="button" className={`poll-opt ${mine.includes(i) ? 'mine' : ''} ${top ? 'top' : ''}`} onClick={() => pick(i)} disabled={m.data.closed}>
              <span className="poll-bar" style={{ width: `${(who.length / Math.max(1, voters)) * 100}%` }} />
              <span className={`poll-check ${m.data.multi ? 'multi' : ''}`}>{mine.includes(i) && <Icon name="check" size={11} strokeWidth={3} />}</span>
              <span className="grow poll-label">{o}</span>
              <span className="poll-who">{who.slice(0, 3).map(v => <Avatar key={v.user_id} name={people.get(v.user_id)?.name} email={people.get(v.user_id)?.email} size={18} />)}</span>
              <span className="poll-n tnum">{who.length}</span>
            </button>
          )
        })}
      </div>
      <div className="poll-foot muted small">
        {voters} {voters === 1 ? 'vote' : 'votes'}{m.data.multi ? ' · pick any' : ''}{m.data.closed ? ' · closed' : ''}
        {m.user_id === me && <button type="button" className="link-btn" onClick={() => act.closePoll(m, !m.data.closed)}>{m.data.closed ? 'Open again' : 'Close the poll'}</button>}
      </div>
    </div>
  )
}

function Todo({ m, act, people }: { m: Message; act: MsgActions; people: Map<number, Person> }) {
  const items = m.data.items || []
  const [adding, setAdding] = useState('')
  const done = items.filter(i => i.done).length
  const add = (e: React.FormEvent) => { e.preventDefault(); if (adding.trim()) { act.todo(m, { op: 'add', text: adding.trim() }); setAdding('') } }
  return (
    <div className="todo">
      <div className="todo-head"><Icon name="check-square" size={15} /><b className="grow">{m.data.title || 'To-do list'}</b><span className="muted small tnum">{done}/{items.length}</span></div>
      <div className="todo-progress"><span style={{ width: `${items.length ? (done / items.length) * 100 : 0}%` }} /></div>
      <ul className="todo-items">
        {items.map((it: TodoItem) => {
          const who = it.assignee ? people.get(it.assignee) : undefined
          const by = it.done_by ? people.get(it.done_by) : undefined
          return (
            <li key={it.id} className={it.done ? 'done' : ''}>
              <button type="button" className={`todo-check ${it.done ? 'on' : ''}`} aria-label={it.done ? 'Not done' : 'Done'} onClick={() => act.todo(m, { op: 'toggle', item: it.id, done: !it.done })}>{it.done && <Icon name="check" size={12} strokeWidth={3} />}</button>
              <span className="grow todo-text" title={by ? `Done by ${by.name}` : undefined}>{it.text}</span>
              {who && <span className="todo-who" title={`For ${who.name}`}><Avatar name={who.name} email={who.email} size={18} /></span>}
              <button type="button" className="todo-x" aria-label="Remove" onClick={() => act.todo(m, { op: 'remove', item: it.id })}><Icon name="x" size={12} /></button>
            </li>
          )
        })}
      </ul>
      <form onSubmit={add} className="todo-add"><Icon name="plus" size={13} /><input value={adding} onChange={e => setAdding(e.target.value)} placeholder="Add an item" aria-label="Add an item" /></form>
    </div>
  )
}

function Files({ list, act }: { list: Attachment[]; act: MsgActions }) {
  const imgs = list.filter(a => a.mime.startsWith('image/'))
  const audio = list.filter(a => a.mime.startsWith('audio/'))
  const other = list.filter(a => !a.mime.startsWith('image/') && !a.mime.startsWith('audio/'))
  return <>
    {imgs.length > 0 && <div className={`msg-photos n${Math.min(imgs.length, 4)}`}>
      {imgs.slice(0, 4).map((a, i) => (
        <button key={a.id} type="button" className="msg-photo" onClick={() => act.open(a)} aria-label={`Open ${a.name}`}>
          <img src={a.url} alt={a.name} loading="lazy" style={imgs.length === 1 && a.width && a.height ? { aspectRatio: `${a.width} / ${a.height}` } : undefined} />
          {i === 3 && imgs.length > 4 && <span className="msg-photo-more">+{imgs.length - 4}</span>}
        </button>
      ))}
    </div>}
    {audio.map(a => <audio key={a.id} controls preload="none" src={a.url} className="msg-audio" />)}
    {other.map(a => (
      <a key={a.id} className="msg-file" href={`${a.url}?download=1`} title={`Download ${a.name}`}>
        <span className="msg-file-ico"><Icon name="file" size={18} /></span>
        <span className="grow ellipsis"><b className="ellipsis">{a.name}</b><span className="muted small">{fmtBytesShort(a.bytes)}</span></span>
        <Icon name="download" size={15} />
      </a>
    ))}
  </>
}

function HintChips({ m, act }: { m: Message; act: MsgActions }) {
  const hints = m.data.hints || []
  const made = new Set((m.made || []).map(d => d.target))
  const shown = hints.filter(h => !made.has(h.kind === 'event' ? 'event' : h.kind === 'expense' ? 'expense' : 'task'))
  if (!shown.length) return null
  const chip = (h: Hint, i: number) => {
    if (h.kind === 'event') return <button key={i} type="button" className="hint-chip" onClick={() => act.make(m, 'event', { date: h.date, time: h.time })}><Icon name="calendar-plus" size={12} />Add to calendar · {h.label}</button>
    if (h.kind === 'expense') return <button key={i} type="button" className="hint-chip" onClick={() => act.make(m, 'expense', { amount: h.amount, currency: h.currency, title: h.title })}><Icon name="receipt" size={12} />Split {h.amount?.replace(/\.00$/, '')} {h.currency}</button>
    return <button key={i} type="button" className="hint-chip" onClick={() => act.make(m, 'task', { title: h.title })}><Icon name="check-square" size={12} />Make a task</button>
  }
  return <div className="hint-chips">{shown.map(chip)}</div>
}

export interface ItemProps {
  m: Message
  prev?: Message
  conv: Conversation
  act: MsgActions
  people: Map<number, Person>
  names: Map<string, number>
  seen?: string
  seenLine?: boolean
  translation?: string
  highlight?: boolean
  smart: boolean
}

export const MessageItem = memo(function MessageItem({ m, prev, conv, act, people, names, seen, seenLine, translation, highlight, smart }: ItemProps) {
  const { user } = useApp()
  const { toast } = useToast()
  const me = user?.id || 0
  const [menu, setMenu] = useState<Anchor | null>(null)
  const [emoji, setEmoji] = useState<Anchor | null>(null)
  const press = useRef<number>(0)
  if (m.kind === 'system') return <div className="msg-system"><span>{m.body}</span></div>

  const mine = m.user_id === me && m.user_id !== 0
  const ai = m.kind === 'ai'
  const grouped = !!prev && prev.kind !== 'system' && prev.user_id === m.user_id && prev.author_name === m.author_name && prev.kind !== 'ai' && !ai
    && new Date(m.created_at).getTime() - new Date(prev.created_at).getTime() < 5 * 60e3 && new Date(prev.created_at).getDate() === new Date(m.created_at).getDate()
  const showName = !mine && !grouped && (conv.kind === 'group' || ai || m.kind === 'app' || !!m.data.imported)
  const author = people.get(m.user_id)
  const big = m.kind === 'text' && !m.attachments.length && !m.reply && onlyEmoji(m.body)
  const card = m.data.card
  const canEdit = mine && !m.deleted && ['text', 'card', 'app'].includes(m.kind) && !m.source
  const canDelete = !m.deleted && (mine || conv.role === 'owner' || conv.kind === 'self')
  const text = m.body || (m.kind === 'voice' ? m.data.text || '' : '')

  const items = (): MenuItem[] => [
    { label: <EmojiRow onPick={e => { act.react(m, e); setMenu(null) }} />, onSelect: () => act.react(m, QUICK_EMOJI[0]) },
    'sep',
    { label: 'Reply', icon: 'reply', onSelect: () => act.reply(m) },
    ...(text ? [{ label: 'Copy text', icon: 'copy', onSelect: async () => { if (await copyText(text)) toast('Copied') } }] : []),
    { label: m.starred ? 'Remove from Saved' : 'Save', icon: 'star', onSelect: () => act.star(m) },
    { label: m.pinned_at ? 'Unpin' : 'Pin for everyone', icon: 'pin', onSelect: () => act.pin(m) },
    ...(text ? [{ label: 'Translate', icon: 'languages', onSelect: () => act.translate(m) }] : []),
    'sep',
    { label: 'Make…', icon: 'sparkles', submenu: (Object.keys(MAKE) as MakeTarget[]).map(t => ({ label: MAKE[t].verb, icon: MAKE[t].icon, onSelect: () => act.make(m, t) })) },
    ...(canEdit || canDelete ? ['sep' as const] : []),
    ...(canEdit ? [{ label: 'Edit', icon: 'edit', onSelect: () => act.edit(m) }] : []),
    ...(canDelete ? [{ label: conv.kind === 'self' ? 'Delete' : mine ? 'Delete for everyone' : 'Delete (you own the group)', icon: 'trash', danger: true, onSelect: () => act.remove(m) }] : []),
  ]
  const openMenu = (e: React.MouseEvent) => { if (e.shiftKey || m.deleted || m.pending) return; e.preventDefault(); setMenu({ x: e.clientX, y: e.clientY, w: 0, h: 0 }) }
  const touchStart = (e: React.TouchEvent) => {
    const t = e.touches[0]
    press.current = window.setTimeout(() => { if (!m.deleted && !m.pending) { navigator.vibrate?.(10); setMenu({ x: t.clientX, y: t.clientY, w: 0, h: 0 }) } }, 450)
  }
  const touchEnd = () => window.clearTimeout(press.current)

  const bubbleKind = ai ? 'ai' : card ? 'card' : m.kind
  return (
    <div className={`msg ${mine ? 'mine' : 'theirs'} ${grouped ? 'grouped' : ''} ${highlight ? 'highlight' : ''} ${m.pending ? 'pending' : ''}`} data-id={m.id} id={`m${m.id}`}>
      {!mine && conv.kind === 'group' || (!mine && ai) ? <div className="msg-ava">{!grouped && (ai ? <span className="ai-ava"><Icon name="sparkles" size={15} /></span>
        : m.kind === 'app' ? <span className="ai-ava app"><Icon name="layers" size={14} /></span>
        : <Avatar name={author?.name || m.author_name} email={author?.email} size={30} />)}</div> : null}
      <div className="msg-col">
        {showName && <div className="msg-name" style={{ color: ai ? 'var(--accent)' : nameColor(m.user_id || m.author_name.length) }}>
          {ai ? 'Pocket AI' : m.kind === 'app' ? appName(m.source) : m.author_name || author?.name}
          {m.data.imported && <span className="msg-tag">WhatsApp</span>}
        </div>}
        <div className={`bubble kind-${bubbleKind} ${big ? 'big-emoji' : ''} ${m.deleted ? 'deleted' : ''}`} onContextMenu={openMenu} onTouchStart={touchStart} onTouchEnd={touchEnd} onTouchMove={touchEnd}>
          {m.reply && <button type="button" className="msg-quote" onClick={() => act.jump(m.reply!.id)}>
            <b>{m.reply.user_id === me ? 'You' : m.reply.author_name || 'Someone'}</b><span className="ellipsis">{m.reply.deleted ? 'Message deleted' : m.reply.body || (m.reply.kind === 'poll' ? '📊 Poll' : m.reply.kind === 'todo' ? '☑️ To-do list' : m.reply.kind === 'voice' ? '🎤 Voice message' : 'A card or a photo')}</span>
          </button>}
          {m.deleted ? <span className="muted"><Icon name="x-circle" size={13} /> This message was deleted</span> : <>
            {m.attachments.length > 0 && <Files list={m.attachments} act={act} />}
            {m.kind === 'poll' && <Poll m={m} act={act} me={me} people={people} />}
            {m.kind === 'todo' && <Todo m={m} act={act} people={people} />}
            {m.kind === 'voice' && <div className="voice-text">{m.data.text ? <RichText text={m.data.text} /> : <span className="muted small">{m.attachments[0]?.duration_ms ? fmtDuration(m.attachments[0].duration_ms) + ' · ' : ''}voice message</span>}</div>}
            {m.body && m.kind !== 'voice' && <div className="msg-text"><RichText text={m.body} people={names} /></div>}
            {card && <CardView card={card} m={m} act={act} mine={mine} />}
            {m.data.preview && !card && (
              <a className="link-preview" href={m.data.preview.url} target="_blank" rel="noreferrer noopener">
                {m.data.preview.image && <img src={m.data.preview.image} alt="" loading="lazy" referrerPolicy="no-referrer" onError={e => { (e.target as HTMLElement).style.display = 'none' }} />}
                <span className="link-preview-body"><span className="muted small">{m.data.preview.site}</span><b>{m.data.preview.title}</b>{m.data.preview.description && <span className="small">{m.data.preview.description}</span>}</span>
              </a>
            )}
            {translation && <div className="msg-translation"><Icon name="languages" size={12} /> {translation}</div>}
          </>}
          <span className="msg-meta">
            {m.pinned_at && <Icon name="pin" size={11} />}
            {m.starred && <Icon name="star" size={11} />}
            {m.edited_at && <span>edited</span>}
            <span className="tnum">{hhmm(m.created_at)}</span>
            {mine && !m.pending && !m.failed && seen !== undefined && <span className={`ticks ${seen ? 'seen' : ''}`} title={seen || 'Sent'}><Icon name={seen ? 'check-double' : 'check'} size={13} /></span>}
            {m.pending && !m.failed && <Icon name="clock" size={11} />}
          </span>
        </div>
        {m.failed && <div className="msg-failed"><Icon name="alert" size={13} />Not sent: {m.failed} <button type="button" className="link-btn" onClick={() => act.retry(m)}>Try again</button></div>}
        {ai && (m.data.used?.length || m.data.asked_by) ? <div className="msg-ai-foot muted small">
          {m.data.asked_by ? `Asked by ${m.data.asked_by === me ? 'you' : people.get(m.data.asked_by)?.name || 'someone'}` : ''}
          {m.data.used?.length ? ` · looked at ${m.data.used.join(', ')}` : ''}
        </div> : null}
        {m.reactions.length > 0 && <div className="reactions">
          {m.reactions.map(r => (
            <button key={r.emoji} type="button" className={`reaction ${r.users.includes(me) ? 'mine' : ''}`} title={r.users.map(u => people.get(u)?.name || '').join(', ')} onClick={() => act.react(m, r.emoji)}>
              <span>{r.emoji}</span>{r.users.length > 1 && <span className="tnum">{r.users.length}</span>}
            </button>
          ))}
        </div>}
        {m.made && m.made.length > 0 && <div className="made-chips">{m.made.map(d => (
          <a key={d.id} className="made-chip" href={d.url || undefined} target="_blank" rel="noreferrer" title={`Only you see this. ${d.title}`}><Icon name="check" size={11} strokeWidth={2.6} />{MAKE[d.target]?.label || d.target} in {appName(d.app)}</a>
        ))}</div>}
        {smart && !m.deleted && !m.pending && <HintChips m={m} act={act} />}
        {seen && seenLine && <div className="msg-seen muted small">{seen}</div>}
      </div>
      {!m.deleted && !m.pending && <div className="msg-tools" role="toolbar" aria-label="Message actions">
        <button type="button" className="btn ghost icon-only xs" aria-label="React" title="React" onClick={e => setEmoji(rectOf(e.currentTarget))}><Icon name="smile" size={15} /></button>
        <button type="button" className="btn ghost icon-only xs" aria-label="Reply" title="Reply" onClick={() => act.reply(m)}><Icon name="reply" size={15} /></button>
        <button type="button" className="btn ghost icon-only xs" aria-label="More" title="More" onClick={e => setMenu(rectOf(e.currentTarget))}><Icon name="more" size={15} /></button>
      </div>}
      {menu && <Menu anchor={menu} items={items()} onClose={() => setMenu(null)} width={240} align={mine ? 'right' : 'left'} />}
      {emoji && <Popover anchor={emoji} onClose={() => setEmoji(null)} width={260} prefer="below" sheetOnMobile={false} label="React" className="emoji-pop"><EmojiRow onPick={e => { act.react(m, e); setEmoji(null) }} /></Popover>}
    </div>
  )
})
