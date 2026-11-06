package store

import (
	"context"
)

const attCols = `id, user_id, conversation_id, COALESCE(message_id, 0), file, name, mime, bytes, width, height, duration_ms, transcript, created_at`

func scanAtt(row interface{ Scan(...any) error }) (*Attachment, error) {
	var a Attachment
	if err := row.Scan(&a.ID, &a.UserID, &a.ConversationID, &a.MessageID, &a.File, &a.Name, &a.Mime, &a.Bytes, &a.Width, &a.Height, &a.DurationMS, &a.Transcript, &a.CreatedAt); err != nil {
		return nil, err
	}
	a.URL = "/api/v1/files/" + a.File
	return &a, nil
}

// CreateAttachment stores an uploaded file, not yet in a message.
func (s *Store) CreateAttachment(ctx context.Context, a *Attachment) error {
	a.CreatedAt = Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO attachments (user_id, conversation_id, file, name, mime, bytes, width, height, duration_ms, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.UserID, a.ConversationID, a.File, a.Name, a.Mime, a.Bytes, a.Width, a.Height, a.DurationMS, a.CreatedAt)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	a.URL = "/api/v1/files/" + a.File
	return nil
}

// ClaimAttachments puts the person's own unsent uploads for this
// conversation into a message; others' or used ones are left alone. It
// returns how many it took.
func (s *Store) ClaimAttachments(ctx context.Context, uid, cid, mid int64, ids []int64) (int, error) {
	n := 0
	for _, id := range ids {
		res, err := s.DB.ExecContext(ctx, `UPDATE attachments SET message_id = ? WHERE id = ? AND user_id = ? AND conversation_id = ? AND message_id IS NULL`, mid, id, uid, cid)
		if err != nil {
			return n, err
		}
		if k, _ := res.RowsAffected(); k > 0 {
			n++
		}
	}
	return n, nil
}

// AttachmentByFile returns a file for someone in its conversation.
func (s *Store) AttachmentByFile(ctx context.Context, viewer int64, file string) (*Attachment, error) {
	a, err := scanAtt(s.DB.QueryRowContext(ctx, `SELECT `+attCols+` FROM attachments a
		WHERE a.file = ? AND EXISTS (SELECT 1 FROM members m WHERE m.conversation_id = a.conversation_id AND m.user_id = ?)`, file, viewer))
	return a, notFound(err)
}

// GetAttachment returns an attachment for someone in its conversation.
func (s *Store) GetAttachment(ctx context.Context, viewer, id int64) (*Attachment, error) {
	a, err := scanAtt(s.DB.QueryRowContext(ctx, `SELECT `+attCols+` FROM attachments a
		WHERE a.id = ? AND EXISTS (SELECT 1 FROM members m WHERE m.conversation_id = a.conversation_id AND m.user_id = ?)`, id, viewer))
	return a, notFound(err)
}

// SetTranscript saves the words of a voice message.
func (s *Store) SetTranscript(ctx context.Context, id int64, text string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE attachments SET transcript = ? WHERE id = ?`, text, id)
	return err
}

// Shared lists the photos and files in a conversation, newest first.
func (s *Store) Shared(ctx context.Context, viewer, cid int64, photos bool, limit int) ([]Attachment, error) {
	q := `SELECT ` + attCols + ` FROM attachments a WHERE a.conversation_id = ? AND a.message_id IS NOT NULL
		AND EXISTS (SELECT 1 FROM members m WHERE m.conversation_id = a.conversation_id AND m.user_id = ?)`
	if photos {
		q += ` AND a.mime LIKE 'image/%'`
	} else {
		q += ` AND a.mime NOT LIKE 'image/%'`
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY a.id DESC LIMIT ?`, cid, viewer, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		a, err := scanAtt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// StaleUploads are files uploaded but never sent, older than the time given.
func (s *Store) StaleUploads(ctx context.Context, before string) ([]string, error) {
	files, err := s.strings(ctx, `SELECT file FROM attachments WHERE message_id IS NULL AND created_at < ?`, before)
	if err != nil {
		return nil, err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM attachments WHERE message_id IS NULL AND created_at < ?`, before)
	return files, err
}

// AllFiles lists every file name the database knows (to find orphans on disk).
func (s *Store) AllFiles(ctx context.Context) (map[string]bool, error) {
	list, err := s.strings(ctx, `SELECT file FROM attachments`)
	out := map[string]bool{}
	for _, f := range list {
		out[f] = true
	}
	return out, err
}

// ConversationFiles lists every file in a conversation (to remove when it is deleted).
func (s *Store) ConversationFiles(ctx context.Context, cid int64) ([]string, error) {
	return s.strings(ctx, `SELECT file FROM attachments WHERE conversation_id = ?`, cid)
}
