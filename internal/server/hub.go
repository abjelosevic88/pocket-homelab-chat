package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ---- live updates ----
//
// Each open Chat window holds one Server-Sent Events stream. A change in a
// conversation (a message, an edit, a reaction, someone reading, someone
// typing) goes to the streams of that conversation's members only. Someone
// with an open stream counts as online, so they get no push notification.

type event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type subscriber struct {
	uid int64
	ch  chan event
}

type hub struct {
	mu       sync.Mutex
	subs     map[int64]map[*subscriber]struct{}
	lastSeen map[int64]time.Time
	typing   map[string]time.Time // "<conv>:<uid>" → last typing signal
}

func newHub() *hub {
	return &hub{subs: map[int64]map[*subscriber]struct{}{}, lastSeen: map[int64]time.Time{}, typing: map[string]time.Time{}}
}

func (h *hub) add(uid int64) *subscriber {
	sub := &subscriber{uid: uid, ch: make(chan event, 64)}
	h.mu.Lock()
	if h.subs[uid] == nil {
		h.subs[uid] = map[*subscriber]struct{}{}
	}
	h.subs[uid][sub] = struct{}{}
	h.lastSeen[uid] = time.Now()
	h.mu.Unlock()
	return sub
}

// remove reports whether the person has no stream left (went offline).
func (h *hub) remove(sub *subscriber) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs[sub.uid], sub)
	h.lastSeen[sub.uid] = time.Now()
	if len(h.subs[sub.uid]) == 0 {
		delete(h.subs, sub.uid)
		return true
	}
	return false
}

// online reports whether the person has Chat open somewhere.
func (h *hub) online(uid int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[uid]) > 0
}

// seen is when the person last had Chat open (zero = not since the start).
func (h *hub) seen(uid int64) time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs[uid]) > 0 {
		return time.Now()
	}
	return h.lastSeen[uid]
}

// send delivers an event to these people's open windows. A window that is
// too far behind misses it and catches up on its next reload.
func (h *hub) send(uids []int64, ev event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, uid := range uids {
		for sub := range h.subs[uid] {
			select {
			case sub.ch <- ev:
			default:
			}
		}
	}
}

// typingAllowed throttles typing signals to one per person per 3 seconds.
func (h *hub) typingAllowed(cid, uid int64) bool {
	key := fmt.Sprintf("%d:%d", cid, uid)
	h.mu.Lock()
	defer h.mu.Unlock()
	if t, ok := h.typing[key]; ok && time.Since(t) < 3*time.Second {
		return false
	}
	h.typing[key] = time.Now()
	if len(h.typing) > 5000 {
		h.typing = map[string]time.Time{}
	}
	return true
}

// toMembers sends an event to everyone in a conversation.
func (s *Server) toMembers(ctx context.Context, cid int64, typ string, data any) {
	ids, err := s.store.MemberIDs(ctx, cid)
	if err != nil {
		return
	}
	s.hub.send(ids, event{Type: typ, Data: data})
}

// toPeople sends an event to some people (someone removed from a group still hears it).
func (s *Server) toPeople(ids []int64, typ string, data any) {
	s.hub.send(ids, event{Type: typ, Data: data})
}

// handleStream is the live stream for the signed-in person.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	u := userFrom(r.Context())
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	wasOffline := !s.hub.online(u.ID)
	sub := s.hub.add(u.ID)
	if wasOffline {
		s.presenceChanged(u.ID, true)
	}
	defer func() {
		if s.hub.remove(sub) {
			s.presenceChanged(u.ID, false)
		}
	}()
	fmt.Fprintf(w, "retry: 3000\nevent: hello\ndata: {\"user_id\":%d}\n\n", u.ID)
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stop:
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case ev := <-sub.ch:
			b, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// presenceChanged tells the people someone shares a conversation with that
// they came online or left.
func (s *Server) presenceChanged(uid int64, online bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ids, err := s.store.Contacts(ctx, uid)
	if err != nil {
		return
	}
	s.hub.send(ids, event{Type: "presence", Data: map[string]any{"user_id": uid, "online": online, "seen": time.Now().UTC().Format(time.RFC3339)}})
}
