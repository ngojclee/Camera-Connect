package appdb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	// Pick the first available tenant (single-tenant users get enrolled
	// immediately; multi-tenant UX lands in Phase 6 device picker).
	if s.ctx == nil {
		tenants, err := s.client.Tenants(ctx, sess)
		if err == nil {
			for _, t := range tenants {
				if t.Status == "active" || t.Status == "" {
					enrolled, err := s.client.EnrollInstallation(ctx, sess, t.TenantID, s.machineLabel, s.version)
					if err == nil {
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
					}
					break
				}
			}
		}
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

// PushRcloneConf seals the local rclone.conf with passphrase and upserts it
// to AppDB user scope (key rclone_bundle). Server only sees ciphertext.
func (s *Service) PushRcloneConf(ctx context.Context, passphrase string) error {
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
// writes the local rclone.conf (0600). Returns ErrAuth on wrong passphrase.
func (s *Service) PullRcloneConf(ctx context.Context, passphrase string) error {
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
