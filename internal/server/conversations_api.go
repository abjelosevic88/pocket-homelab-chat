package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
)

// ---- people and conversations ----

// personView is someone on the box with whether they have Chat open.
type personView struct {
	store.Person
	Online bool   `json:"online"`
	Seen   string `json:"seen,omitempty"`
}

func (s *Server) personViews(list []store.Person) []personView {
	out := make([]personView, 0, len(list))
	for _, p := range list {
		v := personView{Person: p, Online: s.hub.online(p.ID)}
		if t := s.hub.seen(p.ID); !t.IsZero() {
			v.Seen = t.UTC().Format(time.RFC3339)
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.People(r.Context())
	if err != nil {
		s.fail(w, err, "people")
		return
	}
	writeJSON(w, http.StatusOK, s.personViews(list))
}

// convView is a conversation with its title as the caller sees it and who is online.
type convView struct {
	store.Conversation
	Name   string  `json:"name"`
	Online []int64 `json:"online"`
}

func (s *Server) view(uid int64, c store.Conversation) convView {
	v := convView{Conversation: c, Online: []int64{}}
	switch c.Kind {
	case store.KindSelf:
		v.Name = "Me"
	case store.KindDirect:
		v.Name = "Just you"
		for _, m := range c.Members {
			if m.UserID != uid {
				v.Name = firstNonEmpty(m.Name, m.Email)
			}
		}
	default:
		v.Name = firstNonEmpty(c.Title, "Group")
	}
	for _, m := range c.Members {
		if m.UserID != uid && s.hub.online(m.UserID) {
			v.Online = append(v.Online, m.UserID)
		}
	}
	if v.Last != nil {
		s.decorate(v.Last)
	}
	return v
}

func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
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
	out := make([]convView, 0, len(list))
	for _, c := range list {
		out = append(out, s.view(uid, c))
	}
	writeJSON(w, http.StatusOK, out)
}

// conversation loads one conversation for the caller, or answers 404.
func (s *Server) conversation(w http.ResponseWriter, r *http.Request) (*store.Conversation, bool) {
	c, err := s.store.GetConversation(r.Context(), actorID(r.Context()), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "conversation")
		return nil, false
	}
	return c, true
}

func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conversation(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.view(actorID(r.Context()), *c))
}

var groupColors = []string{"#128c7e", "#2563eb", "#7c3aed", "#db2777", "#ea580c", "#ca8a04", "#0891b2", "#4d7c0f"}

