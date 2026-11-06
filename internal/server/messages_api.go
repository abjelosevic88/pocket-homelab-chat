package server

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
)

const maxText = 8000

// decorate makes a message safe to show: lists never null.
func (s *Server) decorate(m *store.Message) {
	if m.Attachments == nil {
		m.Attachments = []store.Attachment{}
	}
	if m.Reactions == nil {
		m.Reactions = []store.Reaction{}
	}
	if m.Data == nil {
		m.Data = map[string]any{}
	}
}

func (s *Server) decorateAll(list []store.Message) {
	for i := range list {
		s.decorate(&list[i])
	}
}

// shared is a message as everyone in the conversation may see it: without
// one person's own stars and things made from it (for live updates).
func shared(m *store.Message) *store.Message {
	c := *m
	c.Starred, c.Made = false, nil
	return &c
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	cid := idParam(r, "id")
	if _, err := s.store.Role(ctx, uid, cid); err != nil {
		s.fail(w, err, "conversation")
		return
	}
	var list []store.Message
	var err error
	if around := queryInt64(r, "around"); around > 0 {
		list, err = s.store.MessagesAround(ctx, uid, cid, around, 60)
	} else {
		list, err = s.store.ListMessages(ctx, uid, cid, queryInt64(r, "before"), queryInt64(r, "after"), queryInt(r, "limit"))
	}
	if err != nil {
		s.fail(w, err, "messages")
		return
	}
	s.decorateAll(list)
	writeJSON(w, http.StatusOK, list)
}

func queryInt64(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	return n
}

