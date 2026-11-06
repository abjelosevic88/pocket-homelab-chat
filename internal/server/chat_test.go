package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
)

// carol adds a third person (a member) and returns her token.
func (a *app) carol() string {
	a.t.Helper()
	u := &store.User{Email: "carol@example.com", Name: "Carol", Role: "member"}
	if err := a.st.CreateUser(context.Background(), u); err != nil {
		a.t.Fatal(err)
	}
	tok, _, _ := a.st.CreateAPIToken(context.Background(), u.ID, "t")
	a.ids["Carol"] = u.ID
	return tok
}

func TestConversationsArePrivateToTheirMembers(t *testing.T) {
	a := newApp(t, nil)
	carol := a.carol()
	// Bob and Carol talk; Alice (the admin) is not in it
	dm := a.ok(a.bob, "POST", "/api/v1/conversations", map[string]any{"kind": "direct", "with": a.ids["Carol"]})
	cid := id(dm)
	msg := a.ok(a.bob, "POST", "/api/v1/conversations/"+cid+"/messages", map[string]any{"text": "a secret between us"})
	mid := id(msg)
	a.ok(carol, "GET", "/api/v1/conversations/"+cid, nil)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/conversations/" + cid},
		{"GET", "/api/v1/conversations/" + cid + "/messages"},
		{"POST", "/api/v1/conversations/" + cid + "/messages"},
		{"PUT", "/api/v1/conversations/" + cid},
		{"PUT", "/api/v1/conversations/" + cid + "/me"},
		{"POST", "/api/v1/conversations/" + cid + "/read"},
		{"GET", "/api/v1/conversations/" + cid + "/pinned"},
		{"PUT", "/api/v1/messages/" + mid},
		{"DELETE", "/api/v1/messages/" + mid},
		{"POST", "/api/v1/messages/" + mid + "/react"},
		{"POST", "/api/v1/messages/" + mid + "/star"},
		{"POST", "/api/v1/messages/" + mid + "/pin"},
	} {
		body := map[string]any{"text": "x", "emoji": "👍", "on": true}
		if code, m, _ := a.call(a.alice, c.method, c.path, body); code != http.StatusNotFound {
			t.Fatalf("admin %s %s → %d (%v), want 404", c.method, c.path, code, m)
		}
	}
	// nothing of it in Alice's lists or search
	for _, c := range a.list(a.alice, "/api/v1/conversations") {
		if id(c.(map[string]any)) == cid {
			t.Fatal("Alice lists a conversation she is not in")
		}
	}
	res := a.ok(a.alice, "GET", "/api/v1/search?q=secret", nil)
	if l, _ := res["results"].([]any); len(l) != 0 {
		t.Fatalf("Alice finds others' messages: %v", l)
	}
	res = a.ok(carol, "GET", "/api/v1/search?q=secret", nil)
	if l, _ := res["results"].([]any); len(l) != 1 {
		t.Fatalf("Carol should find the message: %v", res)
	}
}

func TestFilesOnlyForMembers(t *testing.T) {
	a := newApp(t, nil)
	g := a.ok(a.alice, "POST", "/api/v1/conversations", map[string]any{"kind": "group", "title": "Alice only"})
	cid := id(g)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "notes.txt")
	_, _ = fw.Write([]byte("hello file"))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", a.url+"/api/v1/conversations/"+cid+"/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+a.alice)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 201 {
		t.Fatalf("upload: %v %v", err, res.Status)
	}
	var att map[string]any
	_ = json.NewDecoder(res.Body).Decode(&att)
	res.Body.Close()
	// Bob cannot use Alice's upload in his own conversation, nor read it
	a.expect(404, a.bob, "GET", str(att, "url"), nil)
	a.ok(a.alice, "POST", "/api/v1/conversations/"+cid+"/messages", map[string]any{"attachments": []any{att["id"]}})
	code, _, _ := a.call(a.alice, "GET", str(att, "url"), nil)
	if code != 200 {
		t.Fatalf("Alice reads her file: %d", code)
	}
	a.expect(404, a.bob, "GET", str(att, "url"), nil)
}

func TestDirectAndOwnSpaceAreUnique(t *testing.T) {
	a := newApp(t, nil)
	one := a.ok(a.alice, "POST", "/api/v1/conversations", map[string]any{"kind": "direct", "with": a.ids["Bob"]})
	two := a.ok(a.bob, "POST", "/api/v1/conversations", map[string]any{"kind": "direct", "with": a.ids["Alice"]})
	if id(one) != id(two) {
		t.Fatalf("two direct chats between the same people: %s %s", id(one), id(two))
	}
	if str(one, "name") != "Bob" || str(two, "name") != "Alice" {
		t.Fatalf("direct chat names: %q %q", str(one, "name"), str(two, "name"))
	}
	list := a.list(a.alice, "/api/v1/conversations")
	selfs := 0
	for _, c := range list {
		if str(c.(map[string]any), "kind") == "self" {
			selfs++
		}
	}
	a.list(a.alice, "/api/v1/conversations")
	if selfs != 1 {
		t.Fatalf("own space: %d", selfs)
	}
}

