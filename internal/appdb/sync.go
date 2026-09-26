package appdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ngojclee/camera-connect/internal/vault"
)

// Setting keys used inside extension_settings for camera_connect.
const (
	KeySharedConfig = "shared_config" // user scope: SharedConfig JSON
	KeyDevicePaths  = "device_paths"  // device scope: profile→path map
	KeyRcloneBundle = "rclone_bundle" // user scope: vault-sealed rclone.conf
	KeyProfileDefs  = "profile_defs"  // user scope: profiles + active profile
)

// Service manages login, enrollment, and settings sync for the agent.
type Service struct {
	client     *Client
	store      *SessionStore
	secretsDir string

	mu           sync.Mutex
	session      *Session
	ctx          *Context
	version      string
	machineLabel string
}

// NewService wires a sync service. secretsDir holds session + rclone.conf.
func NewService(client *Client, secretsDir, machineLabel, version string) *Service {
	return &Service{
		client:       client,
		store:        NewSessionStore(secretsDir),
		secretsDir:   secretsDir,
		version:      version,
		machineLabel: machineLabel,
	}
}

// Login signs in and loads/creates the enrollment context.
func (s *Service) Login(ctx context.Context, email, password string) error {
	sess, err := s.client.Login(ctx, email, password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.session = sess
	s.mu.Unlock()

	if s.ctx == nil {
		if err := s.enroll(ctx, sess, email); err != nil {
			return err
		}
	}
	return s.persist()
}

// enroll picks (or creates) a tenant and registers this installation.
// Every failure surfaces — a login without enrollment previously failed
// silently and left every later RPC returning "not enrolled".
func (s *Service) enroll(ctx context.Context, sess *Session, email string) error {
	tenants, err := s.client.Tenants(ctx, sess)
	if err != nil {
		return fmt.Errorf("appdb: list tenants: %w", err)
	}
	if len(tenants) == 0 {
		// First login ever — create a personal tenant so enrollment has
		// somewhere to land (mirrors LNC-Proxy first-run behavior).
		slug := "user-" + tenantSlugFromEmail(email)
		tid, err := s.client.CreateTenant(ctx, sess, slug, s.machineLabel)
		if err != nil {
			return fmt.Errorf("appdb: create tenant: %w", err)
		}
		tenants = []struct {
			TenantID string `json:"tenant_id"`
			Role     string `json:"role"`
			Status   string `json:"status"`
		}{{TenantID: tid, Role: "owner", Status: "active"}}
	}
	var lastErr error
	for _, t := range tenants {
		if t.Status != "active" && t.Status != "" {
			continue
		}
		enrolled, err := s.client.EnrollInstallation(ctx, sess, t.TenantID, s.machineLabel, s.version)
		if err != nil {
			lastErr = err
			continue
		}
		s.mu.Lock()
		s.ctx = &Context{
			TenantID:           enrolled.TenantID,
			ExtensionID:        enrolled.ExtensionID,
			DeviceEnrollmentID: enrolled.DeviceEnrollmentID,
			InstallationID:     enrolled.InstallationID,
			InstallationKey:    installationKey(),
			MachineLabel:       s.machineLabel,
		}
		s.mu.Unlock()
		break
	}
	if s.ctx == nil {
		if lastErr != nil {
			return fmt.Errorf("appdb: enroll installation: %w", lastErr)
		}
		return fmt.Errorf("appdb: no active tenant to enroll into")
	}
	return nil
}

// EnsureEnrolled lazily enrolls when a restored session has no context —
// e.g. an earlier login enrolled nothing, or a persisted session predates
// the enrollment context.
func (s *Service) EnsureEnrolled(ctx context.Context) error {
	s.mu.Lock()
	need := s.ctx == nil
	s.mu.Unlock()
	if !need {
		return nil
	}
	sess, err := s.sessionOrRefresh(ctx)
	if err != nil {
		return err
	}
	email := sess.Email
	if err := s.enroll(ctx, sess, email); err != nil {
		return err
	}
	return s.persist()
}

// Logout clears the local session (server tokens just expire).
func (s *Service) Logout() error {
	s.mu.Lock()
	s.session = nil
	s.ctx = nil
	s.mu.Unlock()
	return s.store.Clear()
}

// session returns a fresh-enough session, refreshing when near expiry.
func (s *Service) sessionOrRefresh(ctx context.Context) (*Session, error) {
	s.mu.Lock()
	if s.session == nil {
		s.restore()
	}
	sess := s.session
	s.mu.Unlock()
	if sess == nil {
		return nil, ErrNotAuthenticated
	}
	if sess.NeedsRefresh(2 * time.Minute) {
		fresh, err := s.client.Refresh(ctx, sess)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.session = fresh
		s.mu.Unlock()
		_ = s.persist()
	}
	return sess, nil
}

func (s *Service) requireContext() (*Session, *Context, error) {
	sess, err := s.sessionOrRefresh(context.Background())
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	c := s.ctx
	s.mu.Unlock()
	if c == nil {
		return nil, nil, fmt.Errorf("appdb: installation not enrolled")
	}
	return sess, c, nil
}

// Heartbeat pings the server-side installation row — lets AppDB know this
// installation is alive and surfaces its app version.
func (s *Service) Heartbeat(ctx context.Context) error {
	sess, c, err := s.requireContext()
	if err != nil {
		return err
	}
	return s.client.Heartbeat(ctx, sess, c.InstallationID, 0, s.version)
}

// PushSettingValue upserts one scoped key with read-modify-write revision
// handling; retries once on ErrRevisionConflict by re-reading the remote rev.
func (s *Service) PushSettingValue(ctx context.Context, scopeType, key string, value any) (int, error) {
	existing, err := s.GetSetting(ctx, scopeType, key)
	if err != nil {
		return 0, err
	}
	rev := 0
	if existing != nil {
		rev = existing.Revision
	}
	newRev, err := s.UpsertSetting(ctx, scopeType, key, value, rev)
	if err == ErrRevisionConflict {
		// Someone else wrote between our read and write — retry once.
		if existing, rerr := s.GetSetting(ctx, scopeType, key); rerr == nil && existing != nil {
			return s.UpsertSetting(ctx, scopeType, key, value, existing.Revision)
		}
	}
	return newRev, err
}

// ---------- settings sync ----------

// GetSetting reads a scoped setting (nil when absent).
func (s *Service) GetSetting(ctx context.Context, scopeType, key string) (*ScopedSetting, error) {
	sess, c, err := s.requireContext()
	if err != nil {
		return nil, err
	}
	return s.client.GetSetting(ctx, sess, c, scopeType, key)
}

// UpsertSetting writes a scoped setting (revision-checked).
func (s *Service) UpsertSetting(ctx context.Context, scopeType, key string, value any, expectedRev int) (int, error) {
	sess, c, err := s.requireContext()
	if err != nil {
		return 0, err
	}
	return s.client.UpsertSetting(ctx, sess, c, scopeType, key, value, expectedRev)
}

// ---------- rclone vault ----------

// RcloneConfPath is where the decrypted rclone.conf lives locally.
func (s *Service) RcloneConfPath() string {
	return filepath.Join(s.secretsDir, "rclone.conf")
}

// derivedPassphrase deterministically derives a vault passphrase from the
// logged-in user id — same on every machine, so push/pull work without the
// user typing anything. Honest trade-off: convenient but not secret — anyone
// with the ciphertext, the user_id (stored server-side) and this source can
// re-derive it. For real secrecy use a manual passphrase instead.
func (s *Service) derivedPassphrase() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil || s.session.UserID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("camera-connect-vault-v1:" + s.session.UserID))
	return hex.EncodeToString(sum[:])
}

