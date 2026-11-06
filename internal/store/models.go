package store

import (
	"encoding/json"
)

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Conversation kinds.
const (
	KindDirect = "direct" // two people
	KindGroup  = "group"  // a named group
	KindSelf   = "self"   // "Me": your own space
)

// Conversation is a chat, with the caller's own state in it.
type Conversation struct {
	ID            int64    `json:"id"`
	Kind          string   `json:"kind"`
	Title         string   `json:"title"`
	About         string   `json:"about"`
	Emoji         string   `json:"emoji"`
	Color         string   `json:"color"`
	CreatedBy     int64    `json:"created_by"`
	AI            bool     `json:"ai"`
	Smart         bool     `json:"smart"`
	LastMessageID int64    `json:"last_message_id"`
	LastMessageAt string   `json:"last_message_at"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	Members       []Member `json:"members"`

	// The caller's own state.
	Role       string   `json:"role"`
	LastReadID int64    `json:"last_read_id"`
	Notify     string   `json:"notify"`
	MutedUntil string   `json:"muted_until"`
	Pinned     bool     `json:"pinned"`
	Archived   bool     `json:"archived"`
	Draft      string   `json:"draft"`
	Unread     int      `json:"unread"`
	Mentioned  int      `json:"mentioned"` // unread messages naming the caller
	Last       *Message `json:"last,omitempty"`
}

// Member is a person in a conversation.
type Member struct {
	UserID     int64  `json:"user_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	LastReadID int64  `json:"last_read_id"`
	JoinedAt   string `json:"joined_at"`
}

// Message kinds.
const (
	MsgText   = "text"
	MsgCard   = "card"   // something shared from another Pocket app
	MsgPoll   = "poll"   // a question with options
	MsgTodo   = "todo"   // a shared to-do list
	MsgVoice  = "voice"  // a voice message (attachment + transcript)
	MsgAI     = "ai"     // Pocket AI answering in the conversation
	MsgApp    = "app"    // posted by another app through a Pocket Core link
	MsgSystem = "system" // "Ana added Bob"
)

// Message is one message as a member sees it.
type Message struct {
	ID             int64          `json:"id"`
	ConversationID int64          `json:"conversation_id"`
	UserID         int64          `json:"user_id"`
	Kind           string         `json:"kind"`
	Body           string         `json:"body"`
	Data           map[string]any `json:"data"`
	ReplyTo        int64          `json:"reply_to"`
	Mentions       []string       `json:"mentions"`
	AuthorName     string         `json:"author_name"`
	Source         string         `json:"source"`
	SourceRef      string         `json:"source_ref"`
	ClientID       string         `json:"client_id,omitempty"`
	PinnedAt       string         `json:"pinned_at,omitempty"`
	EditedAt       string         `json:"edited_at,omitempty"`
	Deleted        bool           `json:"deleted"`
	CreatedAt      string         `json:"created_at"`

	Attachments []Attachment `json:"attachments"`
	Reactions   []Reaction   `json:"reactions"`
	Votes       []Vote       `json:"votes,omitempty"`
	Reply       *Quote       `json:"reply,omitempty"`
	Starred     bool         `json:"starred"`
	Made        []Made       `json:"made,omitempty"`

	// Search results and Saved: where it is.
	ConversationTitle string `json:"conversation_title,omitempty"`
	Snippet           string `json:"snippet,omitempty"`
}

// Quote is the message a reply answers, short.
type Quote struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	AuthorName string `json:"author_name"`
	Kind       string `json:"kind"`
	Body       string `json:"body"`
	Deleted    bool   `json:"deleted"`
}

// Attachment is a photo, a file or a voice recording.
type Attachment struct {
	ID             int64  `json:"id"`
	UserID         int64  `json:"user_id"`
	ConversationID int64  `json:"conversation_id"`
	MessageID      int64  `json:"message_id"`
	File           string `json:"-"`
	Name           string `json:"name"`
	Mime           string `json:"mime"`
	Bytes          int64  `json:"bytes"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	DurationMS     int    `json:"duration_ms"`
	Transcript     string `json:"transcript"`
	CreatedAt      string `json:"created_at"`
	URL            string `json:"url"`
}

// Reaction is one emoji and who used it.
type Reaction struct {
	Emoji string  `json:"emoji"`
	Users []int64 `json:"users"`
}

// Vote is one person's choice in a poll.
type Vote struct {
	UserID int64 `json:"user_id"`
	Option int   `json:"option"`
}

// Made is something the caller made from a message in another app.
type Made struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	Target    string `json:"target"`
	App       string `json:"app"`
	Ref       string `json:"ref"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
}

// Conn is one person's connection to a sibling Pocket app.
type Conn struct {
	Mode      string `json:"mode,omitempty"`
	URL       string `json:"url,omitempty"`
	Token     string `json:"token,omitempty"`
	PublicURL string `json:"public_url,omitempty"`
}

// Settings are the box-wide values plus one person's preferences and their
// own connections.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`
	WeekStart  int    `json:"week_start"` // 1 = Monday, 7 = Sunday

	// Chatting.
	EnterSends   bool   `json:"enter_sends"`   // Enter sends, Shift+Enter is a new line
	SmartActions bool   `json:"smart_actions"` // "add to calendar", "split this"… under messages
	LinkPreviews bool   `json:"link_previews"` // show a preview of web links
	ReadReceipts bool   `json:"read_receipts"` // others see when you have read
	Language     string `json:"language"`      // for translations and AI answers ("" = as written)

	// Notifications.
	NotifyMode    string `json:"notify_mode"`    // all | mentions | off: the default for every conversation
	NotifyPreview bool   `json:"notify_preview"` // show the message text in the notification
	QuietFrom     int    `json:"quiet_from"`     // hour; quiet_from == quiet_to = no quiet hours
	QuietTo       int    `json:"quiet_to"`
	NotifyDigest  bool   `json:"notify_digest"` // morning: what you missed, summarised
	NotifyHour    int    `json:"notify_hour"`
	NotifyURL     string `json:"notify_url"`
	NotifyFormat  string `json:"notify_format"` // apprise | ntfy | json

	// Each sibling Pocket app, by slug.
	Conns map[string]Conn `json:"conns"`

	// AI: Pocket AI through Core, or an OpenAI-compatible API with the
	// person's own key.
	AIProvider    string `json:"ai_provider"` // auto | pocket | openai
	AIPocketModel string `json:"ai_pocket_model"`
	OpenAIKey     string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
