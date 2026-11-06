export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export type ConnVia = '' | 'core' | 'direct'
export type AppSlug = 'tasks' | 'calendar' | 'notes' | 'split' | 'wishlist' | 'places' | 'voice' | 'kitchen' | 'travel' | 'parcels' | 'weather' | 'bookshelf' | 'watch'

type ConnFields = { [K in AppSlug as `${K}_mode`]: ConnMode } & { [K in AppSlug as `${K}_via`]: ConnVia } & { [K in AppSlug as `${K}_detail` | `${K}_url` | `${K}_token` | `${K}_public_url` | `${K}_open_url`]: string } & { [K in AppSlug as `${K}_configured`]: boolean }

export type Settings = ConnFields & {
  core_configured: boolean
  core_registered?: boolean
  title: string; date_format: string; time_format: string; week_start: number
  enter_sends: boolean; smart_actions: boolean; link_previews: boolean; read_receipts: boolean; language: string
  notify_mode: 'all' | 'mentions' | 'off'; notify_preview: boolean; quiet_from: number; quiet_to: number; notify_digest: boolean; notify_hour: number
  notify_url: string; notify_format: string; notify_configured: boolean
  ai_provider: 'auto' | 'pocket' | 'openai'; ai_pocket_model: string; openai_api_key: string; openai_model: string; openai_base_url: string
  setup_complete: boolean; base_url: string; demo: boolean; max_upload_mb: number
}

export interface Person { id: number; name: string; email: string; online: boolean; seen?: string }

export interface Member { user_id: number; name: string; email: string; role: 'owner' | 'member'; last_read_id: number; joined_at: string }

export type ConvKind = 'direct' | 'group' | 'self'

export interface Conversation {
  id: number; kind: ConvKind; title: string; about: string; emoji: string; color: string; created_by: number
  ai: boolean; smart: boolean; last_message_id: number; last_message_at: string; created_at: string; updated_at: string
  members: Member[]; role: 'owner' | 'member'; last_read_id: number; notify: 'default' | 'all' | 'mentions' | 'off'; muted_until: string
  pinned: boolean; archived: boolean; draft: string; unread: number; mentioned: number; last?: Message
  name: string; online: number[]
}

export interface Attachment { id: number; user_id: number; conversation_id: number; message_id: number; name: string; mime: string; bytes: number; width: number; height: number; duration_ms: number; transcript: string; created_at: string; url: string }
export interface Reaction { emoji: string; users: number[] }
export interface Vote { user_id: number; option: number }
export interface Made { id: number; message_id: number; target: MakeTarget; app: string; ref: string; title: string; url: string; created_at: string }
export interface Quote { id: number; user_id: number; author_name: string; kind: string; body: string; deleted: boolean }

export type MsgKind = 'text' | 'card' | 'poll' | 'todo' | 'voice' | 'ai' | 'app' | 'system'

export interface Hint { kind: 'event' | 'expense' | 'task'; date?: string; time?: string; label?: string; amount?: string; minor?: number; currency?: string; title?: string }
export interface Card { app?: string; kind?: string; icon?: string; title: string; subtitle?: string; text?: string; lines?: string[]; url?: string; image?: string; badge?: string; rating?: number; date?: string; ref?: string; fields?: Record<string, unknown> }
export interface Preview { url: string; title: string; description?: string; image?: string; site?: string; app?: string }
export interface TodoItem { id: number; text: string; done: boolean; assignee?: number; done_by?: number; done_at?: string }

export interface MsgData {
  hints?: Hint[]; card?: Card; preview?: Preview
  question?: string; options?: string[]; multi?: boolean; closed?: boolean
  title?: string; items?: TodoItem[]
  text?: string // a voice message's words
  asked_by?: number; used?: string[]; error?: boolean; provider?: string
  imported?: string; ref?: number
}

export interface Message {
  id: number; conversation_id: number; user_id: number; kind: MsgKind; body: string; data: MsgData; reply_to: number
  mentions: string[]; author_name: string; source: string; source_ref: string; client_id?: string; pinned_at?: string; edited_at?: string
  deleted: boolean; created_at: string; attachments: Attachment[]; reactions: Reaction[]; votes?: Vote[]; reply?: Quote
  starred: boolean; made?: Made[]; conversation_title?: string; snippet?: string
  pending?: boolean; failed?: string // not sent yet (this window only)
}

export type MakeTarget = 'task' | 'event' | 'note' | 'expense' | 'wish' | 'place'

export interface ShareApp { slug: AppSlug; name: string; label: string; connected: boolean; detail: string }
export interface ShareItem { id: string; title: string; subtitle?: string; date?: string; card: Card }

export interface Catchup { summary: string; for_you: string[]; plans: string[]; count: number; unread: boolean; provider?: string }
