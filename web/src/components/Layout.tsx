// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { LiveProvider, useLive } from '../lib/live'
import type { Conversation } from '../lib/types'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { NewChat } from './NewChat'
import { ImportModal } from './ImportModal'
import { Avatar, Loading, type MenuItem, MenuButton, modalOpen, useIsMobile, useMediaQuery, useToast } from './ui'
import { AskButton } from './AskButton'

const SIDEBAR_KEY = 'pch-sidebar'

/** The page's own name for the top bar breadcrumb. */
const CrumbCtx = createContext<(t: string) => void>(() => {})
export function useCrumb(title: string | undefined) {
  const set = useContext(CrumbCtx)
  useEffect(() => { set(title || ''); return () => set('') }, [title, set])
}

export type CreateKind = 'chat' | 'group' | 'self' | 'import'

/** Start something from anywhere (a page's button). */
export const openCreate = (k: CreateKind) => window.dispatchEvent(new CustomEvent('pch:create', { detail: { kind: k } }))

/** The start menu: the sidebar's caret. */
export function createItems(onCreate: (k: CreateKind) => void): MenuItem[] {
  return [
    { label: 'Chat with someone', icon: 'message-plus', hint: 'N', onSelect: () => onCreate('chat') },
    { label: 'New group', icon: 'users', hint: 'G', onSelect: () => onCreate('group') },
    { label: 'Your own space (Me)', icon: 'bookmark', onSelect: () => onCreate('self') },
    'sep',
    { label: 'Bring a WhatsApp chat…', icon: 'upload', onSelect: () => onCreate('import') },
  ]
}

export default function Layout() {
  return <LiveProvider><Shell /></LiveProvider>
}