// resolvePassphrase returns the manual passphrase when given, else the
// account-derived one. Empty result means no session is logged in.
func (s *Service) resolvePassphrase(passphrase string) (string, error) {
	if passphrase != "" {
		return passphrase, nil
	}
	if p := s.derivedPassphrase(); p != "" {
		return p, nil
	}
	return "", ErrNotAuthenticated
}

// PushRcloneConf seals the local rclone.conf with passphrase and upserts it
// to AppDB user scope (key rclone_bundle). Server only sees ciphertext.
// Empty passphrase auto-derives one from the logged-in account.
func (s *Service) PushRcloneConf(ctx context.Context, passphrase string) error {
	passphrase, err := s.resolvePassphrase(passphrase)
	if err != nil {
		return err
	}
	confPath := s.RcloneConfPath()
	data, err := os.ReadFile(confPath)
	if err != nil {
		return fmt.Errorf("read rclone.conf: %w (configure a remote first)", err)
	}
	blob, err := vault.Seal(passphrase, map[string]string{
		"rclone_conf": string(data),
		"machine":     s.machineLabel,
		"pushed_at":   time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}

	existing, err := s.GetSetting(ctx, "user", KeyRcloneBundle)
	if err != nil {
		return err
	}
	rev := 0
	if existing != nil {
		rev = existing.Revision
	}
	_, err = s.UpsertSetting(ctx, "user", KeyRcloneBundle, blob, rev)
	return err
}

// PullRcloneConf fetches the sealed bundle, unseals with passphrase, and
// writes the local rclone.conf (0600). Empty passphrase auto-derives one
// from the logged-in account. Returns ErrAuth on wrong passphrase.
func (s *Service) PullRcloneConf(ctx context.Context, passphrase string) error {
	passphrase, err := s.resolvePassphrase(passphrase)
	if err != nil {
		return err
	}
	setting, err := s.GetSetting(ctx, "user", KeyRcloneBundle)
	if err != nil {
		return err
	}
	if setting == nil {
		return fmt.Errorf("appdb: no rclone bundle stored — push one from another machine first")
	}
	var blob vault.Blob
	if err := json.Unmarshal(setting.Value, &blob); err != nil {
		return vault.ErrMalformed
	}
	var payload struct {
		RcloneConf string `json:"rclone_conf"`
	}
	if err := vault.Open(passphrase, &blob, &payload); err != nil {
		return err
	}
	if payload.RcloneConf == "" {
		return vault.ErrMalformed
	}
	if err := os.MkdirAll(s.secretsDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.RcloneConfPath(), []byte(payload.RcloneConf), 0o600)
}

// tenantSlugFromEmail derives a safe tenant slug from a login email —
// must satisfy '^[a-z0-9]+(?:[._-][a-z0-9]+)*$'.
func tenantSlugFromEmail(email string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(email)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == '@' || r == '.' || r == '_' || r == '-' || r == '+' {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "personal"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// ---------- persistence ----------

func (s *Service) persist() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Save(s.session, s.ctx)
}

func (s *Service) restore() {
	sess, ctx, err := s.store.Load()
	if err == nil {
		s.session = sess
		s.ctx = ctx
	}
}
