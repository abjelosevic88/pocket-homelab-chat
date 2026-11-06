package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Every read starts from the caller's membership: a conversation someone is
// not in does not exist for them (ErrNotFound), admins included.

const convCols = `c.id, c.kind, c.title, c.about, c.emoji, c.color, COALESCE(c.created_by, 0), c.ai, c.smart,
	c.last_message_id, COALESCE(c.last_message_at, ''), c.created_at, c.updated_at,
	m.role, m.last_read_id, m.notify, COALESCE(m.muted_until, ''), m.pinned, m.archived, m.draft`

// unreadSQL counts what a member has not read: others' messages after
// their last read one (deleted messages and system lines do not count).
const unreadSQL = `(SELECT COUNT(1) FROM messages x WHERE x.conversation_id = c.id AND x.id > m.last_read_id
	AND (x.user_id IS NULL OR x.user_id != m.user_id) AND x.deleted_at IS NULL AND x.kind != 'system')`

const mentionedSQL = `(SELECT COUNT(1) FROM messages x WHERE x.conversation_id = c.id AND x.id > m.last_read_id
	AND (x.user_id IS NULL OR x.user_id != m.user_id) AND x.deleted_at IS NULL AND x.mentions LIKE '% ' || m.user_id || ' %')`

func scanConv(row interface{ Scan(...any) error }) (*Conversation, error) {
	var c Conversation
	var ai, smart, pinned, archived int
	err := row.Scan(&c.ID, &c.Kind, &c.Title, &c.About, &c.Emoji, &c.Color, &c.CreatedBy, &ai, &smart,
		&c.LastMessageID, &c.LastMessageAt, &c.CreatedAt, &c.UpdatedAt,
		&c.Role, &c.LastReadID, &c.Notify, &c.MutedUntil, &pinned, &archived, &c.Draft, &c.Unread, &c.Mentioned)
	if err != nil {
		return nil, err
	}
	c.AI, c.Smart, c.Pinned, c.Archived = ai == 1, smart == 1, pinned == 1, archived == 1
	c.Members = []Member{}
	return &c, nil
}

