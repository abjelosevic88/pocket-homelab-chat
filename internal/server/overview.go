package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
)

// ---- for the rest of the box: Pocket Day, Search, Pocket AI, Flows ----

// handleSummary is a short overview: what is unread and where, who
// mentioned you, today's activity.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	if _, _, err := s.store.SelfFor(ctx, uid); err != nil {
		s.fail(w, err, "your own space")
		return
	}
	list, err := s.store.ListConversations(ctx, uid)
	if err != nil {
		s.fail(w, err, "conversations")
		return
	}
	n, convs, mentions, _ := s.store.UnreadTotals(ctx, uid)
	unread := []map[string]any{}
	for _, c := range list {
		if c.Unread == 0 || c.Archived {
			continue
		}
		v := s.view(uid, c)
		item := map[string]any{"id": c.ID, "name": v.Name, "kind": c.Kind, "unread": c.Unread, "mentioned": c.Mentioned, "last_message_at": c.LastMessageAt}
		if c.Last != nil {
			item["last"] = firstNonEmpty(c.Last.AuthorName, "Someone") + ": " + clip(describe(c.Last), 160)
		}
		unread = append(unread, item)
		if len(unread) >= 10 {
			break
		}
	}
	ment := []map[string]any{}
	if mentions > 0 {
		ms, _ := s.store.UnreadMessages(ctx, uid, 0, 100)
		me := " " + strconv.FormatInt(uid, 10) + " "
		for _, m := range ms {
			if strings.Contains(" "+strings.Join(m.Mentions, " ")+" ", me) {
				ment = append(ment, map[string]any{"id": m.ID, "conversation_id": m.ConversationID, "conversation": m.ConversationTitle, "from": m.AuthorName, "text": clip(describe(&m), 200), "at": m.CreatedAt})
			}
		}
	}
	day := time.Now()
	from, to := dayBounds(day)
	act, _ := s.store.ActivityBetween(ctx, uid, from, to)
	today := 0
	for _, a := range act {
		today += a.Messages
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"unread":  map[string]any{"messages": n, "conversations": convs, "mentions": mentions},
		"waiting": unread, "mentions": ment, "conversations": len(list), "today": map[string]any{"messages": today, "conversations": act},
		"url": strings.TrimRight(s.cfg.BaseURL, "/") + "/",
	})
}

// dayBounds is a local day as stored (UTC) times.
func dayBounds(d time.Time) (string, string) {
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.Local)
	return start.UTC().Format(time.RFC3339Nano), start.AddDate(0, 0, 1).UTC().Format(time.RFC3339Nano)
}

// handleDay is one day in the person's chats: how busy each conversation
// was, the plans made, what was shared, the photos.
func (s *Server) handleDay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	date := chiParam(r, "date")
	d, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the date is YYYY-MM-DD")
		return
	}
	from, to := dayBounds(d)
	act, err := s.store.ActivityBetween(ctx, uid, from, to)
	if err != nil {
		s.fail(w, err, "day")
		return
	}
	msgs, _ := s.store.MessagesBetween(ctx, uid, from, to, 2000)
	plans, shared := []map[string]any{}, []map[string]any{}
	photos, voice, total, mine := 0, 0, 0, 0
	for _, m := range msgs {
		total++
		if m.UserID == uid {
			mine++
		}
		for _, a := range m.Attachments {
			if strings.HasPrefix(a.Mime, "image/") {
				photos++
			}
		}
		if m.Kind == store.MsgVoice {
			voice++
		}
		for _, h := range rows(map[string]any{"items": m.Data["hints"]}) {
			if str(h, "kind") == "event" && len(plans) < 10 {
				plans = append(plans, map[string]any{"conversation": m.ConversationTitle, "from": m.AuthorName, "text": clip(m.Body, 200), "date": str(h, "date"), "time": str(h, "time"), "message_id": m.ID, "conversation_id": m.ConversationID})
			}
		}
		if card, ok := m.Data["card"].(map[string]any); ok && len(shared) < 20 {
			shared = append(shared, map[string]any{"conversation": m.ConversationTitle, "from": m.AuthorName, "app": str(card, "app"), "title": str(card, "title"), "subtitle": str(card, "subtitle"), "message_id": m.ID, "conversation_id": m.ConversationID})
		}
	}
	summary := "No messages."
	if total > 0 {
		var parts []string
		for i, a := range act {
			if i == 3 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s (%d)", a.Title, a.Messages))
		}
		summary = fmt.Sprintf("%d %s, %d by you: %s", total, plural(total, "message", "messages"), mine, strings.Join(parts, ", "))
		if photos > 0 {
			summary += fmt.Sprintf("; %d %s", photos, plural(photos, "photo", "photos"))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": date, "messages": total, "mine": mine, "photos": photos, "voice": voice,
		"conversations": act, "plans": plans, "shared": shared, "summary": summary})
}

// handleSendTo posts a message without knowing a conversation id: to
// "me" (your own space), to a person (their email) or to a group (its id
// or its name). For Pocket Flows, Pocket AI and other apps.
func (s *Server) handleSendTo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	var in struct {
		To string `json:"to"`
		sendInput
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	to := strings.TrimSpace(in.To)
	var cid int64
	switch {
	case to == "" || strings.EqualFold(to, "me") || strings.EqualFold(to, "self"):
		id, _, err := s.store.SelfFor(ctx, me.ID)
		if err != nil {
			s.fail(w, err, "your own space")
			return
		}
		cid = id
	case strings.Contains(to, "@"):
		u, err := s.store.GetUserByEmail(ctx, strings.ToLower(to))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "nobody with that email is on Pocket Chat")
			return
		}
		if u.ID == me.ID {
			cid, _, err = s.store.SelfFor(ctx, me.ID)
		} else {
			cid, _, err = s.store.DirectWith(ctx, me.ID, u.ID)
		}
		if err != nil {
			s.fail(w, err, "chat")
			return
		}
	default:
		if id, err := strconv.ParseInt(to, 10, 64); err == nil {
			cid = id
			break
		}
		list, err := s.store.ListConversations(ctx, me.ID)
		if err != nil {
			s.fail(w, err, "conversations")
			return
		}
		var names []string
		want := store.Fold(to)
		for _, c := range list {
			v := s.view(me.ID, c)
			names = append(names, v.Name)
			if store.Fold(v.Name) == want {
				cid = c.ID
			}
		}
		if cid == 0 {
			writeErr(w, http.StatusBadRequest, "no conversation called “"+to+"”; yours are: "+strings.Join(names, ", "))
			return
		}
	}
	m, status, err := s.send(ctx, me, cid, in.sendInput)
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
