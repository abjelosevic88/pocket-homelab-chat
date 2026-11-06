// The person's conversations, the people on the box and the live stream
// (Server-Sent Events) that keeps them current, shared by every page.
import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { api, V1 } from './api'
import { useApp } from './app-context'
import type { Conversation, Message, Person } from './types'

export type LiveEvent =
  | { type: 'message' | 'message_update'; data: Message }
  | { type: 'read'; data: { conversation_id: number; user_id: number; last_read_id: number } }
  | { type: 'typing'; data: { conversation_id: number; user_id: number; name: string } }
  | { type: 'conversation'; data: { id: number; deleted?: boolean } }
  | { type: 'presence'; data: { user_id: number; online: boolean; seen: string } }
  | { type: 'resync'; data: null }

interface Typing { user_id: number; name: string; until: number }

interface LiveState {
  conversations: Conversation[] | null
  people: Person[]
  connected: boolean
  typing: Record<number, Typing[]>
  reload: () => Promise<void>
  patch: (id: number, p: Partial<Conversation>) => void
  setActive: (id: number) => void
  subscribe: (fn: (e: LiveEvent) => void) => () => void
  unread: number
  mentions: number
}

const Ctx = createContext<LiveState>(null as unknown as LiveState)
export function useLive() { return useContext(Ctx) }

/** Live events for a component (a conversation listening to its messages). */
export function useLiveEvents(fn: (e: LiveEvent) => void) {
  const { subscribe } = useLive()
  const ref = useRef(fn)
  ref.current = fn
  useEffect(() => subscribe(e => ref.current(e)), [subscribe])
}

function sortConvs(list: Conversation[]): Conversation[] {
  return [...list].sort((a, b) => (Number(b.pinned) - Number(a.pinned)) || (b.last_message_at || b.created_at).localeCompare(a.last_message_at || a.created_at) || b.id - a.id)
}

