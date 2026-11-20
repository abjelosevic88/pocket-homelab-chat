package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/config"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// moveLinks starts Chat behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for alice@, bob@ or carol@.
func moveLinks(t *testing.T) *app {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "chat:read"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"chat","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	return newApp(t, func(c *config.Config) { c.CoreURL = coreSrv.URL })
}

func (a *app) raw(tok, method, path, ctype string, body []byte) (int, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.url+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func (a *app) moveImport(tok, path string, data []byte, multi bool) map[string]any {
	a.t.Helper()
	ctype, body := "application/json", data
	if multi {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", "WhatsApp Chat.txt")
		_, _ = fw.Write(data)
		_ = mw.Close()
		ctype, body = mw.FormDataContentType(), buf.Bytes()
	}
	code, out := a.raw(tok, "POST", path, ctype, body)
	if code != 200 {
		a.t.Fatalf("POST %s → %d %s", path, code, out)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m
}

func TestMoveLinkScopes(t *testing.T) {
	a := moveLinks(t)
	exp, imp, read := "pcx_bob_export", "pcx_bob_import", "pcx_bob_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "POST", "/api/v1/import/whatsapp", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "POST", "/api/v1/conversations", 403},
		{imp, "POST", "/api/v1/send", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
		{read, "POST", "/api/v1/import", 403},
	} {
		if code, body := a.raw(c.tok, c.method, c.path, "application/json", []byte("{}")); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, body := a.raw(exp, "GET", core.PortablePath, "", nil)
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 1 || len(p.Imports) != 2 || p.Imports[1].Form != "file" {
		t.Fatalf("portable: %s", body)
	}
}

// Move brings Alice's chats to Bob: each import adds once, as Bob's own
// group, and nothing of one person shows up for the other.
func TestMoveImportsAddOnce(t *testing.T) {
	a := moveLinks(t)
	carol := a.carol()
	dm := a.ok(a.alice, "POST", "/api/v1/conversations", map[string]any{"kind": "direct", "with": a.ids["Carol"]})
	cid := id(dm)
	a.ok(a.alice, "POST", "/api/v1/conversations/"+cid+"/messages", map[string]any{"text": "who takes Mila today?"})
	a.ok(carol, "POST", "/api/v1/conversations/"+cid+"/messages", map[string]any{"text": "I do, at four"})

	code, export := a.raw("pcx_alice_export", "GET", "/api/v1/export.json", "", nil)
	if code != 200 || !strings.Contains(string(export), "who takes Mila today?") || !strings.Contains(string(export), "I do, at four") {
		t.Fatalf("alice's export: %d %s", code, export)
	}
	if _, bob := a.raw("pcx_bob_export", "GET", "/api/v1/export.json", "", nil); strings.Contains(string(bob), "Mila") {
		t.Fatalf("bob's export holds alice's chat: %s", bob)
	}

	first := a.moveImport("pcx_bob_import", "/api/v1/import", export, false)
	if num(first, "conversations") != 1 || num(first, "messages") != 2 {
		t.Fatalf("first import: %v", first)
	}
	second := a.moveImport("pcx_bob_import", "/api/v1/import", export, false)
	if num(second, "conversations")+num(second, "messages") != 0 || num(second, "skipped") != 2 {
		t.Fatalf("second import added: %v", second)
	}

	w1 := a.moveImport("pcx_bob_import", "/api/v1/import/whatsapp", []byte(waExport), true)
	w2 := a.moveImport("pcx_bob_import", "/api/v1/import/whatsapp", []byte(waExport), true)
	if num(w1, "messages") != 3 || num(w2, "messages") != 0 || num(w2, "skipped") != 3 || num(w1, "conversation_id") != num(w2, "conversation_id") {
		t.Fatalf("whatsapp imports: %v then %v", w1, w2)
	}

	groups := 0
	for _, c := range a.list(a.bob, "/api/v1/conversations") {
		if str(c.(map[string]any), "kind") == "group" {
			groups++
		}
	}
	if groups != 2 {
		t.Fatalf("bob has %d groups", groups)
	}
	var cnt int
	_ = a.st.DB.QueryRow(`SELECT COUNT(1) FROM members WHERE user_id IN (?, ?)`, a.ids["Alice"], a.ids["Carol"]).Scan(&cnt)
	if cnt != 2 {
		t.Fatalf("alice/carol memberships changed: %d", cnt)
	}
}
