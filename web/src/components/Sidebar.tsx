// Left column: start a chat (and the start menu), catch up, saved and
// search, then every conversation. The Pocket Calendar sidebar.
import { useState } from 'react'
import { Link, NavLink } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { useLive } from '../lib/live'
import { Icon } from './icons'
import { createItems, type CreateKind } from './Layout'
import { ConvList } from './ConvList'
import { type Anchor, Menu, rectOf } from './ui'

export function Sidebar({ onCreate, onNavigate }: { onCreate: (k: CreateKind) => void; onNavigate: () => void }) {
  const { status, settings } = useApp()
  const { unread, mentions, connected, conversations } = useLive()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const [filter, setFilter] = useState('')
  const title = settings?.title || status?.title || 'Pocket Chat'
  const nav: { to: string; label: string; icon: string; end?: boolean; count?: number; warn?: boolean; hint?: string }[] = [
    { to: '/', label: 'Catch up', icon: 'sparkles', end: true, count: mentions || undefined, warn: true, hint: 'Messages that mention you' },
    { to: '/saved', label: 'Saved', icon: 'star' },
    { to: '/search', label: 'Search', icon: 'search' },
  ]
  return (
    <aside className="sidebar" aria-label="Pocket Chat">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Chat' ? title : <>Pocket <b>Chat</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
        <span className={`sb-live ${connected ? 'on' : ''}`} title={connected ? 'Live: new messages arrive at once' : 'Reconnecting…'} />
      </div>
      <div className="create-split">
        <button type="button" className="btn primary create-btn" onClick={() => onCreate('chat')} title="Start a chat (N)">
          <Icon name="message-plus" size={15} strokeWidth={2.1} />New chat
        </button>
        <button type="button" className="btn primary create-more" aria-label="More ways to start" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}>
          <Icon name="chevron-down" size={15} />
        </button>
      </div>
      {menu && <Menu anchor={menu} items={createItems(onCreate)} onClose={() => setMenu(null)} width={260} />}

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(n => (
          <NavLink key={n.to} to={n.to} end={n.end} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}
            {!!n.count && <span className={`sb-count ${n.warn ? 'warn' : ''}`} title={n.hint}>{n.count}</span>}
          </NavLink>
        ))}
      </nav>

      <section className="sb-sec sb-chats">
        <h4 className="sb-h">Chats{unread > 0 && <span className="sb-h-note"> · {unread} unread</span>}</h4>
        {(conversations?.length || 0) > 6 && <label className="sb-filter"><Icon name="search" size={13} /><input value={filter} onChange={e => setFilter(e.target.value)} placeholder="Find a chat" aria-label="Find a chat" /></label>}
        <ConvList onPick={onNavigate} filter={filter} />
      </section>

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pch:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}