export function LiveProvider({ children }: { children: React.ReactNode }) {
  const { user, settings } = useApp()
  const me = user?.id || 0
  const [conversations, setConversations] = useState<Conversation[] | null>(null)
  const [people, setPeople] = useState<Person[]>([])
  const [connected, setConnected] = useState(false)
  const [typing, setTyping] = useState<Record<number, Typing[]>>({})
  const listeners = useRef(new Set<(e: LiveEvent) => void>())
  const active = useRef(0)
  const reloadTimer = useRef<number>(0)

  const reload = useCallback(async () => {
    try {
      const [c, p] = await Promise.all([api.get<Conversation[]>(`${V1}/conversations`), api.get<Person[]>(`${V1}/people`)])
      setConversations(c)
      setPeople(p)
    } catch { setConversations(x => x || []) }
  }, [])
  const reloadSoon = useCallback(() => { window.clearTimeout(reloadTimer.current); reloadTimer.current = window.setTimeout(reload, 250) }, [reload])
  useEffect(() => { reload() }, [reload])

  const emit = useCallback((e: LiveEvent) => { listeners.current.forEach(fn => { try { fn(e) } catch { /* a listener's problem stays its own */ } }) }, [])
  const subscribe = useCallback((fn: (e: LiveEvent) => void) => { listeners.current.add(fn); return () => { listeners.current.delete(fn) } }, [])
  const patch = useCallback((id: number, p: Partial<Conversation>) => setConversations(cs => cs && cs.map(c => c.id === id ? { ...c, ...p } : c)), [])
  const setActive = useCallback((id: number) => { active.current = id }, [])

  // the live stream; the browser reconnects by itself, and after a gap the
  // lists reload so nothing is missed
  useEffect(() => {
    if (!me) return
    let es: EventSource | null = null
    let closed = false
    let retry = 0
    let wasConnected = false
    const open = () => {
      if (closed) return
      es = new EventSource(`${V1}/stream`)
      es.addEventListener('hello', () => {
        setConnected(true)
        if (wasConnected) { reload(); emit({ type: 'resync', data: null }) }
        wasConnected = true
        retry = 0
      })
      const on = (type: LiveEvent['type']) => es!.addEventListener(type, ev => {
        let data: unknown
        try { data = JSON.parse((ev as MessageEvent).data) } catch { return }
        handle({ type, data } as LiveEvent)
      })
      ;(['message', 'message_update', 'read', 'typing', 'conversation', 'presence'] as const).forEach(on)
      es.onerror = () => {
        setConnected(false)
        if (es?.readyState === EventSource.CLOSED) { es.close(); retry = Math.min(retry + 1, 6); window.setTimeout(open, 1000 * 2 ** retry) }
      }
    }
    const handle = (e: LiveEvent) => {
      switch (e.type) {
        case 'message': {
          const m = e.data
          setConversations(cs => {
            if (!cs) return cs
            const c = cs.find(x => x.id === m.conversation_id)
            if (!c) { reloadSoon(); return cs }
            const mine = m.user_id === me
            const seeing = active.current === m.conversation_id && document.visibilityState === 'visible'
            const counts = !mine && !seeing && m.kind !== 'system'
            const named = m.mentions?.includes(String(me))
            const next = { ...c, last: m, last_message_id: Math.max(c.last_message_id, m.id), last_message_at: m.created_at,
              unread: counts ? c.unread + 1 : mine ? 0 : c.unread, mentioned: counts && named ? c.mentioned + 1 : c.mentioned }
            return sortConvs(cs.map(x => x.id === c.id ? next : x))
          })
          setTyping(t => t[m.conversation_id] ? { ...t, [m.conversation_id]: t[m.conversation_id].filter(x => x.user_id !== (m.user_id || 0)) } : t)
          break
        }
        case 'message_update':
          setConversations(cs => cs && cs.map(c => c.id === e.data.conversation_id && c.last?.id === e.data.id ? { ...c, last: e.data } : c))
          break
        case 'read':
          if (e.data.user_id === me) setConversations(cs => cs && cs.map(c => c.id === e.data.conversation_id ? { ...c, last_read_id: e.data.last_read_id, unread: e.data.last_read_id >= c.last_message_id ? 0 : c.unread, mentioned: e.data.last_read_id >= c.last_message_id ? 0 : c.mentioned } : c))
          else setConversations(cs => cs && cs.map(c => c.id === e.data.conversation_id ? { ...c, members: c.members.map(mm => mm.user_id === e.data.user_id ? { ...mm, last_read_id: e.data.last_read_id } : mm) } : c))
          break
        case 'typing': {
          const d = e.data
          setTyping(t => ({ ...t, [d.conversation_id]: [...(t[d.conversation_id] || []).filter(x => x.user_id !== d.user_id), { user_id: d.user_id, name: d.name, until: Date.now() + 5000 }] }))
          break
        }
        case 'conversation':
          reloadSoon()
          break
        case 'presence':
          setPeople(ps => ps.map(p => p.id === e.data.user_id ? { ...p, online: e.data.online, seen: e.data.seen } : p))
          setConversations(cs => cs && cs.map(c => c.members.some(m => m.user_id === e.data.user_id && m.user_id !== me)
            ? { ...c, online: e.data.online ? [...c.online.filter(x => x !== e.data.user_id), e.data.user_id] : c.online.filter(x => x !== e.data.user_id) } : c))
          break
      }
      emit(e)
    }
    open()
    return () => { closed = true; es?.close(); setConnected(false) }
  }, [me, emit, reload, reloadSoon])

  // typing marks fade by themselves
  useEffect(() => {
    const t = window.setInterval(() => setTyping(cur => {
      const now = Date.now()
      let changed = false
      const next: Record<number, Typing[]> = {}
      for (const [k, v] of Object.entries(cur)) {
        const keep = v.filter(x => x.until > now)
        if (keep.length !== v.length) changed = true
        if (keep.length) next[Number(k)] = keep
      }
      return changed ? next : cur
    }), 1000)
    return () => window.clearInterval(t)
  }, [])

  const unread = useMemo(() => (conversations || []).filter(c => !c.archived && !(c.muted_until && new Date(c.muted_until) > new Date())).reduce((n, c) => n + c.unread, 0), [conversations])
  const mentions = useMemo(() => (conversations || []).reduce((n, c) => n + c.mentioned, 0), [conversations])

  // the tab title carries the unread count
  useEffect(() => {
    const base = settings?.title || 'Pocket Chat'
    document.title = unread > 0 ? `(${unread}) ${base}` : base
  }, [unread, settings?.title])

  const value = useMemo(() => ({ conversations, people, connected, typing, reload, patch, setActive, subscribe, unread, mentions }), [conversations, people, connected, typing, reload, patch, setActive, subscribe, unread, mentions])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}
