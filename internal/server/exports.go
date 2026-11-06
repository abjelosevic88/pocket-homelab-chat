package server

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
)

// ---- your chats, out and in ----

// exportDoc is everything one person can see, as JSON.
type exportDoc struct {
	App           string               `json:"app"`
	Version       string               `json:"version"`
	Exported      string               `json:"exported_at"`
	Conversations []exportConversation `json:"conversations"`
	Made          []store.Made         `json:"made"`
}

type exportConversation struct {
	ID       int64           `json:"id"`
	Kind     string          `json:"kind"`
	Name     string          `json:"name"`
	About    string          `json:"about,omitempty"`
	Members  []store.Member  `json:"members"`
	Messages []store.Message `json:"messages"`
}

func (s *Server) export(ctx context.Context, uid int64) exportDoc {
	doc := exportDoc{App: "pocket-chat", Version: Version, Exported: store.Now(), Conversations: []exportConversation{}}
	list, _ := s.store.ListConversations(ctx, uid)
	for _, c := range list {
		ec := exportConversation{ID: c.ID, Kind: c.Kind, Name: s.view(uid, c).Name, About: c.About, Members: c.Members, Messages: []store.Message{}}
		var before int64
		for {
			page, err := s.store.ListMessages(ctx, uid, c.ID, before, 0, 200)
			if err != nil || len(page) == 0 {
				break
			}
			ec.Messages = append(page, ec.Messages...)
			before = page[0].ID
		}
		doc.Conversations = append(doc.Conversations, ec)
	}
	doc.Made, _ = s.store.MadeList(ctx, uid, 100000)
	return doc
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-chat-export.json"`)
	writeJSON(w, http.StatusOK, s.export(r.Context(), actorID(r.Context())))
}

// handleExportText is one conversation as plain text, like a WhatsApp export.
func (s *Server) handleExportText(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c, ok := s.conversation(w, r)
	if !ok {
		return
	}
	var all []store.Message
	var before int64
	for {
		page, err := s.store.ListMessages(ctx, uid, c.ID, before, 0, 200)
		if err != nil || len(page) == 0 {
			break
		}
		all = append(page, all...)
		before = page[0].ID
	}
	name := s.view(uid, *c).Name
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Pocket Chat - %s.txt"`, strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, name)))
	bw := bufio.NewWriter(w)
	for _, m := range all {
		t, _ := time.Parse(time.RFC3339Nano, m.CreatedAt)
		stamp := t.Local().Format("02.01.2006, 15:04")
		if m.Kind == store.MsgSystem {
			fmt.Fprintf(bw, "%s - %s\n", stamp, m.Body)
			continue
		}
		text := describe(&m)
		if m.Deleted {
			text = "(deleted)"
		}
		fmt.Fprintf(bw, "%s - %s: %s\n", stamp, firstNonEmpty(m.AuthorName, "Someone"), text)
	}
	_ = bw.Flush()
}

// ---- WhatsApp import ----
//
// "Export chat" in WhatsApp gives a .txt (or a .zip with the photos). Chat
// reads both phone formats, lets the person say which name is who on this
// box, and brings the messages (and the photos in a .zip) into a new group
// or an existing conversation. Importing the same file again adds nothing
// twice.

var waLine = regexp.MustCompile(`^\x{200e}?\[?(\d{1,4}[./-]\d{1,2}[./-]\d{1,4})\.?,?\s+(\d{1,2}:\d{2}(?::\d{2})?(?:\s?[AaPp]\.?\s?[Mm]\.?)?)\]?\s*(?:-\s)?(.*)$`)
var waAttached = regexp.MustCompile(`<attached:\s*([^>]+)>|^\x{200e}?(\S+\.(?:jpg|jpeg|png|webp|gif|heic|pdf|mp4|opus|m4a|docx?|xlsx?))\s+\(file attached\)`)

type waMessage struct {
	at   time.Time
	name string
	text string
	file string
}

type waParse struct {
	messages []waMessage
	names    map[string]int
}

