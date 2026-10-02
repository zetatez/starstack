// Package store provides SQLite-backed metadata persistence.
package store

import (
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database with prepared domain queries.
type Store struct {
	db *sql.DB
}

// User is an account that can log into the web UI.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	IsAdmin      bool
	CreatedAt    time.Time
	Disabled     bool
}

// Open opens (and migrates) the SQLite database at dbPath with WAL enabled.
func Open(dbPath string) (*Store, error) {
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite: allow at most a few concurrent connections to stay WAL-friendly.
	db.SetMaxOpenConns(8)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  is_admin      INTEGER NOT NULL DEFAULT 0,
  disabled      INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token_hash  TEXT PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS shares (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  token       TEXT NOT NULL UNIQUE,
  scope       TEXT NOT NULL DEFAULT 'me',
  path        TEXT NOT NULL,
  password    TEXT NOT NULL DEFAULT '',
  expires_at  INTEGER NOT NULL DEFAULT 0,
  allow_down  INTEGER NOT NULL DEFAULT 1,
  max_uses    INTEGER NOT NULL DEFAULT 0,
  used        INTEGER NOT NULL DEFAULT 0,
  created_by  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS trash (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  orig_path  TEXT NOT NULL,
  trash_path TEXT NOT NULL,
  is_dir     INTEGER NOT NULL,
  size       INTEGER NOT NULL DEFAULT 0,
  deleted_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_trash_user ON trash(user_id);
CREATE INDEX IF NOT EXISTS idx_shares_user ON shares(created_by);
`
	_, err := s.db.Exec(schema)
	return err
}

// ---- users ----

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(username, passwordHash string, isAdmin bool) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO users(username, password_hash, is_admin, created_at) VALUES(?,?,?,?)`,
		username, passwordHash, boolInt(isAdmin), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetUserByName(username string) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, username, password_hash, is_admin, disabled, created_at FROM users WHERE username = ?`,
		username)
	var u User
	var isAdmin, disabled int
	var created int64
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &isAdmin, &disabled, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	u.Disabled = disabled != 0
	u.CreatedAt = time.Unix(created, 0)
	return &u, nil
}

func (s *Store) GetUserByID(id int64) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, username, password_hash, is_admin, disabled, created_at FROM users WHERE id = ?`, id)
	var u User
	var isAdmin, disabled int
	var created int64
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &isAdmin, &disabled, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	u.Disabled = disabled != 0
	u.CreatedAt = time.Unix(created, 0)
	return &u, nil
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(
		`SELECT id, username, password_hash, is_admin, disabled, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]User, 0, 64)
	for rows.Next() {
		var u User
		var isAdmin, disabled, created int64
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &isAdmin, &disabled, &created); err != nil {
			return nil, err
		}
		u.IsAdmin = isAdmin != 0
		u.Disabled = disabled != 0
		u.CreatedAt = time.Unix(created, 0)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) UpdatePassword(id int64, hash string) error {
	_, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
	return err
}

func (s *Store) SetDisabled(id int64, disabled bool) error {
	// Disabling a user also invalidates their refresh sessions.
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE users SET disabled = ? WHERE id = ?`, boolInt(disabled), id)
	return err
}

func (s *Store) DeleteUser(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// ---- sessions (refresh tokens) ----

func (s *Store) SaveSession(tokenHash string, userID int64, expiresAt time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions(token_hash, user_id, expires_at) VALUES(?,?,?)`,
		tokenHash, userID, expiresAt.Unix())
	return err
}

func (s *Store) GetSession(tokenHash string) (int64, time.Time, error) {
	var userID int64
	var exp int64
	err := s.db.QueryRow(
		`SELECT user_id, expires_at FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&userID, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, time.Time{}, ErrNotFound
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	return userID, time.Unix(exp, 0), nil
}

func (s *Store) RotateSession(oldHash, newHash string, userID int64, expiresAt time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM sessions WHERE token_hash = ?`, oldHash); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO sessions(token_hash, user_id, expires_at) VALUES(?,?,?)`,
		newHash, userID, expiresAt.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) PruneSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	return err
}

// ---- shares ----

// Share is a public link that exposes a path without requiring login.
type Share struct {
	ID        int64     `json:"id"`
	Token     string    `json:"token"`
	Scope     string    `json:"scope"`
	Path      string    `json:"path"`
	Password  string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	AllowDown bool      `json:"allow_down"`
	MaxUses   int64     `json:"max_uses"`
	Used      int64     `json:"used"`
	CreatedBy int64     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CreateShare(sh *Share) error {
	res, err := s.db.Exec(
		`INSERT INTO shares(token, scope, path, password, expires_at, allow_down, max_uses, created_by, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		sh.Token, sh.Scope, sh.Path, sh.Password, sh.ExpiresAt.Unix(), boolInt(sh.AllowDown),
		sh.MaxUses, sh.CreatedBy, time.Now().Unix())
	if err != nil {
		return err
	}
	sh.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetShare(token string) (*Share, error) {
	row := s.db.QueryRow(
		`SELECT id, token, scope, path, password, expires_at, allow_down, max_uses, used, created_by, created_at
		 FROM shares WHERE token = ?`, token)
	var sh Share
	var pw string
	var scope string
	var expires, allow, used, created int64
	var maxUses int64
	if err := row.Scan(&sh.ID, &sh.Token, &scope, &sh.Path, &pw, &expires, &allow, &maxUses, &used, &sh.CreatedBy, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	sh.Scope = scope
	sh.Password = pw
	sh.ExpiresAt = time.Unix(expires, 0)
	sh.AllowDown = allow != 0
	sh.MaxUses = maxUses
	sh.Used = used
	sh.CreatedAt = time.Unix(created, 0)
	return &sh, nil
}

func (s *Store) ListShares(createdBy int64) ([]Share, error) {
	rows, err := s.db.Query(
		`SELECT id, token, scope, path, password, expires_at, allow_down, max_uses, used, created_by, created_at
		 FROM shares WHERE created_by = ? ORDER BY created_at DESC`, createdBy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Share, 0, 32)
	for rows.Next() {
		var sh Share
		var pw string
		var scope string
		var expires, allow, used, created int64
		var maxUses int64
		if err := rows.Scan(&sh.ID, &sh.Token, &scope, &sh.Path, &pw, &expires, &allow, &maxUses, &used, &sh.CreatedBy, &created); err != nil {
			return nil, err
		}
		sh.Scope = scope
		sh.Password = pw
		sh.ExpiresAt = time.Unix(expires, 0)
		sh.AllowDown = allow != 0
		sh.MaxUses = maxUses
		sh.Used = used
		sh.CreatedAt = time.Unix(created, 0)
		out = append(out, sh)
	}
	return out, rows.Err()
}

func (s *Store) DeleteShare(token string) error {
	_, err := s.db.Exec(`DELETE FROM shares WHERE token = ?`, token)
	return err
}

func (s *Store) PruneShares() error {
	// Expired shares and those that exhausted their allowed uses.
	_, err := s.db.Exec(
		`DELETE FROM shares
		 WHERE (expires_at > 0 AND expires_at < ?) OR (max_uses > 0 AND used >= max_uses)`,
		time.Now().Unix())
	return err
}

func (s *Store) BumpShareUsed(id int64) error {
	_, err := s.db.Exec(`UPDATE shares SET used = used + 1 WHERE id = ?`, id)
	return err
}

// ---- trash ----

type TrashEntry struct {
	ID        int64     `json:"id"`
	OrigPath  string    `json:"orig_path"`
	TrashPath string    `json:"trash_path"`
	IsDir     bool      `json:"is_dir"`
	Size      int64     `json:"size"`
	DeletedAt time.Time `json:"deleted_at"`
}

func (s *Store) AddTrash(userID int64, e *TrashEntry) error {
	res, err := s.db.Exec(
		`INSERT INTO trash(user_id, orig_path, trash_path, is_dir, size, deleted_at)
		 VALUES(?,?,?,?,?,?)`,
		userID, e.OrigPath, e.TrashPath, boolInt(e.IsDir), e.Size, time.Now().Unix())
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

func (s *Store) ListTrash(userID int64) ([]TrashEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, orig_path, trash_path, is_dir, size, deleted_at
		 FROM trash WHERE user_id = ? ORDER BY deleted_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TrashEntry, 0, 32)
	for rows.Next() {
		var e TrashEntry
		var isDir, deleted int64
		if err := rows.Scan(&e.ID, &e.OrigPath, &e.TrashPath, &isDir, &e.Size, &deleted); err != nil {
			return nil, err
		}
		e.IsDir = isDir != 0
		e.DeletedAt = time.Unix(deleted, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) GetTrash(userID, id int64) (*TrashEntry, error) {
	row := s.db.QueryRow(
		`SELECT id, orig_path, trash_path, is_dir, size, deleted_at
		 FROM trash WHERE user_id = ? AND id = ?`, userID, id)
	var e TrashEntry
	var isDir, deleted int64
	if err := row.Scan(&e.ID, &e.OrigPath, &e.TrashPath, &isDir, &e.Size, &deleted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	e.IsDir = isDir != 0
	e.DeletedAt = time.Unix(deleted, 0)
	return &e, nil
}

func (s *Store) DeleteTrashRow(id int64) error {
	_, err := s.db.Exec(`DELETE FROM trash WHERE id = ?`, id)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
