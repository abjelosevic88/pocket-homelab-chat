package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
)

// ---- Pocket AI in the conversation ----
//
// Writing @ai in a conversation (or "ai …" in your own space) asks Pocket
// AI. It answers in the conversation for everyone to read, with the
// conversation so far as context and — when the person allows it in
// Settings → AI — what their own apps say about the question: their
// calendar, tasks, the weather, the meal plan, trips, deliveries, notes and
// places. It is the asker's AI and the asker's apps; the others only see
// the answer.

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

// lookups are what the asker's own apps say about a question.
type lookup struct {
	app  string
	text string
}

func has(f string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(f, w) {
			return true
		}
	}
	return false
}

// gather reads the asker's apps that the question is about (by its words;
// nothing is read when the question is about none of them).
func (s *Server) gather(ctx context.Context, uid int64, question string) []lookup {
	f := store.Fold(question)
	c := s.clients(ctx, uid)
	now := time.Now()
	from, to := now, now.AddDate(0, 0, 14)
	if d, _ := findDate(f, now); d != "" {
		if t, err := time.ParseInLocation("2006-01-02", d, time.Local); err == nil {
			from, to = t, t.AddDate(0, 0, 1)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out []lookup
	try := func(a pocketApp, scope string, fn func(*siblingConn) (string, error)) {
		sib, err := optional(c.conn(ctx, a, scope))
		if err != nil || sib == nil {
			return
		}
		text, err := fn(sib)
		if err != nil {
			s.log.Debug("ai lookup", "app", a.slug, "err", err)
			return
		}
		if strings.TrimSpace(text) != "" {
			out = append(out, lookup{a.name, text})
		}
	}
	if has(f, "free", "busy", "calendar", "schedule", "plan", "when", "meet", "appointment", "event", "week", "weekend", "today", "tomorrow", "tonight",
		"slobod", "zauzet", "kalendar", "kad", "termin", "sastan", "vikend", "danas", "sutra", "veceras", "subot", "nedjelj", "nedelj", "petak", "ponedj", "utor", "srijed", "sred", "cetvrt") {
		try(appCalendar, "calendar:read", func(sib *siblingConn) (string, error) {
			v, err := getJSONFrom(ctx, sib, "/api/v1/events", url.Values{"from": {from.Format("2006-01-02")}, "to": {to.Format("2006-01-02")}, "tz": {time.Local.String()}})
			if err != nil {
				return "", err
			}
			var lines []string
			for _, e := range rows(v, "events") {
				allDay, _ := e["all_day"].(bool)
				lines = append(lines, "- "+joinNonEmpty(" · ", when(str(e, "start"), str(e, "end"), allDay), str(e, "title"), str(e, "location")))
			}
			if len(lines) == 0 {
				return fmt.Sprintf("Nothing in the calendar from %s to %s.", from.Format("Mon 2 Jan"), to.AddDate(0, 0, -1).Format("Mon 2 Jan")), nil
			}
			return fmt.Sprintf("Calendar %s – %s:\n%s", from.Format("Mon 2 Jan"), to.AddDate(0, 0, -1).Format("Mon 2 Jan"), strings.Join(lines, "\n")), nil
		})
	}
	if has(f, "task", "todo", "to-do", "to do", "remind", "list", "zadat", "obavez", "podsjet", "podset", "spisak", "lista") {
		try(appTasks, "tasks:read", func(sib *siblingConn) (string, error) {
			v, err := getJSONFrom(ctx, sib, "/api/v1/tasks", url.Values{"status": {"open"}, "due_to": {to.Format("2006-01-02")}, "limit": {"25"}})
			if err != nil {
				return "", err
			}
			var lines []string
			for _, t := range rows(v, "tasks") {
				lines = append(lines, "- "+joinNonEmpty(" · ", str(t, "title"), str(t, "due")))
			}
			return "Open tasks:\n" + strings.Join(lines, "\n"), nil
		})
	}
	if has(f, "weather", "rain", "snow", "sun", "cold", "warm", "hot", "temperature", "umbrella", "wind", "vrijeme", "vreme", "kis", "snijeg", "sneg", "sunc", "hladno", "toplo", "temperatur", "kisobran", "vjetar", "vetar") {
		try(appWeather, "weather:read", func(sib *siblingConn) (string, error) {
			v, err := getJSONFrom(ctx, sib, "/api/v1/forecast", url.Values{"hours": {"24"}})
			if err != nil {
				return "", err
			}
			m, _ := v.(map[string]any)
			var lines []string
			if loc := obj(m, "location"); str(loc, "name") != "" {
				lines = append(lines, "Place: "+str(loc, "name"))
			}
			if n := obj(m, "now"); len(n) > 0 {
				lines = append(lines, fmt.Sprintf("Now: %s, %.0f °C", str(n, "text", "condition"), num(n, "temp")))
			}
			for _, d := range rows(m, "days") {
				if len(lines) > 9 {
					break
				}
				lines = append(lines, fmt.Sprintf("- %s: %s, %.0f–%.0f °C, rain %.0f%% (%.1f mm), wind %.0f km/h", str(d, "date"), str(d, "text", "condition"), num(d, "t_min"), num(d, "t_max"), num(d, "precip_prob"), num(d, "precip"), num(d, "wind")))
			}
			return "Weather (metric):\n" + strings.Join(lines, "\n"), nil
		})
	}
	if has(f, "dinner", "lunch", "breakfast", "cook", "meal", "eat", "recipe", "food", "vecer", "rucak", "rucku", "dorucak", "kuha", "jelo", "jesti", "recept", "hrana") {
		try(appKitchen, "plan:read", func(sib *siblingConn) (string, error) {
			v, err := getJSONFrom(ctx, sib, "/api/v1/plan", url.Values{"from": {now.Format("2006-01-02")}, "to": {now.AddDate(0, 0, 7).Format("2006-01-02")}})
			if err != nil {
				return "", err
			}
			var lines []string
			for _, e := range rows(v, "entries") {
				lines = append(lines, "- "+joinNonEmpty(" · ", str(e, "date"), str(e, "slot"), firstNonEmpty(str(e, "recipe_title"), str(e, "title"))))
			}
			if len(lines) == 0 {
				return "No meals planned this week.", nil
			}
			return "Meal plan:\n" + strings.Join(lines, "\n"), nil
		})
	}
	if has(f, "trip", "travel", "flight", "holiday", "vacation", "hotel", "putovan", "put ", "let ", "odmor", "godisnj", "ljetovan", "letovan") {
		try(appTravel, "trips:read", func(sib *siblingConn) (string, error) {
			v, err := getJSONFrom(ctx, sib, "/api/v1/trips", nil)
			if err != nil {
				return "", err
			}
			var lines []string
			for _, t := range rows(v, "trips") {
				if str(t, "end_date") != "" && str(t, "end_date") < now.Format("2006-01-02") {
					continue
				}
				lines = append(lines, "- "+joinNonEmpty(" · ", str(t, "name"), str(t, "destination"), dateRange(str(t, "start_date"), str(t, "end_date")), str(t, "status")))
			}
			return "Upcoming trips:\n" + strings.Join(lines, "\n"), nil
		})
	}
	if has(f, "parcel", "package", "delivery", "deliver", "order", "paket", "posiljk", "dostav", "narudzb") {
		try(appParcels, "parcels:read", func(sib *siblingConn) (string, error) {
			v, err := getJSONFrom(ctx, sib, "/api/v1/parcels", url.Values{"view": {"active"}})
			if err != nil {
				return "", err
			}
			var lines []string
			for _, p := range rows(v, "parcels") {
				lines = append(lines, "- "+joinNonEmpty(" · ", firstNonEmpty(str(p, "title"), str(p, "shop")), str(p, "status_label"), str(p, "expected_on"), str(p, "location")))
			}
			if len(lines) == 0 {
				return "No deliveries on the way.", nil
			}
			return "Deliveries on the way:\n" + strings.Join(lines, "\n"), nil
		})
	}
	if q := topicWords(question); q != "" && has(f, "note", "wrote", "biljesk", "belesk", "zapis") {
		try(appNotes, "notes:read", func(sib *siblingConn) (string, error) {
			v, err := getJSONFrom(ctx, sib, "/api/v1/search", url.Values{"q": {q}, "limit": {"5"}})
			if err != nil {
				return "", err
			}
			var lines []string
			for _, n := range rows(v, "notes") {
				lines = append(lines, "- "+str(n, "title")+": "+clip(firstNonEmpty(str(n, "snippet"), str(n, "excerpt")), 300))
			}
			return "Notes about “" + q + "”:\n" + strings.Join(lines, "\n"), nil
		})
	}
	if q := topicWords(question); q != "" && has(f, "restaurant", "cafe", "bar", "place", "where to", "go out", "restoran", "kafic", "mjesto", "mesto", "gdje", "gde", "izac", "izadj") {
		try(appPlaces, "places:read", func(sib *siblingConn) (string, error) {
			v, err := getJSONFrom(ctx, sib, "/api/v1/places", url.Values{"limit": {"12"}, "sort": {"rating"}})
			if err != nil {
				return "", err
			}
			var lines []string
			for _, p := range rows(v, "places") {
				lines = append(lines, "- "+joinNonEmpty(" · ", str(p, "name"), str(p, "kind"), str(p, "city"), stars(num(p, "rating")), str(p, "status")))
			}
			return "Saved places:\n" + strings.Join(lines, "\n"), nil
		})
	}
	return out
}

// topicWords is a question without the question words (a search for notes).
func topicWords(q string) string {
	stop := map[string]bool{"what": true, "did": true, "i": true, "we": true, "my": true, "the": true, "a": true, "about": true, "in": true, "notes": true, "note": true, "write": true, "wrote": true,
		"sta": true, "šta": true, "sam": true, "smo": true, "o": true, "u": true, "biljeske": true, "bilješke": true, "ai": true, "@ai": true, "where": true, "is": true, "to": true, "go": true}
	var keep []string
	for _, w := range strings.Fields(strings.ToLower(strings.Trim(q, "?!. "))) {
		w = strings.Trim(w, "?!.,:;\"'")
		if w != "" && !stop[w] && !strings.HasPrefix(w, "@") {
			keep = append(keep, w)
		}
	}
	if len(keep) > 4 {
		keep = keep[:4]
	}
	return strings.Join(keep, " ")
}

const aiChatSystem = `You are Pocket AI, a helpful assistant inside Pocket Chat, a private chat on a family's own home server.
Someone in the conversation asked you something by writing @ai. Everyone in the conversation will read your answer.
Rules:
- Answer in the language of the question. Be brief and friendly, like a person in a chat: short paragraphs or a short list, no headings.
- Use the conversation for context (who said what, plans being made, questions asked).
- "From <person>'s apps" is that person's own data (their calendar, tasks, weather, meal plan…). Use only what the question needs; in a group, do not mention unrelated private details.
- Never invent facts about the people or their plans. If you do not know, say so briefly.
- If they are deciding something (a time, a place), you may suggest a clear option; they can make a poll themselves.
- Plain text with light Markdown (**bold**, lists) only.`

// answerAI answers an @ai question in the conversation.
func (s *Server) answerAI(ctx context.Context, conv *store.Conversation, q *store.Message, asker *store.User) {
	stopTyping := s.aiTyping(conv.ID)
	defer stopTyping()
	st, _ := s.store.GetSettings(ctx, asker.ID)
	reply := &store.Message{ConversationID: conv.ID, Kind: store.MsgAI, AuthorName: "Pocket AI", ReplyTo: q.ID,
		Data: map[string]any{"asked_by": asker.ID}}
	client, provider, why := s.aiFor(ctx, asker.ID, st)
	if client == nil {
		reply.Body = "I can't answer yet: " + firstNonEmpty(why, "no AI is set up") + ". " + asker.Name + " can set it up in Settings → AI."
		reply.Data["error"] = true
	} else {
		select {
		case s.aiSem <- struct{}{}:
			defer func() { <-s.aiSem }()
		case <-ctx.Done():
			return
		}
		history, _ := s.store.LastMessages(ctx, asker.ID, conv.ID, 40)
		var b strings.Builder
		fmt.Fprintf(&b, "Conversation: %s (%s). Members: %s.\nNow: %s.\n\n", s.store.ConversationTitle(ctx, asker.ID, conv.ID), conv.Kind, memberNames(conv), time.Now().Format("Monday 2 January 2006, 15:04"))
		b.WriteString("The conversation so far:\n")
		b.WriteString(s.transcript(history))
		var used []any
		if st.AIApps {
			for _, l := range s.gather(ctx, asker.ID, q.Body) {
				fmt.Fprintf(&b, "\nFrom %s's apps — %s:\n%s\n", asker.Name, l.app, l.text)
				used = append(used, l.app)
			}
		}
		fmt.Fprintf(&b, "\n%s asks you: %s", asker.Name, strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(q.Body, "@ai", ""), "@AI", "")))
		if st.Language != "" {
			fmt.Fprintf(&b, "\n(If the question's language is unclear, answer in %s.)", st.Language)
		}
		text, err := client.Complete(ctx, aiChatSystem, b.String(), false)
		if err != nil {
			reply.Body = "Sorry, I couldn't answer just now (" + clip(err.Error(), 160) + ")."
			reply.Data["error"] = true
		} else {
			reply.Body = clip(strings.TrimSpace(text), maxText)
			reply.Data["provider"] = provider
			if len(used) > 0 {
				reply.Data["used"] = used
			}
		}
	}
	if _, err := s.store.InsertMessage(ctx, reply); err != nil {
		s.log.Warn("ai answer", "err", err)
		return
	}
	if full, err := s.store.GetMessage(ctx, asker.ID, reply.ID); err == nil {
		s.decorate(full)
		s.toMembers(ctx, conv.ID, "message", shared(full))
		s.notifyMembers(ctx, conv, full, nil)
	}
}

// aiTyping shows "Pocket AI is writing…" until stopped.
func (s *Server) aiTyping(cid int64) func() {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			s.toMembers(ctx, cid, "typing", map[string]any{"conversation_id": cid, "user_id": 0, "name": "Pocket AI"})
			cancel()
			select {
			case <-done:
				return
			case <-t.C:
			}
		}
	}()
	return func() { close(done) }
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