function Shell() {
  const navigate = useNavigate()
  const loc = useLocation()
  const { conversations } = useLive()
  const { toast } = useToast()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  const inChat = loc.pathname.startsWith('/c/')
  useEffect(() => { if (!inChat) document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname, inChat])

  const [crumb, setCrumbRaw] = useState('')
  const setCrumb = useCallback((t: string) => setCrumbRaw(t), [])

  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [newChat, setNewChat] = useState<'chat' | 'group' | null>(null)
  const [importing, setImporting] = useState(false)

  const create = async (k: CreateKind) => {
    closeDrawer()
    if (k === 'import') setImporting(true)
    else if (k === 'self') {
      try { const c = await api.post<Conversation>(`${V1}/conversations`, { kind: 'self' }); navigate(`/c/${c.id}`) } catch (e) { toast((e as Error).message, 'err') }
    } else setNewChat(k)
  }
  const createRef = useRef(create)
  createRef.current = create
  // Settings → Backup & data → Bring a WhatsApp chat
  useEffect(() => {
    if ((loc.state as { import?: boolean } | null)?.import) { setImporting(true); navigate(loc.pathname, { replace: true, state: null }) }
  }, [loc.state]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const open = () => setShortcuts(true)
    const make = (e: Event) => createRef.current((e as CustomEvent<{ kind: CreateKind }>).detail.kind)
    window.addEventListener('pch:shortcuts', open)
    window.addEventListener('pch:create', make)
    return () => { window.removeEventListener('pch:shortcuts', open); window.removeEventListener('pch:create', make) }
  }, [])

  // jump between conversations with Alt+↑/↓
  const step = (d: number) => {
    const list = (conversations || []).filter(c => !c.archived)
    if (!list.length) return
    const cur = list.findIndex(c => loc.pathname === `/c/${c.id}`)
    const next = list[(cur + d + list.length) % list.length]
    navigate(`/c/${next.id}`)
  }
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) { e.preventDefault(); step(e.key === 'ArrowUp' ? -1 : 1); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      const mk = (k: CreateKind) => { e.preventDefault(); createRef.current(k) }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'n': mk('chat'); break
        case 'g': mk('group'); break
        case 'h': go('/'); break
        case 's': go('/saved'); break
        case 'f': go('/search'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const commands: PaletteCommand[] = [
    { id: 'chat', label: 'Chat with someone', icon: 'message-plus', hint: 'N', keywords: 'new message direct person', run: () => create('chat') },
    { id: 'group', label: 'New group', icon: 'users', hint: 'G', keywords: 'family friends team', run: () => create('group') },
    { id: 'self', label: 'Your own space (Me)', icon: 'bookmark', keywords: 'notes to self later', run: () => create('self') },
    { id: 'home', label: 'Catch up', icon: 'sparkles', hint: 'H', keywords: 'missed unread summary overview', run: () => navigate('/') },
    { id: 'saved', label: 'Saved messages', icon: 'star', hint: 'S', keywords: 'starred kept', run: () => navigate('/saved') },
    { id: 'search', label: 'Search messages', icon: 'search', hint: 'F', keywords: 'find', run: () => navigate('/search') },
    ...(conversations || []).map(c => ({ id: `c${c.id}`, label: c.name, icon: c.kind === 'group' ? 'users' : c.kind === 'self' ? 'bookmark' : 'user', keywords: c.members.map(m => m.name).join(' '), run: () => navigate(`/c/${c.id}`) })),
    { id: 'import', label: 'Bring a WhatsApp chat', icon: 'upload', keywords: 'import export whatsapp', run: () => create('import') },
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', run: () => navigate('/settings') },
    { id: 'notify', label: 'Settings: notifications', icon: 'bell', keywords: 'push mute quiet', run: () => navigate('/settings/notifications') },
    { id: 'apps', label: 'Settings: connected apps', icon: 'layers', keywords: 'tasks calendar notes split places core', run: () => navigate('/settings/apps') },
    { id: 'ai', label: 'Settings: AI', icon: 'sparkles', keywords: 'pocket ai openai', run: () => navigate('/settings/ai') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  return (
    <CrumbCtx.Provider value={setCrumb}>
      <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''} ${inChat ? 'in-chat' : ''}`}>
        {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
        <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
          <Sidebar onCreate={create} onNavigate={closeDrawer} />
        </div>
        <div className="main">
          {!(mobile && inChat) && <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} crumb={crumb} />}
          <main className={`content ${inChat ? 'chat-content' : ''}`}>
            {inChat ? (conversations ? <Outlet context={{ onMenu: toggleSidebar }} /> : <Loading />)
              : <div className="content-inner">{conversations ? <Outlet context={{ onMenu: toggleSidebar }} /> : <Loading />}</div>}
          </main>
        </div>
        {mobile && !inChat && <button type="button" className="fab" aria-label="New chat" title="New chat" onClick={() => create('chat')}><Icon name="message-plus" size={24} strokeWidth={2.1} /></button>}
        {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} />}
        {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
        {newChat && <NewChat mode={newChat} onClose={() => setNewChat(null)} onDone={id => { setNewChat(null); navigate(`/c/${id}`) }} />}
        {importing && <ImportModal onClose={() => setImporting(false)} onDone={id => { setImporting(false); navigate(`/c/${id}`) }} />}
      </div>
    </CrumbCtx.Provider>
  )
}

const SECTION: Record<string, [string, string]> = { '': ['Catch up', '/'], saved: ['Saved', '/saved'], search: ['Search', '/search'], c: ['Chats', '/'] }

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen, crumb }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean; crumb: string }) {
  const { user, themeMode, setThemeMode, status, setUser } = useApp()
  const { unread } = useLive()
  const navigate = useNavigate()
  const loc = useLocation()
  const [section, sectionTo] = mobile && loc.pathname === '/' ? ['Chats', '/'] : SECTION[loc.pathname.split('/')[1] || ''] || ['', '/']
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pch:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only menu-btn" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)">
        <Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} />
        {(mobile || compact) && !sidebarOpen && unread > 0 && <span className="menu-dot" aria-label={`${unread} unread`} />}
      </button>
      <h1 className="range-title">
        {crumb ? <><button type="button" className="crumb" onClick={() => navigate(sectionTo)}>{section}</button><Icon name="chevron-right" size={16} className="crumb-sep" /><span className="ellipsis">{crumb}</span></> : section}
      </h1>
      <span className="spacer" />
      <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>
      <AskButton />
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        <Avatar name={user?.name} email={user?.email} size={30} />
      </MenuButton>
    </header>
  )
}
