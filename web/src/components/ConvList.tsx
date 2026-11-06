// The list of conversations (sidebar, and the phone's home), and their pictures.
import { useState } from 'react'
import { NavLink } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { useLive } from '../lib/live'
import { listTime, preview } from '../lib/chat'
import type { Conversation } from '../lib/types'
import { Avatar } from './ui'
import { Icon } from './icons'

export function ConvAvatar({ c, size = 36 }: { c: Conversation; size?: number }) {
  const { user } = useApp()
  if (c.kind === 'self') return <span className="conv-ava self" style={{ width: size, height: size }} aria-hidden="true"><Icon name="bookmark" size={Math.round(size * 0.48)} /></span>
  if (c.kind === 'direct') {
    const other = c.members.find(m => m.user_id !== user?.id)
    return <span className="conv-ava-wrap" style={{ width: size, height: size }}>
      <Avatar name={other?.name} email={other?.email} size={size} />
      {other && c.online.includes(other.user_id) && <span className="online-dot" title="Online now" />}
    </span>
  }
  return <span className="conv-ava group" style={{ width: size, height: size, background: c.color || 'var(--accent)', fontSize: Math.round(size * 0.5) }} aria-hidden="true">
    {c.emoji || <Icon name="users" size={Math.round(size * 0.46)} />}
  </span>
}

const muted = (c: Conversation) => !!c.muted_until && new Date(c.muted_until) > new Date()

export function ConvList({ onPick, filter = '' }: { onPick?: () => void; filter?: string }) {
  const { conversations, typing } = useLive()
  const { user } = useApp()
  const [archived, setArchived] = useState(false)
  if (!conversations) return <div className="conv-list-empty muted small">Loading…</div>
  const q = filter.trim().toLowerCase()
  const match = (c: Conversation) => !q || c.name.toLowerCase().includes(q) || c.members.some(m => m.name.toLowerCase().includes(q))
  const live = conversations.filter(c => !c.archived && match(c))
  const arch = conversations.filter(c => c.archived && match(c))
  const shown = archived ? arch : live
  return (
    <div className="conv-list" role="list">
      {shown.map(c => {
        const t = typing[c.id]?.filter(x => x.user_id !== user?.id)
        return (
          <NavLink key={c.id} to={`/c/${c.id}`} onClick={onPick} role="listitem" className={({ isActive }) => `conv-row ${isActive ? 'active' : ''} ${c.unread ? 'unread' : ''}`}>
            <ConvAvatar c={c} size={38} />
            <span className="conv-row-main">
              <span className="conv-row-top">
                <b className="ellipsis">{c.name}</b>
                {c.pinned && <Icon name="pin" size={12} className="conv-row-flag" />}
                {muted(c) && <Icon name="bell-off" size={12} className="conv-row-flag" />}
                <span className="conv-row-time tnum">{listTime(c.last_message_at || c.created_at)}</span>
              </span>
              <span className="conv-row-bottom">
                <span className={`ellipsis conv-row-last ${t?.length ? 'typing' : ''}`}>
                  {t?.length ? `${t[0].name.split(' ')[0]} is writing…` : c.draft ? <><span className="draft-mark">Draft:</span> {c.draft}</> : preview(c.last, user?.id) || (c.kind === 'self' ? 'Notes to yourself, links, things to file later' : 'No messages yet')}
                </span>
                {c.mentioned > 0 && <span className="conv-badge at" title="You are mentioned">@</span>}
                {c.unread > 0 && <span className={`conv-badge ${muted(c) ? 'muted' : ''}`} title={`${c.unread} unread`}>{c.unread > 99 ? '99+' : c.unread}</span>}
              </span>
            </span>
          </NavLink>
        )
      })}
      {shown.length === 0 && <div className="conv-list-empty muted small">{q ? 'Nothing matches.' : archived ? 'Nothing archived.' : 'No conversations yet.'}</div>}
      {(arch.length > 0 || archived) && <button type="button" className="conv-archived-toggle" onClick={() => setArchived(a => !a)}>
        <Icon name={archived ? 'chevron-left' : 'archive'} size={14} />{archived ? 'Back to chats' : `Archived (${arch.length})`}
      </button>}
    </div>
  )
}
