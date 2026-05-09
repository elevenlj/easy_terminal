package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"easy_terminal/internal/session"
)

type SQLite struct {
	db *sql.DB
}

func Open(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &SQLite{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) Close() error { return s.db.Close() }

func (s *SQLite) migrate(ctx context.Context) error {
	stmts := []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			exit_code INTEGER,
			live INTEGER NOT NULL DEFAULT 0,
			notify_on_waiting INTEGER NOT NULL DEFAULT 0,
			peer_session_id TEXT NOT NULL DEFAULT '',
			bridge_enabled INTEGER NOT NULL DEFAULT 0,
			lark_chat_id TEXT NOT NULL DEFAULT '',
			history_size INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS session_output_chunks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			content BLOB NOT NULL,
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_session_output_chunks_session_seq ON session_output_chunks(session_id, seq)`,
		`CREATE TABLE IF NOT EXISTS quick_commands (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			text TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	for _, col := range []struct{ name, typ string }{
		{"live", "INTEGER NOT NULL DEFAULT 0"},
		{"notify_on_waiting", "INTEGER NOT NULL DEFAULT 0"},
		{"peer_session_id", "TEXT NOT NULL DEFAULT ''"},
		{"bridge_enabled", "INTEGER NOT NULL DEFAULT 0"},
		{"lark_chat_id", "TEXT NOT NULL DEFAULT ''"},
		{"history_size", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := s.ensureSessionColumn(ctx, col.name, col.typ); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) ensureSessionColumn(ctx context.Context, name, typ string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(sessions)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var col, colType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &col, &colType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if col == name {
			return nil
		}
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE sessions ADD COLUMN %s %s`, name, typ))
	return err
}

func (s *SQLite) CreateSession(ctx context.Context, sess session.Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id,name,status,created_at,updated_at,exit_code,live,notify_on_waiting,peer_session_id,bridge_enabled,lark_chat_id,history_size) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		sess.ID, sess.Name, sess.Status, sess.CreatedAt, sess.UpdatedAt, sess.ExitCode, boolInt(sess.Live), boolInt(sess.NotifyOnWaiting), sess.PeerSessionID, boolInt(sess.BridgeEnabled), sess.LarkChatID, sess.HistorySize)
	return err
}

func (s *SQLite) UpdateSession(ctx context.Context, sess session.Session) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET name=?,status=?,updated_at=?,exit_code=?,live=?,notify_on_waiting=?,peer_session_id=?,bridge_enabled=?,lark_chat_id=?,history_size=? WHERE id=?`,
		sess.Name, sess.Status, sess.UpdatedAt, sess.ExitCode, boolInt(sess.Live), boolInt(sess.NotifyOnWaiting), sess.PeerSessionID, boolInt(sess.BridgeEnabled), sess.LarkChatID, sess.HistorySize, sess.ID)
	return err
}

func (s *SQLite) ListSessions(ctx context.Context) ([]session.Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,status,created_at,updated_at,exit_code,live,notify_on_waiting,peer_session_id,bridge_enabled,lark_chat_id,history_size FROM sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []session.Session{}
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

func (s *SQLite) GetSession(ctx context.Context, id string) (session.Session, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,name,status,created_at,updated_at,exit_code,live,notify_on_waiting,peer_session_id,bridge_enabled,lark_chat_id,history_size FROM sessions WHERE id=?`, id)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return session.Session{}, false, nil
	}
	return sess, err == nil, err
}

func (s *SQLite) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM session_output_chunks WHERE session_id=?`, id)
	return err
}

func (s *SQLite) AppendOutput(ctx context.Context, sessionID string, seq int64, content []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO session_output_chunks (session_id,seq,content,created_at) VALUES (?,?,?,?)`, sessionID, seq, content, time.Now().UTC())
	return err
}

func (s *SQLite) Output(ctx context.Context, sessionID string) ([]byte, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT content FROM session_output_chunks WHERE session_id=? ORDER BY seq ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []byte
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, rows.Err()
}

func (s *SQLite) MarkAllNonTerminalSessionsExited(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET status=?, live=0, updated_at=? WHERE status IN (?,?)`, session.StatusExited, time.Now().UTC(), session.StatusRunning, session.StatusWaiting)
	return err
}

func (s *SQLite) ListQuickCommands(ctx context.Context) ([]session.QuickCommand, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,text,created_at FROM quick_commands ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []session.QuickCommand{}
	for rows.Next() {
		var qc session.QuickCommand
		if err := rows.Scan(&qc.ID, &qc.Name, &qc.Text, &qc.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, qc)
	}
	return out, rows.Err()
}

func (s *SQLite) CreateQuickCommand(ctx context.Context, qc session.QuickCommand) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO quick_commands (id,name,text,created_at) VALUES (?,?,?,?)`, qc.ID, qc.Name, qc.Text, qc.CreatedAt)
	return err
}

func (s *SQLite) DeleteQuickCommand(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM quick_commands WHERE id=?`, id)
	return err
}

type sessionScanner interface {
	Scan(dest ...any) error
}

func scanSession(row sessionScanner) (session.Session, error) {
	var sess session.Session
	var exit sql.NullInt64
	var live, notify, bridge int
	if err := row.Scan(&sess.ID, &sess.Name, &sess.Status, &sess.CreatedAt, &sess.UpdatedAt, &exit, &live, &notify, &sess.PeerSessionID, &bridge, &sess.LarkChatID, &sess.HistorySize); err != nil {
		return session.Session{}, err
	}
	if exit.Valid {
		code := int(exit.Int64)
		sess.ExitCode = &code
	}
	sess.Live = live != 0
	sess.NotifyOnWaiting = notify != 0
	sess.BridgeEnabled = bridge != 0
	return sess, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
