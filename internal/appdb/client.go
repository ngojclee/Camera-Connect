// Package appdb talks to appdb.lengoc.me (Supabase: GoTrue auth + PostgREST).
// Plain HTTPS REST — no SDK. The anon key embedded in the app is public by
// design (RLS enforces access); service keys are never allowed here.
package appdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the AppDB instance used by Camera Connect.
const DefaultBaseURL = "https://appdb.lengoc.me"

// ExtensionSlug registers this app inside the shared extension_settings schema.
const ExtensionSlug = "camera_connect"

// Client is a thread-safe-ish AppDB API client for one session.
type Client struct {
	BaseURL string
	AnonKey string
	HTTP    *http.Client
}

// Session is the GoTrue auth session (persisted locally).
type Session struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	ExpiresAt    int64  `json:"expires_at"`
	UserID       string `json:"user_id"`
	Email        string `json:"email"`
}

// ErrNotAuthenticated is returned when there's no valid session.
var ErrNotAuthenticated = fmt.Errorf("appdb: not authenticated")

// NewClient creates a client. anonKey must be a public anon key —
// service keys (sb_secret_*) are rejected.
func NewClient(baseURL, anonKey string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if strings.HasPrefix(anonKey, "sb_secret_") || strings.Contains(anonKey, "service_role") {
		return nil, fmt.Errorf("appdb: service keys are forbidden — only the anon key may be embedded")
	}
	return &Client{
		BaseURL: baseURL,
		AnonKey: anonKey,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// ---------- auth ----------

// Login authenticates via GoTrue password grant.
func (c *Client) Login(ctx context.Context, email, password string) (*Session, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.BaseURL+"/auth/v1/token?grant_type=password", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", c.AnonKey)
	req.Header.Set("Content-Type", "application/json")

	var resp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		User         struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}
	if resp.AccessToken == "" {
		desc := resp.ErrorDescription
		if desc == "" {
			desc = resp.Error
		}
		return nil, fmt.Errorf("appdb login failed: %s", desc)
	}
	return &Session{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		TokenType:    resp.TokenType,
		ExpiresIn:    resp.ExpiresIn,
		ExpiresAt:    time.Now().Unix() + int64(resp.ExpiresIn),
		UserID:       resp.User.ID,
		Email:        resp.User.Email,
	}, nil
}

// Refresh exchanges the refresh token for a new session.
func (c *Client) Refresh(ctx context.Context, s *Session) (*Session, error) {
	if s == nil || s.RefreshToken == "" {
		return nil, ErrNotAuthenticated
	}
	body, _ := json.Marshal(map[string]string{"refresh_token": s.RefreshToken})
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.BaseURL+"/auth/v1/token?grant_type=refresh_token", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", c.AnonKey)
	req.Header.Set("Content-Type", "application/json")
	var resp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}
	if resp.AccessToken == "" {
		return nil, ErrNotAuthenticated
	}
	s.AccessToken = resp.AccessToken
	if resp.RefreshToken != "" {
		s.RefreshToken = resp.RefreshToken
	}
	s.ExpiresIn = resp.ExpiresIn
	s.ExpiresAt = time.Now().Unix() + int64(resp.ExpiresIn)
	return s, nil
}

// NeedsRefresh reports whether the token expires within skew.
func (s *Session) NeedsRefresh(skew time.Duration) bool {
	if s == nil || s.AccessToken == "" {
		return true
	}
	return time.Now().Add(skew).Unix() >= s.ExpiresAt
}

// ---------- REST plumbing ----------

func (c *Client) authed(ctx context.Context, s *Session, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", c.AnonKey)
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *Client) rpc(ctx context.Context, s *Session, fn string, args map[string]any, out any) error {
	return c.authed(ctx, s, "POST", "/rest/v1/rpc/"+fn, args, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("appdb request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("appdb %s %s → %d: %s", req.Method, req.URL.Path, resp.StatusCode, truncate(string(data), 300))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("appdb response decode: %w", err)
		}
	}
	return nil
}

// ---------- enroll / heartbeat ----------

// EnrollResult mirrors extension_enroll_current_installation's return row.
type EnrollResult struct {
	TenantID           string `json:"tenant_id"`
	ExtensionID        string `json:"extension_id"`
	DeviceEnrollmentID string `json:"device_enrollment_id"`
	InstallationID     string `json:"installation_id"`
}

// EnrollInstallation registers this machine installation for the tenant.
func (c *Client) EnrollInstallation(ctx context.Context, s *Session, tenantID, machineLabel, version string) (*EnrollResult, error) {
	var rows []EnrollResult
	err := c.rpc(ctx, s, "extension_enroll_current_installation", map[string]any{
		"p_tenant_id":         tenantID,
		"p_extension_key":     ExtensionSlug,
		"p_installation_key":  installationKey(),
		"p_machine_label":     machineLabel,
		"p_platform":          "windows",
		"p_installed_version": version,
	}, &rows)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("appdb: empty enroll response")
	}
	return &rows[0], nil
}