func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	var in struct {
		Kind    string  `json:"kind"`
		With    int64   `json:"with"`
		Title   string  `json:"title"`
		About   string  `json:"about"`
		Emoji   string  `json:"emoji"`
		Color   string  `json:"color"`
		Members []int64 `json:"members"`
		AI      *bool   `json:"ai"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	var cid int64
	switch in.Kind {
	case store.KindSelf:
		id, _, err := s.store.SelfFor(ctx, me.ID)
		if err != nil {
			s.fail(w, err, "your own space")
			return
		}
		cid = id
	case store.KindDirect:
		if in.With == me.ID {
			id, _, err := s.store.SelfFor(ctx, me.ID)
			if err != nil {
				s.fail(w, err, "your own space")
				return
			}
			cid = id
			break
		}
		if _, err := s.store.GetUser(ctx, in.With); err != nil {
			writeErr(w, http.StatusBadRequest, "that person is not on this Pocket Chat")
			return
		}
		id, _, err := s.store.DirectWith(ctx, me.ID, in.With)
		if err != nil {
			s.fail(w, err, "start a chat")
			return
		}
		cid = id
	default:
		title := strings.TrimSpace(in.Title)
		if title == "" {
			writeErr(w, http.StatusBadRequest, "give the group a name")
			return
		}
		var others []int64
		for _, id := range in.Members {
			if id != me.ID {
				if _, err := s.store.GetUser(ctx, id); err == nil {
					others = append(others, id)
				}
			}
		}
		c := &store.Conversation{Title: clip(title, 80), About: clip(strings.TrimSpace(in.About), 500), Emoji: clipEmoji(in.Emoji), Color: in.Color, AI: true, Smart: true}
		if in.AI != nil {
			c.AI = *in.AI
		}
		if !oneOf(c.Color, groupColors) {
			c.Color = groupColors[int(time.Now().UnixNano()/1e6)%len(groupColors)]
		}
		if err := s.store.CreateGroup(ctx, me.ID, c, others); err != nil {
			s.fail(w, err, "create group")
			return
		}
		cid = c.ID
		s.systemLine(ctx, cid, me.Name+" made the group “"+c.Title+"”", nil)
	}
	c, err := s.store.GetConversation(ctx, me.ID, cid)
	if err != nil {
		s.fail(w, err, "conversation")
		return
	}
	for _, m := range c.Members {
		if m.UserID != me.ID {
			s.toPeople([]int64{m.UserID}, "conversation", map[string]any{"id": cid})
		}
	}
	writeJSON(w, http.StatusCreated, s.view(me.ID, *c))
}

func clipEmoji(v string) string {
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > 8 {
		return ""
	}
	return v
}

// handleUpdateConversation changes a group's name, description, look, or
// the Pocket AI and suggestion switches (any member may; everyone sees it).
func (s *Server) handleUpdateConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	c, ok := s.conversation(w, r)
	if !ok {
		return
	}
	var in struct {
		Title *string `json:"title"`
		About *string `json:"about"`
		Emoji *string `json:"emoji"`
		Color *string `json:"color"`
		AI    *bool   `json:"ai"`
		Smart *bool   `json:"smart"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	var lines []string
	if c.Kind == store.KindGroup {
		if in.Title != nil {
			t := clip(strings.TrimSpace(*in.Title), 80)
			if t == "" {
				writeErr(w, http.StatusBadRequest, "a group needs a name")
				return
			}
			if t != c.Title {
				lines = append(lines, me.Name+" renamed the group to “"+t+"”")
			}
			c.Title = t
		}
		if in.About != nil {
			c.About = clip(strings.TrimSpace(*in.About), 500)
		}
		if in.Emoji != nil {
			c.Emoji = clipEmoji(*in.Emoji)
		}
		if in.Color != nil && oneOf(*in.Color, groupColors) {
			c.Color = *in.Color
		}
	}
	if in.AI != nil && *in.AI != c.AI {
		c.AI = *in.AI
		if c.Kind != store.KindSelf {
			if c.AI {
				lines = append(lines, me.Name+" let Pocket AI answer here when someone writes @ai")
			} else {
				lines = append(lines, me.Name+" switched Pocket AI off in this conversation")
			}
		}
	}
	if in.Smart != nil {
		c.Smart = *in.Smart
	}
	if err := s.store.UpdateConversation(ctx, c); err != nil {
		s.fail(w, err, "save")
		return
	}
	for _, l := range lines {
		s.systemLine(ctx, c.ID, l, nil)
	}
	s.toMembers(ctx, c.ID, "conversation", map[string]any{"id": c.ID})
	c, _ = s.store.GetConversation(ctx, me.ID, c.ID)
	writeJSON(w, http.StatusOK, s.view(me.ID, *c))
}