// ListConversations returns every conversation the person is in, pinned
// first, then the most recent.
func (s *Store) ListConversations(ctx context.Context, uid int64) ([]Conversation, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+convCols+`, `+unreadSQL+`, `+mentionedSQL+`
		FROM members m JOIN conversations c ON c.id = m.conversation_id
		WHERE m.user_id = ?
		ORDER BY m.pinned DESC, COALESCE(c.last_message_at, c.created_at) DESC, c.id DESC`, uid)
	if err != nil {
		return nil, err
	}
	var out []Conversation
	for rows.Next() {
		c, err := scanConv(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.fillConversations(ctx, uid, out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Conversation{}
	}
	return out, nil
}

// GetConversation returns one conversation the person is in.
func (s *Store) GetConversation(ctx context.Context, uid, cid int64) (*Conversation, error) {
	c, err := scanConv(s.DB.QueryRowContext(ctx, `SELECT `+convCols+`, `+unreadSQL+`, `+mentionedSQL+`
		FROM members m JOIN conversations c ON c.id = m.conversation_id
		WHERE m.user_id = ? AND c.id = ?`, uid, cid))
	if err != nil {
		return nil, notFound(err)
	}
	list := []Conversation{*c}
	if err := s.fillConversations(ctx, uid, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// fillConversations adds the members and the last message.
func (s *Store) fillConversations(ctx context.Context, uid int64, list []Conversation) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]any, len(list))
	idx := map[int64]int{}
	var lastIDs []int64
	for i, c := range list {
		ids[i] = c.ID
		idx[c.ID] = i
		if c.LastMessageID > 0 {
			lastIDs = append(lastIDs, c.LastMessageID)
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT m.conversation_id, u.id, u.name, u.email, m.role, m.last_read_id, m.joined_at
		FROM members m JOIN users u ON u.id = m.user_id
		WHERE m.conversation_id IN (`+marks(len(ids))+`) ORDER BY m.joined_at, u.id`, ids...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int64
		var m Member
		if err := rows.Scan(&cid, &m.UserID, &m.Name, &m.Email, &m.Role, &m.LastReadID, &m.JoinedAt); err != nil {
			rows.Close()
			return err
		}
		list[idx[cid]].Members = append(list[idx[cid]].Members, m)
	}
	rows.Close()
	if len(lastIDs) == 0 {
		return nil
	}
	last, err := s.messagesByID(ctx, uid, lastIDs, false)
	if err != nil {
		return err
	}
	for i := range last {
		if j, ok := idx[last[i].ConversationID]; ok {
			m := last[i]
			list[j].Last = &m
		}
	}
	return nil
}

// Role returns the person's role in a conversation, ErrNotFound when they are not in it.
func (s *Store) Role(ctx context.Context, uid, cid int64) (string, error) {
	var role string
	err := s.DB.QueryRowContext(ctx, `SELECT role FROM members WHERE user_id = ? AND conversation_id = ?`, uid, cid).Scan(&role)
	return role, notFound(err)
}

// MemberIDs lists who is in a conversation.
func (s *Store) MemberIDs(ctx context.Context, cid int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id FROM members WHERE conversation_id = ? ORDER BY user_id`, cid)
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

// MemberState is a member's notification state in one conversation.
type MemberState struct {
	UserID     int64
	LastReadID int64
	Notify     string
	MutedUntil string
}

// MemberStates lists every member with their notification settings.
func (s *Store) MemberStates(ctx context.Context, cid int64) ([]MemberState, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id, last_read_id, notify, COALESCE(muted_until, '') FROM members WHERE conversation_id = ?`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemberState
	for rows.Next() {
		var m MemberState
		if err := rows.Scan(&m.UserID, &m.LastReadID, &m.Notify, &m.MutedUntil); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CreateGroup creates a group with its creator as the owner and the others as members.
func (s *Store) CreateGroup(ctx context.Context, creator int64, c *Conversation, others []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := Now()
	res, err := tx.ExecContext(ctx, `INSERT INTO conversations (kind, title, about, emoji, color, created_by, ai, smart, created_at, updated_at)
		VALUES ('group', ?, ?, ?, ?, ?, ?, ?, ?, ?)`, c.Title, c.About, c.Emoji, c.Color, creator, boolInt(c.AI), boolInt(c.Smart), now, now)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO members (user_id, conversation_id, role, joined_at) VALUES (?, ?, 'owner', ?)`, creator, c.ID, now); err != nil {
		return err
	}
	for _, id := range others {
		if id == creator {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO members (user_id, conversation_id, role, joined_at) VALUES (?, ?, 'member', ?)`, id, c.ID, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DirectWith returns the one conversation between two people, creating it
// the first time (created = true).
func (s *Store) DirectWith(ctx context.Context, a, b int64) (int64, bool, error) {
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	return s.uniqueConversation(ctx, KindDirect, fmt.Sprintf("d:%d:%d", lo, hi), a, []int64{lo, hi})
}

// SelfFor returns the person's own space ("Me"), creating it the first time.
func (s *Store) SelfFor(ctx context.Context, uid int64) (int64, bool, error) {
	return s.uniqueConversation(ctx, KindSelf, fmt.Sprintf("s:%d", uid), uid, []int64{uid})
}

func (s *Store) uniqueConversation(ctx context.Context, kind, key string, creator int64, people []int64) (int64, bool, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM conversations WHERE direct_key = ?`, key).Scan(&id)
	if err == nil {
		// someone who left a direct chat comes back into it
		for _, p := range people {
			if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO members (user_id, conversation_id, role) VALUES (?, ?, 'member')`, p, id); err != nil {
				return 0, false, err
			}
		}
		return id, false, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	now := Now()
	res, err := tx.ExecContext(ctx, `INSERT INTO conversations (kind, direct_key, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, kind, key, creator, now, now)
	if err != nil {
		return 0, false, conflict(err)
	}
	id, _ = res.LastInsertId()
	for _, p := range people {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO members (user_id, conversation_id, role, joined_at) VALUES (?, ?, 'member', ?)`, p, id, now); err != nil {
			return 0, false, err
		}
	}
	return id, true, tx.Commit()
}

// UpdateConversation saves a group's name, description, look and switches.
func (s *Store) UpdateConversation(ctx context.Context, c *Conversation) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE conversations SET title = ?, about = ?, emoji = ?, color = ?, ai = ?, smart = ?, updated_at = ? WHERE id = ?`,
		c.Title, c.About, c.Emoji, c.Color, boolInt(c.AI), boolInt(c.Smart), Now(), c.ID)
	return err
}

// MemberPrefs are a person's own switches for a conversation.
type MemberPrefs struct {
	Notify     *string `json:"notify"`
	MutedUntil *string `json:"muted_until"`
	Pinned     *bool   `json:"pinned"`
	Archived   *bool   `json:"archived"`
	Draft      *string `json:"draft"`
}

// UpdateMember saves the person's own switches (only the ones given).
func (s *Store) UpdateMember(ctx context.Context, uid, cid int64, p MemberPrefs) error {
	var sets []string
	var args []any
	if p.Notify != nil {
		sets, args = append(sets, "notify = ?"), append(args, *p.Notify)
	}
	if p.MutedUntil != nil {
		if *p.MutedUntil == "" {
			sets = append(sets, "muted_until = NULL")
		} else {
			sets, args = append(sets, "muted_until = ?"), append(args, *p.MutedUntil)
		}
	}
	if p.Pinned != nil {
		sets, args = append(sets, "pinned = ?"), append(args, boolInt(*p.Pinned))
	}
	if p.Archived != nil {
		sets, args = append(sets, "archived = ?"), append(args, boolInt(*p.Archived))
	}
	if p.Draft != nil {
		sets, args = append(sets, "draft = ?"), append(args, *p.Draft)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, uid, cid)
	res, err := s.DB.ExecContext(ctx, `UPDATE members SET `+strings.Join(sets, ", ")+` WHERE user_id = ? AND conversation_id = ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkRead moves the person's read mark forward (never back). It reports
// whether it moved.
func (s *Store) MarkRead(ctx context.Context, uid, cid, upto int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE members SET last_read_id = MIN(?, (SELECT last_message_id FROM conversations WHERE id = ?))
		WHERE user_id = ? AND conversation_id = ? AND last_read_id < MIN(?, (SELECT last_message_id FROM conversations WHERE id = ?))`, upto, cid, uid, cid, upto, cid)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// AddMembers puts people into a group; it returns who was new.
func (s *Store) AddMembers(ctx context.Context, cid int64, ids []int64) ([]int64, error) {
	var added []int64
	for _, id := range ids {
		res, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO members (user_id, conversation_id, role) SELECT ?, ? WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)`, id, cid, id)
		if err != nil {
			return added, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added = append(added, id)
		}
	}
	return added, nil
}

// SetRole makes someone an owner of a group or a member again.
func (s *Store) SetRole(ctx context.Context, cid, uid int64, role string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE members SET role = ? WHERE conversation_id = ? AND user_id = ?`, role, cid, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveMember takes someone out of a conversation. When nobody is left the
// conversation and everything in it are deleted; when the last owner left a
// group, the longest-standing member becomes its owner.
func (s *Store) RemoveMember(ctx context.Context, cid, uid int64) (emptied bool, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM members WHERE conversation_id = ? AND user_id = ?`, cid, uid)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, ErrNotFound
	}
	var left, owners int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(role = 'owner'), 0) FROM members WHERE conversation_id = ?`, cid).Scan(&left, &owners); err != nil {
		return false, err
	}
	if left == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, cid); err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	if owners == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE members SET role = 'owner' WHERE conversation_id = ? AND user_id = (SELECT user_id FROM members WHERE conversation_id = ? ORDER BY joined_at, user_id LIMIT 1)`, cid, cid); err != nil {
			return false, err
		}
	}
	return false, tx.Commit()
}

// DeleteConversation removes a conversation for everybody.
func (s *Store) DeleteConversation(ctx context.Context, cid int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, cid)
	return err
}

// UnreadTotals is how much the person has not read, everywhere.
func (s *Store) UnreadTotals(ctx context.Context, uid int64) (messages, conversations, mentions int, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(u), 0), COALESCE(SUM(u > 0), 0), COALESCE(SUM(n), 0) FROM (
		SELECT `+unreadSQL+` AS u, `+mentionedSQL+` AS n FROM members m JOIN conversations c ON c.id = m.conversation_id
		WHERE m.user_id = ? AND m.archived = 0)`, uid).Scan(&messages, &conversations, &mentions)
	return
}

func marks(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func anyIDs(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// Contacts are the people someone shares at least one conversation with (themselves included).
func (s *Store) Contacts(ctx context.Context, uid int64) ([]int64, error) {
	return s.ids(ctx, `SELECT DISTINCT b.user_id FROM members a JOIN members b ON b.conversation_id = a.conversation_id WHERE a.user_id = ?`, uid)
}

// Person is someone on this box, as the others see them.
type Person struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// People lists everyone on this box (to start a chat with).
func (s *Store) People(ctx context.Context) ([]Person, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, email FROM users ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name, &p.Email); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