func parseWhatsApp(text string) waParse {
	type raw struct{ d, t, rest string }
	var lines []raw
	sc := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(text, "\r\n", "\n")))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		if m := waLine.FindStringSubmatch(l); m != nil {
			lines = append(lines, raw{m[1], m[2], m[3]})
		} else if len(lines) > 0 {
			lines[len(lines)-1].rest += "\n" + l
		}
	}
	// day/month or month/day: whichever part goes above 12 decides
	dayFirst := true
	for _, l := range lines {
		p := regexp.MustCompile(`[./-]`).Split(l.d, -1)
		if len(p) != 3 {
			continue
		}
		a, _ := strconv.Atoi(p[0])
		b, _ := strconv.Atoi(p[1])
		if len(p[0]) == 4 {
			continue
		}
		if b > 12 {
			dayFirst = false
			break
		}
		if a > 12 {
			break
		}
	}
	out := waParse{names: map[string]int{}}
	for _, l := range lines {
		name, body, ok := strings.Cut(l.rest, ": ")
		if !ok || strings.TrimSpace(name) == "" || len(name) > 80 {
			continue // "Messages are end-to-end encrypted", "Ana added Bob"
		}
		at, err := waTime(l.d, l.t, dayFirst)
		if err != nil {
			continue
		}
		name = strings.Trim(strings.TrimSpace(name), "‎‪‬~ ")
		body = strings.TrimSpace(strings.TrimPrefix(body, "‎"))
		msg := waMessage{at: at, name: name, text: body}
		if m := waAttached.FindStringSubmatch(body); m != nil {
			msg.file = strings.TrimSpace(m[1] + m[2])
			msg.text = strings.TrimSpace(waAttached.ReplaceAllString(body, ""))
		}
		lower := strings.ToLower(msg.text)
		if strings.Contains(lower, "<media omitted>") || strings.HasSuffix(lower, " omitted") || lower == "null" {
			msg.text = "(" + strings.Trim(strings.TrimPrefix(lower, "‎"), "<>") + ")"
		}
		if msg.text == "This message was deleted" || msg.text == "You deleted this message" {
			continue
		}
		out.messages = append(out.messages, msg)
		out.names[name]++
	}
	return out
}

func waTime(d, t string, dayFirst bool) (time.Time, error) {
	p := regexp.MustCompile(`[./-]`).Split(d, -1)
	if len(p) != 3 {
		return time.Time{}, fmt.Errorf("date")
	}
	n := make([]int, 3)
	for i := range p {
		n[i], _ = strconv.Atoi(p[i])
	}
	var y, mo, day int
	switch {
	case len(p[0]) == 4:
		y, mo, day = n[0], n[1], n[2]
	case dayFirst:
		day, mo, y = n[0], n[1], n[2]
	default:
		mo, day, y = n[0], n[1], n[2]
	}
	if y < 100 {
		y += 2000
	}
	t = strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(t), ".", ""), " ", " "))
	pm, am := strings.HasSuffix(t, "PM"), strings.HasSuffix(t, "AM")
	t = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(t, "PM"), "AM"))
	hp := strings.Split(t, ":")
	h, _ := strconv.Atoi(hp[0])
	mi, _ := strconv.Atoi(hp[1])
	sec := 0
	if len(hp) > 2 {
		sec, _ = strconv.Atoi(hp[2])
	}
	if pm && h < 12 {
		h += 12
	}
	if am && h == 12 {
		h = 0
	}
	if mo < 1 || mo > 12 || day < 1 || day > 31 || h > 23 || mi > 59 {
		return time.Time{}, fmt.Errorf("date")
	}
	return time.Date(y, time.Month(mo), day, h, mi, sec, 0, time.Local), nil
}

// readExport finds the chat text (and the files beside it) in an upload.
func readExport(data []byte) (string, map[string]*zip.File, error) {
	if !bytes.HasPrefix(data, []byte("PK")) {
		return string(data), nil, nil
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", nil, err
	}
	files := map[string]*zip.File{}
	var chat *zip.File
	for _, f := range zr.File {
		base := path.Base(f.Name)
		files[base] = f
		if strings.HasSuffix(strings.ToLower(base), ".txt") && (chat == nil || base == "_chat.txt") {
			chat = f
		}
	}
	if chat == nil {
		return "", nil, fmt.Errorf("there is no chat .txt in the zip")
	}
	rc, err := chat.Open()
	if err != nil {
		return "", nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, 64<<20))
	return string(b), files, err
}

