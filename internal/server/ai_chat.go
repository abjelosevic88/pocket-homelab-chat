package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
)

// ---- Pocket AI for one person ----
//
// Pocket AI never writes into a conversation. It helps the person who asks,
// and only them: a catch-up summary of what they missed, reply ideas,
// a translation, filling in "Make…". Nothing it says is sent to anyone.

// transcript is the conversation so far as plain lines for the AI.
func (s *Server) transcript(list []store.Message) string {
	var b strings.Builder
	for _, m := range list {
		if m.Deleted || m.Kind == store.MsgSystem {
			continue
		}
		who := firstNonEmpty(m.AuthorName, "Someone")
		at := m.CreatedAt
		if t, err := time.Parse(time.RFC3339Nano, m.CreatedAt); err == nil {
			at = t.Local().Format("Mon 2 Jan 15:04")
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n", at, who, describe(&m))
	}
	return b.String()
}

// describe is a message in words: its text, or what it is.
func describe(m *store.Message) string {
	parts := []string{}
	if m.Body != "" {
		parts = append(parts, m.Body)
	}
	switch m.Kind {
	case store.MsgPoll:
		opts, _ := m.Data["options"].([]any)
		var o []string
		for _, x := range opts {
			o = append(o, fmt.Sprint(x))
		}
		parts = append(parts, fmt.Sprintf("(poll: %s — %s)", str(m.Data, "question"), strings.Join(o, " / ")))
	case store.MsgTodo:
		var o []string
		for _, it := range rows(map[string]any{"items": m.Data["items"]}) {
			mark := "[ ]"
			if done, _ := it["done"].(bool); done {
				mark = "[x]"
			}
			o = append(o, mark+" "+str(it, "text"))
		}
		parts = append(parts, fmt.Sprintf("(to-do list %s: %s)", str(m.Data, "title"), strings.Join(o, "; ")))
	case store.MsgVoice:
		parts = append(parts, "(voice message) "+str(m.Data, "text"))
	}
	if card, ok := m.Data["card"].(map[string]any); ok {
		parts = append(parts, fmt.Sprintf("(shared from Pocket %s: %s — %s %s)", str(card, "app"), str(card, "title"), str(card, "subtitle"), clip(str(card, "text"), 200)))
	}
	for _, a := range m.Attachments {
		if strings.HasPrefix(a.Mime, "image/") {
			parts = append(parts, "(a photo)")
		} else if !strings.HasPrefix(a.Mime, "audio/") {
			parts = append(parts, "(a file: "+a.Name+")")
		}
	}
	return strings.Join(parts, " ")
}

func memberNames(c *store.Conversation) string {
	var names []string
	for _, m := range c.Members {
		names = append(names, firstNonEmpty(m.Name, m.Email))
	}
	return strings.Join(names, ", ")
}

// aiOr answers 409 with why when the person has no AI.
func (s *Server) aiOr(w http.ResponseWriter, ctx context.Context, uid int64) (*ai.Client, store.Settings, bool) {
	st, _ := s.store.GetSettings(ctx, uid)
	client, _, why := s.aiFor(ctx, uid, st)
	if client == nil {
		writeErr(w, http.StatusConflict, "AI is not set up: "+firstNonEmpty(why, "connect Pocket AI or add a key in Settings → AI"))
		return nil, st, false
	}
	select {
	case s.aiSem <- struct{}{}:
		return client, st, true
	case <-ctx.Done():
		writeErr(w, http.StatusServiceUnavailable, "busy")
		return nil, st, false
	}
}

func (s *Server) aiDone() { <-s.aiSem }

// ---- catch up: what did I miss? ----

type catchup struct {
	Summary  string   `json:"summary"`
	ForYou   []string `json:"for_you"`
	Plans    []string `json:"plans"`
	Count    int      `json:"count"`
	Unread   bool     `json:"unread"` // false: a summary of the latest messages (nothing unread)
	Provider string   `json:"provider,omitempty"`
}

const catchupSystem = `You summarise a private group or family chat for one person who was away.
Write in the language most of the messages are written in. Be short and concrete; name people.
Answer as JSON: {"summary": "2–5 short sentences", "for_you": ["things addressed to the reader or needing their answer"], "plans": ["plans, dates and decisions made"]}.
Leave a list empty when there is nothing. Do not invent anything.`

func (s *Server) handleCatchup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	cid := idParam(r, "id")
	conv, err := s.store.GetConversation(ctx, me.ID, cid)
	if err != nil {
		s.fail(w, err, "conversation")
		return
	}
	list, err := s.store.UnreadMessages(ctx, me.ID, cid, 200)
	if err != nil {
		s.fail(w, err, "messages")
		return
	}
	unread := len(list) > 0
	if !unread {
		list, _ = s.store.LastMessages(ctx, me.ID, cid, 50)
	}
	if len(list) == 0 {
		writeJSON(w, http.StatusOK, catchup{Summary: "Nothing here yet.", ForYou: []string{}, Plans: []string{}})
		return
	}
	key := fmt.Sprintf("catchup:%d:%d:%d:%d", me.ID, cid, list[0].ID, list[len(list)-1].ID)
	if v, _, ok := s.store.CacheGet(ctx, key); ok {
		var c catchup
		if json.Unmarshal([]byte(v), &c) == nil {
			writeJSON(w, http.StatusOK, c)
			return
		}
	}
	client, _, ok := s.aiOr(w, ctx, me.ID)
	if !ok {
		return
	}
	defer s.aiDone()
	prompt := fmt.Sprintf("The reader is %s. Conversation: %s (members: %s).\n\nMessages:\n%s", me.Name, s.store.ConversationTitle(ctx, me.ID, cid), memberNames(conv), s.transcript(list))
	var out catchup
	if err := client.JSON(ctx, catchupSystem, prompt, &out); err != nil {
		writeErr(w, http.StatusBadGateway, "AI: "+err.Error())
		return
	}
	out.Count, out.Unread = len(list), unread
	if out.ForYou == nil {
		out.ForYou = []string{}
	}
	if out.Plans == nil {
		out.Plans = []string{}
	}
	if b, err := json.Marshal(out); err == nil {
		_ = s.store.CachePut(ctx, key, string(b))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCatchupAll summarises everything unread, conversation by conversation.
func (s *Server) handleCatchupAll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	list, err := s.store.UnreadMessages(ctx, me.ID, 0, 300)
	if err != nil {
		s.fail(w, err, "messages")
		return
	}
	if len(list) == 0 {
		writeJSON(w, http.StatusOK, catchup{Summary: "You are all caught up.", ForYou: []string{}, Plans: []string{}})
		return
	}
	key := fmt.Sprintf("catchup-all:%d:%d:%d:%d", me.ID, len(list), list[0].ID, list[len(list)-1].ID)
	if v, _, ok := s.store.CacheGet(ctx, key); ok {
		var c catchup
		if json.Unmarshal([]byte(v), &c) == nil {
			writeJSON(w, http.StatusOK, c)
			return
		}
	}
	client, _, ok := s.aiOr(w, ctx, me.ID)
	if !ok {
		return
	}
	defer s.aiDone()
	byConv := map[string][]store.Message{}
	var order []string
	for _, m := range list {
		if _, ok := byConv[m.ConversationTitle]; !ok {
			order = append(order, m.ConversationTitle)
		}
		byConv[m.ConversationTitle] = append(byConv[m.ConversationTitle], m)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The reader is %s. Unread messages in %d conversations:\n", me.Name, len(order))
	for _, t := range order {
		fmt.Fprintf(&b, "\n## %s\n%s", t, s.transcript(byConv[t]))
	}
	var out catchup
	if err := client.JSON(ctx, catchupSystem+"\nThere are several conversations: say which one each point is from.", b.String(), &out); err != nil {
		writeErr(w, http.StatusBadGateway, "AI: "+err.Error())
		return
	}
	out.Count, out.Unread = len(list), true
	if out.ForYou == nil {
		out.ForYou = []string{}
	}
	if out.Plans == nil {
		out.Plans = []string{}
	}
	if bb, err := json.Marshal(out); err == nil {
		_ = s.store.CachePut(ctx, key, string(bb))
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- reply ideas, translation, filling in "Make…" ----

func (s *Server) handleReplies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	cid := idParam(r, "id")
	if _, err := s.store.Role(ctx, me.ID, cid); err != nil {
		s.fail(w, err, "conversation")
		return
	}
	list, _ := s.store.LastMessages(ctx, me.ID, cid, 20)
	if len(list) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"replies": []string{}})
		return
	}
	client, _, ok := s.aiOr(w, ctx, me.ID)
	if !ok {
		return
	}
	defer s.aiDone()
	var out struct {
		Replies []string `json:"replies"`
	}
	prompt := fmt.Sprintf("I am %s. Suggest three short, different replies I could send next (in the conversation's language, my voice, casual; at most 15 words each). Answer as JSON {\"replies\": [\"…\", \"…\", \"…\"]}.\n\nConversation:\n%s", me.Name, s.transcript(list))
	if err := client.JSON(ctx, "You help a person reply in a private chat with family and friends.", prompt, &out); err != nil {
		writeErr(w, http.StatusBadGateway, "AI: "+err.Error())
		return
	}
	var clean []string
	for _, r := range out.Replies {
		if r = strings.TrimSpace(r); r != "" && len(clean) < 3 {
			clean = append(clean, clip(r, 200))
		}
	}
	if clean == nil {
		clean = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"replies": clean})
}