func TestMessagesFlow(t *testing.T) {
	a := newApp(t, nil)
	g := a.ok(a.alice, "POST", "/api/v1/conversations", map[string]any{"kind": "group", "title": "Family", "members": []any{a.ids["Bob"]}})
	cid := id(g)
	p := "/api/v1/conversations/" + cid
	// retry-safe send
	m1 := a.ok(a.alice, "POST", p+"/messages", map[string]any{"text": "@bob can you buy milk tomorrow at 18:00? I paid 12,50 € for bread", "client_id": "c1"})
	again := a.ok(a.alice, "POST", p+"/messages", map[string]any{"text": "@bob can you buy milk tomorrow at 18:00? I paid 12,50 € for bread", "client_id": "c1"})
	if id(m1) != id(again) {
		t.Fatal("a retried send made two messages")
	}
	mentions, _ := m1["mentions"].([]any)
	if len(mentions) != 1 || mentions[0] != itoa(a.ids["Bob"]) {
		t.Fatalf("mentions: %v", mentions)
	}
	hints := rows(map[string]any{"items": obj(m1, "data")["hints"]})
	kinds := map[string]map[string]any{}
	for _, h := range hints {
		kinds[str(h, "kind")] = h
	}
	if kinds["event"] == nil || str(kinds["event"], "time") != "18:00" || kinds["expense"] == nil || num(kinds["expense"], "minor") != 1250 || kinds["task"] == nil {
		t.Fatalf("hints: %v", hints)
	}
	// Bob has it unread, with a mention
	u := a.ok(a.bob, "GET", "/api/v1/unread", nil)
	if num(u, "messages") != 1 || num(u, "mentions") != 1 {
		t.Fatalf("unread: %v", u)
	}
	a.ok(a.bob, "POST", p+"/read", map[string]any{})
	u = a.ok(a.bob, "GET", "/api/v1/unread", nil)
	if num(u, "messages") != 0 {
		t.Fatalf("unread after reading: %v", u)
	}
	// only your own messages change
	a.expect(403, a.bob, "PUT", "/api/v1/messages/"+id(m1), map[string]any{"text": "changed"})
	ed := a.ok(a.alice, "PUT", "/api/v1/messages/"+id(m1), map[string]any{"text": "milk please"})
	if str(ed, "body") != "milk please" || str(ed, "edited_at") == "" {
		t.Fatalf("edit: %v", ed)
	}
	// reactions toggle
	r := a.ok(a.bob, "POST", "/api/v1/messages/"+id(m1)+"/react", map[string]any{"emoji": "👍"})
	if len(r["reactions"].([]any)) != 1 {
		t.Fatalf("reaction: %v", r["reactions"])
	}
	r = a.ok(a.bob, "POST", "/api/v1/messages/"+id(m1)+"/react", map[string]any{"emoji": "👍"})
	if len(r["reactions"].([]any)) != 0 {
		t.Fatalf("reaction off: %v", r["reactions"])
	}
	// a star is yours only
	a.ok(a.bob, "POST", "/api/v1/messages/"+id(m1)+"/star", map[string]any{"on": true})
	if len(a.list(a.bob, "/api/v1/saved")) != 1 || len(a.list(a.alice, "/api/v1/saved")) != 0 {
		t.Fatal("stars are per person")
	}
	// poll: one choice each, closed by its author
	poll := a.ok(a.alice, "POST", p+"/messages", map[string]any{"kind": "poll", "poll": map[string]any{"question": "Lunch?", "options": []any{"Pizza", "Ćevapi"}}})
	v := a.ok(a.bob, "POST", "/api/v1/messages/"+id(poll)+"/vote", map[string]any{"options": []any{0, 1}})
	if len(v["votes"].([]any)) != 1 {
		t.Fatalf("single-choice poll took %v", v["votes"])
	}
	a.expect(403, a.bob, "POST", "/api/v1/messages/"+id(poll)+"/close", map[string]any{"closed": true})
	a.ok(a.alice, "POST", "/api/v1/messages/"+id(poll)+"/close", map[string]any{"closed": true})
	a.expect(409, a.bob, "POST", "/api/v1/messages/"+id(poll)+"/vote", map[string]any{"options": []any{1}})
	// to-do list: anyone ticks, adds
	todo := a.ok(a.alice, "POST", p+"/messages", map[string]any{"kind": "todo", "todo": map[string]any{"title": "Cottage", "items": []any{map[string]any{"text": "Wood", "assignee": a.ids["Bob"]}}}})
	a.ok(a.bob, "POST", "/api/v1/messages/"+id(todo)+"/todo", map[string]any{"op": "toggle", "item": 1})
	tl := a.ok(a.bob, "POST", "/api/v1/messages/"+id(todo)+"/todo", map[string]any{"op": "add", "text": "Games"})
	items := rows(map[string]any{"items": obj(tl, "data")["items"]})
	if len(items) != 2 || items[0]["done"] != true || num(items[0], "done_by") != float64(a.ids["Bob"]) {
		t.Fatalf("to-do: %v", items)
	}
	// delete: Bob can't delete Alice's; Alice (owner) can delete Bob's
	b1 := a.ok(a.bob, "POST", p+"/messages", map[string]any{"text": "oops"})
	a.expect(403, a.bob, "DELETE", "/api/v1/messages/"+id(m1), nil)
	a.ok(a.alice, "DELETE", "/api/v1/messages/"+id(b1), nil)
	gone := a.ok(a.alice, "GET", "/api/v1/conversations/"+cid, nil)
	_ = gone
	msgs := a.list(a.bob, p+"/messages")
	for _, x := range msgs {
		mm := x.(map[string]any)
		if id(mm) == id(b1) && (mm["deleted"] != true || str(mm, "body") != "") {
			t.Fatalf("deleted message still shows: %v", mm)
		}
	}
}

