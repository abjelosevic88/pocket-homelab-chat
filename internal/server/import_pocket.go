package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
)

// handleImport takes a Pocket Chat export (export.json) back: each
// conversation in it comes in as a group of the person's own, with the
// messages as they were (the person's own under their name, everyone
// else's with the name they had). The other people are not added — they
// may not be on this box, and an import never speaks for them. Importing
// the same file again adds nothing.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b, err := s.readBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the file is too big")
		return
	}
	var doc exportDoc
	if err := json.Unmarshal(b, &doc); err != nil || doc.App != "pocket-chat" {
		writeErr(w, http.StatusBadRequest, "that is not a Pocket Chat export")
		return
	}
	out, err := s.importPocket(ctx, userFrom(ctx), &doc)
	if err != nil {
		s.fail(w, err, "import")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// importKey is a message's stable id across boxes: its own client id when
// it had one (a send or an earlier import), else its time, author and text.
func importKey(m store.Message, author string) string {
	if m.ClientID != "" {
		return m.ClientID
	}
	h := sha1.Sum([]byte(m.CreatedAt + "|" + author + "|" + m.Body))
	return "pc:" + hex.EncodeToString(h[:10])
}

func (s *Server) importPocket(ctx context.Context, me *store.User, doc *exportDoc) (map[string]any, error) {
	convs, added, skipped := 0, 0, 0
	for _, ec := range doc.Conversations {
		names := map[int64]string{}
		for _, m := range ec.Members {
			names[m.UserID] = m.Name
		}
		type one struct {
			msg store.Message
			key string
		}
		var list []one
		var keys []string
		for _, m := range ec.Messages {
			if m.Deleted || m.Kind == store.MsgSystem {
				continue
			}
			mine := doc.Me != nil && m.UserID == doc.Me.ID && m.UserID > 0
			author := firstNonEmpty(m.AuthorName, names[m.UserID])
			switch {
			case mine:
				author = ""
			case m.Kind == store.MsgAI:
				author = firstNonEmpty(author, "Pocket AI")
			default:
				author = firstNonEmpty(author, "Someone")
			}
			body := m.Body
			for _, a := range m.Attachments {
				body = strings.TrimSpace(body + "\n(" + a.Name + ")")
			}
			if strings.TrimSpace(body) == "" {
				continue
			}
			k := importKey(m, author)
			nm := store.Message{Kind: store.MsgText, Body: clip(body, maxText), AuthorName: author, Source: "import", SourceRef: "pocket-chat",
				ClientID: k, CreatedAt: m.CreatedAt, Data: map[string]any{"imported": "pocket-chat"}}
			if mine {
				nm.UserID = me.ID
			}
			list = append(list, one{nm, k})
			keys = append(keys, k)
		}
		if len(list) == 0 {
			continue
		}
		cid := s.store.ImportedConversation(ctx, me.ID, keys)
		if cid == 0 {
			title := clip(firstNonEmpty(strings.TrimSpace(ec.Name), "Imported chat"), 80)
			c := &store.Conversation{Title: title, About: clip(ec.About, 500), Emoji: "💬", Color: groupColors[0], AI: true, Smart: true}
			if err := s.store.CreateGroup(ctx, me.ID, c, nil); err != nil {
				return nil, err
			}
			cid = c.ID
			convs++
		}
		n := 0
		for _, o := range list {
			m := o.msg
			m.ConversationID = cid
			dup, err := s.store.InsertMessage(ctx, &m)
			if err != nil {
				return nil, err
			}
			if dup {
				skipped++
				continue
			}
			n++
		}
		added += n
		if n > 0 {
			s.toMembers(ctx, cid, "conversation", map[string]any{"id": cid})
		}
	}
	return map[string]any{"conversations": convs, "messages": added, "skipped": skipped,
		"text": fmt.Sprintf("Brought in %d %s", added, plural(added, "message", "messages"))}, nil
}