func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	m, err := s.store.GetMessage(ctx, me.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	var in struct {
		Language string `json:"language"`
	}
	_ = decode(r, &in)
	text := firstNonEmpty(m.Body, str(m.Data, "text"))
	if text == "" {
		writeErr(w, http.StatusBadRequest, "nothing to translate")
		return
	}
	st, _ := s.store.GetSettings(ctx, me.ID)
	lang := clip(firstNonEmpty(strings.TrimSpace(in.Language), st.Language, "English"), 40)
	key := fmt.Sprintf("tr:%d:%s:%s", m.ID, lang, m.EditedAt)
	if v, _, ok := s.store.CacheGet(ctx, key); ok {
		writeJSON(w, http.StatusOK, map[string]any{"text": v, "language": lang})
		return
	}
	client, _, ok := s.aiOr(w, ctx, me.ID)
	if !ok {
		return
	}
	defer s.aiDone()
	out, err := client.Complete(ctx, "Translate the chat message into "+lang+". Keep its tone, names and emoji. Answer with the translation only.", text, false)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AI: "+err.Error())
		return
	}
	out = strings.TrimSpace(out)
	_ = s.store.CachePut(ctx, key, out)
	writeJSON(w, http.StatusOK, map[string]any{"text": out, "language": lang})
}