func (s *Server) handleImportWhatsApp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	r.Body = http.MaxBytesReader(w, r.Body, 512<<20)
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "choose the .txt or .zip WhatsApp made (Export chat)")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the file could not be read")
		return
	}
	text, files, err := readExport(data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p := parseWhatsApp(text)
	if len(p.messages) == 0 {
		writeErr(w, http.StatusBadRequest, "no WhatsApp messages found in this file")
		return
	}
	if r.FormValue("preview") == "1" {
		type nameCount struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		}
		var names []nameCount
		for n, c := range p.names {
			names = append(names, nameCount{n, c})
		}
		sort.Slice(names, func(i, j int) bool { return names[i].Count > names[j].Count })
		writeJSON(w, http.StatusOK, map[string]any{"messages": len(p.messages), "names": names, "files": len(files),
			"first": p.messages[0].at.Format("2006-01-02"), "last": p.messages[len(p.messages)-1].at.Format("2006-01-02")})
		return
	}
	// who is who: {"Ana Petrović": 3, "Me": <my id>}
	people := map[string]int64{}
	if v := r.FormValue("people"); v != "" {
		if err := json.Unmarshal([]byte(v), &people); err != nil {
			writeErr(w, http.StatusBadRequest, "people is a JSON object of name → person id")
			return
		}
	}
	for name, id := range people {
		if id != me.ID {
			if _, err := s.store.GetUser(ctx, id); err != nil {
				delete(people, name)
			}
		}
	}
	cid, _ := strconv.ParseInt(r.FormValue("conversation"), 10, 64)
	if cid > 0 {
		if _, err := s.store.Role(ctx, me.ID, cid); err != nil {
			s.fail(w, err, "conversation")
			return
		}
	} else {
		title := clip(firstNonEmpty(strings.TrimSpace(r.FormValue("title")), "WhatsApp chat"), 80)
		var others []int64
		for _, id := range people {
			others = append(others, id)
		}
		c := &store.Conversation{Title: title, Emoji: "💬", Color: groupColors[0], AI: true, Smart: true}
		if err := s.store.CreateGroup(ctx, me.ID, c, others); err != nil {
			s.fail(w, err, "create group")
			return
		}
		cid = c.ID
	}
	members, _ := s.store.MemberIDs(ctx, cid)
	isMember := map[int64]bool{}
	for _, id := range members {
		isMember[id] = true
	}
	added, dup, filesIn := 0, 0, 0
	for _, wm := range p.messages {
		h := sha1.Sum([]byte(wm.at.Format(time.RFC3339) + "|" + wm.name + "|" + wm.text + "|" + wm.file))
		m := &store.Message{ConversationID: cid, Kind: store.MsgText, Body: clip(wm.text, maxText), Source: "import", SourceRef: "whatsapp",
			ClientID: "wa:" + hex.EncodeToString(h[:10]), CreatedAt: wm.at.UTC().Format(time.RFC3339Nano), Data: map[string]any{"imported": "whatsapp"}}
		if id, ok := people[wm.name]; ok && isMember[id] {
			m.UserID = id
		} else {
			m.AuthorName = wm.name
		}
		var zf *zip.File
		if wm.file != "" && files != nil {
			if f, ok := files[wm.file]; ok && f.UncompressedSize64 <= uint64(s.cfg.MaxUploadBytes) {
				zf = f
			}
		}
		if m.Body == "" && zf == nil {
			m.Body = "(" + firstNonEmpty(wm.file, "a file") + " was not in the export)"
		}
		isDup, err := s.store.InsertMessage(ctx, m)
		if err != nil {
			s.fail(w, err, "import")
			return
		}
		if isDup {
			dup++
			continue
		}
		added++
		if zf != nil {
			if att := s.importFile(ctx, zf, me.ID, cid); att != nil {
				if n, _ := s.store.ClaimAttachments(ctx, me.ID, cid, m.ID, []int64{att.ID}); n > 0 {
					filesIn++
				}
			}
		}
	}
	s.systemLine(ctx, cid, fmt.Sprintf("%s brought %d %s from WhatsApp", me.Name, added, plural(added, "message", "messages")), nil)
	s.toMembers(ctx, cid, "conversation", map[string]any{"id": cid})
	writeJSON(w, http.StatusOK, map[string]any{"conversation_id": cid, "messages": added, "skipped": dup, "files": filesIn})
}

// importFile stores one photo or file from a WhatsApp zip as an upload.
func (s *Server) importFile(ctx context.Context, zf *zip.File, uid, cid int64) *store.Attachment {
	rc, err := zf.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, s.cfg.MaxUploadBytes+1))
	if err != nil || int64(len(data)) > s.cfg.MaxUploadBytes {
		return nil
	}
	name := path.Base(zf.Name)
	a := &store.Attachment{UserID: uid, ConversationID: cid, Name: name}
	ext := strings.ToLower(filepath.Ext(name))
	sniff := http.DetectContentType(data)
	switch {
	case strings.HasPrefix(sniff, "image/"):
		out, mime, e, wd, ht, err := preparePicture(data)
		if err != nil {
			return nil
		}
		data, a.Mime, ext, a.Width, a.Height = out, mime, e, wd, ht
	case ext == ".opus" || ext == ".m4a" || ext == ".ogg":
		a.Mime = map[string]string{".opus": "audio/ogg", ".m4a": "audio/mp4", ".ogg": "audio/ogg"}[ext]
	default:
		a.Mime = sniff
		if strings.HasPrefix(a.Mime, "text/html") {
			a.Mime = "application/octet-stream"
		}
	}
	a.File = store.RandomToken(12) + ext
	a.Bytes = int64(len(data))
	if err := os.MkdirAll(s.cfg.FilesDir, 0o755); err != nil {
		return nil
	}
	if err := os.WriteFile(filepath.Join(s.cfg.FilesDir, a.File), data, 0o644); err != nil {
		return nil
	}
	if err := s.store.CreateAttachment(ctx, a); err != nil {
		_ = os.Remove(filepath.Join(s.cfg.FilesDir, a.File))
		return nil
	}
	return a
}
