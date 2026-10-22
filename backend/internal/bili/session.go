package bili

// SessionStore persists the login cookie in a standalone JSON file rather
// than the database: it is a credential (SESSDATA is the account password,
// effectively), so it must not ride along in VACUUM INTO backups, API rows
// or logs. File mode 0600, written atomically.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Session is the persisted login state.
type Session struct {
	Cookie       string    `json:"cookie"` // "SESSDATA=…; bili_jct=…; DedeUserID=…; …"
	Mid          int64     `json:"mid"`
	Uname        string    `json:"uname"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ObtainedAt   time.Time `json:"obtained_at"`
	// ExpiresAt is the SESSDATA expiry; filled from the login response and
	// refined by later nav checks. Zero means unknown.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	// LastRefreshedAt records the last successful passport cookie rotation
	// (refresh.go); it throttles re-rotation attempts.
	LastRefreshedAt time.Time `json:"last_refreshed_at,omitempty"`
}

// Expired reports whether the session is definitely past its lifetime.
// Bilibili also invalidates SESSDATA on rare security events, so an
// unexpired session can still be dead — the nav check is the source of
// truth; this is only an early warning.
func (s *Session) Expired(now time.Time) bool {
	return !s.ExpiresAt.IsZero() && now.After(s.ExpiresAt)
}

// SessionStore reads and writes the session file.
type SessionStore struct {
	path string
}

// NewSessionStore points at a JSON file (typically <DATA_DIR>/bili-session.json).
func NewSessionStore(path string) *SessionStore {
	return &SessionStore{path: path}
}

// Path exposes the file location for logs and the CLI.
func (s *SessionStore) Path() string { return s.path }

// Load returns the stored session, or nil when no session was saved yet.
func (s *SessionStore) Load() (*Session, error) {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	var session Session
	if err := json.Unmarshal(raw, &session); err != nil {
		return nil, fmt.Errorf("parse session %s: %w", s.path, err)
	}
	if session.Cookie == "" {
		return nil, nil
	}
	return &session, nil
}

// Save writes the session atomically with owner-only permissions.
func (s *SessionStore) Save(session *Session) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	raw, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit session: %w", err)
	}
	return nil
}

// Clear removes the session file (logout).
func (s *SessionStore) Clear() error {
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove session: %w", err)
	}
	return nil
}