// handleFill reads a message for what "Make…" needs: rules first (always),
// then Pocket AI for a clean title, the day, time, place and amount.
func (s *Server) handleFill(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	m, err := s.store.GetMessage(ctx, me.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	var in struct {
		Target string `json:"target"`
		AI     bool   `json:"ai"`
	}
	_ = decode(r, &in)
	text := firstNonEmpty(m.Body, str(m.Data, "text"))
	out := map[string]any{"title": taskTitle(text)}
	for _, h := range detectHints(text, time.Now()) {
		switch h.Kind {
		case "event":
			out["date"], out["time"] = h.Date, h.Time
		case "expense":
			out["amount"], out["currency"] = h.Amount, h.Currency
		}
	}
	if card, ok := m.Data["card"].(map[string]any); ok {
		out["title"] = str(card, "title")
		f := obj(card, "fields")
		for _, k := range []string{"url", "city", "address", "kind", "location"} {
			if v := str(f, k); v != "" {
				out[k] = v
			}
		}
		if v := str(f, "website"); v != "" {
			out["url"] = v
		}
		if v := str(f, "start"); len(v) >= 10 {
			out["date"] = v[:10]
			if len(v) >= 16 && !strings.HasSuffix(v, "T00:00") {
				if allDay, _ := f["all_day"].(bool); !allDay {
					out["time"] = v[11:16]
				}
			}
		}
	}
	if p, ok := m.Data["preview"].(map[string]any); ok {
		if _, has := out["url"]; !has {
			out["url"] = str(p, "url")
		}
		if in.Target == "wish" && str(p, "title") != "" {
			out["title"] = str(p, "title")
		}
	}
	if u := reURL.FindString(text); u != "" {
		if _, has := out["url"]; !has {
			out["url"] = strings.TrimRight(u, ".,;:!?")
		}
	}
	if !in.AI || text == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	client, _, ok := s.aiOr(w, ctx, me.ID)
	if !ok {
		return
	}
	defer s.aiDone()
	history, _ := s.store.MessagesAround(ctx, me.ID, m.ConversationID, m.ID, 12)
	prompt := fmt.Sprintf("Today is %s. I (%s) want to make a %s from this chat message.\nRecent conversation:\n%s\nThe message: %s: %s\n\nAnswer as JSON with only the fields you can tell: {\"title\": \"short, clear (in the message's language)\", \"date\": \"YYYY-MM-DD\", \"time\": \"HH:MM\", \"minutes\": duration, \"location\": \"\", \"amount\": number, \"currency\": \"EUR\", \"notes\": \"\", \"kind\": \"restaurant|cafe|bar|sight|shop|other\", \"city\": \"\"}.",
		time.Now().Format("Monday 2 January 2006"), me.Name, in.Target, s.transcript(history), m.AuthorName, text)
	var got map[string]any
	if err := client.JSON(ctx, "You extract details from chat messages. Never invent details that are not there.", prompt, &got); err == nil {
		for k, v := range got {
			if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
				continue
			}
			if v != nil {
				out[k] = v
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSaveNote keeps a summary (or any text) from a conversation as a
// note in the person's Pocket Notes.
func (s *Server) handleSaveNote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	cid := idParam(r, "id")
	if _, err := s.store.Role(ctx, me.ID, cid); err != nil {
		s.fail(w, err, "conversation")
		return
	}
	var in struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Content) == "" {
		writeErr(w, http.StatusBadRequest, "nothing to keep")
		return
	}
	sib, err := s.clients(ctx, me.ID).conn(ctx, appNotes, "notes:write")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	link := strings.TrimRight(s.cfg.BaseURL, "/") + fmt.Sprintf("/c/%d", cid)
	title := firstNonEmpty(strings.TrimSpace(in.Title), s.store.ConversationTitle(ctx, me.ID, cid)+" — "+time.Now().Format("2 Jan 2006"))
	res, err := postJSONTo(ctx, sib, "/api/v1/notes", map[string]any{"title": clip(title, 120), "content": clip(in.Content, 20000) + "\n\n— Pocket Chat: " + link})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Pocket Notes: "+err.Error())
		return
	}
	u := ""
	if sib.public != "" {
		u = strings.TrimRight(sib.public, "/") + "/note/" + idOf(res)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": idOf(res), "url": u, "title": title})
}
