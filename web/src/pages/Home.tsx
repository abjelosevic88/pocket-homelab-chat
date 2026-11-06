// Catch up: what waits for you (unread, who mentioned you), Pocket AI's
// summary of everything you missed, plans spotted in your chats today and
// what you made from messages. On a phone the conversations come first.
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useLive } from '../lib/live'
import { appName, listTime, MAKE } from '../lib/chat'
import type { Catchup, Made } from '../lib/types'
import { ConvAvatar, ConvList } from '../components/ConvList'
import { openCreate } from '../components/Layout'
import { RichText } from '../components/MessageItem'
import { useAsync, useIsMobile, useToast } from '../components/ui'
import { Icon } from '../components/icons'

interface Summary {
  unread: { messages: number; conversations: number; mentions: number }
  mentions: { id: number; conversation_id: number; conversation: string; from: string; text: string; at: string }[]
}
interface Day { plans: { conversation: string; from: string; text: string; date: string; time: string; message_id: number; conversation_id: number }[]; summary: string; messages: number }

const greeting = () => { const h = new Date().getHours(); return h < 5 ? 'Good night' : h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening' }
const today = () => { const d = new Date(); return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}` }

export default function Home() {
  const { user, settings } = useApp()
  const { conversations, unread } = useLive()
  const { toast } = useToast()
  const mobile = useIsMobile()
  const { data: sum } = useAsync(() => api.get<Summary>(`${V1}/summary`), [unread])
  const { data: day } = useAsync(() => api.get<Day>(`${V1}/day/${today()}`), [])
  const { data: made } = useAsync(() => api.get<Made[]>(`${V1}/made`), [])
  const [cu, setCu] = useState<Catchup | null>(null)
  const [busy, setBusy] = useState(false)
  const waiting = (conversations || []).filter(c => c.unread > 0 && !c.archived)
  const first = (user?.name || '').split(' ')[0]

  const catchUp = async () => {
    setBusy(true)
    try { setCu(await api.get<Catchup>(`${V1}/catchup`)) } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }

  const summaryCard = (
    <section className="home-card catchup">
      <div className="row"><span className="home-ico ai"><Icon name="sparkles" size={16} /></span><div className="grow"><h3>What did I miss?</h3>
        <p className="muted small">{unread ? `${unread} unread in ${waiting.length} ${waiting.length === 1 ? 'conversation' : 'conversations'}${sum?.unread.mentions ? `, ${sum.unread.mentions} naming you` : ''}.` : 'You are all caught up.'}</p></div>
        {unread > 0 && <button type="button" className="btn primary sm" onClick={catchUp} disabled={busy}>{busy ? 'Reading…' : 'Summarise'}</button>}
      </div>
      {cu && <div className="catchup-body">
        <p><RichText text={cu.summary} /></p>
        {cu.for_you.length > 0 && <><h5>For you</h5><ul>{cu.for_you.map((x, i) => <li key={i}>{x}</li>)}</ul></>}
        {cu.plans.length > 0 && <><h5>Plans</h5><ul>{cu.plans.map((x, i) => <li key={i}>{x}</li>)}</ul></>}
      </div>}
    </section>
  )

  if (mobile) return (
    <div className="home-mobile">
      {unread > 0 && summaryCard}
      <ConvList />
    </div>
  )

  return (
    <div className="home">
      <header className="home-head">
        <h2>{greeting()}{first ? `, ${first}` : ''}</h2>
        <p className="muted">{day?.messages ? day.summary : 'A quiet day so far.'}</p>
      </header>
      <div className="home-grid">
        <div className="stack">
          {summaryCard}
          {sum && sum.mentions.length > 0 && <section className="home-card">
            <h4><Icon name="at" size={15} />Mentioned you</h4>
            {sum.mentions.map(m => (
              <Link key={m.id} to={`/c/${m.conversation_id}?m=${m.id}`} className="home-row">
                <span className="grow"><b>{m.from}</b> <span className="muted small">in {m.conversation}</span><span className="ellipsis block-line">{m.text}</span></span>
                <span className="muted small tnum">{listTime(m.at)}</span>
              </Link>
            ))}
          </section>}
          {waiting.length > 0 && <section className="home-card">
            <h4><Icon name="message" size={15} />Waiting for you</h4>
            {waiting.map(c => (
              <Link key={c.id} to={`/c/${c.id}`} className="home-row">
                <ConvAvatar c={c} size={32} />
                <span className="grow"><b>{c.name}</b><span className="ellipsis block-line muted small">{c.last ? `${c.last.author_name ? c.last.author_name.split(' ')[0] + ': ' : ''}${c.last.body || 'a card or a photo'}` : ''}</span></span>
                <span className="conv-badge">{c.unread}</span>
              </Link>
            ))}
          </section>}
          {(conversations || []).length <= 1 && <section className="home-card">
            <h4><Icon name="message-plus" size={15} />Start</h4>
            <p className="muted small">Chat with someone on this box, make a group for the family, or bring a chat from WhatsApp.</p>
            <div className="row wrap"><button type="button" className="btn primary sm" onClick={() => openCreate('chat')}>Chat with someone</button><button type="button" className="btn sm" onClick={() => openCreate('group')}>New group</button><button type="button" className="btn sm" onClick={() => openCreate('import')}>Bring a WhatsApp chat</button></div>
          </section>}
        </div>
        <div className="stack">
          <section className="home-card">
            <h4><Icon name="calendar" size={15} />Plans in your chats today</h4>
            {day?.plans.length ? day.plans.map(p => (
              <Link key={p.message_id} to={`/c/${p.conversation_id}?m=${p.message_id}`} className="home-row">
                <span className="home-date"><b>{new Date(p.date + 'T12:00').toLocaleDateString([], { weekday: 'short' })}</b><span>{new Date(p.date + 'T12:00').getDate()}</span></span>
                <span className="grow"><span className="ellipsis block-line">{p.text}</span><span className="muted small">{p.from} · {p.conversation}{p.time ? ` · ${p.time}` : ''}</span></span>
              </Link>
            )) : <p className="muted small">When someone suggests a day and a time, it shows here with “Add to calendar” under the message.</p>}
          </section>
          <section className="home-card">
            <h4><Icon name="layers" size={15} />Made from messages</h4>
            {made?.length ? made.slice(0, 8).map(d => (
              <a key={d.id} className="home-row" href={d.url || undefined} target="_blank" rel="noreferrer">
                <span className="home-ico"><Icon name={MAKE[d.target]?.icon || 'check'} size={14} /></span>
                <span className="grow"><span className="ellipsis block-line">{d.title || MAKE[d.target]?.label}</span><span className="muted small">{MAKE[d.target]?.label} in {appName(d.app)} · {listTime(d.created_at)}</span></span>
              </a>
            )) : <p className="muted small">Tap a message’s ⋯ → <b>Make…</b> to turn it into a task, an event, a note, a split cost, a wish or a place in your other Pocket apps.</p>}
            {settings && !settings.core_configured && <p className="muted small">The other Pocket apps connect through Pocket Core (or directly) in <Link to="/settings/apps">Settings → Connected apps</Link>.</p>}
          </section>
        </div>
      </div>
    </div>
  )
}