func TestLeavingAndRemoving(t *testing.T) {
	a := newApp(t, nil)
	carol := a.carol()
	g := a.ok(a.alice, "POST", "/api/v1/conversations", map[string]any{"kind": "group", "title": "Trip", "members": []any{a.ids["Bob"], a.ids["Carol"]}})
	cid := id(g)
	a.expect(403, a.bob, "DELETE", "/api/v1/conversations/"+cid+"/members/"+itoa(a.ids["Carol"]), nil)
	a.ok(a.alice, "DELETE", "/api/v1/conversations/"+cid+"/members/"+itoa(a.ids["Carol"]), nil)
	a.expect(404, carol, "GET", "/api/v1/conversations/"+cid, nil)
	// the owner leaves: Bob becomes the owner
	a.ok(a.alice, "DELETE", "/api/v1/conversations/"+cid+"/members/"+itoa(a.ids["Alice"]), nil)
	c := a.ok(a.bob, "GET", "/api/v1/conversations/"+cid, nil)
	if str(c, "role") != "owner" {
		t.Fatalf("Bob should own the group now: %v", c["role"])
	}
	a.expect(404, a.bob, "DELETE", "/api/v1/conversations/"+cid+"/members/9999", nil)
}

func TestLinkRoutes(t *testing.T) {
	cases := []struct {
		method, path string
		scopes       []string
		want         bool
	}{
		{"GET", "/api/v1/auth/me", nil, true},
		{"GET", "/api/v1/summary", []string{"chat:read"}, true},
		{"GET", "/api/v1/conversations/3/messages", []string{"chat:read"}, true},
		{"GET", "/api/v1/day/2026-11-06", []string{"chat:read"}, true},
		{"POST", "/api/v1/send", []string{"chat:read"}, false},
		{"POST", "/api/v1/send", []string{"chat:write"}, true},
		{"POST", "/api/v1/conversations/3/messages", []string{"chat:write"}, true},
		{"DELETE", "/api/v1/conversations/3", []string{"chat:write", "chat:read"}, false},
		{"GET", "/api/v1/settings", []string{"chat:read", "chat:write"}, false},
		{"POST", "/api/v1/messages/3/make", []string{"chat:write"}, false},
		{"GET", "/api/v1/export.json", []string{"chat:read"}, false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := linkAllows(c.scopes, r); got != c.want {
			t.Errorf("%s %s with %v: %v, want %v", c.method, c.path, c.scopes, got, c.want)
		}
	}
}

func TestHints(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local) // a Friday
	cases := []struct {
		text, kind, date, tm string
	}{
		{"Dinner on Saturday at 8pm?", "event", "2026-11-07", "20:00"},
		{"Vidimo se sutra u 19h", "event", "2026-11-07", "19:00"},
		{"Koncert je 15.11. u 18:30", "event", "2026-11-15", "18:30"},
		{"next friday?", "event", "2026-11-13", ""},
		{"Party on 3 December", "event", "2026-12-03", ""},
	}
	for _, c := range cases {
		var got *hint
		for _, h := range detectHints(c.text, now) {
			if h.Kind == c.kind {
				hh := h
				got = &hh
			}
		}
		if got == nil || got.Date != c.date || got.Time != c.tm {
			t.Errorf("%q: %+v, want %s %s", c.text, got, c.date, c.tm)
		}
	}
	for _, c := range []struct {
		text  string
		minor int64
		cur   string
	}{{"I paid 42 € for groceries", 4200, "EUR"}, {"gorivo 30 KM", 3000, "BAM"}, {"$9.99 a month", 999, "USD"}} {
		h := findMoney(c.text)
		if h == nil || h.Minor != c.minor || h.Currency != c.cur {
			t.Errorf("%q: %+v", c.text, h)
		}
	}
	if findMoney("I ran 5 km today") != nil {
		t.Error("5 km is a distance")
	}
	if hs := detectHints("Kupi hljeb i mlijeko", now); len(hs) != 1 || hs[0].Kind != "task" || hs[0].Title != "Kupi hljeb i mlijeko" {
		t.Errorf("task: %+v", hs)
	}
	if hs := detectHints("ok 👍", now); len(hs) != 0 {
		t.Errorf("nothing to suggest: %+v", hs)
	}
}

