package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const msgCols = `x.id, x.conversation_id, COALESCE(x.user_id, 0), x.kind, x.body, x.data, COALESCE(x.reply_to, 0), x.mentions,
	CASE WHEN x.user_id IS NULL OR x.author_name != '' THEN x.author_name ELSE COALESCE((SELECT name FROM users WHERE id = x.user_id), '') END,
	x.source, x.source_ref, x.client_id, COALESCE(x.pinned_at, ''), COALESCE(x.edited_at, ''), x.deleted_at IS NOT NULL, x.created_at`

func scanMsg(row interface{ Scan(...any) error }) (*Message, error) {
	var m Message
	var data, mentions string
	err := row.Scan(&m.ID, &m.ConversationID, &m.UserID, &m.Kind, &m.Body, &data, &m.ReplyTo, &mentions, &m.AuthorName,
		&m.Source, &m.SourceRef, &m.ClientID, &m.PinnedAt, &m.EditedAt, &m.Deleted, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	m.Data = map[string]any{}
	_ = json.Unmarshal([]byte(data), &m.Data)
	m.Mentions = strings.Fields(mentions)
	if m.Mentions == nil {
		m.Mentions = []string{}
	}
	m.Attachments, m.Reactions = []Attachment{}, []Reaction{}
	return &m, nil
}

// Fold is text made easy to search: lower case, accents off (č → c, đ → dj).
func Fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'č', 'ć', 'ç':
			b.WriteRune('c')
		case 'š', 'ś':
			b.WriteRune('s')
		case 'ž', 'ź', 'ż':
			b.WriteRune('z')
		case 'đ':
			b.WriteString("dj")
		case 'á', 'à', 'â', 'ä', 'ã', 'å', 'ą':
			b.WriteRune('a')
		case 'é', 'è', 'ê', 'ë', 'ę', 'ě':
			b.WriteRune('e')
		case 'í', 'ì', 'î', 'ï':
			b.WriteRune('i')
		case 'ó', 'ò', 'ô', 'ö', 'õ', 'ø':
			b.WriteRune('o')
		case 'ú', 'ù', 'û', 'ü', 'ů':
			b.WriteRune('u')
		case 'ñ', 'ń', 'ň':
			b.WriteRune('n')
		case 'ł':
			b.WriteRune('l')
		case 'ř':
			b.WriteRune('r')
		case 'ý', 'ÿ':
			b.WriteRune('y')
		case 'ß':
			b.WriteString("ss")
		default:
			if unicode.IsPrint(r) {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// searchText is what search looks through: the words, a card's title and
// lines, a poll's question and options, a to-do list's items.
func searchText(m *Message) string {
	parts := []string{m.Body, m.AuthorName}
	for _, k := range []string{"title", "subtitle", "question", "text"} {
		if v, ok := m.Data[k].(string); ok {
			parts = append(parts, v)
		}
	}
	for _, k := range []string{"lines", "options", "items"} {
		if arr, ok := m.Data[k].([]any); ok {
			for _, it := range arr {
				switch t := it.(type) {
				case string:
					parts = append(parts, t)
				case map[string]any:
					if v, ok := t["text"].(string); ok {
						parts = append(parts, v)
					}
				}
			}
		}
	}
	if p, ok := m.Data["preview"].(map[string]any); ok {
		if v, ok := p["title"].(string); ok {
			parts = append(parts, v)
		}
	}
	return Fold(strings.Join(parts, " "))
}

func mentionsText(m []string) string {
	if len(m) == 0 {
		return ""
	}
	return " " + strings.Join(m, " ") + " "
}

// InsertMessage stores a message and moves the conversation's last message.
// A message with a client_id that was already sent is returned as it was
// (dup = true): a retried send never shows twice.
func (s *Store) InsertMessage(ctx context.Context, m *Message) (dup bool, err error) {
	if m.Data == nil {
		m.Data = map[string]any{}
	}
	if m.ClientID != "" {
		var id int64
		err := s.DB.QueryRowContext(ctx, `SELECT id FROM messages WHERE conversation_id = ? AND client_id = ?`, m.ConversationID, m.ClientID).Scan(&id)
		if err == nil {
			got, err := s.messagesByID(ctx, m.UserID, []int64{id}, true)
			if err != nil || len(got) == 0 {
				return true, notFound(err)
			}
			*m = got[0]
			return true, nil
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if m.CreatedAt == "" {
		m.CreatedAt = Now()
	}
	var uid any
	if m.UserID > 0 {
		uid = m.UserID
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO messages (conversation_id, user_id, kind, body, data, reply_to, mentions, author_name, source, source_ref, client_id, search, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ConversationID, uid, m.Kind, m.Body, toJSON(m.Data), nullI(&m.ReplyTo), mentionsText(m.Mentions), m.AuthorName, m.Source, m.SourceRef, m.ClientID, searchText(m), m.CreatedAt)
	if err != nil {
		return false, conflict(err)
	}
	m.ID, _ = res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET last_message_id = MAX(last_message_id, ?),
		last_message_at = CASE WHEN last_message_at IS NULL OR last_message_at < ? THEN ? ELSE last_message_at END WHERE id = ?`,
		m.ID, m.CreatedAt, m.CreatedAt, m.ConversationID); err != nil {
		return false, err
	}
	// the sender has read up to what they wrote
	if m.UserID > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE members SET last_read_id = MAX(last_read_id, ?), draft = '' WHERE user_id = ? AND conversation_id = ?`, m.ID, m.UserID, m.ConversationID); err != nil {
			return false, err
		}
	}
	return false, tx.Commit()
}

// ListMessages returns a page of a conversation for one member: the newest
// (or those before / after an id), oldest first.
func (s *Store) ListMessages(ctx context.Context, viewer, cid, before, after int64, limit int) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	q := `SELECT x.id FROM messages x WHERE x.conversation_id = ?`
	args := []any{cid}
	order := "DESC"
	if before > 0 {
		q += ` AND x.id < ?`
		args = append(args, before)
	}
	if after > 0 {
		q += ` AND x.id > ?`
		args = append(args, after)
		order = "ASC"
	}
	q += ` ORDER BY x.id ` + order + ` LIMIT ?`
	args = append(args, limit)
	ids, err := s.ids(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return s.messagesByID(ctx, viewer, ids, true)
}

// MessagesAround returns a page centred on one message (opening a search result).
func (s *Store) MessagesAround(ctx context.Context, viewer, cid, id int64, limit int) ([]Message, error) {
	half := limit / 2
	older, err := s.ids(ctx, `SELECT id FROM messages WHERE conversation_id = ? AND id <= ? ORDER BY id DESC LIMIT ?`, cid, id, half+1)
	if err != nil {
		return nil, err
	}
	newer, err := s.ids(ctx, `SELECT id FROM messages WHERE conversation_id = ? AND id > ? ORDER BY id ASC LIMIT ?`, cid, id, half)
	if err != nil {
		return nil, err
	}
	return s.messagesByID(ctx, viewer, append(older, newer...), true)
}

func (s *Store) ids(ctx context.Context, q string, args ...any) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// GetMessage returns one message the viewer can see (they are in its conversation).
func (s *Store) GetMessage(ctx context.Context, viewer, id int64) (*Message, error) {
	var cid int64
	err := s.DB.QueryRowContext(ctx, `SELECT x.conversation_id FROM messages x JOIN members m ON m.conversation_id = x.conversation_id AND m.user_id = ? WHERE x.id = ?`, viewer, id).Scan(&cid)
	if err != nil {
		return nil, notFound(err)
	}
	got, err := s.messagesByID(ctx, viewer, []int64{id}, true)
	if err != nil {
		return nil, err
	}
	if len(got) == 0 {
		return nil, ErrNotFound
	}
	return &got[0], nil
}

// messagesByID loads messages (oldest first) with what a member sees with
// them: attachments, reactions, votes, the quoted message, and their own
// stars and things made from them. full = false skips the extras (lists).
func (s *Store) messagesByID(ctx context.Context, viewer int64, ids []int64, full bool) ([]Message, error) {
	if len(ids) == 0 {
		return []Message{}, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+msgCols+` FROM messages x WHERE x.id IN (`+marks(len(ids))+`) ORDER BY x.id`, anyIDs(ids)...)
	if err != nil {
		return nil, err
	}
	var out []Message
	for rows.Next() {
		m, err := scanMsg(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if m.Deleted {
			m.Body, m.Data = "", map[string]any{}
		}
		out = append(out, *m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	idx := map[int64]int{}
	for i, m := range out {
		idx[m.ID] = i
	}
	if err := s.fillAttachments(ctx, out, idx); err != nil {
		return nil, err
	}
	if !full {
		return out, nil
	}
	args := anyIDs(ids)
	// reactions
	rr, err := s.DB.QueryContext(ctx, `SELECT message_id, emoji, user_id FROM reactions WHERE message_id IN (`+marks(len(ids))+`) ORDER BY created_at, user_id`, args...)
	if err != nil {
		return nil, err
	}
	for rr.Next() {
		var mid, uid int64
		var e string
		if err := rr.Scan(&mid, &e, &uid); err != nil {
			rr.Close()
			return nil, err
		}
		m := &out[idx[mid]]
		found := false
		for i := range m.Reactions {
			if m.Reactions[i].Emoji == e {
				m.Reactions[i].Users = append(m.Reactions[i].Users, uid)
				found = true
			}
		}
		if !found {
			m.Reactions = append(m.Reactions, Reaction{Emoji: e, Users: []int64{uid}})
		}
	}
	rr.Close()
	// votes
	vr, err := s.DB.QueryContext(ctx, `SELECT message_id, user_id, option FROM votes WHERE message_id IN (`+marks(len(ids))+`) ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	for vr.Next() {
		var mid int64
		var v Vote
		if err := vr.Scan(&mid, &v.UserID, &v.Option); err != nil {
			vr.Close()
			return nil, err
		}
		out[idx[mid]].Votes = append(out[idx[mid]].Votes, v)
	}
	vr.Close()
	for i := range out {
		if out[i].Kind == MsgPoll && out[i].Votes == nil {
			out[i].Votes = []Vote{}
		}
	}
	// the viewer's stars and what they made
	sr, err := s.DB.QueryContext(ctx, `SELECT message_id FROM stars WHERE user_id = ? AND message_id IN (`+marks(len(ids))+`)`, append([]any{viewer}, args...)...)
	if err != nil {
		return nil, err
	}
	for sr.Next() {
		var mid int64
		if err := sr.Scan(&mid); err == nil {
			out[idx[mid]].Starred = true
		}
	}
	sr.Close()
	mr, err := s.DB.QueryContext(ctx, `SELECT id, message_id, target, app, ref, title, url, created_at FROM made WHERE user_id = ? AND message_id IN (`+marks(len(ids))+`) ORDER BY id`, append([]any{viewer}, args...)...)
	if err != nil {
		return nil, err
	}
	for mr.Next() {
		var d Made
		if err := mr.Scan(&d.ID, &d.MessageID, &d.Target, &d.App, &d.Ref, &d.Title, &d.URL, &d.CreatedAt); err != nil {
			mr.Close()
			return nil, err
		}
		out[idx[d.MessageID]].Made = append(out[idx[d.MessageID]].Made, d)
	}
	mr.Close()
	// quoted messages
	var quoted []any
	for _, m := range out {
		if m.ReplyTo > 0 {
			quoted = append(quoted, m.ReplyTo)
		}
	}
	if len(quoted) > 0 {
		qr, err := s.DB.QueryContext(ctx, `SELECT x.id, COALESCE(x.user_id, 0), x.kind, x.body, x.deleted_at IS NOT NULL,
			CASE WHEN x.user_id IS NULL OR x.author_name != '' THEN x.author_name ELSE COALESCE((SELECT name FROM users WHERE id = x.user_id), '') END
			FROM messages x WHERE x.id IN (`+marks(len(quoted))+`)`, quoted...)
		if err != nil {
			return nil, err
		}
		qs := map[int64]Quote{}
		for qr.Next() {
			var q Quote
			if err := qr.Scan(&q.ID, &q.UserID, &q.Kind, &q.Body, &q.Deleted, &q.AuthorName); err != nil {
				qr.Close()
				return nil, err
			}
			if q.Deleted {
				q.Body = ""
			}
			q.Body = clipRunes(q.Body, 160)
			qs[q.ID] = q
		}
		qr.Close()
		for i := range out {
			if q, ok := qs[out[i].ReplyTo]; ok {
				out[i].Reply = &q
			}
		}
	}
	return out, nil
}

func (s *Store) fillAttachments(ctx context.Context, out []Message, idx map[int64]int) error {
	if len(out) == 0 {
		return nil
	}
	ids := make([]any, len(out))
	for i, m := range out {
		ids[i] = m.ID
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+attCols+` FROM attachments WHERE message_id IN (`+marks(len(ids))+`) ORDER BY id`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAtt(rows)
		if err != nil {
			return err
		}
		if i, ok := idx[a.MessageID]; ok && !out[i].Deleted {
			out[i].Attachments = append(out[i].Attachments, *a)
		}
	}
	return rows.Err()
}

// UpdateMessage saves a changed text (an edit) and data.
func (s *Store) UpdateMessage(ctx context.Context, m *Message, edited bool) error {
	q := `UPDATE messages SET body = ?, data = ?, mentions = ?, search = ?`
	args := []any{m.Body, toJSON(m.Data), mentionsText(m.Mentions), searchText(m)}
	if edited {
		m.EditedAt = Now()
		q += `, edited_at = ?`
		args = append(args, m.EditedAt)
	}
	args = append(args, m.ID)
	_, err := s.DB.ExecContext(ctx, q+` WHERE id = ?`, args...)
	return err
}

// SetMessageData replaces a message's data only (a link preview arriving,
// a transcript, a poll closed). It runs fn on the current data in one
// transaction so two people ticking a to-do list never lose a tick.
func (s *Store) SetMessageData(ctx context.Context, id int64, fn func(map[string]any) error) (map[string]any, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT data FROM messages WHERE id = ? AND deleted_at IS NULL`, id).Scan(&raw); err != nil {
		return nil, notFound(err)
	}
	data := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &data)
	if err := fn(data); err != nil {
		return nil, err
	}
	m := &Message{Data: data}
	var body, author string
	_ = tx.QueryRowContext(ctx, `SELECT body, author_name FROM messages WHERE id = ?`, id).Scan(&body, &author)
	m.Body, m.AuthorName = body, author
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET data = ?, search = ? WHERE id = ?`, toJSON(data), searchText(m), id); err != nil {
		return nil, err
	}
	return data, tx.Commit()
}

// DeleteMessage wipes a message for everyone, keeping a "deleted" line.
// It returns the files of its attachments, to remove from the disk.
func (s *Store) DeleteMessage(ctx context.Context, id int64) ([]string, error) {
	files, err := s.strings(ctx, `SELECT file FROM attachments WHERE message_id = ?`, id)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`UPDATE messages SET body = '', data = '{}', search = '', mentions = '', pinned_at = NULL, deleted_at = ? WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, Now(), id); err != nil {
			return nil, err
		}
	}
	for _, q := range []string{`DELETE FROM attachments WHERE message_id = ?`, `DELETE FROM reactions WHERE message_id = ?`, `DELETE FROM votes WHERE message_id = ?`, `DELETE FROM stars WHERE message_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return nil, err
		}
	}
	return files, tx.Commit()
}

func (s *Store) strings(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ToggleReaction adds the emoji from the person, or takes it away.
func (s *Store) ToggleReaction(ctx context.Context, mid, uid int64, emoji string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM reactions WHERE message_id = ? AND user_id = ? AND emoji = ?`, mid, uid, emoji)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return false, nil
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO reactions (message_id, user_id, emoji) VALUES (?, ?, ?)`, mid, uid, emoji)
	return true, err
}

// SetVotes replaces the person's choices in a poll.
func (s *Store) SetVotes(ctx context.Context, mid, uid int64, options []int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM votes WHERE message_id = ? AND user_id = ?`, mid, uid); err != nil {
		return err
	}
	for _, o := range options {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO votes (message_id, user_id, option) VALUES (?, ?, ?)`, mid, uid, o); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Star keeps a message in the person's Saved (or lets it go).
func (s *Store) Star(ctx context.Context, uid, mid int64, on bool) error {
	q := `DELETE FROM stars WHERE user_id = ? AND message_id = ?`
	if on {
		q = `INSERT OR IGNORE INTO stars (user_id, message_id) VALUES (?, ?)`
	}
	_, err := s.DB.ExecContext(ctx, q, uid, mid)
	return err
}

// Starred lists the person's saved messages that are still in their conversations, newest first.
func (s *Store) Starred(ctx context.Context, uid int64) ([]Message, error) {
	ids, err := s.ids(ctx, `SELECT st.message_id FROM stars st JOIN messages x ON x.id = st.message_id
		JOIN members m ON m.conversation_id = x.conversation_id AND m.user_id = st.user_id
		WHERE st.user_id = ? ORDER BY st.created_at DESC LIMIT 500`, uid)
	if err != nil {
		return nil, err
	}
	out, err := s.messagesByID(ctx, uid, ids, true)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, s.withTitles(ctx, uid, out)
}

// Pin pins a message in its conversation for everyone (or unpins it).
func (s *Store) Pin(ctx context.Context, mid, uid int64, on bool) error {
	var err error
	if on {
		_, err = s.DB.ExecContext(ctx, `UPDATE messages SET pinned_at = ?, pinned_by = ? WHERE id = ?`, Now(), uid, mid)
	} else {
		_, err = s.DB.ExecContext(ctx, `UPDATE messages SET pinned_at = NULL, pinned_by = NULL WHERE id = ?`, mid)
	}
	return err
}

// Pinned lists a conversation's pinned messages, last pinned first.
func (s *Store) Pinned(ctx context.Context, viewer, cid int64) ([]Message, error) {
	ids, err := s.ids(ctx, `SELECT id FROM messages WHERE conversation_id = ? AND pinned_at IS NOT NULL AND deleted_at IS NULL ORDER BY pinned_at DESC LIMIT 50`, cid)
	if err != nil {
		return nil, err
	}
	out, err := s.messagesByID(ctx, viewer, ids, true)
	sort.SliceStable(out, func(i, j int) bool { return out[i].PinnedAt > out[j].PinnedAt })
	return out, err
}

// SearchOptions narrows a search.
type SearchOptions struct {
	Conversation int64
	From         int64  // who wrote it
	Kind         string // links | files | cards | polls | todos | photos
	Since, Until string // YYYY-MM-DD
	Limit        int
}

// Search finds messages in the person's conversations, newest first.
func (s *Store) Search(ctx context.Context, uid int64, q string, o SearchOptions) ([]Message, error) {
	where := []string{"x.deleted_at IS NULL", "x.kind != 'system'"}
	args := []any{uid}
	for _, w := range strings.Fields(Fold(q)) {
		where = append(where, "x.search LIKE ? ESCAPE '\\'")
		args = append(args, "%"+likeEscape(w)+"%")
	}
	if o.Conversation > 0 {
		where, args = append(where, "x.conversation_id = ?"), append(args, o.Conversation)
	}
	if o.From > 0 {
		where, args = append(where, "x.user_id = ?"), append(args, o.From)
	}
	switch o.Kind {
	case "links":
		where = append(where, "(x.body LIKE '%http://%' OR x.body LIKE '%https://%')")
	case "files":
		where = append(where, "EXISTS (SELECT 1 FROM attachments a WHERE a.message_id = x.id AND a.mime NOT LIKE 'image/%' AND a.mime NOT LIKE 'audio/%')")
	case "photos":
		where = append(where, "EXISTS (SELECT 1 FROM attachments a WHERE a.message_id = x.id AND a.mime LIKE 'image/%')")
	case "cards":
		where = append(where, "x.kind = 'card'")
	case "polls":
		where = append(where, "x.kind = 'poll'")
	case "todos":
		where = append(where, "x.kind = 'todo'")
	case "voice":
		where = append(where, "x.kind = 'voice'")
	}
	if o.Since != "" {
		where, args = append(where, "x.created_at >= ?"), append(args, o.Since)
	}
	if o.Until != "" {
		where, args = append(where, "x.created_at < ?"), append(args, AddDays(o.Until, 1))
	}
	if o.Limit <= 0 || o.Limit > 200 {
		o.Limit = 50
	}
	args = append(args, o.Limit)
	ids, err := s.ids(ctx, `SELECT x.id FROM messages x JOIN members m ON m.conversation_id = x.conversation_id AND m.user_id = ?
		WHERE `+strings.Join(where, " AND ")+` ORDER BY x.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	out, err := s.messagesByID(ctx, uid, ids, true)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	for i := range out {
		out[i].Snippet = snippet(out[i].Body, q, 160)
	}
	return out, s.withTitles(ctx, uid, out)
}

// MessagesBetween lists messages in the person's conversations in a time
// range (a day for Pocket Day), oldest first.
func (s *Store) MessagesBetween(ctx context.Context, uid int64, from, to string, limit int) ([]Message, error) {
	ids, err := s.ids(ctx, `SELECT x.id FROM messages x JOIN members m ON m.conversation_id = x.conversation_id AND m.user_id = ?
		WHERE x.created_at >= ? AND x.created_at < ? AND x.deleted_at IS NULL AND x.kind != 'system' ORDER BY x.id LIMIT ?`, uid, from, to, limit)
	if err != nil {
		return nil, err
	}
	out, err := s.messagesByID(ctx, uid, ids, false)
	if err != nil {
		return nil, err
	}
	return out, s.withTitles(ctx, uid, out)
}

// UnreadMessages lists what the person has not read in one conversation
// (or everywhere, cid 0), oldest first, at most limit.
func (s *Store) UnreadMessages(ctx context.Context, uid, cid int64, limit int) ([]Message, error) {
	q := `SELECT x.id FROM messages x JOIN members m ON m.conversation_id = x.conversation_id AND m.user_id = ?
		WHERE x.id > m.last_read_id AND (x.user_id IS NULL OR x.user_id != m.user_id) AND x.deleted_at IS NULL AND x.kind != 'system' AND m.archived = 0`
	args := []any{uid}
	if cid > 0 {
		q += ` AND x.conversation_id = ?`
		args = append(args, cid)
	}
	q += ` ORDER BY x.id DESC LIMIT ?`
	args = append(args, limit)
	ids, err := s.ids(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out, err := s.messagesByID(ctx, uid, ids, false)
	if err != nil {
		return nil, err
	}
	return out, s.withTitles(ctx, uid, out)
}

// withTitles names each message's conversation as the person sees it.
func (s *Store) withTitles(ctx context.Context, uid int64, list []Message) error {
	titles := map[int64]string{}
	for i := range list {
		cid := list[i].ConversationID
		t, ok := titles[cid]
		if !ok {
			t = s.ConversationTitle(ctx, uid, cid)
			titles[cid] = t
		}
		list[i].ConversationTitle = t
	}
	return nil
}

// ConversationTitle is how a conversation is called for one person: a
// group's name, the other person's name, or "Me".
func (s *Store) ConversationTitle(ctx context.Context, uid, cid int64) string {
	var kind, title string
	if err := s.DB.QueryRowContext(ctx, `SELECT kind, title FROM conversations WHERE id = ?`, cid).Scan(&kind, &title); err != nil {
		return ""
	}
	switch kind {
	case KindSelf:
		return "Me"
	case KindDirect:
		var name string
		_ = s.DB.QueryRowContext(ctx, `SELECT u.name FROM members m JOIN users u ON u.id = m.user_id WHERE m.conversation_id = ? AND m.user_id != ? LIMIT 1`, cid, uid).Scan(&name)
		if name == "" {
			return "Just you"
		}
		return name
	}
	return title
}

// AddMade records what the person made from a message.
func (s *Store) AddMade(ctx context.Context, uid int64, d *Made) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO made (user_id, message_id, target, app, ref, title, url) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		uid, d.MessageID, d.Target, d.App, d.Ref, d.Title, d.URL)
	if err != nil {
		return err
	}
	d.ID, _ = res.LastInsertId()
	d.CreatedAt = Now()
	return nil
}

// MadeList lists everything the person made from messages, newest first.
func (s *Store) MadeList(ctx context.Context, uid int64, limit int) ([]Made, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, message_id, target, app, ref, title, url, created_at FROM made WHERE user_id = ? ORDER BY id DESC LIMIT ?`, uid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Made{}
	for rows.Next() {
		var d Made
		if err := rows.Scan(&d.ID, &d.MessageID, &d.Target, &d.App, &d.Ref, &d.Title, &d.URL, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// LastMessages returns the last n messages of a conversation (for AI context), oldest first.
func (s *Store) LastMessages(ctx context.Context, viewer, cid int64, n int) ([]Message, error) {
	ids, err := s.ids(ctx, `SELECT id FROM messages WHERE conversation_id = ? AND deleted_at IS NULL ORDER BY id DESC LIMIT ?`, cid, n)
	if err != nil {
		return nil, err
	}
	return s.messagesByID(ctx, viewer, ids, false)
}

// Activity counts a person's messages per conversation in a time range.
type Activity struct {
	ConversationID int64  `json:"conversation_id"`
	Title          string `json:"title"`
	Messages       int    `json:"messages"`
	Mine           int    `json:"mine"`
}

// ActivityBetween is how busy each of the person's conversations was.
func (s *Store) ActivityBetween(ctx context.Context, uid int64, from, to string) ([]Activity, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT x.conversation_id, COUNT(1), COALESCE(SUM(x.user_id = ?), 0) FROM messages x
		JOIN members m ON m.conversation_id = x.conversation_id AND m.user_id = ?
		WHERE x.created_at >= ? AND x.created_at < ? AND x.deleted_at IS NULL AND x.kind != 'system'
		GROUP BY x.conversation_id ORDER BY COUNT(1) DESC`, uid, uid, from, to)
	if err != nil {
		return nil, err
	}
	var out []Activity
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ConversationID, &a.Messages, &a.Mine); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, a)
	}
	rows.Close()
	for i := range out {
		out[i].Title = s.ConversationTitle(ctx, uid, out[i].ConversationID)
	}
	if out == nil {
		out = []Activity{}
	}
	return out, rows.Err()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func clipRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// snippet is the part of a text around the first word searched for.
func snippet(text, q string, n int) string {
	words := strings.Fields(Fold(q))
	r := []rune(text)
	if len(words) == 0 || len(r) <= n {
		return clipRunes(text, n)
	}
	folded := []rune(Fold(text))
	pos := strings.Index(string(folded), words[0])
	if pos < 0 || len(folded) != len(r) {
		return clipRunes(text, n)
	}
	at := len([]rune(string(folded)[:pos]))
	start := at - n/3
	if start < 0 {
		start = 0
	}
	end := start + n
	if end > len(r) {
		end = len(r)
	}
	out := strings.TrimSpace(string(r[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}

// ParseMentions turns the stored form back into user ids.
func ParseMentions(m []string) []int64 {
	var out []int64
	for _, v := range m {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			out = append(out, id)
		}
	}
	return out
}

var _ = sql.ErrNoRows