// sendInput is a new message.
type sendInput struct {
	Text        string  `json:"text"`
	ReplyTo     int64   `json:"reply_to"`
	Attachments []int64 `json:"attachments"`
	ClientID    string  `json:"client_id"`
	Kind        string  `json:"kind"` // text (default) | poll | todo | voice
	Poll        *struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
		Multi    bool     `json:"multi"`
	} `json:"poll"`
	Todo *struct {
		Title string `json:"title"`
		Items []struct {
			Text     string `json:"text"`
			Assignee int64  `json:"assignee"`
		} `json:"items"`
	} `json:"todo"`
	// Another app posting (Pocket Core link) or a share: a card.
	Card map[string]any `json:"card"`
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in sendInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	m, status, err := s.send(ctx, userFrom(ctx), idParam(r, "id"), in)
	if err != nil {
		if status == 0 {
			s.fail(w, err, "send")
		} else {
			writeErr(w, status, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// send checks and stores a message from a person (or, with a link token,
// from an app acting for them), then tells everyone and does the rest in
// the background: link previews, notifications, Pocket AI.
func (s *Server) send(ctx context.Context, me *store.User, cid int64, in sendInput) (*store.Message, int, error) {
	conv, err := s.store.GetConversation(ctx, me.ID, cid)
	if err != nil {
		return nil, 0, err
	}
	app := linkApp(ctx)
	text := strings.TrimSpace(in.Text)
	if utf8.RuneCountInString(text) > maxText {
		return nil, http.StatusBadRequest, fmt.Errorf("a message is at most %d characters", maxText)
	}
	m := &store.Message{ConversationID: cid, UserID: me.ID, Kind: store.MsgText, Body: text, ReplyTo: in.ReplyTo, ClientID: clip(strings.TrimSpace(in.ClientID), 80), Data: map[string]any{}}
	if app != "" {
		m.Kind, m.Source = store.MsgApp, app
		m.ClientID = ""
		if in.ClientID != "" {
			m.ClientID = app + ":" + clip(in.ClientID, 70)
		}
	}
	switch in.Kind {
	case "", store.MsgText:
	case store.MsgVoice:
		if len(in.Attachments) != 1 {
			return nil, http.StatusBadRequest, fmt.Errorf("a voice message is one recording")
		}
		m.Kind = store.MsgVoice
	case store.MsgPoll:
		if in.Poll == nil {
			return nil, http.StatusBadRequest, fmt.Errorf("a poll needs a question and options")
		}
		q := clip(strings.TrimSpace(in.Poll.Question), 300)
		var opts []any
		for _, o := range in.Poll.Options {
			if o = clip(strings.TrimSpace(o), 120); o != "" && len(opts) < 12 {
				opts = append(opts, o)
			}
		}
		if q == "" || len(opts) < 2 {
			return nil, http.StatusBadRequest, fmt.Errorf("a poll needs a question and at least two options")
		}
		m.Kind = store.MsgPoll
		m.Data = map[string]any{"question": q, "options": opts, "multi": in.Poll.Multi, "closed": false}
	case store.MsgTodo:
		if in.Todo == nil {
			return nil, http.StatusBadRequest, fmt.Errorf("a to-do list needs items")
		}
		members := map[int64]bool{}
		for _, mm := range conv.Members {
			members[mm.UserID] = true
		}
		var items []any
		for i, it := range in.Todo.Items {
			t := clip(strings.TrimSpace(it.Text), 200)
			if t == "" || len(items) >= 50 {
				continue
			}
			item := map[string]any{"id": i + 1, "text": t, "done": false}
			if members[it.Assignee] {
				item["assignee"] = it.Assignee
			}
			items = append(items, item)
		}
		if len(items) == 0 {
			return nil, http.StatusBadRequest, fmt.Errorf("a to-do list needs at least one item")
		}
		m.Kind = store.MsgTodo
		m.Data = map[string]any{"title": clip(strings.TrimSpace(in.Todo.Title), 120), "items": items, "next": len(in.Todo.Items) + 1}
	default:
		return nil, http.StatusBadRequest, fmt.Errorf("unknown message kind")
	}
	if in.Card != nil {
		card := cleanCard(in.Card)
		if card == nil {
			return nil, http.StatusBadRequest, fmt.Errorf("a card needs a title")
		}
		if m.Kind == store.MsgText {
			m.Kind = store.MsgCard
		}
		m.Data["card"] = card
	}
	if m.Body == "" && len(in.Attachments) == 0 && m.Kind == store.MsgText {
		return nil, http.StatusBadRequest, fmt.Errorf("write something first")
	}
	if m.ReplyTo > 0 {
		q, err := s.store.GetMessage(ctx, me.ID, m.ReplyTo)
		if err != nil || q.ConversationID != cid {
			m.ReplyTo = 0
		}
	}
	m.Mentions = mentionsIn(m.Body, conv.Members, me.ID, conv.AI)
	st, _ := s.store.GetSettings(ctx, me.ID)
	if conv.Smart && m.Body != "" && !askedAI(m.Mentions) {
		if hints := detectHints(m.Body, time.Now()); len(hints) > 0 {
			m.Data["hints"] = hints
		}
	}
	dup, err := s.store.InsertMessage(ctx, m)
	if err != nil {
		return nil, 0, err
	}
	if dup {
		s.decorate(m)
		return m, 0, nil
	}
	if len(in.Attachments) > 0 {
		n, err := s.store.ClaimAttachments(ctx, me.ID, cid, m.ID, in.Attachments)
		if err != nil {
			return nil, 0, err
		}
		if n == 0 && m.Body == "" && m.Kind != store.MsgVoice {
			_, _ = s.store.DeleteMessage(ctx, m.ID)
			return nil, http.StatusBadRequest, fmt.Errorf("the file was not found; upload it again")
		}
	}
	full, err := s.store.GetMessage(ctx, me.ID, m.ID)
	if err != nil {
		return nil, 0, err
	}
	s.decorate(full)
	s.toMembers(ctx, cid, "message", shared(full))
	go s.afterSend(context.WithoutCancel(ctx), conv, full, me, st)
	return full, 0, nil
}

var mentionRe = regexp.MustCompile(`(?i)(^|[^\p{L}\p{N}_])@([\p{L}\p{N}_.-]+)`)

// mentionsIn finds who a message names: @ai, @all / @everyone, and members
// by their first name or their whole name without spaces.
func mentionsIn(text string, members []store.Member, me int64, aiOn bool) []string {
	if !strings.Contains(text, "@") {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	lower := store.Fold(text)
	for _, mm := range mentionRe.FindAllStringSubmatch(lower, -1) {
		w := strings.TrimRight(mm[2], ".-")
		switch w {
		case "ai", "pocketai":
			if aiOn {
				add("ai")
			}
			continue
		case "all", "everyone", "svi", "sve":
			for _, m := range members {
				if m.UserID != me {
					add(strconv.FormatInt(m.UserID, 10))
				}
			}
			continue
		}
		for _, m := range members {
			if m.UserID == me {
				continue
			}
			name := store.Fold(m.Name)
			first := strings.Fields(name)
			if w == strings.ReplaceAll(name, " ", "") || (len(first) > 0 && w == first[0]) || w == strings.Split(store.Fold(m.Email), "@")[0] {
				add(strconv.FormatInt(m.UserID, 10))
			}
		}
	}
	return out
}

// afterSend: link preview, Core events, notifications, Pocket AI.
func (s *Server) afterSend(ctx context.Context, conv *store.Conversation, m *store.Message, me *store.User, st store.Settings) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	s.publish(ctx, me.ID, "chat.message_sent", fmt.Sprintf("conversation:%d", conv.ID), map[string]any{
		"conversation_id": conv.ID, "message_id": m.ID, "kind": m.Kind, "conversation": s.store.ConversationTitle(ctx, me.ID, conv.ID), "text": clip(m.Body, 500),
	})
	for _, id := range store.ParseMentions(m.Mentions) {
		s.publish(ctx, id, "chat.mentioned", fmt.Sprintf("message:%d", m.ID), map[string]any{
			"conversation_id": conv.ID, "message_id": m.ID, "from": me.Name, "conversation": s.store.ConversationTitle(ctx, id, conv.ID), "text": clip(m.Body, 500),
		})
	}
	if m.Kind == store.MsgVoice {
		s.transcribe(ctx, me.ID, m)
	}
	s.notifyMembers(ctx, conv, m, me)
	asked := false
	for _, v := range m.Mentions {
		if v == "ai" {
			asked = true
		}
	}
	if conv.Kind == store.KindSelf && conv.AI && strings.HasPrefix(strings.ToLower(m.Body), "ai ") {
		asked = true
	}
	if asked && conv.AI {
		s.answerAI(ctx, conv, m, me)
	}
	if st.LinkPreviews && m.Kind == store.MsgText {
		s.previewLinks(ctx, m)
	}
}

func (s *Server) handleEditMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	m, err := s.store.GetMessage(ctx, me.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	if m.UserID != me.ID || m.Deleted || !oneOf(m.Kind, []string{store.MsgText, store.MsgCard, store.MsgApp}) || m.Source != linkApp(ctx) {
		writeErr(w, http.StatusForbidden, "you can change only your own messages")
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	text := strings.TrimSpace(in.Text)
	if text == "" && len(m.Attachments) == 0 && m.Kind == store.MsgText {
		writeErr(w, http.StatusBadRequest, "a message cannot be empty; delete it instead")
		return
	}
	if utf8.RuneCountInString(text) > maxText {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("a message is at most %d characters", maxText))
		return
	}
	conv, err := s.store.GetConversation(ctx, me.ID, m.ConversationID)
	if err != nil {
		s.fail(w, err, "conversation")
		return
	}
	m.Body = text
	m.Mentions = mentionsIn(text, conv.Members, me.ID, conv.AI)
	delete(m.Data, "hints")
	delete(m.Data, "preview")
	if conv.Smart {
		if hints := detectHints(text, time.Now()); len(hints) > 0 {
			m.Data["hints"] = hints
		}
	}
	if err := s.store.UpdateMessage(ctx, m, true); err != nil {
		s.fail(w, err, "save")
		return
	}
	s.decorate(m)
	s.toMembers(ctx, m.ConversationID, "message_update", shared(m))
	st, _ := s.store.GetSettings(ctx, me.ID)
	if st.LinkPreviews {
		go s.previewLinks(context.WithoutCancel(ctx), m)
	}
	writeJSON(w, http.StatusOK, m)
}

// handleDeleteMessage deletes a message for everyone: your own, or any in
// a group you own.
func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	m, err := s.store.GetMessage(ctx, me.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	if m.Deleted {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if app := linkApp(ctx); app != "" && (m.Source != app || m.UserID != me.ID) {
		writeErr(w, http.StatusForbidden, "an app may delete only the messages it sent")
		return
	}
	if m.UserID != me.ID {
		role, _ := s.store.Role(ctx, me.ID, m.ConversationID)
		if c, err := s.store.GetConversation(ctx, me.ID, m.ConversationID); err == nil && c.Kind == store.KindSelf {
			role = "owner" // your own space is all yours
		}
		if role != "owner" {
			writeErr(w, http.StatusForbidden, "you can delete only your own messages")
			return
		}
	}
	files, err := s.store.DeleteMessage(ctx, m.ID)
	if err != nil {
		s.fail(w, err, "delete")
		return
	}
	s.removeFiles(files)
	m, _ = s.store.GetMessage(ctx, me.ID, m.ID)
	if m != nil {
		s.decorate(m)
		s.toMembers(ctx, m.ConversationID, "message_update", shared(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// member loads a message for someone in its conversation and broadcasts
// its new state after fn changed it.
func (s *Server) updateAndTell(ctx context.Context, uid, id int64) (*store.Message, error) {
	m, err := s.store.GetMessage(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	s.decorate(m)
	s.toMembers(ctx, m.ConversationID, "message_update", shared(m))
	return m, nil
}

func (s *Server) handleReact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMessage(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	var in struct {
		Emoji string `json:"emoji"`
	}
	if err := decode(r, &in); err != nil || in.Emoji == "" || len(in.Emoji) > 32 || utf8.RuneCountInString(in.Emoji) > 8 || m.Deleted {
		writeErr(w, http.StatusBadRequest, "one emoji")
		return
	}
	if _, err := s.store.ToggleReaction(ctx, m.ID, uid, in.Emoji); err != nil {
		s.fail(w, err, "react")
		return
	}
	m, err = s.updateAndTell(ctx, uid, m.ID)
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleVote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMessage(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	if m.Kind != store.MsgPoll || m.Deleted {
		writeErr(w, http.StatusBadRequest, "that is not a poll")
		return
	}
	if closed, _ := m.Data["closed"].(bool); closed {
		writeErr(w, http.StatusConflict, "the poll is closed")
		return
	}
	var in struct {
		Options []int `json:"options"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	opts, _ := m.Data["options"].([]any)
	multi, _ := m.Data["multi"].(bool)
	var clean []int
	seen := map[int]bool{}
	for _, o := range in.Options {
		if o >= 0 && o < len(opts) && !seen[o] {
			seen[o] = true
			clean = append(clean, o)
		}
	}
	if !multi && len(clean) > 1 {
		clean = clean[:1]
	}
	if err := s.store.SetVotes(ctx, m.ID, uid, clean); err != nil {
		s.fail(w, err, "vote")
		return
	}
	m, err = s.updateAndTell(ctx, uid, m.ID)
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// handleClosePoll closes (or reopens) a poll: the person who asked.
func (s *Server) handleClosePoll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMessage(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	if m.Kind != store.MsgPoll || m.UserID != uid {
		writeErr(w, http.StatusForbidden, "only the person who asked can close the poll")
		return
	}
	var in struct {
		Closed bool `json:"closed"`
	}
	_ = decode(r, &in)
	if _, err := s.store.SetMessageData(ctx, m.ID, func(d map[string]any) error { d["closed"] = in.Closed; return nil }); err != nil {
		s.fail(w, err, "poll")
		return
	}
	m, err = s.updateAndTell(ctx, uid, m.ID)
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// handleTodo changes a shared to-do list: tick or untick an item, add one,
// remove one, or give it to someone. Anyone in the conversation may.
func (s *Server) handleTodo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	m, err := s.store.GetMessage(ctx, me.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	if m.Kind != store.MsgTodo || m.Deleted {
		writeErr(w, http.StatusBadRequest, "that is not a to-do list")
		return
	}
	var in struct {
		Op       string `json:"op"` // toggle | add | remove | assign
		Item     int    `json:"item"`
		Text     string `json:"text"`
		Assignee int64  `json:"assignee"`
		Done     *bool  `json:"done"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	members := map[int64]bool{}
	if ids, err := s.store.MemberIDs(ctx, m.ConversationID); err == nil {
		for _, id := range ids {
			members[id] = true
		}
	}
	_, err = s.store.SetMessageData(ctx, m.ID, func(d map[string]any) error {
		items, _ := d["items"].([]any)
		find := func() map[string]any {
			for _, it := range items {
				if im, ok := it.(map[string]any); ok && int(num(im, "id")) == in.Item {
					return im
				}
			}
			return nil
		}
		switch in.Op {
		case "toggle":
			it := find()
			if it == nil {
				return store.ErrNotFound
			}
			done, _ := it["done"].(bool)
			if in.Done != nil {
				done = !*in.Done
			}
			it["done"] = !done
			if !done {
				it["done_by"], it["done_at"] = me.ID, store.Now()
			} else {
				delete(it, "done_by")
				delete(it, "done_at")
			}
		case "add":
			t := clip(strings.TrimSpace(in.Text), 200)
			if t == "" || len(items) >= 50 {
				return fmt.Errorf("an item needs words (at most 50 items)")
			}
			next := int(num(d, "next"))
			if next <= len(items) {
				next = len(items) + 1
			}
			it := map[string]any{"id": next, "text": t, "done": false}
			if members[in.Assignee] {
				it["assignee"] = in.Assignee
			}
			d["items"] = append(items, it)
			d["next"] = next + 1
		case "remove":
			var keep []any
			for _, it := range items {
				if im, ok := it.(map[string]any); ok && int(num(im, "id")) == in.Item {
					continue
				}
				keep = append(keep, it)
			}
			if keep == nil {
				keep = []any{}
			}
			d["items"] = keep
		case "assign":
			it := find()
			if it == nil {
				return store.ErrNotFound
			}
			if members[in.Assignee] {
				it["assignee"] = in.Assignee
			} else {
				delete(it, "assignee")
			}
		default:
			return fmt.Errorf("op is toggle, add, remove or assign")
		}
		return nil
	})
	if err != nil {
		if err == store.ErrNotFound {
			s.fail(w, err, "item")
		} else {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	m, err = s.updateAndTell(ctx, me.ID, m.ID)
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handlePin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	m, err := s.store.GetMessage(ctx, me.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	var in struct {
		On bool `json:"on"`
	}
	_ = decode(r, &in)
	if m.Deleted || m.Kind == store.MsgSystem {
		writeErr(w, http.StatusBadRequest, "that cannot be pinned")
		return
	}
	if err := s.store.Pin(ctx, m.ID, me.ID, in.On); err != nil {
		s.fail(w, err, "pin")
		return
	}
	if in.On && m.PinnedAt == "" {
		s.systemLine(ctx, m.ConversationID, me.Name+" pinned a message", map[string]any{"ref": m.ID})
	}
	m, err = s.updateAndTell(ctx, me.ID, m.ID)
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleStar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMessage(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	var in struct {
		On bool `json:"on"`
	}
	_ = decode(r, &in)
	if err := s.store.Star(ctx, uid, m.ID, in.On); err != nil {
		s.fail(w, err, "save")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "starred": in.On})
}

func (s *Server) handleSaved(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Starred(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "saved")
		return
	}
	s.decorateAll(list)
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	o := store.SearchOptions{Conversation: queryInt64(r, "conversation"), From: queryInt64(r, "from"), Kind: r.URL.Query().Get("kind"),
		Since: r.URL.Query().Get("since"), Until: r.URL.Query().Get("until"), Limit: queryInt(r, "limit")}
	if q == "" && o.Kind == "" && o.From == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"query": q, "results": []store.Message{}})
		return
	}
	if !validDate(o.Since) || !validDate(o.Until) {
		writeErr(w, http.StatusBadRequest, "dates are YYYY-MM-DD")
		return
	}
	list, err := s.store.Search(ctx, actorID(ctx), q, o)
	if err != nil {
		s.fail(w, err, "search")
		return
	}
	s.decorateAll(list)
	writeJSON(w, http.StatusOK, map[string]any{"query": q, "results": list})
}

// ---- files ----

var audioExt = map[string]string{"audio/webm": ".webm", "audio/ogg": ".ogg", "audio/mp4": ".m4a", "audio/mpeg": ".mp3", "audio/wav": ".wav", "audio/x-wav": ".wav", "audio/aac": ".aac"}

// handleUpload stores a photo, a file or a voice recording for a
// conversation; it goes into the next message the person sends there.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	cid := idParam(r, "id")
	if _, err := s.store.Role(ctx, uid, cid); err != nil {
		s.fail(w, err, "conversation")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes+1<<20)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("the file is missing or bigger than %d MB", s.cfg.MaxUploadBytes>>20))
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, s.cfg.MaxUploadBytes+1))
	if err != nil || int64(len(data)) > s.cfg.MaxUploadBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("files can be at most %d MB", s.cfg.MaxUploadBytes>>20))
		return
	}
	name := filepath.Base(strings.TrimSpace(hdr.Filename))
	if name == "." || name == "/" || name == "" {
		name = "file"
	}
	a := &store.Attachment{UserID: uid, ConversationID: cid, Name: clip(name, 200)}
	sniff := http.DetectContentType(data)
	declared := strings.Split(hdr.Header.Get("Content-Type"), ";")[0]
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case strings.HasPrefix(sniff, "image/") && sniff != "image/svg+xml":
		out, mime, e, wd, ht, err := preparePicture(data)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "this picture could not be read: "+err.Error())
			return
		}
		data, a.Mime, ext, a.Width, a.Height = out, mime, e, wd, ht
	case r.FormValue("voice") == "1" || strings.HasPrefix(declared, "audio/"):
		a.Mime = declared
		if !strings.HasPrefix(a.Mime, "audio/") {
			a.Mime = "audio/webm"
		}
		if e, ok := audioExt[a.Mime]; ok {
			ext = e
		}
		a.DurationMS, _ = strconv.Atoi(r.FormValue("duration_ms"))
	default:
		a.Mime = firstNonEmpty(mime.TypeByExtension(ext), sniff)
		if strings.HasPrefix(a.Mime, "text/html") || a.Mime == "image/svg+xml" {
			a.Mime = "application/octet-stream" // never served as a page
		}
	}
	if len(ext) > 10 || strings.ContainsAny(ext, "/\\") {
		ext = ""
	}
	a.File = store.RandomToken(12) + ext
	a.Bytes = int64(len(data))
	if err := os.MkdirAll(s.cfg.FilesDir, 0o755); err != nil {
		s.fail(w, err, "files")
		return
	}
	if err := os.WriteFile(filepath.Join(s.cfg.FilesDir, a.File), data, 0o644); err != nil {
		s.fail(w, err, "save file")
		return
	}
	if err := s.store.CreateAttachment(ctx, a); err != nil {
		_ = os.Remove(filepath.Join(s.cfg.FilesDir, a.File))
		s.fail(w, err, "save file")
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// handleFile serves a file to the people in its conversation.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	file := filepath.Base(chiParam(r, "file"))
	a, err := s.store.AttachmentByFile(ctx, actorID(ctx), file)
	if err != nil {
		s.fail(w, err, "file")
		return
	}
	f, err := os.Open(filepath.Join(s.cfg.FilesDir, a.File))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	h := w.Header()
	h.Set("Content-Type", a.Mime)
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	disp := "inline"
	if queryBool(r, "download") || !(strings.HasPrefix(a.Mime, "image/") || strings.HasPrefix(a.Mime, "audio/") || strings.HasPrefix(a.Mime, "video/") || a.Mime == "application/pdf") {
		disp = "attachment"
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": a.Name}))
	http.ServeContent(w, r, a.Name, fi.ModTime(), f)
}

func (s *Server) removeFiles(files []string) {
	for _, f := range files {
		if f = filepath.Base(f); f != "" && f != "." {
			_ = os.Remove(filepath.Join(s.cfg.FilesDir, f))
		}
	}
}

// cleanFiles lets go of uploads nobody sent within a day, and files on the
// disk the database does not know.
func (s *Server) cleanFiles(ctx context.Context) {
	files, err := s.store.StaleUploads(ctx, time.Now().Add(-24*time.Hour).UTC().Format(time.RFC3339Nano))
	if err == nil {
		s.removeFiles(files)
	}
	known, err := s.store.AllFiles(ctx)
	if err != nil {
		return
	}
	entries, err := os.ReadDir(s.cfg.FilesDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || known[e.Name()] {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > time.Hour {
			_ = os.Remove(filepath.Join(s.cfg.FilesDir, e.Name()))
		}
	}
}

// cleanCard keeps the parts of a card that are shown, as text.
func cleanCard(in map[string]any) map[string]any {
	title := clip(strings.TrimSpace(str(in, "title")), 200)
	if title == "" {
		return nil
	}
	out := map[string]any{"title": title}
	for k, n := range map[string]int{"app": 30, "kind": 30, "subtitle": 300, "date": 40, "ref": 120, "icon": 8, "badge": 60, "text": 1500} {
		if v := clip(strings.TrimSpace(str(in, k)), n); v != "" {
			out[k] = v
		}
	}
	for _, k := range []string{"url", "image"} {
		if v, ok := cleanURL(str(in, k)); ok && v != "" {
			out[k] = v
		}
	}
	if arr, ok := in["lines"].([]any); ok {
		var lines []any
		for _, l := range arr {
			if t, ok := l.(string); ok && strings.TrimSpace(t) != "" && len(lines) < 8 {
				lines = append(lines, clip(strings.TrimSpace(t), 200))
			}
		}
		if len(lines) > 0 {
			out["lines"] = lines
		}
	}
	if r := num(in, "rating"); r > 0 && r <= 5 {
		out["rating"] = r
	}
	if f, ok := in["fields"].(map[string]any); ok {
		clean := map[string]any{}
		for k, v := range f {
			if len(clean) >= 20 || len(k) > 40 {
				continue
			}
			switch t := v.(type) {
			case string:
				clean[k] = clip(t, 500)
			case float64, bool:
				clean[k] = t
			}
		}
		out["fields"] = clean
	}
	return out
}

// askedAI: a question to Pocket AI gets no "add to calendar" under it.
func askedAI(mentions []string) bool {
	for _, v := range mentions {
		if v == "ai" {
			return true
		}
	}
	return false
}