const waExport = `06/11/2026, 09:15 - Messages and calls are end-to-end encrypted.
06/11/2026, 09:16 - Ana Kovač: Good morning!
Who takes Mila today?
06/11/2026, 09:17 - Me: I do
13/11/2026, 18:02 - Ana Kovač: <Media omitted>
`

func TestWhatsAppImportIsIdempotent(t *testing.T) {
	a := newApp(t, nil)
	up := func(fields map[string]string) map[string]any {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", "WhatsApp Chat with Ana.txt")
		_, _ = io.WriteString(fw, waExport)
		for k, v := range fields {
			_ = mw.WriteField(k, v)
		}
		_ = mw.Close()
		req, _ := http.NewRequest("POST", a.url+"/api/v1/import/whatsapp", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+a.alice)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != 200 {
			t.Fatalf("import: %d %v", res.StatusCode, out)
		}
		return out
	}
	pre := up(map[string]string{"preview": "1"})
	if num(pre, "messages") != 3 || len(pre["names"].([]any)) != 2 {
		t.Fatalf("preview: %v", pre)
	}
	people, _ := json.Marshal(map[string]int64{"Me": a.ids["Alice"], "Ana Kovač": a.ids["Bob"]})
	first := up(map[string]string{"title": "Ana", "people": string(people)})
	if num(first, "messages") != 3 {
		t.Fatalf("first import: %v", first)
	}
	cid := itoa(int64(num(first, "conversation_id")))
	second := up(map[string]string{"conversation": cid, "people": string(people)})
	if num(second, "messages") != 0 || num(second, "skipped") != 3 {
		t.Fatalf("second import added again: %v", second)
	}
	msgs := a.list(a.bob, "/api/v1/conversations/"+cid+"/messages")
	var bodies []string
	for _, m := range msgs {
		bodies = append(bodies, str(m.(map[string]any), "body"))
	}
	joined := strings.Join(bodies, "|")
	if !strings.Contains(joined, "Good morning!\nWho takes Mila today?") || !strings.Contains(joined, "(media omitted)") {
		t.Fatalf("imported: %q", joined)
	}
}

func TestSendTo(t *testing.T) {
	a := newApp(t, nil)
	m := a.ok(a.alice, "POST", "/api/v1/send", map[string]any{"to": "me", "text": "remember the keys"})
	c := a.ok(a.alice, "GET", "/api/v1/conversations/"+itoa(int64(num(m, "conversation_id"))), nil)
	if str(c, "kind") != "self" {
		t.Fatalf("to me went to %v", c["kind"])
	}
	m = a.ok(a.alice, "POST", "/api/v1/send", map[string]any{"to": "bob@example.com", "text": "hi Bob"})
	a.ok(a.bob, "GET", "/api/v1/conversations/"+itoa(int64(num(m, "conversation_id"))), nil)
	a.expect(400, a.alice, "POST", "/api/v1/send", map[string]any{"to": "No Such Group", "text": "x"})
}

func TestExportHasOnlyYourConversations(t *testing.T) {
	a := newApp(t, nil)
	carol := a.carol()
	dm := a.ok(a.bob, "POST", "/api/v1/conversations", map[string]any{"kind": "direct", "with": a.ids["Carol"]})
	a.ok(carol, "POST", "/api/v1/conversations/"+id(dm)+"/messages", map[string]any{"text": "private"})
	doc := a.ok(a.alice, "GET", "/api/v1/export.json", nil)
	for _, c := range doc["conversations"].([]any) {
		if id(c.(map[string]any)) == id(dm) {
			t.Fatal("export holds a conversation Alice is not in")
		}
	}
}