// handleUpdateMine saves the caller's own switches for a conversation:
// notifications, mute, pin, archive and the unsent draft.
func (s *Server) handleUpdateMine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in store.MemberPrefs
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Notify != nil && !oneOf(*in.Notify, []string{"default", "all", "mentions", "off"}) {
		writeErr(w, http.StatusBadRequest, "notifications are default, all, mentions or off")
		return
	}
	if in.MutedUntil != nil && *in.MutedUntil != "" {
		if _, err := time.Parse(time.RFC3339, *in.MutedUntil); err != nil {
			writeErr(w, http.StatusBadRequest, "muted_until is a time (RFC 3339)")
			return
		}
	}
	if in.Draft != nil {
		d := clip(*in.Draft, 8000)
		in.Draft = &d
	}
	if _, err := s.store.Role(ctx, uid, idParam(r, "id")); err != nil {
		s.fail(w, err, "conversation")
		return
	}
	if err := s.store.UpdateMember(ctx, uid, idParam(r, "id"), in); err != nil {
		s.fail(w, err, "save")
		return
	}
	if in.Draft == nil || in.Pinned != nil || in.Archived != nil {
		s.toPeople([]int64{uid}, "conversation", map[string]any{"id": idParam(r, "id")})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRead(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	cid := idParam(r, "id")
	var in struct {
		Upto int64 `json:"upto"`
	}
	_ = decode(r, &in)
	if _, err := s.store.Role(ctx, uid, cid); err != nil {
		s.fail(w, err, "conversation")
		return
	}
	if in.Upto <= 0 {
		in.Upto = 1 << 62
	}
	moved, err := s.store.MarkRead(ctx, uid, cid, in.Upto)
	if err != nil {
		s.fail(w, err, "read")
		return
	}
	if moved {
		c, _ := s.store.GetConversation(ctx, uid, cid)
		last := int64(0)
		if c != nil {
			last = c.LastReadID
		}
		st, _ := s.store.GetSettings(ctx, uid)
		if st.ReadReceipts {
			s.toMembers(ctx, cid, "read", map[string]any{"conversation_id": cid, "user_id": uid, "last_read_id": last})
		} else {
			s.toPeople([]int64{uid}, "read", map[string]any{"conversation_id": cid, "user_id": uid, "last_read_id": last})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTyping(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	cid := idParam(r, "id")
	if _, err := s.store.Role(ctx, me.ID, cid); err != nil {
		s.fail(w, err, "conversation")
		return
	}
	if s.hub.typingAllowed(cid, me.ID) {
		ids, _ := s.store.MemberIDs(ctx, cid)
		var others []int64
		for _, id := range ids {
			if id != me.ID {
				others = append(others, id)
			}
		}
		s.toPeople(others, "typing", map[string]any{"conversation_id": cid, "user_id": me.ID, "name": me.Name})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAddMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	c, ok := s.conversation(w, r)
	if !ok {
		return
	}
	if c.Kind != store.KindGroup {
		writeErr(w, http.StatusBadRequest, "people can be added to a group only; start a group instead")
		return
	}
	var in struct {
		Members []int64 `json:"members"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	added, err := s.store.AddMembers(ctx, c.ID, in.Members)
	if err != nil {
		s.fail(w, err, "add people")
		return
	}
	var names []string
	for _, id := range added {
		if u, err := s.store.GetUser(ctx, id); err == nil {
			names = append(names, u.Name)
		}
	}
	if len(names) > 0 {
		s.systemLine(ctx, c.ID, me.Name+" added "+humanList(names), nil)
	}
	s.toMembers(ctx, c.ID, "conversation", map[string]any{"id": c.ID})
	c, _ = s.store.GetConversation(ctx, me.ID, c.ID)
	writeJSON(w, http.StatusOK, s.view(me.ID, *c))
}

// handleRemoveMember: an owner removes someone from a group; anyone leaves
// (removes themselves). Leaving a direct chat hides it until the next message.
func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	c, ok := s.conversation(w, r)
	if !ok {
		return
	}
	who := idParam(r, "uid")
	if who != me.ID && (c.Kind != store.KindGroup || c.Role != "owner") {
		writeErr(w, http.StatusForbidden, "only the group's owner can remove someone")
		return
	}
	if c.Kind == store.KindSelf {
		writeErr(w, http.StatusBadRequest, "your own space stays")
		return
	}
	target, err := s.store.GetUser(ctx, who)
	if err != nil {
		s.fail(w, err, "person")
		return
	}
	before, _ := s.store.MemberIDs(ctx, c.ID)
	emptied, err := s.store.RemoveMember(ctx, c.ID, who)
	if err != nil {
		s.fail(w, err, "remove")
		return
	}
	if !emptied && c.Kind == store.KindGroup {
		if who == me.ID {
			s.systemLine(ctx, c.ID, me.Name+" left", nil)
		} else {
			s.systemLine(ctx, c.ID, me.Name+" removed "+target.Name, nil)
		}
	}
	s.toPeople(before, "conversation", map[string]any{"id": c.ID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": emptied})
}

func (s *Server) handleSetRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, ok := s.conversation(w, r)
	if !ok {
		return
	}
	if c.Kind != store.KindGroup || c.Role != "owner" {
		writeErr(w, http.StatusForbidden, "only the group's owner can do that")
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := decode(r, &in); err != nil || !oneOf(in.Role, []string{"owner", "member"}) {
		writeErr(w, http.StatusBadRequest, "role is owner or member")
		return
	}
	if err := s.store.SetRole(ctx, c.ID, idParam(r, "uid"), in.Role); err != nil {
		s.fail(w, err, "role")
		return
	}
	s.toMembers(ctx, c.ID, "conversation", map[string]any{"id": c.ID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDeleteConversation deletes a group for everyone (its owner only).
func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, ok := s.conversation(w, r)
	if !ok {
		return
	}
	if c.Kind != store.KindGroup || c.Role != "owner" {
		writeErr(w, http.StatusForbidden, "only a group's owner can delete it for everyone; you can leave it")
		return
	}
	ids, _ := s.store.MemberIDs(ctx, c.ID)
	files, _ := s.store.ConversationFiles(ctx, c.ID)
	if err := s.store.DeleteConversation(ctx, c.ID); err != nil {
		s.fail(w, err, "delete")
		return
	}
	s.removeFiles(files)
	s.toPeople(ids, "conversation", map[string]any{"id": c.ID, "deleted": true})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePinned(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, ok := s.conversation(w, r)
	if !ok {
		return
	}
	list, err := s.store.Pinned(ctx, actorID(ctx), c.ID)
	if err != nil {
		s.fail(w, err, "pinned")
		return
	}
	s.decorateAll(list)
	writeJSON(w, http.StatusOK, list)
}

// handleShared lists what was shared in a conversation: photos, files,
// links or cards from the apps.
func (s *Server) handleShared(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c, ok := s.conversation(w, r)
	if !ok {
		return
	}
	switch kind := r.URL.Query().Get("type"); kind {
	case "photos", "files":
		list, err := s.store.Shared(ctx, uid, c.ID, kind == "photos", 300)
		if err != nil {
			s.fail(w, err, "shared")
			return
		}
		writeJSON(w, http.StatusOK, list)
	case "links", "cards", "polls", "todos", "voice":
		list, err := s.store.Search(ctx, uid, "", store.SearchOptions{Conversation: c.ID, Kind: kind, Limit: 200})
		if err != nil {
			s.fail(w, err, "shared")
			return
		}
		s.decorateAll(list)
		writeJSON(w, http.StatusOK, list)
	default:
		writeErr(w, http.StatusBadRequest, "type is photos, files, links, cards, polls, todos or voice")
	}
}

func (s *Server) handleUnread(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, convs, mentions, err := s.store.UnreadTotals(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "unread")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": n, "conversations": convs, "mentions": mentions})
}

// systemLine writes "Ana added Bob" into a conversation.
func (s *Server) systemLine(ctx context.Context, cid int64, text string, data map[string]any) {
	m := &store.Message{ConversationID: cid, Kind: store.MsgSystem, Body: text, Data: data}
	if _, err := s.store.InsertMessage(ctx, m); err != nil {
		s.log.Warn("system line", "err", err)
		return
	}
	s.toMembers(ctx, cid, "message", m)
}

func humanList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

var errNotMember = errors.New("not in this conversation")