// CreateTenant creates a personal tenant owned by the signed-in user
// (mirrors extension_create_tenant; returns the new tenant_id).
func (c *Client) CreateTenant(ctx context.Context, s *Session, slug, name string) (string, error) {
	var id string
	err := c.rpc(ctx, s, "extension_create_tenant", map[string]any{
		"p_slug": slug,
		"p_name": name,
	}, &id)
	return id, err
}

// Tenants lists the memberships of the signed-in user.
func (c *Client) Tenants(ctx context.Context, s *Session) ([]struct {
	TenantID string `json:"tenant_id"`
	Role     string `json:"role"`
	Status   string `json:"status"`
}, error) {
	var rows []struct {
		TenantID string `json:"tenant_id"`
		Role     string `json:"role"`
		Status   string `json:"status"`
	}
	q := url.Values{}
	q.Set("select", "tenant_id,role,status")
	err := c.authed(ctx, s, "GET", "/rest/v1/extension_tenant_members?"+q.Encode(), nil, &rows)
	return rows, err
}

// Heartbeat pings the installation (also detects remote-side changes).
func (c *Client) Heartbeat(ctx context.Context, s *Session, installationID string, expectedRev int, version string) error {
	var rows []map[string]any
	err := c.rpc(ctx, s, "extension_heartbeat_installation", map[string]any{
		"p_installation_id":   installationID,
		"p_expected_revision": expectedRev,
		"p_installed_version": version,
	}, &rows)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		if conflict, _ := rows[0]["conflict"].(bool); conflict {
			return fmt.Errorf("appdb: installation changed on another device (revision conflict)")
		}
	}
	return nil
}

// ---------- scoped settings ----------

// Scope columns matching LNC-Proxy appdb-client.js scopeMap.
var scopeColumns = map[string]string{
	"tenant":       "tenant_id",
	"user":         "user_id",
	"device":       "device_enrollment_id",
	"installation": "installation_id",
	"shop":         "shop_registry_id",
}

// ScopedSetting is one row of extension_settings.
type ScopedSetting struct {
	Value    json.RawMessage `json:"value"`
	Revision int             `json:"revision"`
}

// GetSetting reads one scoped setting; returns nil,nil when absent.
func (c *Client) GetSetting(ctx context.Context, s *Session, ctxInfo *Context, scopeType, key string) (*ScopedSetting, error) {
	col, ok := scopeColumns[scopeType]
	if !ok {
		return nil, fmt.Errorf("appdb: unsupported scope %q", scopeType)
	}
	scopeID, err := ctxInfo.ScopeID(scopeType, s.UserID)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("tenant_id", "eq."+ctxInfo.TenantID)
	q.Set("extension_id", "eq."+ctxInfo.ExtensionID)
	q.Set("scope_type", "eq."+scopeType)
	q.Set(col, "eq."+scopeID)
	q.Set("setting_key", "eq."+key)
	q.Set("select", "value,revision")
	q.Set("limit", "1")
	var rows []ScopedSetting
	if err := c.authed(ctx, s, "GET", "/rest/v1/extension_settings?"+q.Encode(), nil, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// ErrRevisionConflict marks a stale expected_revision.
var ErrRevisionConflict = fmt.Errorf("appdb: settings changed on another device (revision conflict)")

// UpsertSetting writes a scoped setting with optimistic concurrency.
func (c *Client) UpsertSetting(ctx context.Context, s *Session, ctxInfo *Context, scopeType, key string, value any, expectedRev int) (int, error) {
	col := scopeColumns[scopeType]
	if col == "" {
		return 0, fmt.Errorf("appdb: unsupported scope %q", scopeType)
	}
	scopeID, err := ctxInfo.ScopeID(scopeType, s.UserID)
	if err != nil {
		return 0, err
	}
	var rows []map[string]any
	err = c.rpc(ctx, s, "extension_upsert_setting", map[string]any{
		"p_tenant_id":         ctxInfo.TenantID,
		"p_extension_key":     ExtensionSlug,
		"p_scope_type":        scopeType,
		"p_scope_id":          scopeID,
		"p_setting_key":       key,
		"p_value":             value,
		"p_expected_revision": expectedRev,
	}, &rows)
	if err != nil {
		return 0, err
	}
	if len(rows) > 0 {
		if conflict, _ := rows[0]["conflict"].(bool); conflict {
			return 0, ErrRevisionConflict
		}
		if rev, ok := rows[0]["revision"].(float64); ok {
			return int(rev), nil
		}
	}
	return expectedRev + 1, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
