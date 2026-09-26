package appdb

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Context is the enrolled installation context (persisted locally).
type Context struct {
	TenantID           string `json:"tenant_id"`
	ExtensionID        string `json:"extension_id"`
	DeviceEnrollmentID string `json:"device_enrollment_id"`
	InstallationID     string `json:"installation_id"`
	InstallationKey    string `json:"installation_key"`
	MachineLabel       string `json:"machine_label"`
	Revision           int    `json:"revision"`
}

// ScopeID resolves the scope column value for this context.
func (c *Context) ScopeID(scopeType, userID string) (string, error) {
	var id string
	switch scopeType {
	case "tenant":
		id = c.TenantID
	case "user":
		id = userID
	case "device":
		id = c.DeviceEnrollmentID
	case "installation":
		id = c.InstallationID
	default:
		return "", fmt.Errorf("appdb: unsupported scope %q", scopeType)
	}
	if id == "" {
		return "", fmt.Errorf("appdb: %s scope unavailable — enroll first", scopeType)
	}
	return id, nil
}

// ---------- session/context persistence ----------

// SessionStore persists the AppDB session + enroll context under the
// secrets dir (chmod'd dir; file carries tokens, never committed).
type SessionStore struct {
	path string
	mu   sync.Mutex
}

// NewSessionStore binds a store file (e.g. secrets/appdb_session.json).
func NewSessionStore(secretsDir string) *SessionStore {
	return &SessionStore{path: filepath.Join(secretsDir, "appdb_session.json")}
}

// persisted combines session + context in one small JSON file.
type persisted struct {
	Session *Session `json:"session,omitempty"`
	Context *Context `json:"context,omitempty"`
}

// Save writes session+context (either may be nil to clear that side).
func (s *SessionStore) Save(sess *Session, ctx *Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(persisted{Session: sess, Context: ctx}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Load reads session+context (nil when absent).
func (s *SessionStore) Load() (*Session, *Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, nil, fmt.Errorf("appdb session file corrupt: %w", err)
	}
	return p.Session, p.Context, nil
}

// Clear removes the stored session.
func (s *SessionStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ---------- installation key ----------

var (
	instKeyOnce sync.Once
	instKey     string
)

// installationKey returns a stable per-installation identifier, generated
// once and persisted in the secrets dir (opaque random id, not a secret).
func installationKey() string {
	instKeyOnce.Do(func() {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			instKey = randomKey()
			return
		}
		path := filepath.Join(local, "CameraConnect", "secrets", "installation.key")
		if data, err := os.ReadFile(path); err == nil {
			// Legacy keys ("cc_<hex>") predate the uuid-typed server column —
			// regenerate so enrollment stops failing with 22P02.
			if k := strings.TrimSpace(string(data)); isUUID(k) {
				instKey = k
				return
			}
		}
		instKey = randomKey()
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		_ = os.WriteFile(path, []byte(instKey), 0o600)
	})
	return instKey
}

// randomKey returns a UUIDv4 — the server column is uuid-typed, so an
// opaque prefix like "cc_<hex>" fails with 22P02 (learned the hard way).
func randomKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}
