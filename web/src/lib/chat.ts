// What every chat screen needs: app names and icons, times, names.
import type { AppSlug, Conversation, MakeTarget, Message } from './types'

export const APP_INFO: Record<AppSlug, { name: string; icon: string }> = {
  tasks: { name: 'Pocket Tasks', icon: 'check-square' },
  calendar: { name: 'Pocket Calendar', icon: 'calendar' },
  notes: { name: 'Pocket Notes', icon: 'note' },
  split: { name: 'Pocket Split', icon: 'receipt' },
  wishlist: { name: 'Pocket Wishlist', icon: 'gift' },
  places: { name: 'Pocket Places', icon: 'pin' },
  voice: { name: 'Pocket Voice', icon: 'mic' },
  kitchen: { name: 'Pocket Kitchen', icon: 'utensils' },
  travel: { name: 'Pocket Travel', icon: 'plane' },
  parcels: { name: 'Pocket Parcels', icon: 'package' },
  weather: { name: 'Pocket Weather', icon: 'cloud' },
  bookshelf: { name: 'Pocket Bookshelf', icon: 'journal' },
  watch: { name: 'Pocket Watch', icon: 'video' },
}

export const appName = (slug?: string) => (slug && APP_INFO[slug as AppSlug]?.name) || (slug ? 'Pocket ' + slug[0].toUpperCase() + slug.slice(1) : '')

export const MAKE: Record<MakeTarget, { label: string; icon: string; app: AppSlug; verb: string }> = {
  task: { label: 'Task', icon: 'check-square', app: 'tasks', verb: 'Make a task' },
  event: { label: 'Event', icon: 'calendar-plus', app: 'calendar', verb: 'Add to calendar' },
  note: { label: 'Note', icon: 'note', app: 'notes', verb: 'Keep as a note' },
  expense: { label: 'Split cost', icon: 'receipt', app: 'split', verb: 'Split this cost' },
  wish: { label: 'Wish', icon: 'gift', app: 'wishlist', verb: 'Add to wishlist' },
  place: { label: 'Place', icon: 'pin', app: 'places', verb: 'Save the place' },
}

export const QUICK_EMOJI = ['👍', '❤️', '😂', '😮', '😢', '🙏']

const sameDay = (a: Date, b: Date) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()

export function hhmm(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

/** A list time: 14:05 today, "Yesterday", Tue this week, 3 Nov before. */
export function listTime(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  const now = new Date()
  if (sameDay(d, now)) return hhmm(iso)
  const y = new Date(now); y.setDate(now.getDate() - 1)
  if (sameDay(d, y)) return 'Yesterday'
  if (now.getTime() - d.getTime() < 6 * 86400e3) return d.toLocaleDateString([], { weekday: 'short' })
  return d.toLocaleDateString([], { day: 'numeric', month: 'short', ...(d.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}) })
}

/** The line between days in a conversation. */
export function dayLine(iso: string): string {
  const d = new Date(iso)
  const now = new Date()
  if (sameDay(d, now)) return 'Today'
  const y = new Date(now); y.setDate(now.getDate() - 1)
  if (sameDay(d, y)) return 'Yesterday'
  return d.toLocaleDateString([], { weekday: 'long', day: 'numeric', month: 'long', ...(d.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}) })
}

export const dayKey = (iso: string) => { const d = new Date(iso); return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}` }

export function seenAgo(iso?: string): string {
  if (!iso) return ''
  const s = (Date.now() - new Date(iso).getTime()) / 1000
  if (s < 90) return 'just now'
  if (s < 3600) return `${Math.round(s / 60)} min ago`
  if (s < 86400) return `${Math.round(s / 3600)} h ago`
  return listTime(iso)
}

/** A message in a few words, for lists and replies. */
export function preview(m?: Message | null, me?: number): string {
  if (!m) return ''
  if (m.deleted) return 'Message deleted'
  const who = m.kind === 'system' ? '' : m.user_id === me && me ? 'You: ' : ''
  let t = m.body
  if (!t) {
    if (m.kind === 'poll') t = '📊 ' + (m.data.question || 'Poll')
    else if (m.kind === 'todo') t = '☑️ ' + (m.data.title || 'To-do list')
    else if (m.kind === 'voice') t = '🎤 Voice message'
    else if (m.data.card) t = (m.data.card.icon || '🔗') + ' ' + m.data.card.title
    else if (m.attachments?.length) t = m.attachments[0].mime.startsWith('image/') ? '📷 Photo' : '📎 ' + m.attachments[0].name
  }
  return who + t.replace(/\s+/g, ' ').trim()
}

export const convName = (c?: Conversation | null) => c?.name || c?.title || ''

export function fmtBytesShort(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

export function fmtDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

/** A stable colour for a person's name in a group. */
const NAME_COLORS = ['#0f766e', '#b45309', '#7c3aed', '#be185d', '#1d4ed8', '#15803d', '#c2410c', '#4338ca']
export function nameColor(id: number): string { return NAME_COLORS[Math.abs(id) % NAME_COLORS.length] }

/** Text with links and @mentions made into pieces (no HTML from messages, ever). */
export type Piece = { t: 'text' | 'link' | 'mention' | 'bold' | 'code'; v: string }
const TOKEN = /(https?:\/\/[^\s<>"'()]+)|(@[\p{L}\p{N}_.-]+)|(\*\*[^*\n]+\*\*)|(`[^`\n]+`)/gu
export function pieces(text: string): Piece[] {
  const out: Piece[] = []
  let last = 0
  for (const m of text.matchAll(TOKEN)) {
    const i = m.index ?? 0
    if (i > last) out.push({ t: 'text', v: text.slice(last, i) })
    if (m[1]) { const u = m[1].replace(/[.,;:!?]+$/, ''); out.push({ t: 'link', v: u }); if (u.length < m[1].length) out.push({ t: 'text', v: m[1].slice(u.length) }) }
    else if (m[2]) out.push({ t: 'mention', v: m[2] })
    else if (m[3]) out.push({ t: 'bold', v: m[3].slice(2, -2) })
    else if (m[4]) out.push({ t: 'code', v: m[4].slice(1, -1) })
    last = i + m[0].length
  }
  if (last < text.length) out.push({ t: 'text', v: text.slice(last) })
  return out
}

export const onlyEmoji = (t: string) => t.length <= 12 && /^(\p{Extended_Pictographic}|\p{Emoji_Component}|‍|️|\s)+$/u.test(t) && /\p{Extended_Pictographic}/u.test(t)

export const newClientId = () => Math.random().toString(36).slice(2) + Date.now().toString(36)
