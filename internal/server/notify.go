package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
)

// ---- push notifications ----
//
// A message reaches a person's phone (through their own notification
// address, e.g. Pocket Alerts) only when they do not have Chat open, the
// conversation is not muted, it is not their quiet hours and their setting
// for that conversation lets it through: everything, only messages that
// name them (and direct chats), or nothing. Several messages in a short
// time come as one push.

type pushGate struct {
	mu   sync.Mutex
	last map[string]time.Time
}

var pushGates = &pushGate{last: map[string]time.Time{}}

// allow lets one push per person and conversation through every 90
// seconds; a mention always goes through.
func (g *pushGate) allow(uid, cid int64, mention bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := fmt.Sprintf("%d:%d", uid, cid)
	if t, ok := g.last[key]; ok && time.Since(t) < 90*time.Second && !mention {
		return false
	}
	g.last[key] = time.Now()
	if len(g.last) > 10000 {
		g.last = map[string]time.Time{}
	}
	return true
}

func quiet(st store.Settings, now time.Time) bool {
	from, to := st.QuietFrom, st.QuietTo
	if from == to {
		return false
	}
	h := now.Hour()
	if from < to {
		return h >= from && h < to
	}
	return h >= from || h < to
}

// notifyMembers pushes a new message to the members who should hear of it
// (sender nil: Pocket AI or an app wrote it).
func (s *Server) notifyMembers(ctx context.Context, conv *store.Conversation, m *store.Message, sender *store.User) {
	if m.Kind == store.MsgSystem {
		return
	}
	states, err := s.store.MemberStates(ctx, conv.ID)
	if err != nil {
		return
	}
	mentioned := map[int64]bool{}
	for _, id := range store.ParseMentions(m.Mentions) {
		mentioned[id] = true
	}
	askedBy := int64(num(m.Data, "asked_by"))
	now := time.Now()
	for _, ms := range states {
		if (sender != nil && ms.UserID == sender.ID) || s.hub.online(ms.UserID) {
			continue
		}
		if conv.Kind == store.KindSelf && m.Kind != store.MsgApp {
			continue
		}
		if ms.MutedUntil != "" {
			if t, err := time.Parse(time.RFC3339, ms.MutedUntil); err == nil && t.After(now) && !mentioned[ms.UserID] {
				continue
			}
		}
		st, err := s.store.GetSettings(ctx, ms.UserID)
		if err != nil || st.NotifyURL == "" {
			continue
		}
		mode := ms.Notify
		if mode == "" || mode == "default" {
			mode = st.NotifyMode
		}
		forMe := mentioned[ms.UserID] || conv.Kind == store.KindDirect || (m.Kind == store.MsgAI && askedBy == ms.UserID)
		switch mode {
		case "off":
			continue
		case "mentions":
			if !forMe {
				continue
			}
		}
		if quiet(st, now) && !mentioned[ms.UserID] {
			continue
		}
		if !pushGates.allow(ms.UserID, conv.ID, mentioned[ms.UserID]) {
			continue
		}
		convTitle := s.store.ConversationTitle(ctx, ms.UserID, conv.ID)
		who := firstNonEmpty(m.AuthorName, "Someone")
		what := clip(firstNonEmpty(describe(m), "a message"), 300)
		title, body := convTitle, who+" sent a message"
		if st.NotifyPreview {
			body = who + ": " + what
		}
		if conv.Kind == store.KindDirect {
			title = who
			if st.NotifyPreview {
				body = what
			}
		}
		if mentioned[ms.UserID] {
			title = who + " mentioned you in " + convTitle
		}
		link := strings.TrimRight(s.cfg.BaseURL, "/") + "/c/" + strconv.FormatInt(conv.ID, 10)
		prio := "info"
		if mentioned[ms.UserID] || conv.Kind == store.KindDirect {
			prio = "high"
		}
		go s.push(context.WithoutCancel(ctx), ms.UserID, st, title, body, link, prio)
	}
}

// morning runs every minute; at each person's own hour it sends the
// morning "what you missed" (when they want it).
func (s *Server) morning(ctx context.Context) {
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	for _, uid := range ids {
		st, err := s.store.GetSettings(ctx, uid)
		if err != nil || !st.NotifyDigest || now.Hour() != st.NotifyHour || now.Minute() > 10 {
			continue
		}
		if !s.once(ctx, fmt.Sprintf("push:%d:%s", uid, store.Today())) {
			continue
		}
		s.dailyCheck(ctx, uid, st)
	}
}

// once is true the first time it is asked for a key.
func (s *Server) once(ctx context.Context, key string) bool {
	if _, _, done := s.store.CacheGet(ctx, key); done {
		return false
	}
	_ = s.store.CachePut(ctx, key, "1")
	return true
}

// dailyCheck sends one person's morning message: unread messages by
// conversation and where they were mentioned.
func (s *Server) dailyCheck(ctx context.Context, uid int64, st store.Settings) (string, bool) {
	list, err := s.store.ListConversations(ctx, uid)
	if err != nil {
		return "", false
	}
	var lines []string
	total := 0
	for _, c := range list {
		if c.Unread == 0 || c.Archived {
			continue
		}
		total += c.Unread
		line := fmt.Sprintf("%s: %d %s", s.view(uid, c).Name, c.Unread, plural(c.Unread, "message", "messages"))
		if c.Mentioned > 0 {
			line += " (you are mentioned)"
		}
		lines = append(lines, line)
	}
	if total == 0 {
		return "", false
	}
	if len(lines) > 6 {
		lines = append(lines[:6], fmt.Sprintf("and %d more", len(lines)-6))
	}
	body := strings.Join(lines, "\n")
	title := fmt.Sprintf("%d unread %s", total, plural(total, "message", "messages"))
	if !s.push(ctx, uid, st, title, body, strings.TrimRight(s.cfg.BaseURL, "/")+"/", "info") {
		return body, false
	}
	return body, true
}

// push sends one message to a person's own notification address.
func (s *Server) push(ctx context.Context, uid int64, st store.Settings, title, body, link, priority string) bool {
	if st.NotifyURL == "" {
		return false
	}
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: title, Body: body, Priority: priority, Tags: []string{"speech_balloon"}, Link: link}); err != nil {
		s.log.Warn("notify failed", "user", uid, "err", err)
		return false
	}
	return true
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Pocket Chat", Body: notifyTestBody, Priority: "info", Tags: []string{"speech_balloon"}}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCheckNow sends the morning message now and says what it sent.
func (s *Server) handleCheckNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	body, sent := s.dailyCheck(ctx, uid, st)
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "body": body})
}

const notifyTestBody = "Notifications work. Messages arrive here when Pocket Chat is not open, as you set them up for each conversation."
